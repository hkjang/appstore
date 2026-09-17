package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hkjang/appstore/internal/auth/authtest"
)

func TestLooksLikeJWT(t *testing.T) {
	for value, want := range map[string]bool{
		"aaa.bbb.ccc": true, "aps_1234567890": false, "a.b": false, "a..c": false, "": false, "a.b.c.d": false,
	} {
		if got := LooksLikeJWT(value); got != want {
			t.Errorf("LooksLikeJWT(%q) = %v, want %v", value, got, want)
		}
	}
}

// guards: AccessTokenVerifier.Verify
func TestVerifyAcceptsAKeycloakAccessTokenAndReadsItsClaims(t *testing.T) {
	idp := authtest.NewIDP(t)
	verifier := &AccessTokenVerifier{}
	token := idp.AccessToken(t, []string{"account"}, map[string]any{
		"azp": "claude-mcp", "scope": "openid mcp:read", "preferred_username": "alice",
	})
	verified, err := verifier.Verify(context.Background(), idp.Issuer()+"/", token)
	if err != nil {
		t.Fatalf("a token signed by the issuer was refused: %v", err)
	}
	if verified.Subject != "subject-mcp" || verified.Username != "alice" || verified.AuthorizedParty != "claude-mcp" {
		t.Errorf("identity claims %+v", verified)
	}
	if len(verified.Audience) != 1 || verified.Audience[0] != "account" {
		t.Errorf("audience %v", verified.Audience)
	}
	if strings.Join(verified.Scopes, " ") != "openid mcp:read" {
		t.Errorf("scopes %v", verified.Scopes)
	}
	if verified.Expiry.Before(time.Now()) {
		t.Errorf("expiry %v is in the past", verified.Expiry)
	}
	// The provider is discovered once and reused; a second call must not
	// need discovery again (the test server would still answer, so check
	// the cache directly).
	if _, err := verifier.Verify(context.Background(), idp.Issuer(), token); err != nil {
		t.Fatal(err)
	}
	if len(verifier.providers) != 1 {
		t.Errorf("providers cached = %d, want 1", len(verifier.providers))
	}
}

// guards: AccessTokenVerifier.Verify — every refusal the standard lists
func TestVerifyRefusesTokensThatAreNotAPICredentialsForThisIssuer(t *testing.T) {
	idp := authtest.NewIDP(t)
	other := authtest.NewIDP(t)
	verifier := &AccessTokenVerifier{}
	now := time.Now()
	cases := map[string]string{
		"expired":        idp.AccessToken(t, "account", map[string]any{"exp": now.Add(-time.Minute).Unix()}),
		"not yet valid":  idp.AccessToken(t, "account", map[string]any{"nbf": now.Add(time.Hour).Unix()}),
		"other issuer":   other.AccessToken(t, "account", nil),
		"typ ID (claim)": idp.AccessToken(t, "account", map[string]any{"typ": "ID"}),
		"typ ID (header)": idp.Sign(t, map[string]any{"alg": "RS256", "typ": "ID", "kid": idp.KeyID}, map[string]any{
			"iss": idp.Issuer(), "aud": "account", "sub": "s", "exp": now.Add(time.Hour).Unix(),
		}),
		"HS256": authtest.SignHS256(t, "shared-secret", map[string]any{
			"iss": idp.Issuer(), "aud": "account", "sub": "s", "exp": now.Add(time.Hour).Unix(), "typ": "Bearer",
		}),
		"cnf bound":  idp.AccessToken(t, "account", map[string]any{"cnf": map[string]any{"jkt": "thumbprint"}}),
		"no subject": idp.AccessToken(t, "account", map[string]any{"sub": nil}),
		"not a JWT":  "aps_notatoken",
		"garbage":    "a.b.c",
	}
	for name, token := range cases {
		if _, err := verifier.Verify(context.Background(), idp.Issuer(), token); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// And the same issuer still accepts a good one, so the refusals above
	// are not the verifier being broken.
	if _, err := verifier.Verify(context.Background(), idp.Issuer(), idp.AccessToken(t, "account", nil)); err != nil {
		t.Fatalf("a good token was refused after the bad ones: %v", err)
	}
}

func TestVerifyReportsAnUnreachableIssuer(t *testing.T) {
	idp := authtest.NewIDP(t)
	token := idp.AccessToken(t, "account", nil)
	verifier := &AccessTokenVerifier{}
	if _, err := verifier.Verify(context.Background(), "http://127.0.0.1:1", token); err == nil {
		t.Fatal("an issuer that does not answer was accepted")
	}
	if _, err := verifier.Verify(context.Background(), "not a url", token); err == nil {
		t.Fatal("a malformed issuer was accepted")
	}
	if len(verifier.providers) != 0 {
		t.Errorf("failed discovery was cached: %d", len(verifier.providers))
	}
}
