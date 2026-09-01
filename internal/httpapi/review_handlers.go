package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/store"
)

func (s *Server) listReviews(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r, 50, 100)
	principal := CurrentPrincipal(r.Context())
	options := store.ReviewListOptions{
		Status: r.URL.Query().Get("status"), Level: intQuery(r.URL.Query().Get("level")), Limit: limit, Offset: offset,
	}
	if options.Status == "" {
		options.Status = "pending"
	}
	teamOnly, allowed, err := s.reviewScope(r, principal)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	if !allowed {
		WriteError(w, r, Forbidden("검토자로 설정된 역할이 필요합니다."))
		return
	}
	if teamOnly {
		if principal.User.Team == "" {
			WriteError(w, r, Forbidden("팀장 검토에는 사용자 팀 정보가 필요합니다."))
			return
		}
		options.Team = principal.User.Team
	}
	page, err := s.repository.ListReviews(r.Context(), options)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, page)
}

func (s *Server) getReview(w http.ResponseWriter, r *http.Request) {
	id, ok := reviewIDParam(w, r)
	if !ok {
		return
	}
	review, err := s.repository.GetReview(r.Context(), id)
	if err != nil {
		WriteError(w, r, storeError(err, "REVIEW_NOT_FOUND", "검토 항목을 찾을 수 없습니다."))
		return
	}
	principal := CurrentPrincipal(r.Context())
	teamOnly, allowed, scopeErr := s.reviewScope(r, principal)
	if scopeErr != nil {
		WriteError(w, r, scopeErr)
		return
	}
	if !allowed {
		WriteError(w, r, Forbidden("검토자로 설정된 역할이 필요합니다."))
		return
	}
	if teamOnly && review.Team != principal.User.Team {
		WriteError(w, r, Forbidden("소속 팀의 검토 항목만 볼 수 있습니다."))
		return
	}
	WriteJSON(w, http.StatusOK, review)
}

func (s *Server) approveReview(w http.ResponseWriter, r *http.Request) {
	s.decideReview(w, r, "approved", "")
}

func (s *Server) rejectReview(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Reason string `json:"reason"`
	}
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	s.decideReview(w, r, "rejected", strings.TrimSpace(input.Reason))
}

func (s *Server) decideReview(w http.ResponseWriter, r *http.Request, decision, reason string) {
	id, ok := reviewIDParam(w, r)
	if !ok {
		return
	}
	before, err := s.repository.GetReview(r.Context(), id)
	if err != nil {
		WriteError(w, r, storeError(err, "REVIEW_NOT_FOUND", "검토 항목을 찾을 수 없습니다."))
		return
	}
	principal := CurrentPrincipal(r.Context())
	teamOnly, allowed, scopeErr := s.reviewScope(r, principal)
	if scopeErr != nil {
		WriteError(w, r, scopeErr)
		return
	}
	if !allowed {
		WriteError(w, r, Forbidden("검토자로 설정된 역할이 필요합니다."))
		return
	}
	if teamOnly && before.Team != principal.User.Team {
		WriteError(w, r, Forbidden("소속 팀의 검토 항목만 처리할 수 있습니다."))
		return
	}
	result, err := s.repository.DecideReview(r.Context(), id, principal.User.ID, decision, reason)
	if err != nil {
		WriteError(w, r, storeError(err, "REVIEW_NOT_FOUND", "검토 항목을 찾을 수 없습니다."))
		return
	}
	action := "app.approve"
	if decision == "rejected" {
		action = "app.reject"
	}
	s.recordAudit(r, action, "review", id.String(), before, result)
	WriteJSON(w, http.StatusOK, result.Review)
}

func (s *Server) reviewScope(r *http.Request, principal *Principal) (teamOnly, allowed bool, err error) {
	if principal == nil {
		return false, false, nil
	}
	// Administrative permission remains a stable recovery path even when role
	// names and OIDC mappings are customized.
	if principal.Can("settings:write") {
		return false, true, nil
	}
	config, err := s.repository.GetWorkflowConfig(r.Context())
	if err != nil {
		return false, false, err
	}
	roles := make(map[string]bool, len(principal.User.Roles))
	for _, role := range principal.User.Roles {
		roles[role] = true
	}
	for _, role := range config.ReviewerRoles {
		if roles[role] {
			return false, true, nil
		}
	}
	for _, role := range config.TeamLeaderRoles {
		if roles[role] {
			return true, true, nil
		}
	}
	return false, false, nil
}

func reviewIDParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, r, Validation("검토 ID가 올바르지 않습니다.", nil))
		return uuid.Nil, false
	}
	return id, true
}

func intQuery(value string) int {
	result := 0
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0
		}
		result = result*10 + int(character-'0')
		if result > 1000000 {
			return 0
		}
	}
	return result
}
