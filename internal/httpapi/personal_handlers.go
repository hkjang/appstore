package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/keymanager"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, CurrentPrincipal(r.Context()).User)
}

func (s *Server) myApps(w http.ResponseWriter, r *http.Request) {
	principal := CurrentPrincipal(r.Context())
	limit, offset := pagination(r, 50, 100)
	page, err := s.repository.ListApps(r.Context(), model.AppListOptions{
		OwnerID: &principal.User.ID, IncludeAll: true, Limit: limit, Offset: offset, Sort: "updated",
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, page)
}

func (s *Server) myFavorites(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r, 50, 100)
	page, err := s.repository.ListFavorites(r.Context(), CurrentPrincipal(r.Context()).User.ID, limit, offset)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, page)
}

func (s *Server) addFavorite(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, Validation("앱 ID가 올바르지 않습니다.", nil))
		return
	}
	if _, err := s.repository.GetAppByID(r.Context(), id); err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
		return
	}
	if err := s.repository.AddFavorite(r.Context(), CurrentPrincipal(r.Context()).User.ID, id); err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeFavorite(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, Validation("앱 ID가 올바르지 않습니다.", nil))
		return
	}
	if err := s.repository.RemoveFavorite(r.Context(), CurrentPrincipal(r.Context()).User.ID, id); err != nil {
		WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) myKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.repository.ListAPIKeys(r.Context(), CurrentPrincipal(r.Context()).User.ID, boolQuery(r.URL.Query().Get("includeRevoked")))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": keys})
}

type createKeyInput struct {
	Name        string   `json:"name"`
	Type        string   `json:"type,omitempty"`
	Permissions []string `json:"permissions"`
}

type oneTimeKeyResponse struct {
	model.APIKey
	Secret                  string     `json:"secret"`
	RotationGracePeriodEnds *time.Time `json:"rotationGraceEndsAt,omitempty"`
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	var input createKeyInput
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 100 {
		WriteError(w, r, Validation("키 이름을 1~100자로 입력하세요.", nil))
		return
	}
	principal := CurrentPrincipal(r.Context())
	policy, err := s.repository.GetKeyPolicy(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	allowed, err := s.allowedKeyPermissions(r, principal)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	generated, err := keymanager.Generate(s.box, policy, input.Permissions, allowed, time.Now().UTC())
	if err != nil {
		WriteError(w, r, Validation(err.Error(), nil))
		return
	}
	key, err := s.repository.CreateAPIKey(r.Context(), store.CreateAPIKeyParams{
		UserID: principal.User.ID, Name: input.Name, Prefix: generated.Prefix,
		Hash: generated.Hash, Permissions: generated.Permissions, ExpiresAt: generated.ExpiresAt,
	})
	if err != nil {
		WriteError(w, r, storeError(err, "KEY_NOT_FOUND", "키를 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "key.create", "api_key", key.ID.String(), nil, key)
	WriteJSON(w, http.StatusCreated, oneTimeKeyResponse{APIKey: key, Secret: generated.Plaintext})
}

func (s *Server) rotateKey(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, Validation("키 ID가 올바르지 않습니다.", nil))
		return
	}
	principal := CurrentPrincipal(r.Context())
	old, err := s.repository.GetAPIKey(r.Context(), principal.User.ID, id)
	if err != nil {
		WriteError(w, r, storeError(err, "KEY_NOT_FOUND", "키를 찾을 수 없습니다."))
		return
	}
	policy, err := s.repository.GetKeyPolicy(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	allowed, err := s.allowedKeyPermissions(r, principal)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	generated, err := keymanager.Generate(s.box, policy, old.Permissions, allowed, time.Now().UTC())
	if err != nil {
		WriteError(w, r, Validation(err.Error(), nil))
		return
	}
	rotated, err := s.repository.RotateAPIKey(r.Context(), id, store.CreateAPIKeyParams{
		UserID: principal.User.ID, Name: old.Name, Prefix: generated.Prefix,
		Hash: generated.Hash, Permissions: generated.Permissions, ExpiresAt: generated.ExpiresAt,
	})
	if err != nil {
		WriteError(w, r, storeError(err, "KEY_NOT_FOUND", "키를 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "key.rotate", "api_key", id.String(), old, rotated.NewKey)
	grace := rotated.GracePeriodEnds
	WriteJSON(w, http.StatusCreated, oneTimeKeyResponse{APIKey: rotated.NewKey, Secret: generated.Plaintext, RotationGracePeriodEnds: &grace})
}

func (s *Server) revokeKey(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, Validation("키 ID가 올바르지 않습니다.", nil))
		return
	}
	principal := CurrentPrincipal(r.Context())
	before, err := s.repository.GetAPIKey(r.Context(), principal.User.ID, id)
	if err != nil {
		WriteError(w, r, storeError(err, "KEY_NOT_FOUND", "키를 찾을 수 없습니다."))
		return
	}
	if err := s.repository.RevokeAPIKey(r.Context(), principal.User.ID, id); err != nil {
		WriteError(w, r, storeError(err, "KEY_NOT_FOUND", "키를 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "key.revoke", "api_key", id.String(), before, map[string]any{"revoked": true})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) keyPermissions(w http.ResponseWriter, r *http.Request) {
	definitions, err := s.repository.ListKeyPermissionDefinitions(r.Context(), false)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	templates, err := s.repository.ListKeyPermissionTemplates(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	policy, err := s.repository.GetKeyPolicy(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	principal := CurrentPrincipal(r.Context())
	filtered := definitions[:0]
	for _, definition := range definitions {
		if principal.Can(definition.Key) {
			filtered = append(filtered, definition)
		}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"permissions": filtered, "templates": templates, "policy": policy})
}

func (s *Server) allowedKeyPermissions(r *http.Request, principal *Principal) (map[string]bool, error) {
	definitions, err := s.repository.ListKeyPermissionDefinitions(r.Context(), false)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, definition := range definitions {
		if principal.Can(definition.Key) {
			allowed[definition.Key] = true
		}
	}
	return allowed, nil
}

func (s *Server) myActivity(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r, 50, 100)
	userID := CurrentPrincipal(r.Context()).User.ID
	page, err := s.repository.ListAuditLogs(r.Context(), store.AuditListOptions{ActorID: &userID, Limit: limit, Offset: offset})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, page)
}

type userPreferences struct {
	Theme         string `json:"theme"`
	Language      string `json:"language"`
	ReducedMotion bool   `json:"reducedMotion"`
	CompactCards  bool   `json:"compactCards"`
}

func defaultUserPreferences() userPreferences {
	return userPreferences{Theme: "system", Language: "ko"}
}

func (s *Server) mySettings(w http.ResponseWriter, r *http.Request) {
	preferences := defaultUserPreferences()
	err := s.repository.GetSetting(r.Context(), "user_preferences:"+CurrentPrincipal(r.Context()).User.ID.String(), &preferences)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, preferences)
}

func (s *Server) updateMySettings(w http.ResponseWriter, r *http.Request) {
	var preferences userPreferences
	if err := DecodeJSON(w, r, &preferences); err != nil {
		WriteError(w, r, err)
		return
	}
	if preferences.Theme != "system" && preferences.Theme != "light" && preferences.Theme != "dark" {
		WriteError(w, r, Validation("테마 설정이 올바르지 않습니다.", nil))
		return
	}
	if preferences.Language == "" || len(preferences.Language) > 16 {
		WriteError(w, r, Validation("언어 설정이 올바르지 않습니다.", nil))
		return
	}
	principal := CurrentPrincipal(r.Context())
	key := "user_preferences:" + principal.User.ID.String()
	var before userPreferences
	_ = s.repository.GetSetting(r.Context(), key, &before)
	if err := s.repository.PutSetting(r.Context(), key, preferences, &principal.User.ID); err != nil {
		WriteError(w, r, err)
		return
	}
	s.recordAudit(r, "user.settings.update", "user_preferences", principal.User.ID.String(), before, preferences)
	WriteJSON(w, http.StatusOK, preferences)
}
