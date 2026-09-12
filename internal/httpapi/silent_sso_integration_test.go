package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hkjang/appstore/internal/config"
	appcrypto "github.com/hkjang/appstore/internal/crypto"
	"github.com/hkjang/appstore/internal/database"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

// TestPostgreSQLSilentSSOIntegration walks the prompt=none leg end to end
// against a stand-in provider: the setting gates the silent start, and a
// refused silent attempt lands on the login screen carrying the marker that
// stops the browser from trying again.
func TestPostgreSQLSilentSSOIntegration(t *testing.T) {
	dsn := os.Getenv("APPSTORE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("APPSTORE_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

	// The provider only has to publish a discovery document; the browser is
	// redirected to its authorization endpoint and never follows it here.
	provider := httptest.NewServer(nil)
	defer provider.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer":"` + provider.URL + `","authorization_endpoint":"` + provider.URL + `/auth","token_endpoint":"` + provider.URL + `/token","jwks_uri":"` + provider.URL + `/certs"}`))
	})
	provider.Config.Handler = mux

	originalOIDC, err := repository.GetOIDCSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = repository.UpdateOIDCSettings(context.Background(), originalOIDC, &originalOIDC.ClientSecret, nil)
	}()
	secret, err := box.Encrypt("client-secret")
	if err != nil {
		t.Fatal(err)
	}
	settings := model.OIDCSettings{
		Enabled: true, IssuerURL: provider.URL, ClientID: "appstore",
		RoleMappings: map[string][]string{}, GroupMappings: map[string][]string{},
	}
	if _, err := repository.UpdateOIDCSettings(ctx, settings, &secret, nil); err != nil {
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
	get := func(target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.RemoteAddr = "198.51.100.40:1040"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	authorizationQuery := func(t *testing.T, response *httptest.ResponseRecorder) url.Values {
		t.Helper()
		if response.Code != http.StatusFound {
			t.Fatalf("login start status=%d body=%s", response.Code, response.Body.String())
		}
		location, err := url.Parse(response.Header().Get("Location"))
		if err != nil || location.Path != "/auth" {
			t.Fatalf("login start redirected to %q (%v)", response.Header().Get("Location"), err)
		}
		return location.Query()
	}

	// auto_login is off by default: a prompt=none request from the address bar
	// becomes an ordinary login and nothing else changes.
	query := authorizationQuery(t, get("/api/v1/auth/oidc/login?prompt=none&returnTo=%2Fmy%2Fapps"))
	if query.Has("prompt") {
		t.Fatalf("auto_login off still forwarded prompt=%q", query.Get("prompt"))
	}
	if response := get("/api/v1/auth/oidc/callback?error=login_required&state=" + url.QueryEscape(query.Get("state"))); response.Code != http.StatusUnauthorized {
		t.Fatalf("interactive refusal status=%d body=%s", response.Code, response.Body.String())
	}
	if response := get("/api/v1/public/config"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"oidcAutoLogin":false`) {
		t.Fatalf("public config with auto_login off = %d %s", response.Code, response.Body.String())
	}

	settings.AutoLogin = true
	if _, err := repository.UpdateOIDCSettings(ctx, settings, nil, nil); err != nil {
		t.Fatal(err)
	}
	if response := get("/api/v1/public/config"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"oidcAutoLogin":true`) {
		t.Fatalf("public config with auto_login on = %d %s", response.Code, response.Body.String())
	}
	// Without the browser asking, the login is still interactive.
	if query := authorizationQuery(t, get("/api/v1/auth/oidc/login?returnTo=%2Fmy%2Fapps")); query.Has("prompt") {
		t.Fatalf("plain login carried prompt=%q", query.Get("prompt"))
	}
	query = authorizationQuery(t, get("/api/v1/auth/oidc/login?prompt=none&returnTo=%2Fmy%2Fapps"))
	if query.Get("prompt") != "none" || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("silent login query = %v", query)
	}
	// The provider had no session: login_required comes back to the callback.
	// That is the ordinary answer, so the browser lands on the login screen
	// with the deep link kept and the marker that forbids another attempt.
	refusal := get("/api/v1/auth/oidc/callback?error=login_required&state=" + url.QueryEscape(query.Get("state")))
	if refusal.Code != http.StatusFound || refusal.Header().Get("Location") != "/login?sso=none&returnTo=%2Fmy%2Fapps" {
		t.Fatalf("silent refusal status=%d location=%q body=%s", refusal.Code, refusal.Header().Get("Location"), refusal.Body.String())
	}
	// The state is single use, so replaying the refusal is not a silent one.
	if response := get("/api/v1/auth/oidc/callback?error=login_required&state=" + url.QueryEscape(query.Get("state"))); response.Code != http.StatusUnauthorized {
		t.Fatalf("replayed refusal status=%d body=%s", response.Code, response.Body.String())
	}
	if response := get("/api/v1/auth/oidc/callback?error=login_required"); response.Code != http.StatusUnauthorized {
		t.Fatalf("stateless refusal status=%d body=%s", response.Code, response.Body.String())
	}
}
