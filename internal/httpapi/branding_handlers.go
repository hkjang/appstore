package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hkjang/appstore/internal/store"
)

// maxBrandingBytes caps both an upload and an imported URL. A logo or favicon
// is small; anything larger is a mistake or an attempt to fill the database.
const maxBrandingBytes = 1 << 20

var brandingContentTypes = map[string]bool{
	"image/png":                true,
	"image/jpeg":               true,
	"image/webp":               true,
	"image/gif":                true,
	"image/svg+xml":            true,
	"image/x-icon":             true,
	"image/vnd.microsoft.icon": true,
}

func normalizeImageContentType(value string) (string, bool) {
	media := strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
	if media == "image/ico" || media == "application/ico" {
		media = "image/x-icon"
	}
	return media, brandingContentTypes[media]
}

func brandingKindParam(r *http.Request) (string, error) {
	kind := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "kind")))
	if !store.ValidBrandingKind(kind) {
		return "", Validation("logo 또는 favicon만 사용할 수 있습니다.", nil)
	}
	return kind, nil
}

// brandingAsset serves a stored image to anyone, since the login screen and the
// browser tab need it before sign-in.
func (s *Server) brandingAsset(w http.ResponseWriter, r *http.Request) {
	kind, err := brandingKindParam(r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	asset, err := s.repository.GetBrandingAsset(r.Context(), kind)
	if err != nil {
		WriteError(w, r, storeError(err, "BRANDING_NOT_FOUND", "등록된 이미지가 없습니다."))
		return
	}
	etag := `"` + asset.Checksum + `"`
	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", asset.ContentType)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(asset.Content)
}

// readBrandingUpload accepts either a multipart file or a JSON body naming a
// source URL. Importing the bytes instead of linking to them keeps the image on
// this origin, so the content security policy stays closed to remote images and
// the branding survives the remote host going away.
func (s *Server) readBrandingUpload(w http.ResponseWriter, r *http.Request) ([]byte, string, error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(maxBrandingBytes); err != nil {
			return nil, "", Validation("업로드 파일을 읽지 못했습니다.", nil)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			return nil, "", Validation("업로드할 파일을 선택하세요.", nil)
		}
		defer file.Close()
		content, err := io.ReadAll(io.LimitReader(file, maxBrandingBytes+1))
		if err != nil {
			return nil, "", Validation("업로드 파일을 읽지 못했습니다.", nil)
		}
		if len(content) > maxBrandingBytes {
			return nil, "", Validation("이미지는 1MB 이하여야 합니다.", nil)
		}
		contentType, ok := normalizeImageContentType(header.Header.Get("Content-Type"))
		if !ok {
			contentType, ok = normalizeImageContentType(http.DetectContentType(content))
		}
		if !ok {
			return nil, "", Validation("PNG, JPEG, WebP, GIF, SVG 또는 ICO 이미지만 사용할 수 있습니다.", nil)
		}
		return content, contentType, nil
	}

	var input struct {
		SourceURL string `json:"sourceUrl"`
	}
	if err := DecodeJSON(w, r, &input); err != nil {
		return nil, "", err
	}
	source := strings.TrimSpace(input.SourceURL)
	parsed, err := url.Parse(source)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, "", Validation("http 또는 https로 시작하는 이미지 URL을 입력하세요.", nil)
	}
	content, contentType, err := s.fetchBrandingSource(r.Context(), source)
	if err != nil {
		return nil, "", err
	}
	return content, contentType, nil
}

func (s *Server) fetchBrandingSource(ctx context.Context, source string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, "", Validation("이미지 URL을 사용할 수 없습니다.", nil)
	}
	request.Header.Set("Accept", "image/*")
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, "", &APIError{
			Status: http.StatusBadGateway, Code: "BRANDING_FETCH_FAILED",
			Message: fmt.Sprintf("이미지를 가져오지 못했습니다: %v", err),
		}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", &APIError{
			Status: http.StatusBadGateway, Code: "BRANDING_FETCH_FAILED",
			Message: fmt.Sprintf("이미지 주소가 HTTP %d를 반환했습니다.", response.StatusCode),
		}
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxBrandingBytes+1))
	if err != nil {
		return nil, "", &APIError{
			Status: http.StatusBadGateway, Code: "BRANDING_FETCH_FAILED",
			Message: "이미지를 읽지 못했습니다.",
		}
	}
	if len(content) > maxBrandingBytes {
		return nil, "", Validation("이미지는 1MB 이하여야 합니다.", nil)
	}
	contentType, ok := normalizeImageContentType(response.Header.Get("Content-Type"))
	if !ok {
		contentType, ok = normalizeImageContentType(http.DetectContentType(content))
	}
	if !ok {
		return nil, "", Validation("해당 주소는 이미지가 아닙니다.", nil)
	}
	return content, contentType, nil
}

func (s *Server) adminUploadBranding(w http.ResponseWriter, r *http.Request) {
	kind, err := brandingKindParam(r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	content, contentType, err := s.readBrandingUpload(w, r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	asset, err := s.repository.SaveBrandingAsset(r.Context(), kind, contentType, content)
	if err != nil {
		WriteError(w, r, storeError(err, "BRANDING_NOT_FOUND", "이미지를 저장하지 못했습니다."))
		return
	}
	s.recordAudit(r, "branding.update", "branding", kind, nil, map[string]any{
		"contentType": asset.ContentType, "size": asset.Size,
	})
	WriteJSON(w, http.StatusOK, map[string]any{
		"kind": asset.Kind, "contentType": asset.ContentType, "size": asset.Size,
		"updatedAt": asset.UpdatedAt, "url": brandingURL(kind, asset.Checksum),
	})
}

func (s *Server) adminDeleteBranding(w http.ResponseWriter, r *http.Request) {
	kind, err := brandingKindParam(r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	if err := s.repository.DeleteBrandingAsset(r.Context(), kind); err != nil {
		WriteError(w, r, storeError(err, "BRANDING_NOT_FOUND", "등록된 이미지가 없습니다."))
		return
	}
	s.recordAudit(r, "branding.delete", "branding", kind, nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

// brandingURL points at this origin and changes whenever the bytes change, so a
// replaced logo is picked up without a hard refresh.
func brandingURL(kind, checksum string) string {
	version := checksum
	if len(version) > 12 {
		version = version[:12]
	}
	return "/api/v1/branding/" + kind + "?v=" + version
}
