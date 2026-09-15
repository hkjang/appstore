package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/config"
	appcrypto "github.com/hkjang/appstore/internal/crypto"
	"github.com/hkjang/appstore/internal/database"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

// TestPostgreSQLAppDocumentHTTPIntegration walks a guide file through the API
// the way the product does: an owner attaches it on the app form, and a
// signed-out visitor downloads it from the app detail page.
func TestPostgreSQLAppDocumentHTTPIntegration(t *testing.T) {
	dsn := os.Getenv("APPSTORE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("APPSTORE_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Initialize(ctx, config.Config{
		PostgresDSN: dsn, BootstrapAdmin: "bootstrap-admin",
		BootstrapAdminPassword: "initial-bootstrap-password",
		EncryptionKey:          "01234567890123456789012345678901",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	lockConnection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockConnection.Exec(ctx, `SELECT pg_advisory_lock($1)`, int64(0x41505053544f5245)); err != nil {
		lockConnection.Release()
		t.Fatal(err)
	}
	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer unlockCancel()
		_, _ = lockConnection.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, int64(0x41505053544f5245))
		lockConnection.Release()
	}()
	repository := store.New(pool)
	box, err := appcrypto.NewSecretBox("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}

	originalSystem, err := repository.GetSystemSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	originalAPI, err := repository.GetAPISettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = repository.UpdateSystemSettings(context.Background(), originalSystem, nil)
		_, _ = repository.UpdateAPISettings(context.Background(), originalAPI, nil)
	}()
	systemSettings := originalSystem
	systemSettings.PublicMode = true
	if _, err := repository.UpdateSystemSettings(ctx, systemSettings, nil); err != nil {
		t.Fatal(err)
	}
	apiSettings := originalAPI
	apiSettings.Enabled = true
	apiSettings.Anonymous = true
	apiSettings.RateLimitPerMinute = 200
	if _, err := repository.UpdateAPISettings(ctx, apiSettings, nil); err != nil {
		t.Fatal(err)
	}

	service, err := New(repository, box, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := service.Handler()
	if err != nil {
		t.Fatal(err)
	}
	send := func(method, target string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, bytes.NewReader(body))
		request.RemoteAddr = "203.0.113.9:1009"
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	login := send(http.MethodPost, "/api/v1/auth/bootstrap/login",
		[]byte(`{"username":"bootstrap-admin","password":"initial-bootstrap-password"}`),
		map[string]string{"Content-Type": "application/json"})
	if login.Code != http.StatusOK {
		t.Fatalf("bootstrap login status=%d body=%s", login.Code, login.Body.String())
	}
	var session sessionResponse
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.CSRFToken == "" {
		t.Fatalf("bootstrap session = %#v err=%v", session, err)
	}
	result := login.Result()
	defer result.Body.Close()
	cookies := make([]string, 0, len(result.Cookies()))
	for _, cookie := range result.Cookies() {
		cookies = append(cookies, cookie.Name+"="+cookie.Value)
	}
	owner := map[string]string{
		"Cookie": strings.Join(cookies, "; "), "X-CSRF-Token": session.CSRFToken,
	}

	categories, err := repository.ListCategories(ctx, true)
	if err != nil || len(categories) == 0 {
		t.Fatalf("categories = %#v err=%v", categories, err)
	}
	slug := "document-integration-" + strings.ToLower(uuid.NewString()[:8])
	app, err := repository.CreateApp(ctx, nil, model.AppInput{
		Name: "Document Integration", Slug: slug, Summary: "guide download",
		Description: "guide download", ServiceURL: "https://service.example.internal",
		CategoryID: categories[0].ID.String(), Visibility: "public",
	}, model.AppStatusPublished)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.DeleteApp(context.Background(), app.ID) }()

	upload := func(fileName string, content []byte) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("title", "운영 가이드"); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		headers := map[string]string{"Content-Type": writer.FormDataContentType()}
		for key, value := range owner {
			headers[key] = value
		}
		return send(http.MethodPost, "/api/v1/apps/"+app.ID.String()+"/documents", body.Bytes(), headers)
	}

	content := []byte("%PDF-1.4 integration guide")
	created := upload("운영 가이드.pdf", content)
	if created.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", created.Code, created.Body.String())
	}
	var document model.AppDocument
	if err := json.Unmarshal(created.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Title != "운영 가이드" || document.ContentType != "application/pdf" ||
		document.Size != len(content) || document.DownloadURL == "" {
		t.Fatalf("created document = %#v", document)
	}
	if rejected := upload("setup.exe", content); rejected.Code != http.StatusUnprocessableEntity {
		t.Fatalf("executable upload status=%d body=%s", rejected.Code, rejected.Body.String())
	}
	if duplicate := upload("운영 가이드.pdf", content); duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate upload status=%d body=%s", duplicate.Code, duplicate.Body.String())
	}

	// The whole point of the feature: no session, no cookie, the slug from the
	// store front, and the file comes back.
	listed := send(http.MethodGet, "/api/v1/apps/"+slug+"/documents", nil, nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("anonymous listing status=%d body=%s", listed.Code, listed.Body.String())
	}
	var listing struct {
		Items []model.AppDocument `json:"items"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Items) != 1 || listing.Items[0].ID != document.ID {
		t.Fatalf("anonymous listing = %#v", listing.Items)
	}
	download := send(http.MethodGet, listing.Items[0].DownloadURL, nil, nil)
	if download.Code != http.StatusOK || !bytes.Equal(download.Body.Bytes(), content) {
		t.Fatalf("anonymous download status=%d body=%q", download.Code, download.Body.String())
	}
	disposition := download.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disposition, "attachment;") ||
		!strings.Contains(disposition, url.PathEscape("운영 가이드.pdf")) {
		t.Fatalf("content disposition = %q", disposition)
	}
	if download.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("download headers = %#v", download.Header())
	}

	// An app the catalog does not show yet keeps its guides to itself, and says
	// so the same way a missing app does.
	if _, err := repository.SetAppStatus(ctx, app.ID, model.AppStatusDraft); err != nil {
		t.Fatal(err)
	}
	if hidden := send(http.MethodGet, "/api/v1/apps/"+slug+"/documents", nil, nil); hidden.Code != http.StatusNotFound {
		t.Fatalf("draft app listing status=%d body=%s", hidden.Code, hidden.Body.String())
	}
	if hidden := send(http.MethodGet, document.DownloadURL, nil, nil); hidden.Code != http.StatusNotFound {
		t.Fatalf("draft app download status=%d body=%s", hidden.Code, hidden.Body.String())
	}
	if allowed := send(http.MethodGet, "/api/v1/apps/"+slug+"/documents", nil, owner); allowed.Code != http.StatusOK {
		t.Fatalf("administrator listing of a draft app status=%d body=%s", allowed.Code, allowed.Body.String())
	}

	removed := send(http.MethodDelete, document.DownloadURL, nil, owner)
	if removed.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", removed.Code, removed.Body.String())
	}
	remaining, err := repository.ListAppDocuments(ctx, app.ID)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("remaining documents = %#v err=%v", remaining, err)
	}
}
