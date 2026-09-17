package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	appauth "github.com/hkjang/appstore/internal/auth"
	"github.com/hkjang/appstore/internal/mcp"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

// MCP through SSO — a Keycloak access token beside the personal key.
//
// The MCP authorization specification (2025-06-18 and later) is OAuth 2.1:
// this server is a resource server that publishes where its authorization
// server is (RFC 9728), turns a 401 into a pointer there, and accepts exactly
// the tokens that server issued for this resource. Signing in, PKCE and the
// code exchange are Keycloak's and the client's; nothing here issues tokens.
//
// The personal key stays: automation without a person behind it, and any
// deployment without Keycloak, keep using it. A token is a second way through
// the same door — it authenticates an account the web sign-in already
// created, with the permissions an administrator chose, and never more than
// a key could carry.

// defaultMCPOAuthScopes is what an SSO subject may do when the administrator
// has not said otherwise: the read-only tools and the caller's own apps.
var defaultMCPOAuthScopes = []string{"mcp:read", "apps:read"}

// mcpOAuthConfig is the resource-server configuration resolved for one
// request: the stored switches plus the addresses derived from them.
type mcpOAuthConfig struct {
	// Enabled is the administrator's switch; Active is that switch with
	// everything it depends on present. When they differ, Reason says why.
	Enabled  bool
	Active   bool
	Reason   string
	Issuer   string
	Resource string
	Audience []string
	Scopes   []string
}

// resolveMCPOAuth turns the three settings that take part — MCP, OIDC and the
// service URL — into one answer. The resource identifier comes from the
// setting, else from the service URL; the request's Host is the last resort
// because anyone can set that header.
func resolveMCPOAuth(mcpSettings model.MCPSettings, oidcSettings model.OIDCSettings, siteURL string, r *http.Request) mcpOAuthConfig {
	oauth := mcpSettings.OAuth
	config := mcpOAuthConfig{
		Enabled:  oauth.Enabled,
		Issuer:   strings.TrimRight(strings.TrimSpace(oidcSettings.IssuerURL), "/"),
		Resource: mcpResource(oauth.Resource, siteURL, r),
		Audience: append([]string(nil), oauth.Audience...),
		Scopes:   append([]string(nil), oauth.Scopes...),
	}
	if len(config.Scopes) == 0 {
		config.Scopes = append([]string(nil), defaultMCPOAuthScopes...)
	}
	var missing []string
	if !mcpSettings.Enabled {
		missing = append(missing, "MCP가 꺼져 있습니다")
	}
	if config.Issuer == "" {
		missing = append(missing, "인증·SSO의 Issuer URL이 비어 있습니다")
	}
	if config.Resource == "" {
		missing = append(missing, "리소스 식별자를 만들 수 없습니다(서비스 접속 URL 또는 리소스 식별자를 입력하세요)")
	}
	switch {
	case !config.Enabled:
		config.Reason = "꺼져 있습니다"
	case len(missing) > 0:
		config.Reason = strings.Join(missing, "; ")
	default:
		config.Active = true
	}
	return config
}

// mcpResource picks the identifier this deployment claims for /mcp.
func mcpResource(configured, siteURL string, r *http.Request) string {
	if configured = strings.TrimSpace(configured); configured != "" {
		return configured
	}
	if base := publicOrigin(siteURL); base != "" {
		return base + "/mcp"
	}
	if r == nil || r.Host == "" {
		return ""
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/mcp"
}

// publicOrigin is the service URL without a trailing slash when it is an
// absolute HTTP(S) address, and empty otherwise.
func publicOrigin(siteURL string) string {
	siteURL = strings.TrimRight(strings.TrimSpace(siteURL), "/")
	parsed, err := url.Parse(siteURL)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return siteURL
}

// validateMCPResource is the shape an identifier must have to ever match a
// token's aud: an absolute HTTP(S) address for the MCP endpoint, without
// credentials, query or fragment.
func validateMCPResource(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("리소스 식별자는 절대 HTTP(S) 주소여야 합니다(예: https://apps.example.com/mcp).")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery {
		return errors.New("리소스 식별자에는 인증 정보·쿼리·프래그먼트를 넣을 수 없습니다.")
	}
	if !strings.HasSuffix(parsed.Path, "/mcp") {
		return errors.New("리소스 식별자는 MCP 경로(/mcp)로 끝나야 합니다.")
	}
	return nil
}

// metadataURL is where a refused client is sent to read the document
// below: the resource's origin plus RFC 9728's well-known path.
func (c mcpOAuthConfig) metadataURL() string {
	parsed, err := url.Parse(c.Resource)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host + "/.well-known/oauth-protected-resource" + parsed.Path
}

func (c mcpOAuthConfig) status() *model.MCPOAuthStatus {
	status := &model.MCPOAuthStatus{Active: c.Active, Issuer: c.Issuer, Resource: c.Resource, Reason: c.Reason}
	if c.Resource != "" {
		status.MetadataURL = c.metadataURL()
	}
	return status
}

func (s *Server) mcpOAuth(r *http.Request) mcpOAuthConfig {
	mcpSettings, err := s.repository.GetMCPSettings(r.Context())
	if err != nil {
		return mcpOAuthConfig{Reason: "MCP 설정을 읽을 수 없습니다"}
	}
	oidcSettings, err := s.repository.GetOIDCSettings(r.Context())
	if err != nil {
		return mcpOAuthConfig{Enabled: mcpSettings.OAuth.Enabled, Reason: "인증·SSO 설정을 읽을 수 없습니다"}
	}
	return resolveMCPOAuth(mcpSettings, oidcSettings, s.loadSystemSettings(r).SiteURL, r)
}

// protectedResourceMetadata is RFC 9728: the document a refused MCP client
// reads to find the authorization server. Public by design — it says where
// to sign in, not who is signed in — and bare JSON rather than this API's
// envelope, because the reader is an OAuth client library.
func (s *Server) protectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	config := s.mcpOAuth(r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if !config.Active {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "MCP_OAUTH_DISABLED", "error_description": "이 서버의 MCP는 SSO 토큰을 받지 않습니다. 개인 키(aps_)를 사용하세요.",
		})
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"resource":                 config.Resource,
		"authorization_servers":    []string{config.Issuer},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         config.Scopes,
		"resource_name":            strings.TrimSpace(s.loadSystemSettings(r).SiteName) + " MCP",
	})
}

// mcpChallenge is the WWW-Authenticate value for a 401 on /mcp. Only there:
// a REST 401 carrying it would send browsers and other clients somewhere
// they have no business going.
func (s *Server) mcpChallenge(r *http.Request, rejected bool) string {
	config := s.mcpOAuth(r)
	if !config.Active {
		return ""
	}
	value := fmt.Sprintf(`Bearer realm=%q, resource_metadata=%q`, "AppStore", config.metadataURL())
	if rejected {
		value += `, error="invalid_token"`
	}
	return value
}

// bearerToken is the Authorization: Bearer value when it is not a personal
// key. Keys are recognised by prefix earlier; what is left is either a JWT
// or nothing this server accepts.
func bearerToken(r *http.Request) string {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1]
	}
	return ""
}

// oauthPrincipal turns a Keycloak access token into the principal the token's
// subject would be with a key, or says exactly what to fix.
func (s *Server) oauthPrincipal(r *http.Request, config mcpOAuthConfig, token string) (*Principal, error) {
	ctx := r.Context()
	verified, err := s.accessTokens.Verify(ctx, config.Issuer, token)
	if err != nil {
		s.logger.WarnContext(ctx, "MCP SSO token rejected", "error", err, "issuer", config.Issuer, "request_id", RequestID(ctx))
		return nil, &mcp.AuthError{Message: "SSO 액세스 토큰이 유효하지 않습니다(서명·발급자·만료·종류). 클라이언트에서 다시 로그인하세요."}
	}
	// Whom the token was minted for. A real Keycloak 26 access token carries
	// the client in azp and aud: ["account"] — the client id is not in aud
	// unless an Audience mapper puts our identifier there. So either aud
	// names this resource (the mapper path), or aud or azp is a client the
	// administrator listed (the path that needs no mapper). Anything else is
	// a token for some other application in the realm.
	if !audienceAccepted(config, verified) {
		return nil, &mcp.AuthError{Message: fmt.Sprintf(
			"SSO 토큰이 이 서버를 위해 발급된 것이 아닙니다(aud=%v, azp=%q). 관리자가 MCP 서버 설정의 허용 대상에 %q를 더하거나, Keycloak 클라이언트에 Audience 매퍼로 %q를 넣어야 합니다.",
			verified.Audience, verified.AuthorizedParty, verified.AuthorizedParty, config.Resource)}
	}
	// The same account the web sign-in provisioned, found the same way, but
	// without the provisioning half: a machine presenting a token is not the
	// moment to decide who somebody is.
	user, err := s.repository.GetUserBySubject(ctx, verified.Subject)
	if errors.Is(err, store.ErrNotFound) {
		return nil, &mcp.AuthError{Message: "이 SSO 계정은 AppStore에 등록되지 않았습니다. 먼저 웹으로 한 번 로그인하세요."}
	}
	if err != nil {
		return nil, err
	}
	if !user.Active {
		return nil, &mcp.AuthError{Message: "이 SSO 계정은 AppStore에서 비활성 상태입니다. 관리자에게 문의하세요."}
	}
	definitions, err := s.repository.ListKeyPermissionDefinitions(ctx, false)
	if err != nil {
		return nil, err
	}
	permissions := oauthPermissions(config.Scopes, verified.Scopes, definitions, user)
	return &Principal{User: user, AuthMethod: "oauth", Permissions: permissions}, nil
}

func audienceAccepted(config mcpOAuthConfig, token appauth.AccessToken) bool {
	for _, audience := range token.Audience {
		if audience != "" && (audience == config.Resource || slices.Contains(config.Audience, audience)) {
			return true
		}
	}
	return token.AuthorizedParty != "" && slices.Contains(config.Audience, token.AuthorizedParty)
}

// oauthPermissions is the key gate applied to an SSO subject: the
// administrator's scopes, kept to the key permissions that exist and are
// active, kept to what the account itself may do. When the token carries
// this app's own vocabulary in its scope claim, only the intersection is
// granted — a token can narrow the grant, never widen it.
func oauthPermissions(configured, tokenScopes []string, definitions []model.KeyPermissionDefinition, user model.User) map[string]bool {
	active := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		if definition.Active {
			active[definition.Key] = true
		}
	}
	var narrowed map[string]bool
	for _, scope := range tokenScopes {
		if active[scope] {
			if narrowed == nil {
				narrowed = map[string]bool{}
			}
			narrowed[scope] = true
		}
	}
	held := make(map[string]bool, len(user.Permissions))
	for _, permission := range user.Permissions {
		held[permission] = true
	}
	superAdmin := slices.Contains(user.Roles, "super_admin")
	permissions := map[string]bool{}
	for _, scope := range configured {
		if !active[scope] || (narrowed != nil && !narrowed[scope]) {
			continue
		}
		if superAdmin || held[scope] {
			permissions[scope] = true
		}
	}
	return permissions
}
