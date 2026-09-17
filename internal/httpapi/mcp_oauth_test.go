package httpapi

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	appauth "github.com/hkjang/appstore/internal/auth"
	"github.com/hkjang/appstore/internal/model"
)

func mcpOAuthSettings(enabled bool, oauth model.MCPOAuthSettings) model.MCPSettings {
	return model.MCPSettings{Enabled: enabled, RateLimitPerMinute: 60, ProtocolVersion: "2026-07-28", OAuth: oauth}
}

// guards: resolveMCPOAuth, mcpResource
func TestResolveMCPOAuthPicksTheResourceFromSettingThenSiteURLThenHost(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://internal:8080/mcp", nil)
	request.Host = "apps.corp.example"
	request.Header.Set("X-Forwarded-Proto", "https")
	oidc := model.OIDCSettings{IssuerURL: "https://sso.example/realms/company/"}

	explicit := resolveMCPOAuth(mcpOAuthSettings(true, model.MCPOAuthSettings{Enabled: true, Resource: "https://public.example/mcp"}), oidc, "https://site.example/", request)
	if !explicit.Active || explicit.Resource != "https://public.example/mcp" || explicit.Issuer != "https://sso.example/realms/company" {
		t.Fatalf("explicit resource: %+v", explicit)
	}
	if explicit.metadataURL() != "https://public.example/.well-known/oauth-protected-resource/mcp" {
		t.Errorf("metadata URL %q", explicit.metadataURL())
	}
	if strings.Join(explicit.Scopes, " ") != "mcp:read apps:read" {
		t.Errorf("default scopes %v", explicit.Scopes)
	}

	fromSite := resolveMCPOAuth(mcpOAuthSettings(true, model.MCPOAuthSettings{Enabled: true, Scopes: []string{"mcp:read"}}), oidc, "https://site.example/", request)
	if !fromSite.Active || fromSite.Resource != "https://site.example/mcp" || strings.Join(fromSite.Scopes, " ") != "mcp:read" {
		t.Fatalf("site URL resource: %+v", fromSite)
	}

	fromHost := resolveMCPOAuth(mcpOAuthSettings(true, model.MCPOAuthSettings{Enabled: true}), oidc, "", request)
	if !fromHost.Active || fromHost.Resource != "https://apps.corp.example/mcp" {
		t.Fatalf("host fallback: %+v", fromHost)
	}
	if got := resolveMCPOAuth(mcpOAuthSettings(true, model.MCPOAuthSettings{Enabled: true}), oidc, "not a url", nil); got.Active || got.Resource != "" {
		t.Fatalf("no request and no site URL must not yield a resource: %+v", got)
	}
}

// guards: resolveMCPOAuth — the switch alone does not make it live
func TestResolveMCPOAuthIsOffUnlessEverythingItNeedsIsPresent(t *testing.T) {
	oidc := model.OIDCSettings{IssuerURL: "https://sso.example/realms/company"}
	if got := resolveMCPOAuth(mcpOAuthSettings(true, model.MCPOAuthSettings{}), oidc, "https://site.example", nil); got.Active || got.Enabled {
		t.Fatalf("default must be off: %+v", got)
	}
	noIssuer := resolveMCPOAuth(mcpOAuthSettings(true, model.MCPOAuthSettings{Enabled: true}), model.OIDCSettings{}, "https://site.example", nil)
	if noIssuer.Active || !noIssuer.Enabled || !strings.Contains(noIssuer.Reason, "Issuer") {
		t.Fatalf("no issuer: %+v", noIssuer)
	}
	mcpOff := resolveMCPOAuth(mcpOAuthSettings(false, model.MCPOAuthSettings{Enabled: true}), oidc, "https://site.example", nil)
	if mcpOff.Active || !strings.Contains(mcpOff.Reason, "MCP") {
		t.Fatalf("MCP off: %+v", mcpOff)
	}
	status := mcpOff.status()
	if status.Active || status.MetadataURL != "https://site.example/.well-known/oauth-protected-resource/mcp" || status.Issuer != oidc.IssuerURL {
		t.Fatalf("status: %+v", status)
	}
}

// guards: validateMCPResource
func TestValidateMCPResource(t *testing.T) {
	for value, ok := range map[string]bool{
		"https://apps.example/mcp":          true,
		"http://localhost:8080/mcp":         true,
		"https://apps.example/appstore/mcp": true,
		"https://apps.example":              false,
		"https://apps.example/api":          false,
		"https://user:pw@apps.example/mcp":  false,
		"https://apps.example/mcp?x=1":      false,
		"https://apps.example/mcp#frag":     false,
		"ftp://apps.example/mcp":            false,
		"/mcp":                              false,
	} {
		if err := validateMCPResource(value); (err == nil) != ok {
			t.Errorf("validateMCPResource(%q) = %v, want ok=%v", value, err, ok)
		}
	}
}

// guards: audienceAccepted
func TestAudienceAcceptedNeedsTheResourceInAudOrAListedClient(t *testing.T) {
	config := mcpOAuthConfig{Resource: "https://apps.example/mcp", Audience: []string{"claude-mcp"}}
	cases := []struct {
		name  string
		token appauth.AccessToken
		want  bool
	}{
		{"resource in aud (mapper path)", appauth.AccessToken{Audience: []string{"account", "https://apps.example/mcp"}, AuthorizedParty: "other"}, true},
		{"listed client in azp (no mapper)", appauth.AccessToken{Audience: []string{"account"}, AuthorizedParty: "claude-mcp"}, true},
		{"listed client in aud", appauth.AccessToken{Audience: []string{"claude-mcp"}}, true},
		{"another app's token", appauth.AccessToken{Audience: []string{"account"}, AuthorizedParty: "weekly-web"}, false},
		{"nothing at all", appauth.AccessToken{}, false},
		{"resource in azp does not count", appauth.AccessToken{AuthorizedParty: "https://apps.example/mcp"}, false},
	}
	for _, tc := range cases {
		if got := audienceAccepted(config, tc.token); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// guards: oauthPermissions
func TestOAuthPermissionsNeverExceedWhatAKeyCouldCarry(t *testing.T) {
	definitions := []model.KeyPermissionDefinition{
		{Key: "mcp:read", Active: true}, {Key: "apps:read", Active: true},
		{Key: "mcp:execute", Active: true}, {Key: "apps:submit", Active: false},
	}
	user := model.User{Permissions: []string{"mcp:read", "apps:read", "apps:submit"}}
	keys := func(permissions map[string]bool) string {
		result := make([]string, 0, len(permissions))
		for key := range permissions {
			result = append(result, key)
		}
		sortStrings(result)
		return strings.Join(result, " ")
	}
	// Configured scopes, kept to active definitions and the account's own
	// permissions: mcp:execute is not the user's, apps:submit is inactive.
	if got := keys(oauthPermissions([]string{"mcp:read", "apps:read", "mcp:execute", "apps:submit"}, nil, definitions, user)); got != "apps:read mcp:read" {
		t.Errorf("configured ∩ active ∩ user = %q", got)
	}
	// A token carrying this app's vocabulary narrows the grant.
	if got := keys(oauthPermissions([]string{"mcp:read", "apps:read"}, []string{"openid", "mcp:read"}, definitions, user)); got != "mcp:read" {
		t.Errorf("token scope narrowing = %q", got)
	}
	// A token carrying only foreign scopes changes nothing.
	if got := keys(oauthPermissions([]string{"mcp:read", "apps:read"}, []string{"openid", "profile"}, definitions, user)); got != "apps:read mcp:read" {
		t.Errorf("foreign scopes = %q", got)
	}
	// A super admin passes the user gate but still not the definition gate,
	// and the token's role claims play no part: the grant is the scopes.
	admin := model.User{Roles: []string{"super_admin"}}
	if got := keys(oauthPermissions([]string{"mcp:read", "mcp:execute", "apps:submit"}, nil, definitions, admin)); got != "mcp:execute mcp:read" {
		t.Errorf("super admin = %q", got)
	}
	if got := oauthPermissions([]string{"mcp:read"}, nil, definitions, admin); got["*"] {
		t.Error("an SSO subject must never receive the wildcard")
	}
}

func sortStrings(values []string) { sort.Strings(values) }
