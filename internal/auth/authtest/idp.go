// Package authtest is a fake identity provider for tests: a real RSA key
// pair, the discovery document and JWKS that go-oidc reads, and a signer that
// mints the JSON Web Tokens Keycloak would. Nothing here is linked into the
// service binary.
package authtest

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type IDP struct {
	Server *httptest.Server
	Key    *rsa.PrivateKey
	KeyID  string
}

// NewIDP starts a provider that serves discovery and a JWKS holding one RSA
// signing key. It is closed when the test ends.
func NewIDP(t testing.TB) *IDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &IDP{Key: key, KeyID: "test-key"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                idp.Server.URL,
			"authorization_endpoint":                idp.Server.URL + "/protocol/openid-connect/auth",
			"token_endpoint":                        idp.Server.URL + "/protocol/openid-connect/token",
			"jwks_uri":                              idp.Server.URL + "/protocol/openid-connect/certs",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/protocol/openid-connect/certs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": idp.KeyID,
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	idp.Server = httptest.NewServer(mux)
	t.Cleanup(idp.Server.Close)
	return idp
}

// Issuer is what the provider calls itself, and what a token's iss must be.
func (idp *IDP) Issuer() string { return idp.Server.URL }

// AccessToken is what Keycloak hands an MCP client after the person signed
// in: signed by the realm key, issued by this issuer, for an audience, with
// typ Bearer and an hour to live. Extra claims override the defaults.
func (idp *IDP) AccessToken(t testing.TB, audience any, extra map[string]any) string {
	t.Helper()
	claims := map[string]any{
		"iss": idp.Issuer(), "aud": audience, "sub": "subject-mcp",
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"typ": "Bearer", "preferred_username": "ssomember",
	}
	for key, value := range extra {
		if value == nil {
			delete(claims, key)
			continue
		}
		claims[key] = value
	}
	return idp.Sign(t, map[string]any{"alg": "RS256", "typ": "JWT", "kid": idp.KeyID}, claims)
}

// Sign produces an RS256 compact JWT with the given header and claims.
func (idp *IDP) Sign(t testing.TB, header, claims map[string]any) string {
	t.Helper()
	signingInput := encodeSegment(t, header) + "." + encodeSegment(t, claims)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, idp.Key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// SignHS256 mints a token with a shared secret — the algorithm a verifier
// must refuse, because it would let anyone with the JWKS forge a token.
func SignHS256(t testing.TB, secret string, claims map[string]any) string {
	t.Helper()
	signingInput := encodeSegment(t, map[string]any{"alg": "HS256", "typ": "JWT"}) + "." + encodeSegment(t, claims)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func encodeSegment(t testing.TB, value map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(base64.RawURLEncoding.EncodeToString(encoded), "=")
}
