package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/mail"
)

// GetMailSettings reads the mail.* rows of system_settings into one
// configuration. Rows that were never written take the defaults, so a fresh
// installation reads as "mail off" without any seed. Password holds the
// stored ciphertext; HTTP handlers must serve Public() and nothing else.
// An empty mail.base_url falls back to the service URL of the system
// settings, which is where links in mail should point anyway.
func (r *Repository) GetMailSettings(ctx context.Context) (mail.Config, error) {
	rows, err := r.pool.Query(ctx, `SELECT key, value FROM system_settings WHERE key LIKE $1`, mail.KeyPrefix+"%")
	if err != nil {
		return mail.Config{}, normalizeError("get mail settings", err)
	}
	defer rows.Close()
	values := map[string]any{}
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			return mail.Config{}, normalizeError("scan mail setting", err)
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return mail.Config{}, fmt.Errorf("decode setting %q: %w", key, err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return mail.Config{}, normalizeError("iterate mail settings", err)
	}
	config := mail.ConfigFromValues(values)
	if config.BaseURL == "" {
		if system, err := r.GetSystemSettings(ctx); err == nil {
			config.BaseURL = strings.TrimRight(strings.TrimSpace(system.SiteURL), "/")
		}
	}
	return config, nil
}

// UpdateMailSettings stores every mail.* row in one transaction. The password
// is written only when the caller hands over a new ciphertext — a settings
// save never blanks it — and an empty ciphertext clears it.
func (r *Repository) UpdateMailSettings(ctx context.Context, config mail.Config, encryptedPassword *string, updatedBy *uuid.UUID) (mail.Config, error) {
	config = config.Normalize()
	if err := config.Validate(); err != nil {
		return mail.Config{}, err
	}
	values := config.Values()
	if encryptedPassword != nil {
		values[mail.KeyPassword] = *encryptedPassword
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return mail.Config{}, normalizeError("begin mail settings", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for key, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			return mail.Config{}, fmt.Errorf("encode setting %q: %w", key, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO system_settings(key, value, updated_by, updated_at)
			VALUES ($1, $2, $3, now())
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value,
				updated_by = EXCLUDED.updated_by, updated_at = now()`, key, encoded, updatedBy); err != nil {
			return mail.Config{}, normalizeError("put mail setting", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return mail.Config{}, normalizeError("commit mail settings", err)
	}
	return r.GetMailSettings(ctx)
}

// LookupUserEmails is the one directory lookup mail borrows: active accounts
// with an address, by id. Everyone else is simply absent from the result.
func (r *Repository) LookupUserEmails(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	result := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT id, email FROM users WHERE id = ANY($1) AND active AND email <> ''`, ids)
	if err != nil {
		return nil, normalizeError("lookup user emails", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var email string
		if err := rows.Scan(&id, &email); err != nil {
			return nil, normalizeError("scan user email", err)
		}
		result[id] = email
	}
	return result, normalizeError("iterate user emails", rows.Err())
}

// ReviewerIDs lists who can decide a review of an app in the given team:
// every active holder of a reviewer role, plus holders of a team leader role
// in that team. It mirrors reviewScope in the HTTP layer, minus the
// administrative fallback — administrators are not mailed about every review.
func (r *Repository) ReviewerIDs(ctx context.Context, reviewerRoles, teamLeaderRoles []string, team string) ([]uuid.UUID, error) {
	team = strings.TrimSpace(team)
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT u.id FROM users u
		JOIN user_roles ur ON ur.user_id = u.id
		JOIN roles ro ON ro.id = ur.role_id
		WHERE u.active AND u.email <> ''
			AND (ro.key = ANY($1) OR ($3 <> '' AND u.team = $3 AND ro.key = ANY($2)))
		ORDER BY u.id`, uniqueStrings(reviewerRoles), uniqueStrings(teamLeaderRoles), team)
	if err != nil {
		return nil, normalizeError("list reviewer ids", err)
	}
	defer rows.Close()
	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, normalizeError("scan reviewer id", err)
		}
		ids = append(ids, id)
	}
	return ids, normalizeError("iterate reviewer ids", rows.Err())
}

func (r *Repository) RecordMailDelivery(ctx context.Context, delivery mail.Delivery) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		INSERT INTO mail_deliveries(event, recipient, subject, reference, actor_id, status, attempts)
		VALUES ($1, $2, $3, $4, $5, $6, 0) RETURNING id`,
		delivery.Event, delivery.Recipient, delivery.Subject, delivery.Reference, delivery.ActorID, mail.StatusQueued).Scan(&id)
	if err != nil {
		return uuid.Nil, normalizeError("record mail delivery", err)
	}
	return id, nil
}

func (r *Repository) FinishMailDelivery(ctx context.Context, id uuid.UUID, status string, attempts int, errorMessage string) error {
	result, err := r.pool.Exec(ctx, `
		UPDATE mail_deliveries SET status = $2, attempts = GREATEST(attempts, $3), error_message = $4, updated_at = now()
		WHERE id = $1`, id, status, attempts, errorMessage)
	if err != nil {
		return normalizeError("finish mail delivery", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("finish mail delivery: %w", ErrNotFound)
	}
	return nil
}

// MailDeliveryPage is the delivery log, newest first, with a status
// breakdown over the whole table so the screen can say how many failed
// without paging through everything.
type MailDeliveryPage struct {
	Items  []mail.Delivery `json:"items"`
	Total  int             `json:"total"`
	Status map[string]int  `json:"status"`
}

func (r *Repository) ListMailDeliveries(ctx context.Context, status string, limit int) (MailDeliveryPage, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	page := MailDeliveryPage{Items: []mail.Delivery{}, Status: map[string]int{}}
	status = normalizeKey(status)
	if status != "" && status != mail.StatusQueued && status != mail.StatusSent && status != mail.StatusFailed {
		return MailDeliveryPage{}, fmt.Errorf("mail delivery status: %w", ErrInvalid)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, event, recipient, subject, reference, actor_id, status, attempts, error_message, created_at, updated_at
		FROM mail_deliveries WHERE ($1 = '' OR status = $1)
		ORDER BY created_at DESC, id LIMIT $2`, status, limit)
	if err != nil {
		return MailDeliveryPage{}, normalizeError("list mail deliveries", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item mail.Delivery
		if err := rows.Scan(&item.ID, &item.Event, &item.Recipient, &item.Subject, &item.Reference, &item.ActorID,
			&item.Status, &item.Attempts, &item.ErrorMessage, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return MailDeliveryPage{}, normalizeError("scan mail delivery", err)
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return MailDeliveryPage{}, normalizeError("iterate mail deliveries", err)
	}
	counts, err := r.pool.Query(ctx, `SELECT status, count(*)::int FROM mail_deliveries GROUP BY status`)
	if err != nil {
		return MailDeliveryPage{}, normalizeError("count mail deliveries", err)
	}
	defer counts.Close()
	for counts.Next() {
		var key string
		var count int
		if err := counts.Scan(&key, &count); err != nil {
			return MailDeliveryPage{}, normalizeError("scan mail delivery count", err)
		}
		page.Status[key] = count
		page.Total += count
	}
	return page, normalizeError("iterate mail delivery counts", counts.Err())
}
