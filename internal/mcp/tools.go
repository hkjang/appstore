package mcp

import (
	"context"
	"errors"
	"fmt"
)

type ExecuteFunc func(context.Context, Caller, string, map[string]any) (any, error)

type AppTools struct {
	Execute ExecuteFunc
}

var ErrUnknownTool = errors.New("unknown tool")

type toolGrant struct {
	Tool       Tool
	Permission string
	AuthOnly   bool
}

var appToolGrants = []toolGrant{
	{Tool: tool("apps_list", "앱 목록", "공개 AppStore 카탈로그를 페이지 단위로 조회합니다.", objectSchema(map[string]any{
		"limit": integerProperty("반환할 앱 수", 1, 100), "offset": integerProperty("시작 위치", 0, 100000),
	})), Permission: ""},
	{Tool: tool("apps_search", "앱 검색", "검색어와 카테고리, 언어, MCP 지원 여부로 앱을 검색합니다.", objectSchema(map[string]any{
		"query": stringProperty("이름과 설명에서 찾을 검색어"), "category": stringProperty("카테고리 slug"),
		"language": stringProperty("개발 언어"), "mcp": map[string]any{"type": "boolean"}, "limit": integerProperty("반환할 앱 수", 1, 100),
	})), Permission: ""},
	{Tool: tool("app_get", "앱 상세", "slug로 공개 앱 상세 정보를 조회합니다.", requiredObjectSchema(map[string]any{
		"slug": stringProperty("앱 slug"),
	}, "slug")), Permission: ""},
	{Tool: tool("categories_list", "카테고리 목록", "활성 카테고리와 앱 수를 조회합니다.", emptySchema()), Permission: ""},
	{Tool: tool("featured_apps", "추천 앱", "관리자가 추천한 앱 목록을 조회합니다.", objectSchema(map[string]any{
		"limit": integerProperty("반환할 앱 수", 1, 100),
	})), Permission: ""},
	{Tool: tool("trending_apps", "인기 앱", "현재 인기 점수가 높은 앱을 조회합니다.", objectSchema(map[string]any{
		"limit": integerProperty("반환할 앱 수", 1, 100),
	})), Permission: ""},
	{Tool: tool("mcp_apps", "MCP 앱", "MCP를 지원하는 공개 앱을 조회합니다.", objectSchema(map[string]any{
		"limit": integerProperty("반환할 앱 수", 1, 100),
	})), Permission: ""},
	{Tool: tool("my_apps", "내 앱", "인증된 호출자가 등록한 앱을 조회합니다.", emptySchema()), Permission: "apps:read", AuthOnly: true},
	{Tool: tool("app_submit", "앱 등록", "앱을 등록합니다. 승인 workflow가 꺼져 있으면 즉시 게시됩니다.", requiredObjectSchema(map[string]any{
		"name": stringProperty("앱 이름"), "slug": stringProperty("URL slug"), "summary": stringProperty("한줄 설명"),
		"description": stringProperty("상세 설명"), "serviceUrl": stringProperty("서비스 접속 URL"), "categoryId": stringProperty("카테고리 ID"),
	}, "name", "slug", "summary", "description", "serviceUrl", "categoryId")), Permission: "apps:submit", AuthOnly: true},
	{Tool: tool("app_update", "앱 수정", "소유한 앱을 수정합니다.", requiredObjectSchema(map[string]any{
		"id": stringProperty("앱 ID"), "name": stringProperty("앱 이름"), "summary": stringProperty("한줄 설명"),
		"description": stringProperty("상세 설명"), "serviceUrl": stringProperty("서비스 접속 URL"),
	}, "id")), Permission: "apps:update", AuthOnly: true},
	{Tool: tool("apps_manage", "전체 앱 관리", "관리자 권한으로 모든 상태의 앱을 조회하거나 관리합니다.", objectSchema(map[string]any{
		"status": stringProperty("상태 filter"), "limit": integerProperty("반환할 앱 수", 1, 100),
	})), Permission: "apps:manage", AuthOnly: true},
	{Tool: tool("settings_get", "설정 조회", "민감한 원문을 제외한 운영 설정을 조회합니다.", emptySchema()), Permission: "settings:read", AuthOnly: true},
	{Tool: tool("workflow_get", "Workflow 조회", "현재 승인 workflow 정책을 조회합니다.", emptySchema()), Permission: "settings:read", AuthOnly: true},
}

func mutatingTool(name string) bool {
	switch name {
	case "app_submit", "app_update", "apps_manage":
		return true
	default:
		return false
	}
}

func (a AppTools) Tools(_ context.Context, caller Caller) []Tool {
	result := make([]Tool, 0, len(appToolGrants))
	for _, grant := range appToolGrants {
		if grant.AuthOnly && !caller.Authenticated {
			continue
		}
		if caller.Authenticated && !caller.Can("mcp:read") {
			continue
		}
		if mutatingTool(grant.Tool.Name) && !caller.Can("mcp:execute") {
			continue
		}
		if grant.Permission != "" && !caller.Can(grant.Permission) {
			continue
		}
		result = append(result, grant.Tool)
	}
	return result
}

func (a AppTools) Call(ctx context.Context, caller Caller, name string, arguments map[string]any) (any, error) {
	var selected *toolGrant
	for index := range appToolGrants {
		if appToolGrants[index].Tool.Name == name {
			selected = &appToolGrants[index]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTool, name)
	}
	if selected.AuthOnly && !caller.Authenticated {
		return nil, errors.New("authentication is required for this tool")
	}
	if caller.Authenticated && !caller.Can("mcp:read") {
		return nil, errors.New("permission mcp:read is required")
	}
	if mutatingTool(selected.Tool.Name) && !caller.Can("mcp:execute") {
		return nil, errors.New("permission mcp:execute is required")
	}
	if selected.Permission != "" && !caller.Can(selected.Permission) {
		return nil, fmt.Errorf("permission %s is required", selected.Permission)
	}
	if a.Execute == nil {
		return nil, errors.New("tool executor is unavailable")
	}
	return a.Execute(ctx, caller, name, arguments)
}

func tool(name, title, description string, schema map[string]any) Tool {
	return Tool{
		Name: name, Title: title, Description: description, InputSchema: schema,
		Annotations: map[string]any{"readOnlyHint": name != "app_submit" && name != "app_update" && name != "apps_manage"},
	}
}

func emptySchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false}
}

func objectSchema(properties map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
}

func requiredObjectSchema(properties map[string]any, required ...string) map[string]any {
	value := objectSchema(properties)
	value["required"] = required
	return value
}

func stringProperty(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func integerProperty(description string, minimum, maximum int) map[string]any {
	return map[string]any{"type": "integer", "description": description, "minimum": minimum, "maximum": maximum}
}
