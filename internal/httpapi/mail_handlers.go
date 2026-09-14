package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/mail"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

// mailSettingsResponse is what the settings screen sees: the configuration
// without the password, plus the event catalogue so the switches carry their
// own labels.
type mailSettingsResponse struct {
	mail.Config
	AvailableEvents []mail.Event `json:"availableEvents"`
}

func (s *Server) adminMail(w http.ResponseWriter, r *http.Request) {
	config, err := s.repository.GetMailSettings(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, mailSettingsResponse{Config: config.Public(), AvailableEvents: mail.Events})
}

// adminUpdateMail saves the relay settings. The password arrives only when
// the administrator typed a new one; the response never carries it back.
func (s *Server) adminUpdateMail(w http.ResponseWriter, r *http.Request) {
	var request struct {
		mail.Config
		Password *string `json:"password"`
	}
	if err := DecodeJSON(w, r, &request); err != nil {
		WriteError(w, r, err)
		return
	}
	before, _ := s.repository.GetMailSettings(r.Context())
	var encrypted *string
	if request.Password != nil {
		plaintext := strings.TrimSpace(*request.Password)
		value := ""
		if plaintext != "" {
			var err error
			if value, err = s.box.Encrypt(plaintext); err != nil {
				WriteError(w, r, err)
				return
			}
		}
		encrypted = &value
	}
	principal := CurrentPrincipal(r.Context())
	after, err := s.repository.UpdateMailSettings(r.Context(), request.Config, encrypted, &principal.User.ID)
	if err != nil {
		WriteError(w, r, mailError(err))
		return
	}
	s.recordAudit(r, "mail.setting.update", "mail_settings", "default", before.Public(), after.Public())
	WriteJSON(w, http.StatusOK, mailSettingsResponse{Config: after.Public(), AvailableEvents: mail.Events})
}

// adminTestMail sends one real message with the saved settings and reports
// the relay's answer, because relay settings are rarely right the first time.
func (s *Server) adminTestMail(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Recipient string `json:"recipient"`
	}
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	principal := CurrentPrincipal(r.Context())
	recipient := strings.TrimSpace(input.Recipient)
	if recipient == "" {
		recipient = strings.TrimSpace(principal.User.Email)
	}
	if !strings.Contains(recipient, "@") {
		WriteError(w, r, Validation("받는 사람 이메일 주소를 입력하세요.", map[string]any{"recipient": "이메일 주소가 필요합니다."}))
		return
	}
	err := s.mail.SendNow(r.Context(), mail.TestMessage(), &principal.User.ID, recipient)
	switch {
	case errors.Is(err, mail.ErrDisabled):
		WriteError(w, r, &APIError{Status: http.StatusConflict, Code: "MAIL_DISABLED", Message: "메일 알림이 꺼져 있습니다. 설정을 켜고 저장한 뒤 다시 시도하세요."})
		return
	case err != nil:
		WriteError(w, r, &APIError{Status: http.StatusBadGateway, Code: "MAIL_SEND_FAILED", Message: "시험 발송에 실패했습니다: " + err.Error()})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"sent": true, "recipient": recipient})
}

func (s *Server) adminMailDeliveries(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, err := s.repository.ListMailDeliveries(r.Context(), r.URL.Query().Get("status"), limit)
	if err != nil {
		WriteError(w, r, storeError(err, "MAIL_NOT_FOUND", "발송 기록을 찾을 수 없습니다."))
		return
	}
	WriteJSON(w, http.StatusOK, page)
}

// mailError turns a field problem into the 422 envelope the console already
// shows next to the field.
func mailError(err error) error {
	var field *mail.FieldError
	if errors.As(err, &field) {
		return Validation(field.Message, map[string]any{field.Field: field.Message})
	}
	return storeError(err, "MAIL_SETTINGS_NOT_FOUND", "메일 설정을 찾을 수 없습니다.")
}

// personLabel is the name people recognise in a mail, never a bare id.
func (s *Server) personLabel(ctx context.Context, id uuid.UUID) string {
	user, err := s.repository.GetUserByID(ctx, id)
	if err != nil {
		return "알 수 없는 사용자"
	}
	if name := strings.TrimSpace(user.DisplayName); name != "" {
		return name
	}
	return user.Username
}

// notifyReviewRequested tells the people who can decide a pending review
// that it is their turn. Every submission path — first submission,
// resubmission after an edit, and the next level after an approval — comes
// through here, and the submitter is dropped by the service.
func (s *Server) notifyReviewRequested(ctx context.Context, review *model.Review, actorID uuid.UUID) {
	if s.mail == nil || review == nil || review.Status != "pending" {
		return
	}
	config, err := s.repository.GetWorkflowConfig(ctx)
	if err != nil {
		return
	}
	recipients, err := s.repository.ReviewerIDs(ctx, config.ReviewerRoles, config.TeamLeaderRoles, review.Team)
	if err != nil {
		s.logger.Warn("review recipients were not resolved", "error", err)
		return
	}
	submitter := review.SubmitterName
	if submitter == "" {
		submitter = s.personLabel(ctx, review.SubmitterID)
	}
	s.mail.Notify(ctx, mail.ReviewRequested(submitter, review.AppName, review.AppID.String(), review.Level, config.Levels), &actorID, recipients)
}

// notifyReviewDecided tells the submitter about a final decision — a
// rejection to act on or the approval that ends the wait — and passes the
// next level, if any, to the reviewers whose turn it now is. An intermediate
// approval is not mailed to the submitter: nothing changed for them yet.
func (s *Server) notifyReviewDecided(ctx context.Context, result store.ReviewDecisionResult, actorID uuid.UUID) {
	if s.mail == nil {
		return
	}
	if result.NextReview != nil {
		s.notifyReviewRequested(ctx, result.NextReview, actorID)
		return
	}
	review := result.Review
	reviewer := review.ReviewerName
	if reviewer == "" {
		reviewer = s.personLabel(ctx, actorID)
	}
	notification := mail.ReviewDecided(reviewer, review.AppName, review.AppID.String(), review.AppSlug,
		review.Status == "approved", result.AppStatus == model.AppStatusPublished, review.Reason)
	s.mail.Notify(ctx, notification, &actorID, []uuid.UUID{review.SubmitterID})
}

// notifyAppStatusChanged tells an owner that an administrator moved their
// app. Owners changing their own app are the actor and are not mailed.
func (s *Server) notifyAppStatusChanged(ctx context.Context, before, after model.App, actorID uuid.UUID) {
	if s.mail == nil || before.Status == after.Status || after.OwnerID == nil {
		return
	}
	actor := s.personLabel(ctx, actorID)
	s.mail.Notify(ctx, mail.AppStatusChanged(actor, after.Name, after.ID.String(), after.Slug, before.Status, after.Status),
		&actorID, []uuid.UUID{*after.OwnerID})
}
