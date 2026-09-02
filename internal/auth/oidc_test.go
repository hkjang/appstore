package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func discoveryServer(t *testing.T, body func(issuer string) string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body(server.URL)))
	})
	server.Config.Handler = mux
	t.Cleanup(server.Close)
	return server
}

func TestDiscoverReadsSnakeCaseDiscoveryDocument(t *testing.T) {
	server := discoveryServer(t, func(issuer string) string {
		return `{
			"issuer": "` + issuer + `",
			"authorization_endpoint": "` + issuer + `/protocol/openid-connect/auth",
			"token_endpoint": "` + issuer + `/protocol/openid-connect/token",
			"userinfo_endpoint": "` + issuer + `/protocol/openid-connect/userinfo",
			"end_session_endpoint": "` + issuer + `/protocol/openid-connect/logout",
			"jwks_uri": "` + issuer + `/protocol/openid-connect/certs",
			"scopes_supported": ["openid", "profile", "email"],
			"code_challenge_methods_supported": ["plain", "S256"]
		}`
	})
	client := &OIDCClient{HTTPClient: server.Client()}
	// A trailing slash on the configured issuer must not fail the match.
	result, err := client.Discover(context.Background(), server.URL+"/")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if result.AuthorizationEndpoint != server.URL+"/protocol/openid-connect/auth" {
		t.Fatalf("authorization endpoint = %q", result.AuthorizationEndpoint)
	}
	if result.TokenEndpoint == "" || result.JWKSURI == "" || result.UserInfoEndpoint == "" || result.EndSessionEndpoint == "" {
		t.Fatalf("endpoints were not decoded: %+v", result)
	}
	if result.DocumentURL != server.URL+"/.well-known/openid-configuration" {
		t.Fatalf("document URL = %q", result.DocumentURL)
	}
	if len(result.ScopesSupported) != 3 || len(result.CodeChallengeMethods) != 2 {
		t.Fatalf("metadata lists = %+v", result)
	}
}

func TestDiscoverReportsWhyItFailed(t *testing.T) {
	incomplete := discoveryServer(t, func(issuer string) string {
		return `{"issuer": "` + issuer + `"}`
	})
	client := &OIDCClient{HTTPClient: incomplete.Client()}
	_, err := client.Discover(context.Background(), incomplete.URL)
	if err == nil || !strings.Contains(err.Error(), "authorization_endpoint") {
		t.Fatalf("expected the missing fields to be named, got %v", err)
	}

	mismatched := discoveryServer(t, func(string) string {
		return `{
			"issuer": "https://elsewhere.example/realms/other",
			"authorization_endpoint": "https://elsewhere.example/auth",
			"token_endpoint": "https://elsewhere.example/token",
			"jwks_uri": "https://elsewhere.example/certs"
		}`
	})
	client = &OIDCClient{HTTPClient: mismatched.Client()}
	if _, err := client.Discover(context.Background(), mismatched.URL); err == nil ||
		!strings.Contains(err.Error(), "https://elsewhere.example/realms/other") {
		t.Fatalf("expected an issuer mismatch naming both values, got %v", err)
	}

	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "realm not found", http.StatusNotFound)
	}))
	defer notFound.Close()
	client = &OIDCClient{HTTPClient: notFound.Client()}
	if _, err := client.Discover(context.Background(), notFound.URL); err == nil ||
		!strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("expected the HTTP status in the error, got %v", err)
	}
}
