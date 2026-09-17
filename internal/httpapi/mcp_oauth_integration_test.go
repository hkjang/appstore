package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hkjang/appstore/internal/auth/authtest"
	"github.com/hkjang/appstore/internal/config"
	appcrypto "github.com/hkjang/appstore/internal/crypto"
	"github.com/hkjang/appstore/internal/database"
	"github.com/hkjang/appstore/internal/keymanager"
	"github.com/hkjang/appstore/internal/mcp"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

// MCP through SSO, end to end against a database and a fake Keycloak: the
// server says where to sign in, a token minted for this resource opens the
// tools for an account the web sign-in already created, and everything else
// — other audiences, unknown people, REST paths — stays shut. The key flow
// is exercised alongside because it must not have moved.
func TestPostgreSQLMCPOAuthIntegration(t *testing.T) {
	dsn := os.Getenv("APPSTORE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("APPSTORE_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := database.Initialize(ctx, config.Config{
		PostgresDSN: dsn, BootstrapAdmin: "bootstrap-admin",
		BootstrapAdminPassword: "initial-bootstrap-password",
		EncryptionKey:          "01234567890123456789012345678901",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	lockConnection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockConnection.Exec(ctx, `SELECT pg_advisory_lock($1)`, int64(0x41505053544f5245)); err != nil {
		lockConnection.Release()
		t.Fatal(err)
	}
	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer unlockCancel()
		_, _ = lockConnection.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, int64(0x41505053544f5245))
		lockConnection.Release()
	}()
	repository := store.New(pool)
	box, err := appcrypto.NewSecretBox("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}

	originalSystem, err := repository.GetSystemSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	originalMCP, err := repository.GetMCPSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	originalOIDC, err := repository.GetOIDCSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = repository.UpdateSystemSettings(context.Background(), originalSystem, nil)
		_, _ = repository.UpdateMCPSettings(context.Background(), originalMCP, nil)
		_, _ = repository.UpdateOIDCSettings(context.Background(), originalOIDC, &originalOIDC.ClientSecret, nil)
	}()

	idp := authtest.NewIDP(t)
	systemSettings := originalSystem
	systemSettings.SiteURL = "https://apps.example.test"
	if _, err := repository.UpdateSystemSettings(ctx, systemSettings, nil); err != nil {
		t.Fatal(err)
	}
	mcpSettings := originalMCP
	mcpSettings.Enabled = true
	mcpSettings.Anonymous = false
	mcpSettings.RateLimitPerMinute = 1000
	mcpSettings.ProtocolVersion = mcp.ProtocolVersion
	mcpSettings.OAuth = model.MCPOAuthSettings{}
	if _, err := repository.UpdateMCPSettings(ctx, mcpSettings, nil); err != nil {
		t.Fatal(err)
	}
	secret, err := box.Encrypt("client-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpdateOIDCSettings(ctx, model.OIDCSettings{
		Enabled: true, IssuerURL: idp.Issuer(), ClientID: "appstore-web",
		RoleMappings: map[string][]string{}, GroupMappings: map[string][]string{},
	}, &secret, nil); err != nil {
		t.Fatal(err)
	}

	// The account the web sign-in provisioned. Only its subject links it to
	// the token; the token's preferred_username is deliberately different.
	// Removed users are retained inactive, so each run needs its own subject.
	stamp := time.Now().Format("150405.000")
	subject := "subject-mcp-" + stamp
	member, err := repository.UpsertOIDCUser(ctx, store.OIDCUserInput{
		Subject: subject, Username: "sso-member-" + stamp, Email: "member@example.test",
		DisplayName: "SSO Member",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.DeleteUser(context.Background(), member.ID) }()
	if _, err := repository.ReplaceUserRoles(ctx, member.ID, []string{store.DefaultUserRole}); err != nil {
		t.Fatal(err)
	}
	policy, err := repository.GetKeyPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := keymanager.Generate(box, policy, []string{"mcp:read", "apps:read"}, map[string]bool{"mcp:read": true, "apps:read": true}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateAPIKey(ctx, store.CreateAPIKeyParams{
		UserID: member.ID, Name: "member key", Prefix: generated.Prefix, Hash: generated.Hash,
		Permissions: generated.Permissions, ExpiresAt: generated.ExpiresAt,
	}); err != nil {
		t.Fatal(err)
	}

	service, err := New(repository, box, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := service.Handler()
	if err != nil {
		t.Fatal(err)
	}
	const listTools = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"test","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}}`
	mcpCall := func(bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://apps.example.test/mcp", strings.NewReader(listTools))
		req.RemoteAddr = "198.51.100.77:4077"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("MCP-Protocol-Version", mcp.ProtocolVersion)
		req.Header.Set("Mcp-Method", "tools/list")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	get := func(target, bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.RemoteAddr = "198.51.100.77:4077"
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	toolNames := func(w *httptest.ResponseRecorder) string {
		var payload struct {
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &payload)
		names := make([]string, 0, len(payload.Result.Tools))
		for _, tool := range payload.Result.Tools {
			names = append(names, tool.Name)
		}
		return strings.Join(names, " ")
	}
	forThisServer := idp.AccessToken(t, []string{"account", "https://apps.example.test/mcp"}, map[string]any{"azp": "claude-mcp", "sub": subject})

	// Off by default: nothing is advertised, a 401 is the 401 it always
	// was, and a token is treated the way a key-only server treats any
	// bearer that is not a key.
	if w := get("/.well-known/oauth-protected-resource/mcp", ""); w.Code != http.StatusNotFound {
		t.Fatalf("metadata with SSO off: %d %s", w.Code, w.Body.String())
	}
	if w := mcpCall(""); w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("401 with SSO off: %d %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
	if w := mcpCall(forThisServer); w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("token with SSO off: %d %q %s", w.Code, w.Header().Get("WWW-Authenticate"), w.Body.String())
	}
	if w := get("/api/v1/public/config", ""); strings.Contains(w.Body.String(), "mcpOauthResource") {
		t.Fatalf("public config advertises SSO MCP while off: %s", w.Body.String())
	}

	mcpSettings.OAuth = model.MCPOAuthSettings{Enabled: true, Audience: []string{"claude-mcp"}}
	if _, err := repository.UpdateMCPSettings(ctx, mcpSettings, nil); err != nil {
		t.Fatal(err)
	}

	// RFC 9728: the bare document, readable cross-origin, naming this
	// resource and the web sign-in's issuer.
	for _, target := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		w := get(target, "")
		if w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("%s: %d %q %s", target, w.Code, w.Header().Get("Access-Control-Allow-Origin"), w.Body.String())
		}
		var metadata struct {
			Resource             string   `json:"resource"`
			AuthorizationServers []string `json:"authorization_servers"`
			BearerMethods        []string `json:"bearer_methods_supported"`
			Scopes               []string `json:"scopes_supported"`
			Name                 string   `json:"resource_name"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &metadata); err != nil {
			t.Fatalf("%s: %v in %s", target, err, w.Body.String())
		}
		if metadata.Resource != "https://apps.example.test/mcp" || len(metadata.AuthorizationServers) != 1 || metadata.AuthorizationServers[0] != idp.Issuer() ||
			strings.Join(metadata.BearerMethods, ",") != "header" || strings.Join(metadata.Scopes, " ") != "mcp:read apps:read" || !strings.HasSuffix(metadata.Name, " MCP") {
			t.Fatalf("%s: %+v", target, metadata)
		}
	}
	if w := get("/api/v1/public/config", ""); !strings.Contains(w.Body.String(), `"mcpOauthResource":"https://apps.example.test/mcp"`) {
		t.Fatalf("public config does not carry the SSO MCP address: %s", w.Body.String())
	}

	// The 401 now points at the document — on /mcp only.
	refused := mcpCall("")
	if refused.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer: %d %s", refused.Code, refused.Body.String())
	}
	challenge := refused.Header().Get("WWW-Authenticate")
	if !strings.HasPrefix(challenge, "Bearer ") || !strings.Contains(challenge, `resource_metadata="https://apps.example.test/.well-known/oauth-protected-resource/mcp"`) || strings.Contains(challenge, "invalid_token") {
		t.Fatalf("WWW-Authenticate %q", challenge)
	}
	if w := get("/api/v1/me", ""); w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("REST 401 must not carry the challenge: %d %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}

	// A token for this resource opens the tools for the registered account,
	// with the administrator's scopes: read tools and my_apps, nothing
	// mutating.
	opened := mcpCall(forThisServer)
	if opened.Code != http.StatusOK {
		t.Fatalf("token for this server refused: %d %s", opened.Code, opened.Body.String())
	}
	if names := toolNames(opened); !strings.Contains(names, "my_apps") || strings.Contains(names, "app_submit") || strings.Contains(names, "apps_manage") {
		t.Fatalf("tools for an SSO subject: %s", names)
	}
	// The same person with a key sees the same tools: the two doors lead
	// into the same room.
	withKey := mcpCall(generated.Plaintext)
	if withKey.Code != http.StatusOK || toolNames(withKey) != toolNames(opened) {
		t.Fatalf("key: %d %s vs token %s", withKey.Code, toolNames(withKey), toolNames(opened))
	}

	// Another application's token is refused, and the refusal says what was
	// seen and what would fix it.
	elsewhere := mcpCall(idp.AccessToken(t, "account", map[string]any{"azp": "weekly-web"}))
	if elsewhere.Code != http.StatusUnauthorized || !strings.Contains(elsewhere.Header().Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatalf("other audience: %d %q", elsewhere.Code, elsewhere.Header().Get("WWW-Authenticate"))
	}
	if body := elsewhere.Body.String(); !strings.Contains(body, "aud=[account]") || !strings.Contains(body, `azp=\"weekly-web\"`) || !strings.Contains(body, "https://apps.example.test/mcp") {
		t.Fatalf("refusal does not say what to fix: %s", body)
	}
	// The compatibility path: azp listed by the administrator, no mapper.
	if w := mcpCall(idp.AccessToken(t, "account", map[string]any{"azp": "claude-mcp", "sub": subject})); w.Code != http.StatusOK {
		t.Fatalf("listed azp without mapper: %d %s", w.Code, w.Body.String())
	}
	// The token's own scope claim can narrow the grant: without apps:read
	// the my_apps tool disappears.
	if names := toolNames(mcpCall(idp.AccessToken(t, "account", map[string]any{"azp": "claude-mcp", "sub": subject, "scope": "openid mcp:read"}))); strings.Contains(names, "my_apps") || !strings.Contains(names, "apps_list") {
		t.Fatalf("narrowed scopes: %s", names)
	}

	// Tokens that are not API credentials for this issuer.
	for name, token := range map[string]string{
		"expired":      idp.AccessToken(t, "https://apps.example.test/mcp", map[string]any{"exp": time.Now().Add(-time.Minute).Unix()}),
		"other issuer": authtest.NewIDP(t).AccessToken(t, "https://apps.example.test/mcp", nil),
		"typ ID":       idp.AccessToken(t, "https://apps.example.test/mcp", map[string]any{"typ": "ID"}),
		"HS256":        authtest.SignHS256(t, "secret", map[string]any{"iss": idp.Issuer(), "aud": "https://apps.example.test/mcp", "sub": "subject-mcp", "exp": time.Now().Add(time.Hour).Unix()}),
		"cnf":          idp.AccessToken(t, "https://apps.example.test/mcp", map[string]any{"cnf": map[string]any{"jkt": "x"}}),
	} {
		if w := mcpCall(token); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}

	// Nobody is provisioned by a token: an unknown subject is refused and
	// told to sign in on the web first, and no account appears.
	usersBefore, err := repository.ListUsers(ctx, store.UserListOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	stranger := mcpCall(idp.AccessToken(t, "https://apps.example.test/mcp", map[string]any{"sub": "subject-unknown", "preferred_username": "stranger"}))
	if stranger.Code != http.StatusUnauthorized || !strings.Contains(stranger.Body.String(), "웹으로") {
		t.Fatalf("unknown subject: %d %s", stranger.Code, stranger.Body.String())
	}
	if _, err := repository.GetUserBySubject(ctx, "subject-unknown"); err == nil {
		t.Fatal("a token created an account")
	}
	usersAfter, err := repository.ListUsers(ctx, store.UserListOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if usersAfter.Total != usersBefore.Total {
		t.Fatalf("user count changed %d -> %d", usersBefore.Total, usersAfter.Total)
	}
	// A suspended account does not come back to life through MCP.
	if err := repository.SetUserActive(ctx, member.ID, false); err != nil {
		t.Fatal(err)
	}
	if w := mcpCall(forThisServer); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "비활성") {
		t.Fatalf("inactive account: %d %s", w.Code, w.Body.String())
	}
	if err := repository.SetUserActive(ctx, member.ID, true); err != nil {
		t.Fatal(err)
	}

	// A valid token opens nothing outside /mcp.
	if w := get("/api/v1/me", forThisServer); w.Code != http.StatusUnauthorized {
		t.Fatalf("REST with a valid SSO token: %d %s", w.Code, w.Body.String())
	}
	if w := get("/api/v1/me", generated.Plaintext); w.Code != http.StatusOK {
		t.Fatalf("REST with the key must still work: %d %s", w.Code, w.Body.String())
	}

	// The administrator cannot switch it on without an issuer, and the
	// response carries the status the screen shows.
	if _, err := repository.UpdateOIDCSettings(ctx, model.OIDCSettings{RoleMappings: map[string][]string{}, GroupMappings: map[string][]string{}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if w := get("/.well-known/oauth-protected-resource/mcp", ""); w.Code != http.StatusNotFound {
		t.Fatalf("metadata without an issuer: %d", w.Code)
	}
	if w := mcpCall(forThisServer); w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("token without an issuer: %d %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
}
