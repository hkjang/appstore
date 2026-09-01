package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
	"github.com/jackc/pgx/v5"
)

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func getWorkflowConfig(ctx context.Context, db queryRower) (model.WorkflowConfig, error) {
	var config model.WorkflowConfig
	var reviewerJSON, leaderJSON []byte
	err := db.QueryRow(ctx, `
		SELECT enabled, levels, reviewer_roles, team_leader_roles, auto_publish,
			reject_reason_required, reapproval_after_edit, prevent_self_approval, updated_at
		FROM workflow_config WHERE singleton`).Scan(
		&config.Enabled, &config.Levels, &reviewerJSON, &leaderJSON, &config.AutoPublish,
		&config.RejectReasonRequired, &config.ReapprovalAfterEdit,
		&config.PreventSelfApproval, &config.UpdatedAt,
	)
	if err != nil {
		return model.WorkflowConfig{}, normalizeError("get workflow config", err)
	}
	if err := json.Unmarshal(reviewerJSON, &config.ReviewerRoles); err != nil {
		return model.WorkflowConfig{}, fmt.Errorf("decode reviewer roles: %w", err)
	}
	if err := json.Unmarshal(leaderJSON, &config.TeamLeaderRoles); err != nil {
		return model.WorkflowConfig{}, fmt.Errorf("decode team leader roles: %w", err)
	}
	return config, nil
}

func (r *Repository) GetWorkflowConfig(ctx context.Context) (model.WorkflowConfig, error) {
	return getWorkflowConfig(ctx, r.pool)
}

func (r *Repository) UpdateWorkflowConfig(ctx context.Context, config model.WorkflowConfig) (model.WorkflowConfig, error) {
	config.ReviewerRoles = uniqueStrings(config.ReviewerRoles)
	config.TeamLeaderRoles = uniqueStrings(config.TeamLeaderRoles)
	if config.Levels < 1 || config.Levels > 10 || len(config.ReviewerRoles) == 0 {
		return model.WorkflowConfig{}, fmt.Errorf("workflow config: %w", ErrInvalid)
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE workflow_config SET enabled = $1, levels = $2, reviewer_roles = $3,
			team_leader_roles = $4, auto_publish = $5, reject_reason_required = $6,
			reapproval_after_edit = $7, prevent_self_approval = $8, updated_at = now()
		WHERE singleton`, config.Enabled, config.Levels, jsonValue(config.ReviewerRoles),
		jsonValue(config.TeamLeaderRoles), config.AutoPublish, config.RejectReasonRequired,
		config.ReapprovalAfterEdit, config.PreventSelfApproval)
	if err != nil {
		return model.WorkflowConfig{}, normalizeError("update workflow config", err)
	}
	return r.GetWorkflowConfig(ctx)
}

type SubmitResult struct {
	App    model.App     `json:"app"`
	Review *model.Review `json:"review,omitempty"`
}

func (r *Repository) SubmitApp(ctx context.Context, appID, submitterID uuid.UUID) (SubmitResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return SubmitResult{}, normalizeError("begin app submission", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var lockedAppID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM apps WHERE id = $1 FOR UPDATE`, appID).Scan(&lockedAppID); err != nil {
		return SubmitResult{}, normalizeError("lock submitted app", err)
	}
	config, err := getWorkflowConfig(ctx, tx)
	if err != nil {
		return SubmitResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE reviews SET status = 'cancelled', decided_at = now()
		WHERE app_id = $1 AND status = 'pending'`, appID); err != nil {
		return SubmitResult{}, normalizeError("cancel superseded reviews", err)
	}

	var reviewID *uuid.UUID
	if config.Enabled {
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO reviews(app_id, submitter_id, level, status)
			VALUES ($1, $2, 1, 'pending') RETURNING id`, appID, submitterID).Scan(&id); err != nil {
			return SubmitResult{}, normalizeError("create app review", err)
		}
		reviewID = &id
		if _, err := tx.Exec(ctx, `UPDATE apps SET status = 'pending_review', updated_at = now() WHERE id = $1`, appID); err != nil {
			return SubmitResult{}, normalizeError("mark app pending review", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE apps SET status = 'published', published_at = COALESCE(published_at, now()), updated_at = now()
			WHERE id = $1`, appID); err != nil {
			return SubmitResult{}, normalizeError("publish app without workflow", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return SubmitResult{}, normalizeError("commit app submission", err)
	}

	result := SubmitResult{}
	result.App, err = r.GetAppByID(ctx, appID)
	if err != nil {
		return SubmitResult{}, err
	}
	if reviewID != nil {
		review, err := r.GetReview(ctx, *reviewID)
		if err != nil {
			return SubmitResult{}, err
		}
		result.Review = &review
	}
	return result, nil
}

const reviewColumns = `
	rv.id, rv.app_id, a.name, a.slug, rv.submitter_id,
	COALESCE(NULLIF(s.display_name, ''), s.username), rv.reviewer_id,
	COALESCE(NULLIF(rev.display_name, ''), rev.username, ''), a.team,
	rv.level, rv.status, rv.reason, rv.created_at, rv.decided_at`

const reviewFrom = `
	FROM reviews rv
	JOIN apps a ON a.id = rv.app_id
	JOIN users s ON s.id = rv.submitter_id
	LEFT JOIN users rev ON rev.id = rv.reviewer_id`

func scanReview(row rowScanner) (model.Review, error) {
	var review model.Review
	err := row.Scan(&review.ID, &review.AppID, &review.AppName, &review.AppSlug,
		&review.SubmitterID, &review.SubmitterName, &review.ReviewerID,
		&review.ReviewerName, &review.Team, &review.Level, &review.Status,
		&review.Reason, &review.CreatedAt, &review.DecidedAt)
	return review, err
}

func scanReviewWithTotal(row rowScanner) (model.Review, int, error) {
	var review model.Review
	var total int
	err := row.Scan(&total, &review.ID, &review.AppID, &review.AppName, &review.AppSlug,
		&review.SubmitterID, &review.SubmitterName, &review.ReviewerID,
		&review.ReviewerName, &review.Team, &review.Level, &review.Status,
		&review.Reason, &review.CreatedAt, &review.DecidedAt)
	return review, total, err
}

type ReviewListOptions struct {
	Status      string
	Team        string
	Level       int
	SubmitterID *uuid.UUID
	ReviewerID  *uuid.UUID
	Limit       int
	Offset      int
}

func (r *Repository) ListReviews(ctx context.Context, options ReviewListOptions) (model.Page[model.Review], error) {
	limit, offset := normalizePage(options.Limit, options.Offset, 50)
	where := []string{"true"}
	args := []any{}
	add := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if status := normalizeKey(options.Status); status != "" {
		where = append(where, `rv.status = `+add(status))
	}
	if team := strings.TrimSpace(options.Team); team != "" {
		where = append(where, `a.team = `+add(team))
	}
	if options.Level > 0 {
		where = append(where, `rv.level = `+add(options.Level))
	}
	if options.SubmitterID != nil {
		where = append(where, `rv.submitter_id = `+add(*options.SubmitterID))
	}
	if options.ReviewerID != nil {
		where = append(where, `(rv.reviewer_id IS NULL OR rv.reviewer_id = `+add(*options.ReviewerID)+`)`)
	}
	args = append(args, limit, offset)
	query := `SELECT count(*) OVER(), ` + reviewColumns + reviewFrom +
		` WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(` ORDER BY rv.created_at, rv.id LIMIT $%d OFFSET $%d`, len(args)-1, len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return model.Page[model.Review]{}, normalizeError("list reviews", err)
	}
	defer rows.Close()
	page := model.Page[model.Review]{Items: []model.Review{}, Limit: limit, Offset: offset}
	for rows.Next() {
		review, total, err := scanReviewWithTotal(rows)
		if err != nil {
			return model.Page[model.Review]{}, normalizeError("scan review", err)
		}
		page.Total = total
		page.Items = append(page.Items, review)
	}
	return page, normalizeError("iterate reviews", rows.Err())
}

func (r *Repository) GetReview(ctx context.Context, id uuid.UUID) (model.Review, error) {
	review, err := scanReview(r.pool.QueryRow(ctx, `SELECT `+reviewColumns+reviewFrom+` WHERE rv.id = $1`, id))
	return review, normalizeError("get review", err)
}

type ReviewDecisionResult struct {
	Review     model.Review  `json:"review"`
	NextReview *model.Review `json:"nextReview,omitempty"`
	AppStatus  string        `json:"appStatus"`
}

func (r *Repository) DecideReview(ctx context.Context, reviewID, reviewerID uuid.UUID, decision, reason string) (ReviewDecisionResult, error) {
	decision = normalizeKey(decision)
	reason = strings.TrimSpace(reason)
	if decision != "approved" && decision != "rejected" {
		return ReviewDecisionResult{}, fmt.Errorf("review decision: %w", ErrInvalid)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ReviewDecisionResult{}, normalizeError("begin review decision", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var appID, submitterID uuid.UUID
	var level int
	var status string
	err = tx.QueryRow(ctx, `
		SELECT app_id, submitter_id, level, status FROM reviews
		WHERE id = $1 FOR UPDATE`, reviewID).Scan(&appID, &submitterID, &level, &status)
	if err != nil {
		return ReviewDecisionResult{}, normalizeError("lock review", err)
	}
	if status != "pending" {
		return ReviewDecisionResult{}, fmt.Errorf("review is already decided: %w", ErrConflict)
	}
	config, err := getWorkflowConfig(ctx, tx)
	if err != nil {
		return ReviewDecisionResult{}, err
	}
	if config.PreventSelfApproval && reviewerID == submitterID {
		return ReviewDecisionResult{}, fmt.Errorf("self approval is disabled: %w", ErrForbidden)
	}
	if decision == "rejected" && config.RejectReasonRequired && reason == "" {
		return ReviewDecisionResult{}, fmt.Errorf("rejection reason is required: %w", ErrInvalid)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE reviews SET reviewer_id = $2, status = $3, reason = $4, decided_at = now()
		WHERE id = $1`, reviewID, reviewerID, decision, reason); err != nil {
		return ReviewDecisionResult{}, normalizeError("record review decision", err)
	}

	appStatus := model.AppStatusPending
	var nextReviewID *uuid.UUID
	if decision == "rejected" {
		appStatus = model.AppStatusRejected
		if _, err := tx.Exec(ctx, `UPDATE apps SET status = 'rejected', updated_at = now() WHERE id = $1`, appID); err != nil {
			return ReviewDecisionResult{}, normalizeError("reject reviewed app", err)
		}
	} else if config.Enabled && level < config.Levels {
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO reviews(app_id, submitter_id, level, status)
			VALUES ($1, $2, $3, 'pending') RETURNING id`, appID, submitterID, level+1).Scan(&id); err != nil {
			return ReviewDecisionResult{}, normalizeError("create next review level", err)
		}
		nextReviewID = &id
	} else if config.AutoPublish || !config.Enabled {
		appStatus = model.AppStatusPublished
		if _, err := tx.Exec(ctx, `
			UPDATE apps SET status = 'published', published_at = COALESCE(published_at, now()), updated_at = now()
			WHERE id = $1`, appID); err != nil {
			return ReviewDecisionResult{}, normalizeError("publish approved app", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ReviewDecisionResult{}, normalizeError("commit review decision", err)
	}

	result := ReviewDecisionResult{AppStatus: appStatus}
	result.Review, err = r.GetReview(ctx, reviewID)
	if err != nil {
		return ReviewDecisionResult{}, err
	}
	if nextReviewID != nil {
		nextReview, err := r.GetReview(ctx, *nextReviewID)
		if err != nil {
			return ReviewDecisionResult{}, err
		}
		result.NextReview = &nextReview
	}
	return result, nil
}
