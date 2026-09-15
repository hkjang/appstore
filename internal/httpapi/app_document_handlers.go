package httpapi

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

// maxDocumentBytes and documentContentTypes are shared with the bundled
// guide sync, so a file one accepts is never refused by the other.
const maxDocumentBytes = model.MaxAppDocumentBytes

const documentExtensionHint = "PDF, Markdown, 텍스트, CSV, Word, PowerPoint, Excel, 한글 문서만 첨부할 수 있습니다."

// sanitizeDocumentFileName keeps the name the uploader recognizes while
// removing everything a path or a header could be steered with. Korean and
// other non-ASCII names survive; directories and control characters do not.
func sanitizeDocumentFileName(value string) string {
	value = strings.TrimSpace(value)
	value = value[strings.LastIndexAny(value, `/\`)+1:]
	value = strings.Map(func(r rune) rune {
		// A tab or a newline is a control character and a separator at once; it
		// becomes a space instead of vanishing from between two words.
		if unicode.IsSpace(r) {
			return ' '
		}
		if r < 0x20 || r == 0x7f || r == '"' {
			return -1
		}
		return r
	}, value)
	value = strings.TrimSpace(strings.Trim(value, "."))
	if len([]rune(value)) > 160 {
		value = string([]rune(value)[:160])
	}
	return value
}

func documentContentType(fileName string) (string, bool) {
	return model.AppDocumentContentType(fileName)
}

// documentTitle falls back to the file name without its extension, so a list
// of attachments always reads as something.
func documentTitle(title, fileName string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = strings.TrimSuffix(fileName, path.Ext(fileName))
	}
	if len([]rune(title)) > 160 {
		title = string([]rune(title)[:160])
	}
	return title
}

func documentDownloadURL(appID uuid.UUID, documentID uuid.UUID) string {
	return "/api/v1/apps/" + appID.String() + "/documents/" + documentID.String()
}

func withDownloadURLs(documents []model.AppDocument) []model.AppDocument {
	for index := range documents {
		documents[index].DownloadURL = documentDownloadURL(documents[index].AppID, documents[index].ID)
	}
	return documents
}

// documentApp resolves the {app} path parameter, which is an identifier on the
// management paths and a slug on the links the store front follows. Apps that
// are not published to everyone stay visible to the people who work on them.
func (s *Server) documentApp(r *http.Request) (model.App, error) {
	param := chi.URLParam(r, "app")
	var app model.App
	var err error
	if id, parseErr := uuid.Parse(param); parseErr == nil {
		app, err = s.repository.GetAppByID(r.Context(), id)
	} else {
		app, err = s.repository.GetAppBySlug(r.Context(), param, true)
	}
	if err != nil {
		return model.App{}, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다.")
	}
	if app.Status == model.AppStatusPublished && app.Visibility == "public" {
		return app, nil
	}
	principal := CurrentPrincipal(r.Context())
	if ownsApp(principal, app) || principal.Can("apps:manage") || principal.Can("reviews:read") {
		return app, nil
	}
	// A draft or private app answers the same way a missing one does, so the
	// documents endpoint cannot be used to discover unpublished slugs.
	return model.App{}, NotFound("APP_NOT_FOUND", "앱을 찾을 수 없습니다.")
}

func ownsApp(principal *Principal, app model.App) bool {
	return principal != nil && app.OwnerID != nil && *app.OwnerID == principal.User.ID
}

// documentWriteApp resolves the app for an upload or a delete: only its owner
// and a catalog manager may change what is attached to it.
func (s *Server) documentWriteApp(r *http.Request) (model.App, error) {
	app, err := s.documentApp(r)
	if err != nil {
		return model.App{}, err
	}
	principal := CurrentPrincipal(r.Context())
	if !ownsApp(principal, app) && !principal.Can("apps:manage") {
		return model.App{}, Forbidden("소유한 앱의 가이드 문서만 관리할 수 있습니다.")
	}
	return app, nil
}

func documentIDParam(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "document"))
	if err != nil {
		return uuid.Nil, Validation("문서 ID가 올바르지 않습니다.", nil)
	}
	return id, nil
}

// listAppDocuments is public for a published app: a visitor reading the app
// detail page downloads its guide without signing in.
func (s *Server) listAppDocuments(w http.ResponseWriter, r *http.Request) {
	app, err := s.documentApp(r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	documents, err := s.repository.ListAppDocuments(r.Context(), app.ID)
	if err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "가이드 문서를 불러오지 못했습니다."))
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": withDownloadURLs(documents)})
}

func (s *Server) downloadAppDocument(w http.ResponseWriter, r *http.Request) {
	app, err := s.documentApp(r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	documentID, err := documentIDParam(r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	document, content, err := s.repository.GetAppDocumentContent(r.Context(), app.ID, documentID)
	if err != nil {
		WriteError(w, r, storeError(err, "DOCUMENT_NOT_FOUND", "가이드 문서를 찾을 수 없습니다."))
		return
	}
	etag := `"` + document.Checksum + `"`
	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", document.ContentType)
	w.Header().Set("Content-Disposition", contentDisposition(document.FileName))
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(content)
}

// contentDisposition always asks the browser to save the file, and carries the
// original name twice: an ASCII fallback for old clients and the UTF-8 form
// every current browser prefers.
func contentDisposition(fileName string) string {
	ascii := strings.Map(func(r rune) rune {
		if r > unicode.MaxASCII || r < 0x20 || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, fileName)
	ascii = strings.TrimSpace(ascii)
	// A wholly non-ASCII name leaves nothing but its extension behind, which
	// is no help to the client reading the fallback.
	if strings.TrimSpace(strings.TrimSuffix(ascii, path.Ext(ascii))) == "" {
		ascii = "document" + path.Ext(ascii)
	}
	return mime.FormatMediaType("attachment", map[string]string{"filename": ascii}) +
		"; filename*=UTF-8''" + url.PathEscape(fileName)
}

func (s *Server) uploadAppDocument(w http.ResponseWriter, r *http.Request) {
	app, err := s.documentWriteApp(r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	// ParseMultipartForm spools anything past its memory budget to a temporary
	// file with no limit of its own, so the body is bounded before it is read.
	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentBytes+multipartEnvelopeBytes)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			WriteError(w, r, Validation("가이드 문서는 20MB 이하여야 합니다.", nil))
			return
		}
		WriteError(w, r, Validation("업로드 파일을 읽지 못했습니다.", nil))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		WriteError(w, r, Validation("업로드할 파일을 선택하세요.", nil))
		return
	}
	defer file.Close()
	fileName := sanitizeDocumentFileName(header.Filename)
	if fileName == "" {
		WriteError(w, r, Validation("파일 이름을 확인할 수 없습니다.", nil))
		return
	}
	contentType, ok := documentContentType(fileName)
	if !ok {
		WriteError(w, r, Validation(documentExtensionHint, nil))
		return
	}
	content, err := io.ReadAll(io.LimitReader(file, maxDocumentBytes+1))
	if err != nil {
		WriteError(w, r, Validation("업로드 파일을 읽지 못했습니다.", nil))
		return
	}
	if len(content) == 0 {
		WriteError(w, r, Validation("빈 파일은 첨부할 수 없습니다.", nil))
		return
	}
	if len(content) > maxDocumentBytes {
		WriteError(w, r, Validation("가이드 문서는 20MB 이하여야 합니다.", nil))
		return
	}
	principal := CurrentPrincipal(r.Context())
	var uploadedBy *uuid.UUID
	if principal != nil {
		id := principal.User.ID
		uploadedBy = &id
	}
	document, err := s.repository.CreateAppDocument(r.Context(), app.ID, store.AppDocumentInput{
		Title:       documentTitle(r.FormValue("title"), fileName),
		FileName:    fileName,
		ContentType: contentType,
		Content:     content,
		UploadedBy:  uploadedBy,
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			WriteError(w, r, &APIError{
				Status: http.StatusConflict, Code: "DOCUMENT_REJECTED",
				Message: "같은 이름의 문서가 이미 있거나 앱당 첨부 한도(10개)를 넘었습니다.",
			})
			return
		}
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "가이드 문서를 저장하지 못했습니다."))
		return
	}
	document.DownloadURL = documentDownloadURL(app.ID, document.ID)
	s.recordAudit(r, "app.document.upload", "app", app.ID.String(), nil, map[string]any{
		"documentId": document.ID, "fileName": document.FileName,
		"contentType": document.ContentType, "size": document.Size,
	})
	WriteJSON(w, http.StatusCreated, document)
}

func (s *Server) deleteAppDocument(w http.ResponseWriter, r *http.Request) {
	app, err := s.documentWriteApp(r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	documentID, err := documentIDParam(r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	if err := s.repository.DeleteAppDocument(r.Context(), app.ID, documentID); err != nil {
		WriteError(w, r, storeError(err, "DOCUMENT_NOT_FOUND", "가이드 문서를 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "app.document.delete", "app", app.ID.String(), map[string]any{
		"documentId": documentID.String(),
	}, nil)
	w.WriteHeader(http.StatusNoContent)
}
