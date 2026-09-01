package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

type roleRequest struct {
	ID          uuid.UUID `json:"id,omitempty"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Permissions []string  `json:"permissions"`
}

func (value roleRequest) input() store.RoleInput {
	return store.RoleInput{Key: value.Key, Name: value.Name, Description: value.Description, Permissions: value.Permissions}
}

func (s *Server) adminRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := s.repository.ListRoles(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	permissions, err := s.repository.ListPermissions(r.Context(), true)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"roles": roles, "permissions": permissions})
}

type bulkRBACRequest struct {
	Roles       []json.RawMessage `json:"roles"`
	Permissions []json.RawMessage `json:"permissions"`
}

func (s *Server) adminReplaceRoles(w http.ResponseWriter, r *http.Request) {
	var input bulkRBACRequest
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	beforeRoles, err := s.repository.ListRoles(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	byID := map[uuid.UUID]model.Role{}
	byKey := map[string]model.Role{}
	for _, role := range beforeRoles {
		byID[role.ID] = role
		byKey[role.Key] = role
	}

	// The JSON editor may contain legacy string arrays. They are accepted as
	// references but do not mutate definitions until an object is supplied.
	for _, raw := range input.Permissions {
		var key string
		if json.Unmarshal(raw, &key) == nil {
			continue
		}
		var permission model.Permission
		if err := json.Unmarshal(raw, &permission); err != nil {
			WriteError(w, r, Validation("권한 정의 JSON을 확인해 주세요.", nil))
			return
		}
		if _, err := s.repository.UpsertPermission(r.Context(), permission); err != nil {
			WriteError(w, r, storeError(err, "PERMISSION_NOT_FOUND", "권한 정의를 저장할 수 없습니다."))
			return
		}
	}
	for _, raw := range input.Roles {
		var key string
		if json.Unmarshal(raw, &key) == nil {
			continue
		}
		var role roleRequest
		if err := json.Unmarshal(raw, &role); err != nil {
			WriteError(w, r, Validation("역할 정의 JSON을 확인해 주세요.", nil))
			return
		}
		current, exists := byID[role.ID]
		if !exists {
			current, exists = byKey[role.Key]
		}
		if exists {
			if current.System && role.Key != current.Key {
				WriteError(w, r, Validation("기본 시스템 역할의 key는 변경할 수 없습니다.", map[string]any{"role": current.Key}))
				return
			}
			if _, err := s.repository.UpdateRole(r.Context(), current.ID, role.input()); err != nil {
				WriteError(w, r, storeError(err, "ROLE_NOT_FOUND", "역할을 저장할 수 없습니다."))
				return
			}
		} else {
			if _, err := s.repository.CreateRole(r.Context(), role.input()); err != nil {
				WriteError(w, r, storeError(err, "ROLE_NOT_FOUND", "역할을 저장할 수 없습니다."))
				return
			}
		}
	}
	afterRoles, _ := s.repository.ListRoles(r.Context())
	s.recordAudit(r, "rbac.definition.update", "rbac", "roles", beforeRoles, afterRoles)
	s.adminRoles(w, r)
}

func (s *Server) adminCreateRole(w http.ResponseWriter, r *http.Request) {
	var input roleRequest
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	role, err := s.repository.CreateRole(r.Context(), input.input())
	if err != nil {
		WriteError(w, r, storeError(err, "ROLE_NOT_FOUND", "역할을 생성할 수 없습니다."))
		return
	}
	s.recordAudit(r, "role.create", "role", role.ID.String(), nil, role)
	WriteJSON(w, http.StatusCreated, role)
}

func (s *Server) adminUpdateRole(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"), "역할")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	var input roleRequest
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	before, err := s.repository.GetRole(r.Context(), id)
	if err != nil {
		WriteError(w, r, storeError(err, "ROLE_NOT_FOUND", "역할을 찾을 수 없습니다."))
		return
	}
	if before.System && input.Key != before.Key {
		WriteError(w, r, Validation("기본 시스템 역할의 key는 변경할 수 없습니다.", nil))
		return
	}
	after, err := s.repository.UpdateRole(r.Context(), id, input.input())
	if err != nil {
		WriteError(w, r, storeError(err, "ROLE_NOT_FOUND", "역할을 저장할 수 없습니다."))
		return
	}
	s.recordAudit(r, "role.update", "role", id.String(), before, after)
	WriteJSON(w, http.StatusOK, after)
}

func (s *Server) adminDeleteRole(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"), "역할")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	before, err := s.repository.GetRole(r.Context(), id)
	if err != nil {
		WriteError(w, r, storeError(err, "ROLE_NOT_FOUND", "역할을 찾을 수 없습니다."))
		return
	}
	if err := s.repository.DeleteRole(r.Context(), id); err != nil {
		WriteError(w, r, storeError(err, "ROLE_NOT_FOUND", "기본 시스템 역할은 삭제할 수 없습니다."))
		return
	}
	s.recordAudit(r, "role.delete", "role", id.String(), before, nil)
	w.WriteHeader(http.StatusNoContent)
}
