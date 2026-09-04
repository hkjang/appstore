package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

func (s *Server) adminDashboard(w http.ResponseWriter, r *http.Request) {
	var appsTotal, appsPublished, reviewsPending, usersActive int
	var oidcConfigured, workflowEnabled, aiStreaming bool
	err := s.repository.Pool().QueryRow(r.Context(), `
		SELECT
			(SELECT count(*)::int FROM apps),
			(SELECT count(*)::int FROM apps WHERE status = 'published'),
			(SELECT count(*)::int FROM reviews WHERE status = 'pending'),
			(SELECT count(*)::int FROM users WHERE active),
			(SELECT enabled AND issuer_url <> '' AND client_id <> '' AND client_secret_encrypted <> '' FROM oidc_settings WHERE singleton),
			(SELECT enabled FROM workflow_config WHERE singleton),
			COALESCE((SELECT bool_or(streaming AND enabled) FROM ai_providers), true)
	`).Scan(&appsTotal, &appsPublished, &reviewsPending, &usersActive, &oidcConfigured, &workflowEnabled, &aiStreaming)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"appsTotal": appsTotal, "appsPublished": appsPublished,
		"reviewsPending": reviewsPending, "usersActive": usersActive,
		"oidcConfigured": oidcConfigured, "workflowEnabled": workflowEnabled,
		"aiStreaming": aiStreaming,
	})
}

func (s *Server) adminApps(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r, 100, 200)
	page, err := s.repository.ListApps(r.Context(), model.AppListOptions{
		Query: r.URL.Query().Get("q"), Category: r.URL.Query().Get("category"),
		Language: r.URL.Query().Get("language"), Status: r.URL.Query().Get("status"),
		MCPOnly: boolQuery(r.URL.Query().Get("mcp")), Sort: normalizedSort(r.URL.Query().Get("sort")),
		Limit: limit, Offset: offset, IncludeAll: true,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, page)
}

func (s *Server) adminSetAppStatus(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"), "앱")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	var input struct {
		Status string `json:"status"`
	}
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	before, err := s.repository.GetAppByID(r.Context(), id)
	if err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
		return
	}
	after, err := s.repository.SetAppStatus(r.Context(), id, input.Status)
	if err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "app.status.update", "app", id.String(), before, after)
	WriteJSON(w, http.StatusOK, after)
}

// adminAppRequest carries the full catalog record an administrator edits,
// including the status and featured flags owners cannot set themselves.
type adminAppRequest struct {
	model.AppInput
	Status   string `json:"status"`
	Featured bool   `json:"featured"`
}

func (s *Server) adminCreateApp(w http.ResponseWriter, r *http.Request) {
	var input adminAppRequest
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	if err := ValidateAppInput(&input.AppInput); err != nil {
		WriteError(w, r, err)
		return
	}
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = model.AppStatusDraft
	}
	// The administrator owns what they add here, so the record has an owner for
	// later edits and the audit trail.
	principal := CurrentPrincipal(r.Context())
	var ownerID *uuid.UUID
	if principal != nil {
		ownerID = &principal.User.ID
	}
	app, err := s.repository.AdminCreateApp(r.Context(), ownerID, input.AppInput, status, input.Featured)
	if err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 등록할 수 없습니다."))
		return
	}
	s.recordAudit(r, "app.create", "app", app.ID.String(), nil, app)
	WriteJSON(w, http.StatusCreated, app)
}

func (s *Server) adminApp(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"), "앱")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	app, err := s.repository.GetAppByID(r.Context(), id)
	if err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
		return
	}
	WriteJSON(w, http.StatusOK, app)
}

func (s *Server) adminUpdateApp(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"), "앱")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	before, err := s.repository.GetAppByID(r.Context(), id)
	if err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
		return
	}
	var input adminAppRequest
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	if err := ValidateAppInput(&input.AppInput); err != nil {
		WriteError(w, r, err)
		return
	}
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = before.Status
	}
	after, err := s.repository.AdminUpdateApp(r.Context(), id, input.AppInput, status, input.Featured)
	if err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "app.update", "app", id.String(), before, after)
	WriteJSON(w, http.StatusOK, after)
}

func (s *Server) adminDeleteApp(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"), "앱")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	before, err := s.repository.GetAppByID(r.Context(), id)
	if err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
		return
	}
	if err := s.repository.DeleteApp(r.Context(), id); err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "app.delete", "app", id.String(), before, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminCategories(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListCategories(r.Context(), true)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

type categoryRequest struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Icon        string `json:"icon,omitempty"`
	Description string `json:"description,omitempty"`
	Position    int    `json:"position,omitempty"`
	Active      *bool  `json:"active,omitempty"`
}

func (i categoryRequest) storeInput() store.CategoryInput {
	active := true
	if i.Active != nil {
		active = *i.Active
	}
	if strings.TrimSpace(i.Icon) == "" {
		i.Icon = "📦"
	}
	return store.CategoryInput{Slug: i.Slug, Name: i.Name, Icon: i.Icon, Description: i.Description, Position: i.Position, Active: active}
}

func (s *Server) adminCreateCategory(w http.ResponseWriter, r *http.Request) {
	var input categoryRequest
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	category, err := s.repository.CreateCategory(r.Context(), input.storeInput())
	if err != nil {
		WriteError(w, r, storeError(err, "CATEGORY_NOT_FOUND", "카테고리를 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "category.create", "category", category.ID.String(), nil, category)
	WriteJSON(w, http.StatusCreated, category)
}

func (s *Server) adminUpdateCategory(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"), "카테고리")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	var input categoryRequest
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	before, err := s.repository.GetCategoryByID(r.Context(), id)
	if err != nil {
		WriteError(w, r, storeError(err, "CATEGORY_NOT_FOUND", "카테고리를 찾을 수 없습니다."))
		return
	}
	after, err := s.repository.UpdateCategory(r.Context(), id, input.storeInput())
	if err != nil {
		WriteError(w, r, storeError(err, "CATEGORY_NOT_FOUND", "카테고리를 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "category.update", "category", id.String(), before, after)
	WriteJSON(w, http.StatusOK, after)
}

func (s *Server) adminDeleteCategory(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"), "카테고리")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	before, err := s.repository.GetCategoryByID(r.Context(), id)
	if err != nil {
		WriteError(w, r, storeError(err, "CATEGORY_NOT_FOUND", "카테고리를 찾을 수 없습니다."))
		return
	}
	if err := s.repository.DeleteCategory(r.Context(), id); err != nil {
		WriteError(w, r, storeError(err, "CATEGORY_NOT_FOUND", "사용 중인 카테고리는 삭제할 수 없습니다."))
		return
	}
	s.recordAudit(r, "category.delete", "category", id.String(), before, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r, 100, 2000)
	var active *bool
	if raw := strings.TrimSpace(r.URL.Query().Get("active")); raw != "" {
		value := boolQuery(raw)
		active = &value
	}
	page, err := s.repository.ListUsers(r.Context(), store.UserListOptions{
		Query: r.URL.Query().Get("q"), Role: r.URL.Query().Get("role"),
		AuthSource: r.URL.Query().Get("authSource"), Active: active,
		Limit: limit, Offset: offset,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, page)
}

type adminUserRequest struct {
	Username    string   `json:"username"`
	Email       string   `json:"email"`
	DisplayName string   `json:"displayName"`
	Team        string   `json:"team"`
	Active      bool     `json:"active"`
	Roles       []string `json:"roles,omitempty"`
}

func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"), "사용자")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	var input adminUserRequest
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	before, err := s.repository.GetUserByID(r.Context(), id)
	if err != nil {
		WriteError(w, r, storeError(err, "USER_NOT_FOUND", "사용자를 찾을 수 없습니다."))
		return
	}
	if before.AuthSource == "bootstrap" {
		input.Username = before.Username
		input.Active = true
	}
	if principal := CurrentPrincipal(r.Context()); principal != nil && principal.User.ID == id && !input.Active {
		WriteError(w, r, Validation("현재 로그인한 계정은 비활성화할 수 없습니다.", nil))
		return
	}
	after, err := s.repository.UpdateUser(r.Context(), id, store.UserUpdate{
		Username: input.Username, Email: input.Email, DisplayName: input.DisplayName,
		Team: input.Team, Active: input.Active,
	})
	if err == nil && input.Roles != nil {
		after, err = s.repository.ReplaceUserRoles(r.Context(), id, input.Roles)
	}
	if err != nil {
		WriteError(w, r, storeError(err, "USER_NOT_FOUND", "사용자를 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "user.update", "user", id.String(), before, after)
	WriteJSON(w, http.StatusOK, after)
}

func (s *Server) adminUpdateUserRoles(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"), "사용자")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	var input struct {
		Roles []string `json:"roles"`
	}
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	before, err := s.repository.GetUserByID(r.Context(), id)
	if err != nil {
		WriteError(w, r, storeError(err, "USER_NOT_FOUND", "사용자를 찾을 수 없습니다."))
		return
	}
	after, err := s.repository.ReplaceUserRoles(r.Context(), id, input.Roles)
	if err != nil {
		WriteError(w, r, storeError(err, "USER_NOT_FOUND", "사용자를 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "role.assignment.update", "user", id.String(), before.Roles, after.Roles)
	WriteJSON(w, http.StatusOK, after)
}

func (s *Server) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"), "사용자")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	if principal := CurrentPrincipal(r.Context()); principal != nil && principal.User.ID == id {
		WriteError(w, r, Forbidden("현재 로그인한 계정은 삭제할 수 없습니다."))
		return
	}
	before, err := s.repository.GetUserByID(r.Context(), id)
	if err != nil {
		WriteError(w, r, storeError(err, "USER_NOT_FOUND", "사용자를 찾을 수 없습니다."))
		return
	}
	if err := s.repository.DeleteUser(r.Context(), id); err != nil {
		WriteError(w, r, storeError(err, "USER_NOT_FOUND", "Bootstrap 관리자는 삭제할 수 없습니다."))
		return
	}
	s.recordAudit(r, "user.delete", "user", id.String(), before, nil)
	w.WriteHeader(http.StatusNoContent)
}

type adminKeyRow struct {
	model.APIKey
	OwnerName string `json:"ownerName"`
}

func (s *Server) adminAPIKeys(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r, 100, 500)
	rows, err := s.repository.Pool().Query(r.Context(), `
		SELECT k.id, k.name, k.key_prefix, k.permissions, k.created_at, k.expires_at,
			k.last_used_at, k.revoked_at, k.rotated_from,
			COALESCE(NULLIF(u.display_name, ''), u.username)
		FROM api_keys k JOIN users u ON u.id = k.user_id
		ORDER BY k.created_at DESC, k.id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	defer rows.Close()
	items := []adminKeyRow{}
	for rows.Next() {
		var item adminKeyRow
		var permissions []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.Prefix, &permissions,
			&item.CreatedAt, &item.ExpiresAt, &item.LastUsedAt, &item.RevokedAt,
			&item.RotatedFrom, &item.OwnerName); err != nil {
			WriteError(w, r, err)
			return
		}
		if err := json.Unmarshal(permissions, &item.Permissions); err != nil {
			WriteError(w, r, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "limit": limit, "offset": offset})
}

func (s *Server) adminAudit(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r, 100, 500)
	page, err := s.repository.ListAuditLogs(r.Context(), store.AuditListOptions{
		Action: r.URL.Query().Get("action"), Resource: r.URL.Query().Get("resource"),
		RequestID: r.URL.Query().Get("requestId"), Limit: limit, Offset: offset,
	})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, page)
}

func parseID(value, resource string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, Validation(resource+" ID가 올바르지 않습니다.", nil)
	}
	return id, nil
}
