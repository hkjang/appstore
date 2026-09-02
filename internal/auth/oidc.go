package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	appcrypto "github.com/hkjang/appstore/internal/crypto"
	"github.com/hkjang/appstore/internal/model"
	"golang.org/x/oauth2"
)

type OIDCClient struct {
	HTTPClient *http.Client
	Box        *appcrypto.SecretBox
}

type LoginRequest struct {
	URL       string
	State     string
	StateHash []byte
	Nonce     string
	Verifier  string
	ReturnTo  string
	ExpiresAt time.Time
}

type OIDCIdentity struct {
	Subject     string
	Username    string
	Email       string
	DisplayName string
	Team        string
	Roles       []string
	Claims      map[string]any
}

type DiscoveryResult struct {
	Issuer                string   `json:"issuer"`
	DocumentURL           string   `json:"documentUrl"`
	AuthorizationEndpoint string   `json:"authorizationEndpoint"`
	TokenEndpoint         string   `json:"tokenEndpoint"`
	UserInfoEndpoint      string   `json:"userInfoEndpoint"`
	EndSessionEndpoint    string   `json:"endSessionEndpoint"`
	JWKSURI               string   `json:"jwksUri"`
	ScopesSupported       []string `json:"scopesSupported,omitempty"`
	CodeChallengeMethods  []string `json:"codeChallengeMethods,omitempty"`
}

// discoveryDocument mirrors the snake_case wire format defined by OpenID
// Connect Discovery 1.0. DiscoveryResult keeps the camelCase names the REST
// API exposes, so the two shapes must stay separate.
type discoveryDocument struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	UserInfoEndpoint      string   `json:"userinfo_endpoint"`
	EndSessionEndpoint    string   `json:"end_session_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	ScopesSupported       []string `json:"scopes_supported"`
	CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
}

func (c *OIDCClient) Start(ctx context.Context, settings model.OIDCSettings, redirectURL, returnTo string) (LoginRequest, error) {
	if !settings.Enabled {
		return LoginRequest{}, errors.New("OIDC is disabled")
	}
	secret, err := c.Box.Decrypt(settings.ClientSecret)
	if err != nil {
		return LoginRequest{}, fmt.Errorf("decrypt OIDC client secret: %w", err)
	}
	provider, err := c.provider(ctx, settings.IssuerURL)
	if err != nil {
		return LoginRequest{}, err
	}
	state, err := appcrypto.RandomToken(32)
	if err != nil {
		return LoginRequest{}, err
	}
	nonce, err := appcrypto.RandomToken(24)
	if err != nil {
		return LoginRequest{}, err
	}
	verifier, err := appcrypto.RandomToken(48)
	if err != nil {
		return LoginRequest{}, err
	}
	config := oauthConfig(provider, settings, secret, redirectURL)
	challenge := sha256.Sum256([]byte(verifier))
	authURL := config.AuthCodeURL(
		state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:])),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	return LoginRequest{
		URL: authURL, State: state, StateHash: c.Box.Digest("oidc-state:" + state),
		Nonce: nonce, Verifier: verifier, ReturnTo: SafeReturnTo(returnTo), ExpiresAt: time.Now().Add(10 * time.Minute),
	}, nil
}

func (c *OIDCClient) Complete(ctx context.Context, settings model.OIDCSettings, redirectURL, code, verifier, expectedNonce string) (OIDCIdentity, error) {
	secret, err := c.Box.Decrypt(settings.ClientSecret)
	if err != nil {
		return OIDCIdentity{}, fmt.Errorf("decrypt OIDC client secret: %w", err)
	}
	provider, err := c.provider(ctx, settings.IssuerURL)
	if err != nil {
		return OIDCIdentity{}, err
	}
	config := oauthConfig(provider, settings, secret, redirectURL)
	token, err := config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return OIDCIdentity{}, fmt.Errorf("exchange authorization code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return OIDCIdentity{}, errors.New("OIDC response did not include an ID token")
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: settings.ClientID}).Verify(ctx, rawIDToken)
	if err != nil {
		return OIDCIdentity{}, fmt.Errorf("verify ID token: %w", err)
	}
	if expectedNonce == "" || idToken.Nonce != expectedNonce {
		return OIDCIdentity{}, errors.New("OIDC nonce validation failed")
	}
	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		return OIDCIdentity{}, fmt.Errorf("decode ID token claims: %w", err)
	}
	identity := identityFromClaims(claims, settings)
	if identity.Subject == "" {
		return OIDCIdentity{}, errors.New("OIDC subject claim is missing")
	}
	return identity, nil
}

func DiscoveryDocumentURL(issuer string) string {
	return strings.TrimRight(strings.TrimSpace(issuer), "/") + "/.well-known/openid-configuration"
}

func (c *OIDCClient) Discover(ctx context.Context, issuer string) (DiscoveryResult, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if _, err := validateIssuer(issuer); err != nil {
		return DiscoveryResult{}, err
	}
	documentURL := DiscoveryDocumentURL(issuer)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, documentURL, nil)
	if err != nil {
		return DiscoveryResult{}, err
	}
	req.Header.Set("Accept", "application/json")
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return DiscoveryResult{}, fmt.Errorf("%s에 연결하지 못했습니다: %w", documentURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return DiscoveryResult{}, fmt.Errorf("%s 응답을 읽지 못했습니다: %w", documentURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		return DiscoveryResult{}, fmt.Errorf("%s 요청이 HTTP %d를 반환했습니다%s", documentURL, resp.StatusCode, bodyHint(body))
	}
	var document discoveryDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return DiscoveryResult{}, fmt.Errorf("%s 응답을 OIDC discovery 문서로 해석하지 못했습니다%s", documentURL, bodyHint(body))
	}
	if missing := missingDiscoveryFields(document); len(missing) > 0 {
		return DiscoveryResult{}, fmt.Errorf("discovery 문서에 필수 항목이 없습니다: %s", strings.Join(missing, ", "))
	}
	if !sameIssuer(document.Issuer, issuer) {
		return DiscoveryResult{}, fmt.Errorf("discovery 문서의 issuer(%s)가 입력한 Issuer URL(%s)과 다릅니다. Keycloak의 frontend URL 설정을 확인하세요", document.Issuer, issuer)
	}
	return DiscoveryResult{
		Issuer: document.Issuer, DocumentURL: documentURL,
		AuthorizationEndpoint: document.AuthorizationEndpoint, TokenEndpoint: document.TokenEndpoint,
		UserInfoEndpoint: document.UserInfoEndpoint, EndSessionEndpoint: document.EndSessionEndpoint,
		JWKSURI: document.JWKSURI, ScopesSupported: document.ScopesSupported,
		CodeChallengeMethods: document.CodeChallengeMethods,
	}, nil
}

func missingDiscoveryFields(document discoveryDocument) []string {
	missing := []string{}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"issuer", document.Issuer},
		{"authorization_endpoint", document.AuthorizationEndpoint},
		{"token_endpoint", document.TokenEndpoint},
		{"jwks_uri", document.JWKSURI},
	} {
		if strings.TrimSpace(field.value) == "" {
			missing = append(missing, field.name)
		}
	}
	return missing
}

// sameIssuer compares issuers the way providers publish them: identical apart
// from a trailing slash.
func sameIssuer(document, configured string) bool {
	return strings.TrimRight(strings.TrimSpace(document), "/") == strings.TrimRight(strings.TrimSpace(configured), "/")
}

func bodyHint(body []byte) string {
	hint := strings.TrimSpace(string(body))
	if hint == "" {
		return ""
	}
	if len([]rune(hint)) > 200 {
		hint = string([]rune(hint)[:200]) + "…"
	}
	return " (" + strings.Join(strings.Fields(hint), " ") + ")"
}

func (c *OIDCClient) provider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	if _, err := validateIssuer(issuer); err != nil {
		return nil, err
	}
	if c.HTTPClient != nil {
		ctx = oidc.ClientContext(ctx, c.HTTPClient)
	}
	provider, err := oidc.NewProvider(ctx, strings.TrimRight(issuer, "/"))
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider: %w", err)
	}
	return provider, nil
}

func validateIssuer(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("issuer URL must be an absolute HTTP(S) URL without credentials or fragment")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, errors.New("issuer URL scheme must be http or https")
	}
	return parsed, nil
}

func oauthConfig(provider *oidc.Provider, settings model.OIDCSettings, secret, redirectURL string) oauth2.Config {
	scopes := settings.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}
	return oauth2.Config{
		ClientID: settings.ClientID, ClientSecret: secret, Endpoint: provider.Endpoint(),
		RedirectURL: redirectURL, Scopes: scopes,
	}
}

func identityFromClaims(claims map[string]any, settings model.OIDCSettings) OIDCIdentity {
	identity := OIDCIdentity{
		Subject: stringClaim(claims, "sub"), Username: stringClaim(claims, "preferred_username"),
		Email: stringClaim(claims, "email"), DisplayName: stringClaim(claims, "name"), Claims: claims,
	}
	if identity.Username == "" {
		identity.Username = identity.Email
	}
	if identity.DisplayName == "" {
		identity.DisplayName = identity.Username
	}
	externalRoles := stringSliceAtPath(claims, settings.RoleClaimPath)
	groups := stringSliceAtPath(claims, settings.GroupClaimPath)
	seen := map[string]bool{}
	for _, external := range externalRoles {
		for _, role := range settings.RoleMappings[external] {
			seen[role] = true
		}
	}
	for _, group := range groups {
		for _, role := range settings.GroupMappings[group] {
			seen[role] = true
		}
	}
	for role := range seen {
		identity.Roles = append(identity.Roles, role)
	}
	if len(groups) > 0 {
		identity.Team = groups[0]
	}
	return identity
}

func stringClaim(claims map[string]any, key string) string {
	value, _ := claims[key].(string)
	return value
}

func stringSliceAtPath(claims map[string]any, path string) []string {
	if path == "" {
		return nil
	}
	var current any = claims
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = object[part]
		if !ok {
			return nil
		}
	}
	switch value := current.(type) {
	case []any:
		result := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return result
	case []string:
		return value
	case string:
		return []string{value}
	default:
		return nil
	}
}
