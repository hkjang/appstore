package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// TestPostgreSQLReviewDetailIntegration checks what a reviewer is given to
// decide on, and that the note they leave with either decision is kept.
func TestPostgreSQLReviewDetailIntegration(t *testing.T) {
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

	originalWorkflow, err := repository.GetWorkflowConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	originalAPI, err := repository.GetAPISettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		restore := context.Background()
		_, _ = repository.UpdateWorkflowConfig(restore, originalWorkflow)
		_, _ = repository.UpdateAPISettings(restore, originalAPI, nil)
	}()
	workflow := originalWorkflow
	workflow.Enabled, workflow.Levels, workflow.AutoPublish = true, 1, true
	// The bootstrap administrator submits and reviews in this test.
	workflow.PreventSelfApproval = false
	if _, err := repository.UpdateWorkflowConfig(ctx, workflow); err != nil {
		t.Fatal(err)
	}
	apiSettings := originalAPI
	apiSettings.Enabled, apiSettings.Anonymous, apiSettings.RateLimitPerMinute = true, true, 500
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
	send := func(method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, strings.NewReader(body))
		request.RemoteAddr = "203.0.113.30:3030"
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

	categories, err := repository.ListCategories(ctx, true)
	if err != nil || len(categories) == 0 {
		t.Fatalf("categories = %#v err=%v", categories, err)
	}
	slug := "review-detail-" + strings.ToLower(uuid.NewString()[:8])
	create := `{"name":"Review Detail App","slug":"` + slug + `","summary":"검토 화면 확인","description":"검토자가 보는 내용 확인용입니다.","icon":"RD","serviceUrl":"https://review.example.internal","categoryId":"` + categories[0].ID.String() + `","tags":["Review","Detail"],"screenshots":[],"language":"Go","framework":"React","supportsMcp":true,"supportsApi":true,"team":"Platform","version":"1.2.3","visibility":"public"}`
	created := send(http.MethodPost, "/api/v1/apps", create, auth)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var app model.App
	if err := json.Unmarshal(created.Body.Bytes(), &app); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.DeleteApp(context.Background(), app.ID) }()
	if app.Status != model.AppStatusPending {
		t.Fatalf("app status = %s, want pending_review", app.Status)
	}

	reviews, err := repository.ListReviews(ctx, store.ReviewListOptions{Status: "pending", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var reviewID uuid.UUID
	for _, review := range reviews.Items {
		if review.AppID == app.ID {
			reviewID = review.ID
		}
	}
	if reviewID == uuid.Nil {
		t.Fatal("the submitted app produced no pending review")
	}

	// The detail carries the app itself, not only the queue row.
	detail := send(http.MethodGet, "/api/v1/reviews/"+reviewID.String(), "", auth)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	var payload struct {
		model.Review
		App           *model.App          `json:"app"`
		Documents     []model.AppDocument `json:"documents"`
		SecurityCheck *struct {
			Verified    bool   `json:"verified"`
			BindingText string `json:"bindingText"`
		} `json:"securityCheck"`
		History []model.Review `json:"history"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ID != reviewID || payload.Status != "pending" {
		t.Fatalf("detail review = %#v", payload.Review)
	}
	if payload.App == nil || payload.App.ID != app.ID || payload.App.Description == "" ||
		payload.App.ServiceURL == "" || len(payload.App.Tags) != 2 {
		t.Fatalf("detail app = %#v", payload.App)
	}
	if payload.Documents == nil {
		t.Fatal("documents must be a list even when empty")
	}
	if payload.SecurityCheck == nil {
		t.Fatal("the security state is missing from the review detail")
	}
	// The binding block is the owner's to paste; a reviewer has no use for it.
	if payload.SecurityCheck.BindingText != "" {
		t.Fatal("the review detail leaked the security binding block")
	}
	if len(payload.History) != 1 || payload.History[0].ID != reviewID {
		t.Fatalf("history = %#v", payload.History)
	}

	// A rejection keeps the reviewer's note, and the owner reads it back.
	rejected := send(http.MethodPost, "/api/v1/reviews/"+reviewID.String()+"/reject",
		`{"comment":"서비스 URL이 사내에서 열리지 않습니다.\n담당팀 확인 후 다시 제출하세요."}`, auth)
	if rejected.Code != http.StatusOK {
		t.Fatalf("reject status=%d body=%s", rejected.Code, rejected.Body.String())
	}
	latest, err := repository.LatestReviewsByApp(ctx, []uuid.UUID{app.ID})
	if err != nil || !strings.Contains(latest[app.ID].Reason, "사내에서 열리지 않습니다") {
		t.Fatalf("stored rejection = %#v err=%v", latest[app.ID], err)
	}

	// Re-submitting produces a second review, and the previous decision is in
	// the history the next reviewer reads.
	resubmitted := send(http.MethodPost, "/api/v1/apps/"+app.ID.String()+"/submit", "", auth)
	if resubmitted.Code != http.StatusOK {
		t.Fatalf("resubmit status=%d body=%s", resubmitted.Code, resubmitted.Body.String())
	}
	reviews, err = repository.ListReviews(ctx, store.ReviewListOptions{Status: "pending", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var secondID uuid.UUID
	for _, review := range reviews.Items {
		if review.AppID == app.ID {
			secondID = review.ID
		}
	}
	if secondID == uuid.Nil || secondID == reviewID {
		t.Fatal("the resubmission produced no new review")
	}
	second := send(http.MethodGet, "/api/v1/reviews/"+secondID.String(), "", auth)
	if err := json.Unmarshal(second.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.History) != 2 {
		t.Fatalf("history after resubmission = %d entries", len(payload.History))
	}
	var carried bool
	for _, item := range payload.History {
		if item.ID == reviewID && strings.Contains(item.Reason, "사내에서 열리지 않습니다") {
			carried = true
		}
	}
	if !carried {
		t.Fatalf("the earlier rejection note is missing from the history: %#v", payload.History)
	}

	// An approval carries a note too, and it is stored with the decision.
	approved := send(http.MethodPost, "/api/v1/reviews/"+secondID.String()+"/approve",
		`{"comment":"사내망에서 접속 확인했습니다."}`, auth)
	if approved.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approved.Code, approved.Body.String())
	}
	var decided model.Review
	if err := json.Unmarshal(approved.Body.Bytes(), &decided); err != nil {
		t.Fatal(err)
	}
	if decided.Status != "approved" || decided.Reason != "사내망에서 접속 확인했습니다." {
		t.Fatalf("approved review = %#v", decided)
	}
	if decided.ReviewerName == "" || decided.DecidedAt == nil {
		t.Fatalf("the decision did not record who and when: %#v", decided)
	}
	// Approving with no body at all still works, which older clients rely on.
	third := send(http.MethodPost, "/api/v1/reviews/"+secondID.String()+"/approve", "", auth)
	if third.Code != http.StatusConflict {
		t.Fatalf("second decision on the same review status=%d", third.Code)
	}
}
