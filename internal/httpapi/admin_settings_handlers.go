package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/ai"
	appauth "github.com/hkjang/appstore/internal/auth"
	"github.com/hkjang/appstore/internal/mcp"
	"github.com/hkjang/appstore/internal/model"
)

func (s *Server) adminWorkflow(w http.ResponseWriter, r *http.Request) {
	value, err := s.repository.GetWorkflowConfig(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, value)
}

func (s *Server) adminUpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	var input model.WorkflowConfig
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	before, _ := s.repository.GetWorkflowConfig(r.Context())
	after, err := s.repository.UpdateWorkflowConfig(r.Context(), input)
	if err != nil {
		WriteError(w, r, storeError(err, "WORKFLOW_NOT_FOUND", "Workflow 설정을 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "workflow.setting.update", "workflow", "default", before, after)
	WriteJSON(w, http.StatusOK, after)
}

func safeOIDCSettings(value model.OIDCSettings) model.OIDCSettings {
	value.ClientSecretSet = value.ClientSecret != "" || value.ClientSecretSet
	value.ClientSecret = ""
	return value
}

func (s *Server) adminAuthentication(w http.ResponseWriter, r *http.Request) {
	value, err := s.repository.GetOIDCSettings(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, safeOIDCSettings(value))
}

func (s *Server) adminUpdateAuthentication(w http.ResponseWriter, r *http.Request) {
	var request struct {
		model.OIDCSettings
		ClientSecret string `json:"clientSecret"`
	}
	if err := DecodeJSON(w, r, &request); err != nil {
		WriteError(w, r, err)
		return
	}
	input := request.OIDCSettings
	before, err := s.repository.GetOIDCSettings(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	plaintext := strings.TrimSpace(request.ClientSecret)
	if input.Enabled && strings.TrimSpace(input.IssuerURL) == "" {
		WriteError(w, r, Validation("OIDC를 활성화하려면 Issuer URL을 입력하세요.", nil))
		return
	}
	if input.Enabled && plaintext == "" && !before.ClientSecretSet {
		WriteError(w, r, Validation("OIDC를 활성화하려면 Client Secret을 입력하세요.", nil))
		return
	}
	var encrypted *string
	if plaintext != "" {
		value, err := s.box.Encrypt(plaintext)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		encrypted = &value
	}
	principal := CurrentPrincipal(r.Context())
	after, err := s.repository.UpdateOIDCSettings(r.Context(), input, encrypted, &principal.User.ID)
	if err != nil {
		WriteError(w, r, storeError(err, "OIDC_NOT_FOUND", "OIDC 설정을 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "oidc.setting.update", "oidc_settings", "default", safeOIDCSettings(before), safeOIDCSettings(after))
	WriteJSON(w, http.StatusOK, safeOIDCSettings(after))
}

func (s *Server) adminTestAuthentication(w http.ResponseWriter, r *http.Request) {
	settings, err := s.repository.GetOIDCSettings(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	// The admin can try an Issuer URL straight from the form, before saving it.
	var input struct {
		IssuerURL string `json:"issuerUrl"`
		ClientID  string `json:"clientId"`
	}
	if r.ContentLength > 0 {
		if err := DecodeJSON(w, r, &input); err != nil {
			WriteError(w, r, err)
			return
		}
	}
	issuer := strings.TrimSpace(input.IssuerURL)
	if issuer == "" {
		issuer = strings.TrimSpace(settings.IssuerURL)
	}
	clientID := strings.TrimSpace(input.ClientID)
	if clientID == "" {
		clientID = strings.TrimSpace(settings.ClientID)
	}
	if issuer == "" {
		WriteError(w, r, Validation("Issuer URL을 입력한 뒤 다시 시도하세요.", map[string]any{"issuerUrl": "Issuer URL이 비어 있습니다."}))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	discovery, err := s.oidc.Discover(ctx, issuer)
	if err != nil {
		s.logger.WarnContext(r.Context(), "OIDC connection test failed", "error", err, "issuer", issuer, "request_id", RequestID(r.Context()))
		WriteError(w, r, &APIError{
			Status: http.StatusBadGateway, Code: "OIDC_DISCOVERY_FAILED",
			Message: "OIDC discovery 연결 테스트에 실패했습니다. " + err.Error(),
			Details: map[string]any{
				"issuerUrl":    issuer,
				"discoveryUrl": appauth.DiscoveryDocumentURL(issuer),
				"reason":       err.Error(),
			},
		})
		return
	}
	result := map[string]any{
		"ok": true, "issuer": discovery.Issuer, "discoveryUrl": discovery.DocumentURL,
		"authorizationEndpoint": discovery.AuthorizationEndpoint,
		"tokenEndpoint":         discovery.TokenEndpoint,
		"userInfoEndpoint":      discovery.UserInfoEndpoint,
		"endSessionEndpoint":    discovery.EndSessionEndpoint,
		"jwksUri":               discovery.JWKSURI,
		"scopesSupported":       discovery.ScopesSupported,
		"pkceSupported":         slices.Contains(discovery.CodeChallengeMethods, "S256"),
		"clientId":              clientID,
		"clientSecretSet":       settings.ClientSecretSet,
		"redirectUrl":           s.oidcRedirectURL(r),
	}
	s.recordAudit(r, "oidc.connection.test", "oidc_settings", "default", nil, map[string]any{"ok": true, "issuer": discovery.Issuer})
	WriteJSON(w, http.StatusOK, result)
}

func defaultAIProvider() model.AIProvider {
	return model.AIProvider{
		Name: "OpenAI Compatible", Kind: "openai-compatible", ContextWindow: 262144,
		MaxInputTokens: 253952, MaxOutputTokens: 8192, Temperature: 0.7,
		TimeoutSeconds: 120, Retries: 1, Streaming: true, Enabled: true,
	}
}

func safeAIProvider(value model.AIProvider) model.AIProvider {
	value.APIKeySet = value.APIKey != "" || value.APIKeySet
	value.APIKey = ""
	return value
}

func (s *Server) adminAI(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListAIProviders(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	if len(items) == 0 {
		WriteJSON(w, http.StatusOK, defaultAIProvider())
		return
	}
	WriteJSON(w, http.StatusOK, safeAIProvider(items[0]))
}

func (s *Server) adminUpdateAI(w http.ResponseWriter, r *http.Request) {
	var request struct {
		model.AIProvider
		APIKey string `json:"apiKey"`
	}
	if err := DecodeJSON(w, r, &request); err != nil {
		WriteError(w, r, err)
		return
	}
	input := request.AIProvider
	plaintext := strings.TrimSpace(request.APIKey)
	if err := ai.ValidateProvider(input); err != nil {
		WriteError(w, r, Validation(err.Error(), nil))
		return
	}
	var before any
	var encrypted *string
	if plaintext != "" {
		value, err := s.box.Encrypt(plaintext)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		encrypted = &value
	}
	var after model.AIProvider
	var err error
	if input.ID == uuid.Nil {
		ciphertext := ""
		if encrypted != nil {
			ciphertext = *encrypted
		}
		after, err = s.repository.CreateAIProvider(r.Context(), input, ciphertext)
	} else {
		current, getErr := s.repository.GetAIProvider(r.Context(), input.ID)
		if getErr != nil {
			WriteError(w, r, storeError(getErr, "AI_PROVIDER_NOT_FOUND", "AI Provider를 찾을 수 없습니다."))
			return
		}
		before = safeAIProvider(current)
		after, err = s.repository.UpdateAIProvider(r.Context(), input, encrypted)
	}
	if err != nil {
		WriteError(w, r, storeError(err, "AI_PROVIDER_NOT_FOUND", "AI Provider를 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "ai.setting.update", "ai_provider", after.ID.String(), before, safeAIProvider(after))
	WriteJSON(w, http.StatusOK, safeAIProvider(after))
}

func (s *Server) adminAIModels(w http.ResponseWriter, r *http.Request) {
	providerID, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("providerId")))
	if err != nil {
		WriteError(w, r, Validation("providerId가 올바르지 않습니다.", nil))
		return
	}
	items, err := s.repository.ListAIModels(r.Context(), providerID)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) adminUpsertAIModel(w http.ResponseWriter, r *http.Request) {
	var input model.AIModel
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	if input.ContextWindow > 0 && input.MaxInputTokens+input.MaxOutputTokens > input.ContextWindow {
		WriteError(w, r, Validation("Model의 최대 입력·출력 token 합은 context window를 넘을 수 없습니다.", nil))
		return
	}
	before := s.findAIModel(r.Context(), input.ProviderID, input.ID, input.Name)
	after, err := s.repository.UpsertAIModel(r.Context(), input)
	if err != nil {
		WriteError(w, r, storeError(err, "AI_MODEL_NOT_FOUND", "AI Model을 저장할 수 없습니다."))
		return
	}
	s.recordAudit(r, "ai.model.update", "ai_model", after.ID.String(), before, after)
	WriteJSON(w, http.StatusOK, after)
}

func (s *Server) adminDeleteAIModel(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"), "AI Model")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	var before any
	providers, _ := s.repository.ListAIProviders(r.Context())
	for _, provider := range providers {
		if item := s.findAIModel(r.Context(), provider.ID, id, ""); item != nil {
			before = item
			break
		}
	}
	if err := s.repository.DeleteAIModel(r.Context(), id); err != nil {
		WriteError(w, r, storeError(err, "AI_MODEL_NOT_FOUND", "AI Model을 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "ai.model.delete", "ai_model", id.String(), before, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) findAIModel(ctx context.Context, providerID, id uuid.UUID, name string) any {
	if providerID == uuid.Nil {
		return nil
	}
	items, err := s.repository.ListAIModels(ctx, providerID)
	if err != nil {
		return nil
	}
	for _, item := range items {
		if (id != uuid.Nil && item.ID == id) || (id == uuid.Nil && item.Name == name) {
			return item
		}
	}
	return nil
}

func (s *Server) adminAPI(w http.ResponseWriter, r *http.Request) {
	value, err := s.repository.GetAPISettings(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, value)
}

func (s *Server) adminUpdateAPI(w http.ResponseWriter, r *http.Request) {
	var input model.APISettings
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	before, _ := s.repository.GetAPISettings(r.Context())
	principal := CurrentPrincipal(r.Context())
	after, err := s.repository.UpdateAPISettings(r.Context(), input, &principal.User.ID)
	if err != nil {
		WriteError(w, r, storeError(err, "API_SETTINGS_NOT_FOUND", "API 설정을 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "api.setting.update", "api_settings", "default", before, after)
	WriteJSON(w, http.StatusOK, after)
}

func (s *Server) adminMCP(w http.ResponseWriter, r *http.Request) {
	value, err := s.repository.GetMCPSettings(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, value)
}

func (s *Server) adminUpdateMCP(w http.ResponseWriter, r *http.Request) {
	var input model.MCPSettings
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	if input.ProtocolVersion != mcp.ProtocolVersion {
		WriteError(w, r, Validation("지원하는 MCP Protocol Version은 "+mcp.ProtocolVersion+"입니다.", nil))
		return
	}
	before, _ := s.repository.GetMCPSettings(r.Context())
	principal := CurrentPrincipal(r.Context())
	after, err := s.repository.UpdateMCPSettings(r.Context(), input, &principal.User.ID)
	if err != nil {
		WriteError(w, r, storeError(err, "MCP_SETTINGS_NOT_FOUND", "MCP 설정을 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "mcp.setting.update", "mcp_settings", "default", before, after)
	WriteJSON(w, http.StatusOK, after)
}

func (s *Server) adminSecurity(w http.ResponseWriter, r *http.Request) {
	policy, err := s.repository.GetKeyPolicy(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	permissions, err := s.repository.ListKeyPermissionDefinitions(r.Context(), true)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	templates, err := s.repository.ListKeyPermissionTemplates(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"maxKeys": policy.MaxKeys, "defaultExpiryDays": policy.DefaultExpiryDays,
		"rotationGraceDays": policy.RotationGraceDays, "expireUnused": policy.ExpireUnused,
		"unusedExpiryDays": policy.UnusedExpiryDays, "forceRotation": policy.ForceRotation,
		"forceRotationDays": policy.ForceRotationDays, "permissions": permissions, "templates": templates,
	})
}

func (s *Server) adminUpdateSecurity(w http.ResponseWriter, r *http.Request) {
	var input model.KeyPolicy
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	if !input.ExpireUnused && input.UnusedExpiryDays < 1 {
		input.UnusedExpiryDays = 90
	}
	if !input.ForceRotation && input.ForceRotationDays < 1 {
		input.ForceRotationDays = 90
	}
	before, _ := s.repository.GetKeyPolicy(r.Context())
	after, err := s.repository.UpdateKeyPolicy(r.Context(), input)
	if err != nil {
		WriteError(w, r, storeError(err, "KEY_POLICY_NOT_FOUND", "Key 정책을 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "key.policy.update", "key_policy", "default", before, after)
	WriteJSON(w, http.StatusOK, after)
}

type keyPermissionRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Active      *bool  `json:"active,omitempty"`
}

func (s *Server) adminUpsertKeyPermission(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(chi.URLParam(r, "key"))
	var input keyPermissionRequest
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	active := true
	if input.Active != nil {
		active = *input.Active
	}
	definitions, _ := s.repository.ListKeyPermissionDefinitions(r.Context(), true)
	var before any
	for _, item := range definitions {
		if item.Key == key {
			before = item
			break
		}
	}
	after, err := s.repository.UpsertKeyPermissionDefinition(r.Context(), model.KeyPermissionDefinition{
		Key: key, Name: input.Name, Description: input.Description, Active: active,
	})
	if err != nil {
		WriteError(w, r, storeError(err, "KEY_PERMISSION_NOT_FOUND", "Key 권한 정의를 저장할 수 없습니다."))
		return
	}
	s.recordAudit(r, "key.permission.update", "key_permission", after.Key, before, after)
	WriteJSON(w, http.StatusOK, after)
}

func (s *Server) adminCreateKeyTemplate(w http.ResponseWriter, r *http.Request) {
	var input model.KeyPermissionTemplate
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	if err := s.validateKeyTemplatePermissions(r.Context(), input.Permissions); err != nil {
		WriteError(w, r, err)
		return
	}
	after, err := s.repository.CreateKeyPermissionTemplate(r.Context(), input)
	if err != nil {
		WriteError(w, r, storeError(err, "KEY_TEMPLATE_NOT_FOUND", "Key 권한 Template을 생성할 수 없습니다."))
		return
	}
	s.recordAudit(r, "key.template.create", "key_permission_template", after.ID.String(), nil, after)
	WriteJSON(w, http.StatusCreated, after)
}

func (s *Server) adminUpdateKeyTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"), "Template")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	var input model.KeyPermissionTemplate
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	input.ID = id
	if err := s.validateKeyTemplatePermissions(r.Context(), input.Permissions); err != nil {
		WriteError(w, r, err)
		return
	}
	before := s.findKeyTemplate(r.Context(), id)
	after, err := s.repository.UpdateKeyPermissionTemplate(r.Context(), input)
	if err != nil {
		WriteError(w, r, storeError(err, "KEY_TEMPLATE_NOT_FOUND", "Key 권한 Template을 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "key.template.update", "key_permission_template", id.String(), before, after)
	WriteJSON(w, http.StatusOK, after)
}

func (s *Server) adminDeleteKeyTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"), "Template")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	before := s.findKeyTemplate(r.Context(), id)
	if err := s.repository.DeleteKeyPermissionTemplate(r.Context(), id); err != nil {
		WriteError(w, r, storeError(err, "KEY_TEMPLATE_NOT_FOUND", "Key 권한 Template을 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "key.template.delete", "key_permission_template", id.String(), before, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) validateKeyTemplatePermissions(ctx context.Context, values []string) error {
	definitions, err := s.repository.ListKeyPermissionDefinitions(ctx, false)
	if err != nil {
		return err
	}
	allowed := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		allowed[definition.Key] = true
	}
	for _, value := range values {
		if !allowed[value] {
			return Validation("활성화된 Key 권한만 Template에 포함할 수 있습니다.", map[string]any{"permission": value})
		}
	}
	return nil
}

func (s *Server) findKeyTemplate(ctx context.Context, id uuid.UUID) any {
	items, err := s.repository.ListKeyPermissionTemplates(ctx)
	if err != nil {
		return nil
	}
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	return nil
}

func (s *Server) adminSystemSettings(w http.ResponseWriter, r *http.Request) {
	value, err := s.repository.GetSystemSettings(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, value)
}

func (s *Server) adminUpdateSystemSettings(w http.ResponseWriter, r *http.Request) {
	var input model.SystemSettings
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	if strings.TrimSpace(input.SiteURL) != "" {
		parsed, err := url.Parse(input.SiteURL)
		if err != nil || !parsed.IsAbs() || parsed.User != nil || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			WriteError(w, r, Validation("서비스 접속 URL은 유효한 HTTP(S) URL이어야 합니다.", nil))
			return
		}
	}
	before, _ := s.repository.GetSystemSettings(r.Context())
	principal := CurrentPrincipal(r.Context())
	after, err := s.repository.UpdateSystemSettings(r.Context(), input, &principal.User.ID)
	if err != nil {
		WriteError(w, r, storeError(err, "SYSTEM_SETTINGS_NOT_FOUND", "시스템 설정을 찾을 수 없습니다."))
		return
	}
	s.recordAudit(r, "system.setting.update", "system_settings", "default", before, after)
	WriteJSON(w, http.StatusOK, after)
}
