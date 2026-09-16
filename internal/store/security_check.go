package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
	"github.com/jackc/pgx/v5"
)

// ErrSecurityCheckRequired is what every path that would publish or queue an
// app returns while SecCheck approval for its current content is missing.
var ErrSecurityCheckRequired = errors.New("store: security check required")

// securityVerifiedSQL is the single definition of "this app is cleared": the
// recorded review is an approval, captured against the challenge the app
// carries right now and the SecCheck connection in use right now. Editing the
// app or repointing AppStore at another SecCheck therefore drops the
// verification without a sweep. It deliberately does not ask whether the gate
// is switched on — an approval an organisation already obtained keeps its
// label — and each caller decides separately whether to enforce it.
const securityVerifiedSQL = `(sc.review_id IS NOT NULL
	AND sc.remote_status = 'APPROVED' AND sc.final_result = 'APPROVED'
	AND sc.approved_at IS NOT NULL
	AND sc.captured_nonce = a.security_challenge_nonce
	AND sc.settings_revision = ss.revision)`

type settingsScanner interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func getSecurityCheckSettings(ctx context.Context, q settingsScanner, forUpdate bool) (model.SecurityCheckSettings, error) {
	query := `SELECT enabled, base_url, api_key_encrypted, timeout_seconds, revision, updated_at
		FROM security_check_settings WHERE singleton`
	if forUpdate {
		query += ` FOR SHARE`
	}
	var settings model.SecurityCheckSettings
	err := q.QueryRow(ctx, query).Scan(&settings.Enabled, &settings.BaseURL, &settings.APIKeyEncrypted,
		&settings.TimeoutSeconds, &settings.Revision, &settings.UpdatedAt)
	if err != nil {
		return model.SecurityCheckSettings{}, normalizeError("get security check settings", err)
	}
	settings.APIKeySet = settings.APIKeyEncrypted != ""
	return settings, nil
}

func (r *Repository) GetSecurityCheckSettings(ctx context.Context) (model.SecurityCheckSettings, error) {
	return getSecurityCheckSettings(ctx, r.pool, false)
}

// UpdateSecurityCheckSettings stores the connection. A nil credential keeps the
// stored one; the revision moves whenever the connection itself changes, which
// is what retires approvals read from the previous SecCheck.
func (r *Repository) UpdateSecurityCheckSettings(ctx context.Context, settings model.SecurityCheckSettings, encryptedKey *string, updatedBy *uuid.UUID) (model.SecurityCheckSettings, error) {
	settings.BaseURL = strings.TrimRight(strings.TrimSpace(settings.BaseURL), "/")
	if settings.TimeoutSeconds < 1 || settings.TimeoutSeconds > 60 {
		return model.SecurityCheckSettings{}, fmt.Errorf("security check timeout: %w", ErrInvalid)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.SecurityCheckSettings{}, normalizeError("begin security check settings", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	before, err := getSecurityCheckSettings(ctx, tx, false)
	if err != nil {
		return model.SecurityCheckSettings{}, err
	}
	key := before.APIKeyEncrypted
	if encryptedKey != nil {
		key = *encryptedKey
	}
	if settings.Enabled && (settings.BaseURL == "" || key == "") {
		return model.SecurityCheckSettings{}, fmt.Errorf("security check credentials: %w", ErrInvalid)
	}
	revision := before.Revision
	if settings.BaseURL != before.BaseURL || key != before.APIKeyEncrypted {
		revision++
	}
	if _, err := tx.Exec(ctx, `
		UPDATE security_check_settings SET enabled = $1, base_url = $2, api_key_encrypted = $3,
			timeout_seconds = $4, revision = $5, updated_by = $6, updated_at = now()
		WHERE singleton`, settings.Enabled, settings.BaseURL, key, settings.TimeoutSeconds, revision, updatedBy); err != nil {
		return model.SecurityCheckSettings{}, normalizeError("update security check settings", err)
	}
	after, err := getSecurityCheckSettings(ctx, tx, false)
	if err != nil {
		return model.SecurityCheckSettings{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.SecurityCheckSettings{}, normalizeError("commit security check settings", err)
	}
	return after, nil
}

// GetAppSecurityCheck reports what AppStore knows about one app's review,
// including the challenge the owner has to carry into SecCheck.
func (r *Repository) GetAppSecurityCheck(ctx context.Context, appID uuid.UUID) (model.SecurityCheckState, error) {
	state := model.SecurityCheckState{AppID: appID}
	var reviewID, reviewNumber, remoteStatus, finalResult *string
	err := r.pool.QueryRow(ctx, `
		SELECT a.security_challenge_nonce, sc.review_id, sc.review_number, sc.remote_status,
			sc.final_result, sc.approved_at, sc.checked_at, `+securityVerifiedSQL+`
		FROM apps a
		CROSS JOIN security_check_settings ss
		LEFT JOIN app_security_checks sc ON sc.review_id = a.security_review_id
		WHERE a.id = $1`, appID).Scan(&state.ChallengeNonce, &reviewID, &reviewNumber,
		&remoteStatus, &finalResult, &state.ApprovedAt, &state.CheckedAt, &state.Verified)
	if err != nil {
		return model.SecurityCheckState{}, normalizeError("get app security check", err)
	}
	for value, destination := range map[**string]*string{
		&reviewID: &state.ReviewID, &reviewNumber: &state.ReviewNumber,
		&remoteStatus: &state.RemoteStatus, &finalResult: &state.FinalResult,
	} {
		if *value != nil {
			*destination = **value
		}
	}
	return state, nil
}

// RecordAppSecurityCheck stores what the client read. The challenge the caller
// verified against is compared inside the transaction, so an app edited while
// SecCheck was being asked never keeps an approval for its previous content. A
// review already bound to another app is refused outright.
func (r *Repository) RecordAppSecurityCheck(ctx context.Context, appID uuid.UUID, verifiedNonce uuid.UUID, settingsRevision int64, result model.SecurityCheckResult) (model.SecurityCheckState, error) {
	if !validRemoteStatus(result.RemoteStatus) || strings.TrimSpace(result.ReviewID) == "" {
		return model.SecurityCheckState{}, fmt.Errorf("security check result: %w", ErrInvalid)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.SecurityCheckState{}, normalizeError("begin security check record", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var currentNonce uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT security_challenge_nonce FROM apps WHERE id = $1 FOR UPDATE`, appID).Scan(&currentNonce); err != nil {
		return model.SecurityCheckState{}, normalizeError("lock app for security check", err)
	}
	if currentNonce != verifiedNonce {
		return model.SecurityCheckState{}, fmt.Errorf("security check challenge moved: %w", ErrConflict)
	}
	settings, err := getSecurityCheckSettings(ctx, tx, true)
	if err != nil {
		return model.SecurityCheckState{}, err
	}
	if settings.Revision != settingsRevision {
		return model.SecurityCheckState{}, fmt.Errorf("security check settings moved: %w", ErrConflict)
	}
	var boundTo *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT app_id FROM app_security_checks WHERE review_id = $1`, result.ReviewID).Scan(&boundTo)
	switch {
	case err == nil && (boundTo == nil || *boundTo != appID):
		return model.SecurityCheckState{}, fmt.Errorf("security review belongs to another app: %w", ErrConflict)
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return model.SecurityCheckState{}, normalizeError("read security review binding", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO app_security_checks(review_id, app_id, captured_nonce, settings_revision,
			review_number, remote_status, final_result, approved_at, checked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		ON CONFLICT (review_id) DO UPDATE SET
			captured_nonce = EXCLUDED.captured_nonce, settings_revision = EXCLUDED.settings_revision,
			review_number = EXCLUDED.review_number, remote_status = EXCLUDED.remote_status,
			final_result = EXCLUDED.final_result, approved_at = EXCLUDED.approved_at, checked_at = now()`,
		result.ReviewID, appID, verifiedNonce, settingsRevision, result.ReviewNumber,
		result.RemoteStatus, result.FinalResult, result.ApprovedAt); err != nil {
		return model.SecurityCheckState{}, normalizeError("record app security check", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE apps SET security_review_id = $2 WHERE id = $1`, appID, result.ReviewID); err != nil {
		return model.SecurityCheckState{}, normalizeError("attach app security check", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return model.SecurityCheckState{}, normalizeError("commit app security check", err)
	}
	return r.GetAppSecurityCheck(ctx, appID)
}

func validRemoteStatus(status string) bool {
	switch status {
	case "DRAFT", "CHANGE_REQUESTED", "SUBMITTED", "RESUBMITTED", "REVIEWING",
		"APPROVAL_PENDING", "APPROVED", "REJECTED", "CANCELLED", "CLOSED":
		return true
	}
	return false
}

// rotateAppChallenge invalidates whatever approval an app carries. It runs
// inside the transaction that changes the app, so no window exists in which
// new content wears an old approval.
func rotateAppChallenge(ctx context.Context, tx pgx.Tx, appID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE apps SET security_challenge_nonce = gen_random_uuid(), security_review_id = NULL
		WHERE id = $1`, appID); err != nil {
		return normalizeError("rotate app security challenge", err)
	}
	return nil
}

// requireSecurityCheck is the gate every transition into the review queue or
// the catalog passes through while the feature is on.
func requireSecurityCheck(ctx context.Context, tx pgx.Tx, appID uuid.UUID) error {
	var verified bool
	err := tx.QueryRow(ctx, `
		SELECT `+securityVerifiedSQL+`
		FROM apps a
		CROSS JOIN security_check_settings ss
		LEFT JOIN app_security_checks sc ON sc.review_id = a.security_review_id
		WHERE a.id = $1`, appID).Scan(&verified)
	if err != nil {
		return normalizeError("check app security verification", err)
	}
	if !verified {
		return ErrSecurityCheckRequired
	}
	return nil
}

// securityCheckEnabled answers within the caller's transaction so a setting
// flipped mid-request cannot be read twice with different answers.
func securityCheckEnabled(ctx context.Context, tx pgx.Tx) (bool, error) {
	settings, err := getSecurityCheckSettings(ctx, tx, true)
	if err != nil {
		return false, err
	}
	return settings.Enabled, nil
}

// securityRelevantChange reports whether an edit changes what a reviewer was
// shown. Everything an owner can type is relevant: a reviewer who approved one
// service URL did not approve another.
func securityRelevantChange(before model.App, input model.AppInput) bool {
	return before.Name != input.Name || before.Slug != input.Slug ||
		before.Summary != input.Summary || before.Description != input.Description ||
		before.Icon != input.Icon || before.Gradient != input.Gradient ||
		before.ServiceURL != input.ServiceURL || before.CategoryID.String() != input.CategoryID ||
		before.Language != input.Language || before.Framework != input.Framework ||
		before.SupportsMCP != input.SupportsMCP || before.SupportsAPI != input.SupportsAPI ||
		before.Team != input.Team || before.Version != input.Version ||
		before.Visibility != input.Visibility ||
		!sameStrings(before.Tags, input.Tags) || !sameStrings(before.Screenshots, input.Screenshots)
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

// gateAppStatus refuses the two transitions that expose an app — the review
// queue and the catalog — unless SecCheck has cleared its current content.
// Draft, rejected and archived are always allowed: they take an app off the
// shelf rather than onto it.
func gateAppStatus(ctx context.Context, tx pgx.Tx, appID uuid.UUID, status string) error {
	if status != model.AppStatusPending && status != model.AppStatusPublished {
		return nil
	}
	enabled, err := securityCheckEnabled(ctx, tx)
	if err != nil || !enabled {
		return err
	}
	return requireSecurityCheck(ctx, tx, appID)
}
