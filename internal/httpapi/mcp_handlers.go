package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/mcp"
	"github.com/hkjang/appstore/internal/model"
)

func (s *Server) mcpPolicy(ctx context.Context) (bool, bool, error) {
	settings, err := s.repository.GetMCPSettings(ctx)
	if err != nil {
		return false, false, err
	}
	return settings.Enabled, settings.Anonymous, nil
}

func (s *Server) executeMCPTool(ctx context.Context, caller mcp.Caller, name string, arguments map[string]any) (any, error) {
	limit := argumentInt(arguments, "limit", 24, 100)
	switch name {
	case "apps_list":
		return s.repository.ListApps(ctx, model.AppListOptions{Limit: limit, Offset: argumentInt(arguments, "offset", 0, 100000), Sort: "updated"})
	case "apps_search":
		return s.repository.ListApps(ctx, model.AppListOptions{
			Query: argumentString(arguments, "query"), Category: argumentString(arguments, "category"),
			Language: argumentString(arguments, "language"), MCPOnly: argumentBool(arguments, "mcp"),
			Limit: limit, Sort: "updated",
		})
	case "app_get":
		slug := argumentString(arguments, "slug")
		if slug == "" {
			return nil, errors.New("slug is required")
		}
		return s.repository.GetAppBySlug(ctx, slug, false)
	case "categories_list":
		items, err := s.repository.ListCategories(ctx, false)
		if err != nil {
			return nil, err
		}
		return map[string]any{"categories": items}, nil
	case "featured_apps":
		return s.repository.ListApps(ctx, model.AppListOptions{Featured: true, Limit: limit, Sort: "updated"})
	case "trending_apps":
		return s.repository.ListApps(ctx, model.AppListOptions{Limit: limit, Sort: "trending"})
	case "mcp_apps":
		return s.repository.ListApps(ctx, model.AppListOptions{MCPOnly: true, Limit: limit, Sort: "updated"})
	case "my_apps":
		userID, err := mcpUserID(caller)
		if err != nil {
			return nil, err
		}
		return s.repository.ListApps(ctx, model.AppListOptions{OwnerID: &userID, IncludeAll: true, Limit: 100, Sort: "updated"})
	case "app_submit":
		return s.mcpSubmitApp(ctx, caller, arguments)
	case "app_update":
		return s.mcpUpdateApp(ctx, caller, arguments)
	case "apps_manage":
		return s.repository.ListApps(ctx, model.AppListOptions{
			Status: argumentString(arguments, "status"), Limit: limit, IncludeAll: true, Sort: "updated",
		})
	case "settings_get":
		return s.mcpSettings(ctx)
	case "workflow_get":
		return s.repository.GetWorkflowConfig(ctx)
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
}

func (s *Server) mcpSubmitApp(ctx context.Context, caller mcp.Caller, arguments map[string]any) (any, error) {
	userID, err := mcpUserID(caller)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return nil, err
	}
	var input model.AppInput
	if err := json.Unmarshal(encoded, &input); err != nil {
		return nil, fmt.Errorf("decode app input: %w", err)
	}
	if err := ValidateAppInput(&input); err != nil {
		return nil, err
	}
	app, err := s.repository.CreateApp(ctx, &userID, input, model.AppStatusDraft)
	if err != nil {
		return nil, err
	}
	result, err := s.repository.SubmitApp(ctx, app.ID, userID)
	if err != nil {
		return nil, err
	}
	s.recordMCPAudit(ctx, caller, "app.create", "app", app.ID.String(), nil, result)
	return result, nil
}

func (s *Server) mcpUpdateApp(ctx context.Context, caller mcp.Caller, arguments map[string]any) (any, error) {
	userID, err := mcpUserID(caller)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(argumentString(arguments, "id"))
	if err != nil {
		return nil, errors.New("a valid app id is required")
	}
	before, err := s.repository.GetAppByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if (before.OwnerID == nil || *before.OwnerID != userID) && !caller.Can("apps:manage") {
		return nil, errors.New("only the app owner can update this app")
	}
	input := model.AppInput{
		Name: before.Name, Slug: before.Slug, Summary: before.Summary, Description: before.Description,
		Icon: before.Icon, Gradient: before.Gradient, ServiceURL: before.ServiceURL,
		CategoryID: before.CategoryID.String(), Tags: before.Tags, Screenshots: before.Screenshots,
		Language: before.Language, Framework: before.Framework, SupportsMCP: before.SupportsMCP,
		SupportsAPI: before.SupportsAPI, Team: before.Team, Version: before.Version, Visibility: before.Visibility,
	}
	mergeMCPAppInput(&input, arguments)
	if err := ValidateAppInput(&input); err != nil {
		return nil, err
	}
	after, err := s.repository.UpdateApp(ctx, id, input)
	if err != nil {
		return nil, err
	}
	workflow, workflowErr := s.repository.GetWorkflowConfig(ctx)
	if workflowErr == nil && workflow.Enabled && workflow.ReapprovalAfterEdit && before.Status == model.AppStatusPublished {
		result, submitErr := s.repository.SubmitApp(ctx, id, userID)
		if submitErr != nil {
			return nil, submitErr
		}
		after = result.App
	}
	s.recordMCPAudit(ctx, caller, "app.update", "app", id.String(), before, after)
	return after, nil
}

func mergeMCPAppInput(input *model.AppInput, arguments map[string]any) {
	stringsToSet := map[string]*string{
		"name": &input.Name, "slug": &input.Slug, "summary": &input.Summary,
		"description": &input.Description, "icon": &input.Icon, "gradient": &input.Gradient,
		"serviceUrl": &input.ServiceURL, "categoryId": &input.CategoryID,
		"language": &input.Language, "framework": &input.Framework, "team": &input.Team,
		"version": &input.Version, "visibility": &input.Visibility,
	}
	for key, destination := range stringsToSet {
		if value, ok := arguments[key].(string); ok {
			*destination = value
		}
	}
	if value, ok := arguments["supportsMcp"].(bool); ok {
		input.SupportsMCP = value
	}
	if value, ok := arguments["supportsApi"].(bool); ok {
		input.SupportsAPI = value
	}
	if value, ok := stringSliceArgument(arguments["tags"]); ok {
		input.Tags = value
	}
	if value, ok := stringSliceArgument(arguments["screenshots"]); ok {
		input.Screenshots = value
	}
}

func stringSliceArgument(value any) ([]string, bool) {
	raw, ok := value.([]any)
	if !ok {
		if direct, directOK := value.([]string); directOK {
			return direct, true
		}
		return nil, false
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		result = append(result, text)
	}
	return result, true
}

func (s *Server) mcpSettings(ctx context.Context) (map[string]any, error) {
	system, err := s.repository.GetSystemSettings(ctx)
	if err != nil {
		return nil, err
	}
	apiSettings, err := s.repository.GetAPISettings(ctx)
	if err != nil {
		return nil, err
	}
	mcpSettings, err := s.repository.GetMCPSettings(ctx)
	if err != nil {
		return nil, err
	}
	oidcSettings, err := s.repository.GetOIDCSettings(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"system": system, "api": apiSettings, "mcp": mcpSettings,
		"authentication": safeOIDCSettings(oidcSettings),
	}, nil
}

func mcpUserID(caller mcp.Caller) (uuid.UUID, error) {
	if !caller.Authenticated {
		return uuid.Nil, errors.New("authentication is required")
	}
	id, err := uuid.Parse(caller.UserID)
	if err != nil {
		return uuid.Nil, errors.New("authenticated user id is invalid")
	}
	return id, nil
}

func argumentString(arguments map[string]any, key string) string {
	value, _ := arguments[key].(string)
	return strings.TrimSpace(value)
}

func argumentBool(arguments map[string]any, key string) bool {
	value, _ := arguments[key].(bool)
	return value
}

func argumentInt(arguments map[string]any, key string, fallback, maximum int) int {
	value := fallback
	switch raw := arguments[key].(type) {
	case json.Number:
		if parsed, err := raw.Int64(); err == nil {
			value = int(parsed)
		}
	case float64:
		value = int(raw)
	case int:
		value = raw
	}
	if value < 0 {
		value = 0
	}
	if maximum > 0 && value > maximum {
		value = maximum
	}
	return value
}

func (s *Server) recordMCPAudit(ctx context.Context, caller mcp.Caller, action, resource, resourceID string, before, after any) {
	userID, err := uuid.Parse(caller.UserID)
	if err != nil {
		return
	}
	actor := "mcp"
	if user, getErr := s.repository.GetUserByID(ctx, userID); getErr == nil {
		actor = user.Username
	}
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if _, err := s.repository.AppendAudit(ctx, model.AuditLog{
		ActorID: &userID, Actor: actor, Action: action, Resource: resource,
		ResourceID: resourceID, Before: beforeJSON, After: afterJSON, RequestID: "mcp",
	}); err != nil {
		s.logger.ErrorContext(ctx, "append MCP audit log failed", "error", err, "action", action)
	}
}
