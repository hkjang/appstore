package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/seccheck"
	"github.com/hkjang/appstore/internal/store"
)

// publicSecurityCheckSettings strips the stored credential and says only
// whether one exists. The ciphertext never leaves the server.
func publicSecurityCheckSettings(settings model.SecurityCheckSettings) model.SecurityCheckSettings {
	settings.APIKeySet = settings.APIKeyEncrypted != ""
	settings.APIKeyEncrypted = ""
	return settings
}

func (s *Server) adminSecurityCheck(w http.ResponseWriter, r *http.Request) {
	settings, err := s.repository.GetSecurityCheckSettings(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, publicSecurityCheckSettings(settings))
}

func (s *Server) adminUpdateSecurityCheck(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled        bool   `json:"enabled"`
		BaseURL        string `json:"baseUrl"`
		APIKey         string `json:"apiKey"`
		TimeoutSeconds int    `json:"timeoutSeconds"`
	}
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	before, err := s.repository.GetSecurityCheckSettings(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	input.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	if input.TimeoutSeconds == 0 {
		input.TimeoutSeconds = before.TimeoutSeconds
	}
	if input.TimeoutSeconds < 1 || input.TimeoutSeconds > 60 {
		WriteError(w, r, Validation("연결 제한 시간은 1~60초로 입력하세요.", nil))
		return
	}
	if input.BaseURL != "" && seccheck.ValidateBaseURL(input.BaseURL) != nil {
		WriteError(w, r, Validation("SecCheck 주소는 인증정보와 쿼리가 없는 http(s) 주소여야 합니다.", nil))
		return
	}
	key := strings.TrimSpace(input.APIKey)
	if strings.ContainsAny(key, "\r\n\x00") || len(key) > 4096 {
		WriteError(w, r, Validation("SecCheck API Key 형식을 확인하세요.", nil))
		return
	}
	if input.Enabled && (input.BaseURL == "" || (key == "" && before.APIKeyEncrypted == "")) {
		WriteError(w, r, Validation("보안 심의를 필수로 적용하려면 SecCheck 주소와 조회용 API Key를 먼저 등록하세요.", nil))
		return
	}
	// A new address must never be handed the previous address's credential.
	if input.BaseURL != before.BaseURL && input.BaseURL != "" && before.APIKeyEncrypted != "" && key == "" {
		WriteError(w, r, Validation("SecCheck 주소를 바꿀 때는 그 서비스의 API Key도 함께 입력하세요.", nil))
		return
	}
	var encrypted *string
	// Re-typing the same key is not a new connection. Encrypting it again would
	// produce different ciphertext, move the revision, and silently retire every
	// approval the organisation has — so the comparison is on the plaintext.
	if key != "" && key != s.storedSecurityCheckKey(before) {
		value, err := s.box.Encrypt(key)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		encrypted = &value
	}
	after, err := s.repository.UpdateSecurityCheckSettings(r.Context(), model.SecurityCheckSettings{
		Enabled: input.Enabled, BaseURL: input.BaseURL, TimeoutSeconds: input.TimeoutSeconds,
	}, encrypted, &CurrentPrincipal(r.Context()).User.ID)
	if err != nil {
		WriteError(w, r, storeError(err, "SECURITY_CHECK_NOT_FOUND", "보안 심의 설정을 저장하지 못했습니다."))
		return
	}
	s.recordAudit(r, "security_check.settings.update", "security_check", "default",
		publicSecurityCheckSettings(before), publicSecurityCheckSettings(after))
	WriteJSON(w, http.StatusOK, publicSecurityCheckSettings(after))
}

// storedSecurityCheckKey returns the credential in use, or an empty string
// when none is stored or it cannot be read.
func (s *Server) storedSecurityCheckKey(settings model.SecurityCheckSettings) string {
	if settings.APIKeyEncrypted == "" {
		return ""
	}
	key, err := s.box.Decrypt(settings.APIKeyEncrypted)
	if err != nil {
		return ""
	}
	return key
}

func (s *Server) adminTestSecurityCheck(w http.ResponseWriter, r *http.Request) {
	settings, err := s.repository.GetSecurityCheckSettings(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	client, err := s.securityCheckClient(settings)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	identity, err := client.TestConnection(r.Context())
	if err != nil {
		WriteError(w, r, securityCheckError(err))
		return
	}
	s.recordAudit(r, "security_check.connection.test", "security_check", "default", nil,
		map[string]any{"account": identity.Username})
	WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "account": identity.Username, "displayName": identity.DisplayName, "roles": identity.Roles,
	})
}

func (s *Server) securityCheckClient(settings model.SecurityCheckSettings) (*seccheck.Client, error) {
	if settings.BaseURL == "" || settings.APIKeyEncrypted == "" {
		return nil, &APIError{Status: http.StatusServiceUnavailable, Code: "SECURITY_CHECK_NOT_CONFIGURED",
			Message: "관리자가 SecCheck 주소와 API Key를 먼저 등록해야 합니다."}
	}
	key, err := s.box.Decrypt(settings.APIKeyEncrypted)
	if err != nil {
		return nil, &APIError{Status: http.StatusServiceUnavailable, Code: "SECURITY_CHECK_CREDENTIAL_UNAVAILABLE",
			Message: "SecCheck 인증정보를 읽지 못했습니다. 관리자에게 문의하세요."}
	}
	client, err := seccheck.NewClient(seccheck.Config{
		BaseURL: settings.BaseURL, APIKey: key, Timeout: time.Duration(settings.TimeoutSeconds) * time.Second,
	}, nil)
	if err != nil {
		return nil, securityCheckError(err)
	}
	return client, nil
}

// securityCheckError turns a client reason into an answer the reader can act
// on. A provider problem is 502; what the owner has to fix is 409.
func securityCheckError(err error) error {
	var api *APIError
	if errors.As(err, &api) {
		return api
	}
	var provider *seccheck.Error
	if !errors.As(err, &provider) {
		return &APIError{Status: http.StatusBadGateway, Code: "SECURITY_CHECK_FAILED",
			Message: "SecCheck 결과를 확인하지 못했습니다. 잠시 후 다시 시도하세요."}
	}
	status := http.StatusBadGateway
	switch provider.Reason {
	case seccheck.ReasonInvalidReviewID, seccheck.ReasonNotFound:
		status = http.StatusUnprocessableEntity
	case seccheck.ReasonBindingMismatch, seccheck.ReasonServiceMismatch,
		seccheck.ReasonNotApproved, seccheck.ReasonIncomplete:
		status = http.StatusConflict
	}
	return &APIError{Status: status, Code: "SECURITY_CHECK_" + strings.ToUpper(string(provider.Reason)), Message: provider.Error()}
}

func securityCheckRequired() error {
	return &APIError{Status: http.StatusConflict, Code: "SECURITY_CHECK_REQUIRED",
		Message: "이 앱의 현재 내용에 대한 SecCheck 최종 승인이 확인되지 않았습니다. 보안 심의를 마친 뒤 제출하세요."}
}

type securityCheckView struct {
	Enabled      bool       `json:"enabled"`
	Configured   bool       `json:"configured"`
	AppID        uuid.UUID  `json:"appId"`
	AppName      string     `json:"appName"`
	AppStatus    string     `json:"appStatus"`
	BindingText  string     `json:"bindingText"`
	NewReviewURL string     `json:"newReviewUrl,omitempty"`
	ReviewURL    string     `json:"reviewUrl,omitempty"`
	ReviewID     string     `json:"reviewId,omitempty"`
	ReviewNumber string     `json:"reviewNumber,omitempty"`
	Status       string     `json:"status"`
	FinalResult  string     `json:"finalResult,omitempty"`
	ApprovedAt   *time.Time `json:"approvedAt,omitempty"`
	CheckedAt    *time.Time `json:"checkedAt,omitempty"`
	Verified     bool       `json:"verified"`
}

// appSecurityBinding is the block an owner pastes into the SecCheck review
// description. SecCheck has no field for "which AppStore app is this", so the
// description carries the app's identity, the unpredictable challenge, and a
// hash of the content that was approved. None of it can be guessed from the
// catalog, and it cannot be appended to a review that is already approved.
func appSecurityBinding(app model.App, nonce uuid.UUID) string {
	input := appBindingInput(app)
	encoded, _ := json.Marshal(input)
	sum := sha256.Sum256(encoded)
	quote := func(value string) string {
		raw, _ := json.Marshal(value)
		return string(raw)
	}
	return fmt.Sprintf("%s\napp_id=%s\nchallenge=%s\nname=%s\nslug=%s\nservice_url=%s\nversion=%s\ncontent_sha256=%s\n%s",
		seccheck.BindingStart, app.ID, nonce, quote(app.Name), quote(app.Slug),
		quote(app.ServiceURL), quote(app.Version), hex.EncodeToString(sum[:]), seccheck.BindingEnd)
}

func appBindingInput(app model.App) model.AppInput {
	return model.AppInput{
		Name: app.Name, Slug: app.Slug, Summary: app.Summary, Description: app.Description,
		Icon: app.Icon, Gradient: app.Gradient, ServiceURL: app.ServiceURL, CategoryID: app.CategoryID.String(),
		Tags: app.Tags, Screenshots: app.Screenshots, Language: app.Language, Framework: app.Framework,
		SupportsMCP: app.SupportsMCP, SupportsAPI: app.SupportsAPI, Team: app.Team,
		Version: app.Version, Visibility: app.Visibility,
	}
}

func buildSecurityCheckView(app model.App, state model.SecurityCheckState, settings model.SecurityCheckSettings) securityCheckView {
	base := strings.TrimRight(settings.BaseURL, "/")
	view := securityCheckView{
		Enabled: settings.Enabled, Configured: base != "" && settings.APIKeyEncrypted != "",
		AppID: app.ID, AppName: app.Name, AppStatus: app.Status,
		BindingText: appSecurityBinding(app, state.ChallengeNonce),
		ReviewID:    state.ReviewID, ReviewNumber: state.ReviewNumber, Status: state.RemoteStatus,
		FinalResult: state.FinalResult, ApprovedAt: state.ApprovedAt, CheckedAt: state.CheckedAt,
		Verified: state.Verified,
	}
	if view.Status == "" {
		view.Status = "UNVERIFIED"
	}
	if base != "" {
		view.NewReviewURL = base + "/reviews/new"
		if state.ReviewID != "" {
			view.ReviewURL = base + "/reviews/" + state.ReviewID
		}
	}
	return view
}

// ownedSecurityApp answers 404 rather than 403 for an app the caller may not
// see, so the endpoint cannot be used to discover other people's apps.
func (s *Server) ownedSecurityApp(w http.ResponseWriter, r *http.Request) (model.App, bool) {
	id, ok := appIDParam(w, r)
	if !ok {
		return model.App{}, false
	}
	app, err := s.repository.GetAppByID(r.Context(), id)
	if err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
		return model.App{}, false
	}
	principal := CurrentPrincipal(r.Context())
	if !ownsApp(principal, app) && !principal.Can("apps:manage") {
		WriteError(w, r, NotFound("APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
		return model.App{}, false
	}
	return app, true
}

func (s *Server) appSecurityCheck(w http.ResponseWriter, r *http.Request) {
	app, ok := s.ownedSecurityApp(w, r)
	if !ok {
		return
	}
	state, settings, err := s.appSecurityState(r.Context(), app.ID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, buildSecurityCheckView(app, state, settings))
}

func (s *Server) appSecurityState(ctx context.Context, id uuid.UUID) (model.SecurityCheckState, model.SecurityCheckSettings, error) {
	settings, err := s.repository.GetSecurityCheckSettings(ctx)
	if err != nil {
		return model.SecurityCheckState{}, settings, err
	}
	state, err := s.repository.GetAppSecurityCheck(ctx, id)
	if err != nil {
		return state, settings, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다.")
	}
	return state, settings, nil
}

// verifyAppSecurityCheck reads the review from SecCheck and records what it
// says. The review identifier is the only thing the owner supplies; everything
// that decides the verdict comes from SecCheck over the server's own
// authenticated connection.
func (s *Server) verifyAppSecurityCheck(w http.ResponseWriter, r *http.Request) {
	app, ok := s.ownedSecurityApp(w, r)
	if !ok {
		return
	}
	var input struct {
		ReviewID string `json:"reviewId"`
	}
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	state, settings, err := s.refreshAppSecurityCheck(r.Context(), app.ID, strings.TrimSpace(input.ReviewID))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	app, err = s.repository.GetAppByID(r.Context(), app.ID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	s.recordAudit(r, "security_check.verify", "app", app.ID.String(), nil, map[string]any{
		"reviewId": state.ReviewID, "status": state.RemoteStatus,
		"result": state.FinalResult, "verified": state.Verified,
	})
	WriteJSON(w, http.StatusOK, buildSecurityCheckView(app, state, settings))
}

func (s *Server) refreshAppSecurityCheck(ctx context.Context, id uuid.UUID, reviewID string) (model.SecurityCheckState, model.SecurityCheckSettings, error) {
	state, settings, err := s.appSecurityState(ctx, id)
	if err != nil {
		return state, settings, err
	}
	if reviewID == "" {
		reviewID = state.ReviewID
	}
	if reviewID == "" {
		return state, settings, securityCheckRequired()
	}
	app, err := s.repository.GetAppByID(ctx, id)
	if err != nil {
		return state, settings, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다.")
	}
	client, err := s.securityCheckClient(settings)
	if err != nil {
		return state, settings, err
	}
	review, err := client.Fetch(ctx, reviewID)
	if err != nil {
		return state, settings, securityCheckError(err)
	}
	verifyErr := seccheck.Verify(review, seccheck.Expected{
		BindingText: appSecurityBinding(app, state.ChallengeNonce), ServiceName: app.Name,
	})
	// A review that is bound to this app but not approved yet is recorded so
	// the owner can watch its progress; anything else is refused outright.
	if verifyErr != nil && seccheck.Code(verifyErr) != seccheck.ReasonNotApproved && seccheck.Code(verifyErr) != seccheck.ReasonIncomplete {
		return state, settings, securityCheckError(verifyErr)
	}
	recorded, err := s.repository.RecordAppSecurityCheck(ctx, id, state.ChallengeNonce, settings.Revision, model.SecurityCheckResult{
		ReviewID: review.ID, ReviewNumber: review.Number, RemoteStatus: review.Status,
		FinalResult: review.FinalResult, ApprovedAt: review.ApprovedAt,
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return state, settings, &APIError{Status: http.StatusConflict, Code: "SECURITY_CHECK_APP_CHANGED",
				Message: "확인하는 동안 앱 정보나 연동 설정이 바뀌었습니다. 연동 정보를 다시 확인한 뒤 시도하세요."}
		}
		return state, settings, storeError(err, "APP_NOT_FOUND", "보안 심의 결과를 저장하지 못했습니다.")
	}
	if verifyErr != nil {
		return recorded, settings, securityCheckError(verifyErr)
	}
	return recorded, settings, nil
}

// submitApp is the owner's "제출" once security has cleared. Re-reading the
// remote review here keeps a cached badge from being the thing that publishes
// an app; the repository checks again inside its own transaction.
func (s *Server) submitApp(w http.ResponseWriter, r *http.Request) {
	app, ok := s.ownedSecurityApp(w, r)
	if !ok {
		return
	}
	if err := s.requireFreshSecurityCheck(r.Context(), app.ID); err != nil {
		WriteError(w, r, err)
		return
	}
	result, err := s.repository.SubmitApp(r.Context(), app.ID, CurrentPrincipal(r.Context()).User.ID)
	if err != nil {
		WriteError(w, r, submitError(err))
		return
	}
	s.recordAudit(r, "app.submit", "app", app.ID.String(), app, result)
	WriteJSON(w, http.StatusOK, result.App)
}

func submitError(err error) error {
	if errors.Is(err, store.ErrSecurityCheckRequired) {
		return securityCheckRequired()
	}
	return storeError(err, "APP_NOT_FOUND", "앱을 제출할 수 없습니다.")
}

func (s *Server) requireFreshSecurityCheck(ctx context.Context, id uuid.UUID) error {
	settings, err := s.repository.GetSecurityCheckSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return nil
	}
	state, _, err := s.refreshAppSecurityCheck(ctx, id, "")
	if err != nil {
		return err
	}
	if !state.Verified {
		return securityCheckRequired()
	}
	return nil
}

// submitOrHold is what registering and editing an app go through. With the
// security gate on and no approval yet, the app stays a draft and the owner is
// sent to the security screen instead of the request failing.
func (s *Server) submitOrHold(ctx context.Context, app model.App, userID uuid.UUID) (store.SubmitResult, error) {
	result, err := s.repository.SubmitApp(ctx, app.ID, userID)
	if errors.Is(err, store.ErrSecurityCheckRequired) {
		held, getErr := s.repository.GetAppByID(ctx, app.ID)
		if getErr != nil {
			return store.SubmitResult{}, getErr
		}
		return store.SubmitResult{App: held}, nil
	}
	return result, err
}
