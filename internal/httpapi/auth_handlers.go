package httpapi

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	appauth "github.com/hkjang/appstore/internal/auth"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

type bootstrapLoginInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type sessionResponse struct {
	Authenticated     bool        `json:"authenticated"`
	BootstrapRequired bool        `json:"bootstrapRequired"`
	OIDCConfigured    bool        `json:"oidcConfigured"`
	CSRFToken         string      `json:"csrfToken,omitempty"`
	User              *model.User `json:"user,omitempty"`
}

func (s *Server) sessionState(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.loadOIDCSettings(r)
	configured := settings.IssuerURL != "" && settings.ClientID != "" && settings.ClientSecretSet
	response := sessionResponse{OIDCConfigured: configured, BootstrapRequired: !configured}
	if principal := CurrentPrincipal(r.Context()); principal != nil {
		response.Authenticated = true
		response.User = &principal.User
		if cookie, err := r.Cookie(appauth.CSRFCookieName); err == nil && principal.AuthMethod == "session" {
			response.CSRFToken = cookie.Value
		}
	}
	WriteJSON(w, http.StatusOK, response)
}

func (s *Server) bootstrapLogin(w http.ResponseWriter, r *http.Request) {
	var input bootstrapLoginInput
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	if allowed, _ := s.loginLimiter.allow("bootstrap:"+clientAddress(r)+":"+strings.ToLower(strings.TrimSpace(input.Username)), 5, time.Now()); !allowed {
		w.Header().Set("Retry-After", "60")
		WriteError(w, r, &APIError{Status: http.StatusTooManyRequests, Code: "LOGIN_RATE_LIMITED", Message: "로그인 시도가 너무 많습니다. 잠시 후 다시 시도하세요."})
		return
	}
	credential, err := s.repository.GetBootstrapCredential(r.Context(), input.Username)
	if err != nil {
		_ = appauth.CheckPassword(s.dummyPasswordHash, input.Password)
		WriteError(w, r, Unauthorized("관리자 이름 또는 비밀번호가 올바르지 않습니다."))
		return
	}
	if !appauth.CheckPassword(credential.PasswordHash, input.Password) {
		WriteError(w, r, Unauthorized("관리자 이름 또는 비밀번호가 올바르지 않습니다."))
		return
	}
	material, err := s.createSession(r, credential.User)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	system := s.loadSystemSettings(r)
	appauth.SetSessionCookies(w, material, IsSecureRequest(r, system.SiteURL))
	user := credential.User
	s.recordUserAudit(r, user, "login.bootstrap")
	WriteJSON(w, http.StatusOK, sessionResponse{Authenticated: true, User: &user, CSRFToken: material.CSRF})
}

func (s *Server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	settings, err := s.loadOIDCSettings(r)
	if err != nil || !settings.Enabled || !settings.ClientSecretSet {
		WriteError(w, r, &APIError{Status: http.StatusServiceUnavailable, Code: "OIDC_NOT_CONFIGURED", Message: "SSO가 아직 설정되지 않았습니다."})
		return
	}
	redirectURL := s.oidcRedirectURL(r)
	request, err := s.oidc.Start(r.Context(), settings, redirectURL, r.URL.Query().Get("returnTo"))
	if err != nil {
		s.logger.WarnContext(r.Context(), "OIDC login start failed", "error", err, "request_id", RequestID(r.Context()))
		WriteError(w, r, &APIError{Status: http.StatusBadGateway, Code: "OIDC_UNAVAILABLE", Message: "SSO 공급자에 연결할 수 없습니다."})
		return
	}
	if err := s.repository.CreateOIDCAuthRequest(r.Context(), store.OIDCAuthRequest{
		StateHash: request.StateHash, Nonce: request.Nonce, Verifier: request.Verifier,
		ReturnTo: request.ReturnTo, ExpiresAt: request.ExpiresAt,
	}); err != nil {
		WriteError(w, r, err)
		return
	}
	http.Redirect(w, r, request.URL, http.StatusFound)
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if providerError := r.URL.Query().Get("error"); providerError != "" {
		WriteError(w, r, &APIError{Status: http.StatusUnauthorized, Code: "OIDC_DENIED", Message: "SSO 로그인이 취소되었거나 거부되었습니다."})
		return
	}
	state, code := r.URL.Query().Get("state"), r.URL.Query().Get("code")
	if state == "" || code == "" {
		WriteError(w, r, &APIError{Status: http.StatusBadRequest, Code: "OIDC_CALLBACK_INVALID", Message: "SSO 응답이 올바르지 않습니다."})
		return
	}
	stored, err := s.repository.ConsumeOIDCAuthRequest(r.Context(), s.box.Digest("oidc-state:"+state))
	if err != nil {
		WriteError(w, r, &APIError{Status: http.StatusBadRequest, Code: "OIDC_STATE_INVALID", Message: "SSO 요청이 만료되었거나 이미 사용되었습니다."})
		return
	}
	settings, err := s.loadOIDCSettings(r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	identity, err := s.oidc.Complete(r.Context(), settings, s.oidcRedirectURL(r), code, stored.Verifier, stored.Nonce)
	if err != nil {
		s.logger.WarnContext(r.Context(), "OIDC callback failed", "error", err, "request_id", RequestID(r.Context()))
		WriteError(w, r, &APIError{Status: http.StatusUnauthorized, Code: "OIDC_VERIFICATION_FAILED", Message: "SSO 응답을 검증할 수 없습니다."})
		return
	}
	user, err := s.repository.UpsertOIDCUser(r.Context(), store.OIDCUserInput{
		Subject: identity.Subject, Username: identity.Username, Email: identity.Email,
		DisplayName: identity.DisplayName, Team: identity.Team,
	})
	if err != nil {
		WriteError(w, r, storeError(err, "USER_NOT_FOUND", "사용자 정보를 저장할 수 없습니다."))
		return
	}
	availableRoles, err := s.repository.ListRoles(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	knownRoles := make(map[string]bool, len(availableRoles))
	for _, role := range availableRoles {
		knownRoles[role.Key] = true
	}
	roleKeys := []string{"user"}
	for _, role := range identity.Roles {
		if knownRoles[role] {
			roleKeys = append(roleKeys, role)
		}
	}
	user, err = s.repository.ReplaceUserRoles(r.Context(), user.ID, roleKeys)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	material, err := s.createSession(r, user)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	appauth.SetSessionCookies(w, material, IsSecureRequest(r, s.loadSystemSettings(r).SiteURL))
	s.recordUserAudit(r, user, "login.oidc")
	http.Redirect(w, r, appauth.SafeReturnTo(stored.ReturnTo), http.StatusFound)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	principal := CurrentPrincipal(r.Context())
	if principal != nil && len(principal.SessionHash) > 0 {
		_ = s.repository.DeleteSessionByTokenHash(r.Context(), principal.SessionHash)
		s.recordAudit(r, "logout", "session", "", nil, nil)
	}
	appauth.ClearSessionCookies(w, IsSecureRequest(r, s.loadSystemSettings(r).SiteURL))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createSession(r *http.Request, user model.User) (appauth.SessionMaterial, error) {
	material, err := appauth.NewSessionMaterial(s.box, time.Now().UTC())
	if err != nil {
		return appauth.SessionMaterial{}, err
	}
	host, _, splitErr := net.SplitHostPort(r.RemoteAddr)
	if splitErr != nil {
		host = r.RemoteAddr
	}
	_, err = s.repository.CreateSession(r.Context(), store.CreateSessionParams{
		UserID: user.ID, TokenHash: material.TokenHash, CSRFHash: material.CSRFHash,
		ExpiresAt: material.ExpiresAt, IP: truncateHTTPValue(host, 128), UserAgent: truncateHTTPValue(r.UserAgent(), 512),
	})
	return material, err
}

func (s *Server) loadOIDCSettings(r *http.Request) (model.OIDCSettings, error) {
	var settings model.OIDCSettings
	var roleMappings, groupMappings, scopes []byte
	err := s.repository.Pool().QueryRow(r.Context(), `
		SELECT enabled, issuer_url, client_id, client_secret_encrypted,
			role_claim_path, group_claim_path, role_mappings, group_mappings, scopes, updated_at
		FROM oidc_settings WHERE singleton`).Scan(
		&settings.Enabled, &settings.IssuerURL, &settings.ClientID, &settings.ClientSecret,
		&settings.RoleClaimPath, &settings.GroupClaimPath, &roleMappings, &groupMappings, &scopes, &settings.UpdatedAt,
	)
	if err != nil {
		return model.OIDCSettings{}, err
	}
	settings.ClientSecretSet = settings.ClientSecret != ""
	if err := json.Unmarshal(roleMappings, &settings.RoleMappings); err != nil {
		return model.OIDCSettings{}, err
	}
	if err := json.Unmarshal(groupMappings, &settings.GroupMappings); err != nil {
		return model.OIDCSettings{}, err
	}
	if err := json.Unmarshal(scopes, &settings.Scopes); err != nil {
		return model.OIDCSettings{}, err
	}
	return settings, nil
}

func (s *Server) loadSystemSettings(r *http.Request) model.SystemSettings {
	settings := model.SystemSettings{SiteName: "Dev App Store", Theme: "system", DefaultLanguage: "ko", PageSize: 24, PublicMode: true}
	var raw []byte
	if err := s.repository.Pool().QueryRow(r.Context(), `SELECT value FROM system_settings WHERE key = 'system'`).Scan(&raw); err == nil {
		_ = json.Unmarshal(raw, &settings)
	}
	return settings
}

func (s *Server) oidcRedirectURL(r *http.Request) string {
	siteURL := strings.TrimRight(s.loadSystemSettings(r).SiteURL, "/")
	if parsed, err := url.Parse(siteURL); err == nil && parsed.IsAbs() && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		return siteURL + "/api/v1/auth/oidc/callback"
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/api/v1/auth/oidc/callback"
}

func truncateHTTPValue(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) > limit {
		return value[:limit]
	}
	return value
}
