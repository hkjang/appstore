package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
)

type AuditListOptions struct {
	ActorID   *uuid.UUID
	Action    string
	Resource  string
	RequestID string
	Limit     int
	Offset    int
}

func nullableJSON(value json.RawMessage) (any, error) {
	if len(value) == 0 {
		return nil, nil
	}
	if !json.Valid(value) {
		return nil, fmt.Errorf("audit JSON: %w", ErrInvalid)
	}
	return []byte(value), nil
}

func (r *Repository) AppendAudit(ctx context.Context, entry model.AuditLog) (model.AuditLog, error) {
	entry.Actor = strings.TrimSpace(entry.Actor)
	entry.Action = strings.TrimSpace(entry.Action)
	entry.Resource = strings.TrimSpace(entry.Resource)
	entry.ResourceID = strings.TrimSpace(entry.ResourceID)
	entry.RequestID = strings.TrimSpace(entry.RequestID)
	if entry.Actor == "" {
		entry.Actor = "system"
	}
	if entry.Action == "" || entry.Resource == "" {
		return model.AuditLog{}, fmt.Errorf("audit action or resource: %w", ErrInvalid)
	}
	before, err := nullableJSON(entry.Before)
	if err != nil {
		return model.AuditLog{}, err
	}
	after, err := nullableJSON(entry.After)
	if err != nil {
		return model.AuditLog{}, err
	}
	err = r.pool.QueryRow(ctx, `
		INSERT INTO audit_logs(actor_id, actor, action, resource, resource_id,
			before_value, after_value, ip, user_agent, request_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, created_at`, entry.ActorID, entry.Actor, entry.Action,
		entry.Resource, entry.ResourceID, before, after, entry.IP,
		entry.UserAgent, entry.RequestID).Scan(&entry.ID, &entry.CreatedAt)
	return entry, normalizeError("append audit log", err)
}

func (r *Repository) ListAuditLogs(ctx context.Context, options AuditListOptions) (model.Page[model.AuditLog], error) {
	limit, offset := normalizePage(options.Limit, options.Offset, 100)
	where := []string{"true"}
	args := []any{}
	add := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if options.ActorID != nil {
		where = append(where, `actor_id = `+add(*options.ActorID))
	}
	if action := strings.TrimSpace(options.Action); action != "" {
		where = append(where, `action = `+add(action))
	}
	if resource := strings.TrimSpace(options.Resource); resource != "" {
		where = append(where, `resource = `+add(resource))
	}
	if requestID := strings.TrimSpace(options.RequestID); requestID != "" {
		where = append(where, `request_id = `+add(requestID))
	}
	args = append(args, limit, offset)
	query := `
		SELECT count(*) OVER(), id, actor_id, actor, action, resource, resource_id,
			before_value, after_value, ip, user_agent, request_id, created_at
		FROM audit_logs WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return model.Page[model.AuditLog]{}, normalizeError("list audit logs", err)
	}
	defer rows.Close()
	page := model.Page[model.AuditLog]{Items: []model.AuditLog{}, Limit: limit, Offset: offset}
	for rows.Next() {
		var entry model.AuditLog
		var total int
		if err := rows.Scan(&total, &entry.ID, &entry.ActorID, &entry.Actor,
			&entry.Action, &entry.Resource, &entry.ResourceID, &entry.Before,
			&entry.After, &entry.IP, &entry.UserAgent, &entry.RequestID,
			&entry.CreatedAt); err != nil {
			return model.Page[model.AuditLog]{}, normalizeError("scan audit log", err)
		}
		page.Total = total
		page.Items = append(page.Items, entry)
	}
	return page, normalizeError("iterate audit logs", rows.Err())
}
