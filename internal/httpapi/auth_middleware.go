package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	appauth "github.com/hkjang/appstore/internal/auth"
	appcrypto "github.com/hkjang/appstore/internal/crypto"
	"github.com/hkjang/appstore/internal/keymanager"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

const principalKey contextKey = "principal"

type Principal struct {
	User        model.User
	AuthMethod  string
	Permissions map[string]bool
	SessionHash []byte
	CSRFHash    []byte
	APIKeyID    *uuid.UUID
}

func (p *Principal) Can(permission string) bool {
	return p != nil && (p.Permissions[permission] || p.Permissions["*"])
}

func CurrentPrincipal(ctx context.Context) *Principal {
	value, _ := ctx.Value(principalKey).(*Principal)
	return value
}

type AuthMiddleware struct {
	Repository *store.Repository
	Box        *appcrypto.SecretBox
}

func (a *AuthMiddleware) Optional(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := a.Authenticate(r)
		if err != nil && !errors.Is(err, store.ErrNotFound) && !errors.Is(err, store.ErrForbidden) {
			WriteError(w, r, &APIError{Status: http.StatusServiceUnavailable, Code: "AUTH_UNAVAILABLE", Message: "인증 정보를 확인할 수 없습니다."})
			return
		}
		if principal != nil {
			r = r.WithContext(context.WithValue(r.Context(), principalKey, principal))
		}
		next.ServeHTTP(w, r)
	})
}

func (a *AuthMiddleware) Authenticate(r *http.Request) (*Principal, error) {
	if plaintext := apiKeyFromRequest(r); plaintext != "" {
		hash, err := keymanager.Digest(a.Box, plaintext)
		if err != nil {
			return nil, store.ErrNotFound
		}
		credential, err := a.Repository.GetAPIKeyCredentialByHash(r.Context(), hash)
		if err != nil {
			return nil, err
		}
		user, err := a.Repository.GetUserByID(r.Context(), credential.UserID)
		if err != nil {
			return nil, err
		}
		current := make(map[string]bool, len(user.Permissions))
		for _, permission := range user.Permissions {
			current[permission] = true
		}
		superAdmin := false
		for _, role := range user.Roles {
			if role == "super_admin" {
				superAdmin = true
				break
			}
		}
		permissions := make(map[string]bool, len(credential.Permissions))
		for _, permission := range credential.Permissions {
			if superAdmin || current[permission] {
				permissions[permission] = true
			}
		}
		if credential.Key.LastUsedAt == nil || time.Since(*credential.Key.LastUsedAt) > 5*time.Minute {
			_ = a.Repository.TouchAPIKey(r.Context(), credential.Key.ID, time.Now().UTC())
		}
		keyID := credential.Key.ID
		return &Principal{User: user, AuthMethod: "api_key", Permissions: permissions, APIKeyID: &keyID}, nil
	}
	token := appauth.SessionToken(r)
	if token == "" {
		return nil, nil
	}
	hash := a.Box.Digest("session:" + token)
	session, user, err := a.Repository.GetSessionByTokenHash(r.Context(), hash)
	if err != nil {
		return nil, err
	}
	permissions := make(map[string]bool, len(user.Permissions))
	for _, permission := range user.Permissions {
		permissions[permission] = true
	}
	for _, role := range user.Roles {
		if role == "super_admin" {
			permissions["*"] = true
		}
	}
	if time.Since(session.LastSeen) > 5*time.Minute {
		_ = a.Repository.TouchSession(r.Context(), hash, time.Now().UTC())
	}
	return &Principal{
		User: user, AuthMethod: "session", Permissions: permissions,
		SessionHash: hash, CSRFHash: session.CSRFHash,
	}, nil
}

func apiKeyFromRequest(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-API-Key")); strings.HasPrefix(value, "aps_") {
		return value
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && strings.HasPrefix(parts[1], "aps_") {
		return parts[1]
	}
	return ""
}

func RequireAuthenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if CurrentPrincipal(r.Context()) == nil {
			WriteError(w, r, Unauthorized("로그인이 필요합니다."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func RequirePermission(permission string, next http.Handler) http.Handler {
	return RequireAuthenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !CurrentPrincipal(r.Context()).Can(permission) {
			WriteError(w, r, Forbidden("이 작업을 수행할 권한이 없습니다."))
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (a *AuthMiddleware) VerifyCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		principal := CurrentPrincipal(r.Context())
		if principal == nil || principal.AuthMethod != "session" {
			next.ServeHTTP(w, r)
			return
		}
		if !appauth.VerifyCSRF(r, principal.CSRFHash, a.Box) {
			WriteError(w, r, &APIError{Status: http.StatusForbidden, Code: "CSRF_INVALID", Message: "보안 토큰이 만료되었거나 올바르지 않습니다."})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func IsSecureRequest(r *http.Request, configuredSiteURL string) bool {
	if r.TLS != nil {
		return true
	}
	if strings.HasPrefix(strings.ToLower(configuredSiteURL), "https://") {
		return true
	}
	return false
}
