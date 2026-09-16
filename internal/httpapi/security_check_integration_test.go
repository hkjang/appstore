package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/config"
	appcrypto "github.com/hkjang/appstore/internal/crypto"
	"github.com/hkjang/appstore/internal/database"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

// fakeSecCheck answers like the real service: one review, whose description
// and verdict the test controls.
type fakeSecCheck struct {
	description atomic.Value
	status      atomic.Value
	result      atomic.Value
	serviceName atomic.Value
	calls       atomic.Int64
}

func (f *fakeSecCheck) handler(reviewID string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer seccheck-read-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user":{"id":"svc","username":"appstore","display_name":"AppStore 연동","active":true,"roles":["AUDITOR"]}}`))
	})
	mux.HandleFunc("/api/v1/review-requests/"+reviewID, func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer seccheck-read-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		payload := map[string]any{
			"id": reviewID, "review_number": "SEC-2026-0007",
			"service_name": f.serviceName.Load(), "description": f.description.Load(),
			"status": f.status.Load(), "final_result": f.result.Load(),
			"approved_at":         "2026-09-15T02:00:00Z",
			"completion_blockers": map[string]int{"unreviewed_items": 0, "unverified_changes": 0, "stale_verdicts": 0},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})
	return mux
}

// TestPostgreSQLSecurityCheckIntegration walks the whole gate: an app is held
// as a draft, an approved SecCheck review releases it, and editing it after
// approval puts it back behind the gate.
func TestPostgreSQLSecurityCheckIntegration(t *testing.T) {
	dsn := os.Getenv("APPSTORE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("APPSTORE_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	repository := store.New(pool)
	box, err := appcrypto.NewSecretBox("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}

	reviewID := uuid.NewString()
	remote := &fakeSecCheck{}
	remote.serviceName.Store("Security Gate App")
	remote.status.Store("REVIEWING")
	remote.result.Store("")
	remote.description.Store("심의 요청입니다.")
	secCheck := httptest.NewServer(remote.handler(reviewID))
	defer secCheck.Close()

	originalSystem, err := repository.GetSystemSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	originalAPI, err := repository.GetAPISettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	originalWorkflow, err := repository.GetWorkflowConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		restore := context.Background()
		_, _ = repository.UpdateSystemSettings(restore, originalSystem, nil)
		_, _ = repository.UpdateAPISettings(restore, originalAPI, nil)
		_, _ = repository.UpdateWorkflowConfig(restore, originalWorkflow)
		_, _ = repository.UpdateSecurityCheckSettings(restore, model.SecurityCheckSettings{TimeoutSeconds: 10}, nil, nil)
	}()
	system := originalSystem
	system.PublicMode = true
	if _, err := repository.UpdateSystemSettings(ctx, system, nil); err != nil {
		t.Fatal(err)
	}
	apiSettings := originalAPI
	apiSettings.Enabled, apiSettings.Anonymous, apiSettings.RateLimitPerMinute = true, true, 500
	if _, err := repository.UpdateAPISettings(ctx, apiSettings, nil); err != nil {
		t.Fatal(err)
	}
	workflow := originalWorkflow
	workflow.Enabled, workflow.Levels = true, 1
	if _, err := repository.UpdateWorkflowConfig(ctx, workflow); err != nil {
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
	send := func(method, target string, body string, headers map[string]string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, strings.NewReader(body))
		request.RemoteAddr = "203.0.113.20:2020"
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	login := send(http.MethodPost, "/api/v1/auth/bootstrap/login",
		`{"username":"bootstrap-admin","password":"initial-bootstrap-password"}`,
		map[string]string{"Content-Type": "application/json"})
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	var session sessionResponse
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	result := login.Result()
	defer result.Body.Close()
	cookies := make([]string, 0, len(result.Cookies()))
	for _, cookie := range result.Cookies() {
		cookies = append(cookies, cookie.Name+"="+cookie.Value)
	}
	auth := map[string]string{
		"Content-Type": "application/json",
		"Cookie":       strings.Join(cookies, "; "), "X-CSRF-Token": session.CSRFToken,
	}

	// The administrator points AppStore at SecCheck and makes the review
	// mandatory.
	settings := `{"enabled":true,"baseUrl":"` + secCheck.URL + `","apiKey":"seccheck-read-key","timeoutSeconds":10}`
	if response := send(http.MethodPut, "/api/v1/admin/security-check", settings, auth); response.Code != http.StatusOK {
		t.Fatalf("settings status=%d body=%s", response.Code, response.Body.String())
	}
	if response := send(http.MethodPost, "/api/v1/admin/security-check/test", "", auth); response.Code != http.StatusOK {
		t.Fatalf("connection test status=%d body=%s", response.Code, response.Body.String())
	}
	// The stored credential must never come back out.
	stored := send(http.MethodGet, "/api/v1/admin/security-check", "", auth)
	if strings.Contains(stored.Body.String(), "seccheck-read-key") {
		t.Fatalf("the API key was served back: %s", stored.Body.String())
	}

	categories, err := repository.ListCategories(ctx, true)
	if err != nil || len(categories) == 0 {
		t.Fatalf("categories = %#v err=%v", categories, err)
	}
	slug := "security-gate-" + strings.ToLower(uuid.NewString()[:8])
	create := `{"name":"Security Gate App","slug":"` + slug + `","summary":"보안 심의 게이트 확인","description":"보안 심의 게이트 확인용 앱입니다.","icon":"SG","serviceUrl":"https://gate.example.internal","categoryId":"` + categories[0].ID.String() + `","tags":[],"screenshots":[],"language":"Go","framework":"React","supportsMcp":false,"supportsApi":true,"team":"Platform","version":"1.0.0","visibility":"public"}`
	created := send(http.MethodPost, "/api/v1/apps", create, auth)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var app model.App
	if err := json.Unmarshal(created.Body.Bytes(), &app); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.DeleteApp(context.Background(), app.ID) }()
	// The point of the feature: registration stops at draft rather than
	// entering the review queue.
	if app.Status != model.AppStatusDraft || app.SecurityVerified {
		t.Fatalf("a new app skipped the security gate: status=%s verified=%v", app.Status, app.SecurityVerified)
	}
	if response := send(http.MethodPost, "/api/v1/apps/"+app.ID.String()+"/submit", "", auth); response.Code != http.StatusConflict {
		t.Fatalf("submit without approval status=%d body=%s", response.Code, response.Body.String())
	}

	view := send(http.MethodGet, "/api/v1/apps/"+app.ID.String()+"/security-check", "", auth)
	if view.Code != http.StatusOK {
		t.Fatalf("security view status=%d body=%s", view.Code, view.Body.String())
	}
	var check securityCheckView
	if err := json.Unmarshal(view.Body.Bytes(), &check); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(check.BindingText, app.ID.String()) || check.Verified {
		t.Fatalf("security view = %#v", check)
	}

	verify := `{"reviewId":"` + reviewID + `"}`
	// A review that is bound but still being reviewed is recorded, not accepted.
	remote.description.Store("심의 요청입니다.\n" + check.BindingText)
	pending := send(http.MethodPost, "/api/v1/apps/"+app.ID.String()+"/security-check/verify", verify, auth)
	if pending.Code != http.StatusConflict {
		t.Fatalf("unapproved verify status=%d body=%s", pending.Code, pending.Body.String())
	}

	// A review for a different service must not pass, however approved.
	remote.status.Store("APPROVED")
	remote.result.Store("APPROVED")
	remote.serviceName.Store("Another Service")
	if response := send(http.MethodPost, "/api/v1/apps/"+app.ID.String()+"/security-check/verify", verify, auth); response.Code != http.StatusConflict {
		t.Fatalf("service mismatch status=%d body=%s", response.Code, response.Body.String())
	}
	// Nor must an approved review that never carried this app's binding.
	remote.serviceName.Store("Security Gate App")
	remote.description.Store("다른 서비스의 심의입니다.")
	if response := send(http.MethodPost, "/api/v1/apps/"+app.ID.String()+"/security-check/verify", verify, auth); response.Code != http.StatusConflict {
		t.Fatalf("binding mismatch status=%d body=%s", response.Code, response.Body.String())
	}

	remote.description.Store("심의 요청입니다.\n" + check.BindingText)
	approved := send(http.MethodPost, "/api/v1/apps/"+app.ID.String()+"/security-check/verify", verify, auth)
	if approved.Code != http.StatusOK {
		t.Fatalf("approved verify status=%d body=%s", approved.Code, approved.Body.String())
	}
	if err := json.Unmarshal(approved.Body.Bytes(), &check); err != nil {
		t.Fatal(err)
	}
	if !check.Verified {
		t.Fatalf("an approved bound review did not clear the app: %#v", check)
	}

	submitted := send(http.MethodPost, "/api/v1/apps/"+app.ID.String()+"/submit", "", auth)
	if submitted.Code != http.StatusOK {
		t.Fatalf("submit status=%d body=%s", submitted.Code, submitted.Body.String())
	}
	if err := json.Unmarshal(submitted.Body.Bytes(), &app); err != nil {
		t.Fatal(err)
	}
	if app.Status != model.AppStatusPending || !app.SecurityVerified {
		t.Fatalf("submitted app = status:%s verified:%v", app.Status, app.SecurityVerified)
	}

	// Editing the app is what the challenge protects: the approval covered the
	// old content, so the label goes and the gate closes again.
	edit := strings.Replace(create, "https://gate.example.internal", "https://moved.example.internal", 1)
	edited := send(http.MethodPut, "/api/v1/apps/"+app.ID.String(), edit, auth)
	if edited.Code != http.StatusOK {
		t.Fatalf("edit status=%d body=%s", edited.Code, edited.Body.String())
	}
	if err := json.Unmarshal(edited.Body.Bytes(), &app); err != nil {
		t.Fatal(err)
	}
	if app.SecurityVerified {
		t.Fatal("an edited app kept its security approval")
	}
	// The same approved review no longer matches, because the binding it
	// carries names content the app no longer has.
	if response := send(http.MethodPost, "/api/v1/apps/"+app.ID.String()+"/security-check/verify", verify, auth); response.Code != http.StatusConflict {
		t.Fatalf("stale binding status=%d body=%s", response.Code, response.Body.String())
	}
	if response := send(http.MethodPost, "/api/v1/apps/"+app.ID.String()+"/submit", "", auth); response.Code != http.StatusConflict {
		t.Fatalf("submit after edit status=%d body=%s", response.Code, response.Body.String())
	}
	// An administrator cannot publish around the gate either.
	if response := send(http.MethodPut, "/api/v1/admin/apps/"+app.ID.String()+"/status", `{"status":"published"}`, auth); response.Code == http.StatusOK {
		t.Fatalf("an administrator published an unapproved app: %s", response.Body.String())
	}
	if remote.calls.Load() == 0 {
		t.Fatal("SecCheck was never asked")
	}

	// Switching enforcement off stops the gate but does not erase what SecCheck
	// already approved: an app that is cleared keeps saying so.
	remote.description.Store("심의 요청입니다.\n" + check.BindingText)
	restored := send(http.MethodGet, "/api/v1/apps/"+app.ID.String()+"/security-check", "", auth)
	if restored.Code != http.StatusOK {
		t.Fatalf("security view status=%d", restored.Code)
	}
	if err := json.Unmarshal(restored.Body.Bytes(), &check); err != nil {
		t.Fatal(err)
	}
	verify = `{"reviewId":"` + reviewID + `"}`
	remote.description.Store("심의 요청입니다.\n" + check.BindingText)
	if response := send(http.MethodPost, "/api/v1/apps/"+app.ID.String()+"/security-check/verify", verify, auth); response.Code != http.StatusOK {
		t.Fatalf("re-verify after edit status=%d body=%s", response.Code, response.Body.String())
	}
	off := `{"enabled":false,"baseUrl":"` + secCheck.URL + `","apiKey":"seccheck-read-key","timeoutSeconds":10}`
	if response := send(http.MethodPut, "/api/v1/admin/security-check", off, auth); response.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", response.Code, response.Body.String())
	}
	fetched, err := repository.GetAppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !fetched.SecurityVerified {
		t.Fatal("turning enforcement off dropped an approval the organisation already had")
	}
}
