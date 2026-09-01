package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
)

func (s *Server) createApp(w http.ResponseWriter, r *http.Request) {
	var input model.AppInput
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	if err := ValidateAppInput(&input); err != nil {
		WriteError(w, r, err)
		return
	}
	principal := CurrentPrincipal(r.Context())
	app, err := s.repository.CreateApp(r.Context(), &principal.User.ID, input, model.AppStatusDraft)
	if err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 등록할 수 없습니다."))
		return
	}
	result, err := s.repository.SubmitApp(r.Context(), app.ID, principal.User.ID)
	if err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 제출할 수 없습니다."))
		return
	}
	s.recordAudit(r, "app.create", "app", app.ID.String(), nil, result)
	WriteJSON(w, http.StatusCreated, result.App)
}

func (s *Server) updateApp(w http.ResponseWriter, r *http.Request) {
	id, ok := appIDParam(w, r)
	if !ok {
		return
	}
	before, err := s.repository.GetAppByID(r.Context(), id)
	if err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
		return
	}
	principal := CurrentPrincipal(r.Context())
	if (before.OwnerID == nil || *before.OwnerID != principal.User.ID) && !principal.Can("apps:manage") {
		WriteError(w, r, Forbidden("소유한 앱만 수정할 수 있습니다."))
		return
	}
	var input model.AppInput
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	if err := ValidateAppInput(&input); err != nil {
		WriteError(w, r, err)
		return
	}
	updated, err := s.repository.UpdateApp(r.Context(), id, input)
	if err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
		return
	}
	config, configErr := s.repository.GetWorkflowConfig(r.Context())
	if configErr == nil && config.Enabled && config.ReapprovalAfterEdit && before.Status == model.AppStatusPublished {
		if result, submitErr := s.repository.SubmitApp(r.Context(), id, principal.User.ID); submitErr == nil {
			updated = result.App
		} else {
			WriteError(w, r, submitErr)
			return
		}
	}
	s.recordAudit(r, "app.update", "app", id.String(), before, updated)
	WriteJSON(w, http.StatusOK, updated)
}

func (s *Server) deleteApp(w http.ResponseWriter, r *http.Request) {
	id, ok := appIDParam(w, r)
	if !ok {
		return
	}
	before, err := s.repository.GetAppByID(r.Context(), id)
	if err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
		return
	}
	principal := CurrentPrincipal(r.Context())
	if (before.OwnerID == nil || *before.OwnerID != principal.User.ID) && !principal.Can("apps:manage") {
		WriteError(w, r, Forbidden("소유한 앱만 삭제 요청할 수 있습니다."))
		return
	}
	archived, err := s.repository.SetAppStatus(r.Context(), id, "archived")
	if err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "app.archive", "app", id.String(), before, archived)
	w.WriteHeader(http.StatusNoContent)
}

func appIDParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "app"))
	if err != nil {
		WriteError(w, r, Validation("앱 ID가 올바르지 않습니다.", nil))
		return uuid.Nil, false
	}
	return id, true
}
