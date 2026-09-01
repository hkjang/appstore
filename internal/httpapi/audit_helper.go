package httpapi

import (
	"net/http"

	"github.com/google/uuid"
	appaudit "github.com/hkjang/appstore/internal/audit"
	"github.com/hkjang/appstore/internal/model"
)

func (s *Server) recordAudit(r *http.Request, action, resource, resourceID string, before, after any) {
	principal := CurrentPrincipal(r.Context())
	var actor string
	var actorID *uuid.UUID
	if principal != nil {
		actor = principal.User.Username
		id := principal.User.ID
		actorID = &id
	}
	entry := appaudit.NewEntry(r, actorID, actor, action, resource, resourceID, RequestID(r.Context()), before, after)
	if _, err := s.repository.AppendAudit(r.Context(), entry); err != nil {
		s.logger.ErrorContext(r.Context(), "append audit log failed", "error", err, "action", action, "request_id", RequestID(r.Context()))
	}
}

func (s *Server) recordUserAudit(r *http.Request, user model.User, action string) {
	id := user.ID
	entry := appaudit.NewEntry(r, &id, user.Username, action, "session", "", RequestID(r.Context()), nil, nil)
	if _, err := s.repository.AppendAudit(r.Context(), entry); err != nil {
		s.logger.ErrorContext(r.Context(), "append authentication audit log failed", "error", err, "action", action, "request_id", RequestID(r.Context()))
	}
}
