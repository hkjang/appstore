package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// AccessToken is what survives verification of a Keycloak access token: the
// claims the MCP endpoint needs to decide whom it is for and who is calling.
type AccessToken struct {
	Subject         string
	Username        string
	Audience        []string
	AuthorizedParty string
	Scopes          []string
	Expiry          time.Time
}

// AccessTokenVerifier checks bearer tokens Keycloak issued for the MCP
// endpoint. It reuses the web sign-in's issuer but not its per-request
// provider: discovery is a round trip to Keycloak and the key set behind it
// verifies every token, so the provider is cached per issuer and built on a
// context that outlives the request that first needed it. go-oidc refetches
// the key set on an unknown key id, so rotation needs no invalidation here.
type AccessTokenVerifier struct {
	HTTPClient *http.Client

	mu        sync.Mutex
	providers map[string]*oidc.Provider
}

// accessTokenAlgorithms is the asymmetric family a realm key signs with. HS*
// would make the shared secret a signing key, and "none" is not a signature.
var accessTokenAlgorithms = []string{
	oidc.RS256, oidc.RS384, oidc.RS512,
	oidc.ES256, oidc.ES384, oidc.ES512,
	oidc.PS256, oidc.PS384, oidc.PS512,
}

// LooksLikeJWT is the cheap shape test that separates "not a key" from "not
// a token of any kind": three non-empty dot-separated segments.
func LooksLikeJWT(token string) bool {
	parts := strings.Split(token, ".")
	return len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != ""
}

func (v *AccessTokenVerifier) provider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if provider := v.providers[issuer]; provider != nil {
		return provider, nil
	}
	if _, err := validateIssuer(issuer); err != nil {
		return nil, err
	}
	ctx = context.WithoutCancel(ctx)
	if v.HTTPClient != nil {
		ctx = oidc.ClientContext(ctx, v.HTTPClient)
	}
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider: %w", err)
	}
	if v.providers == nil {
		v.providers = map[string]*oidc.Provider{}
	}
	v.providers[issuer] = provider
	return provider, nil
}

// Verify checks signature, issuer, expiry and not-before against the issuer's
// key set, then the shape rules an API credential must meet: it is not an ID
// token, it is not bound to a proof of possession this server cannot check,
// and it names a subject. The audience is deliberately not checked here —
// more than one value is acceptable and the caller knows which.
func (v *AccessTokenVerifier) Verify(ctx context.Context, issuer, raw string) (AccessToken, error) {
	if !LooksLikeJWT(raw) {
		return AccessToken{}, errors.New("token is not a JWT")
	}
	if typ, err := tokenType(raw); err != nil {
		return AccessToken{}, err
	} else if strings.EqualFold(typ, "ID") {
		return AccessToken{}, errors.New("ID tokens prove a sign-in, not API access")
	}
	provider, err := v.provider(ctx, strings.TrimRight(strings.TrimSpace(issuer), "/"))
	if err != nil {
		return AccessToken{}, err
	}
	verified, err := provider.Verifier(&oidc.Config{
		SkipClientIDCheck: true, SupportedSigningAlgs: accessTokenAlgorithms,
	}).Verify(ctx, raw)
	if err != nil {
		return AccessToken{}, err
	}
	var claims struct {
		Type         string          `json:"typ"`
		Party        string          `json:"azp"`
		Scope        string          `json:"scope"`
		Username     string          `json:"preferred_username"`
		Confirmation json.RawMessage `json:"cnf"`
	}
	if err := verified.Claims(&claims); err != nil {
		return AccessToken{}, fmt.Errorf("decode access token claims: %w", err)
	}
	if strings.EqualFold(claims.Type, "ID") {
		return AccessToken{}, errors.New("ID tokens prove a sign-in, not API access")
	}
	if len(claims.Confirmation) > 0 && string(claims.Confirmation) != "null" {
		return AccessToken{}, errors.New("token is bound to a proof of possession this server cannot verify")
	}
	if strings.TrimSpace(verified.Subject) == "" {
		return AccessToken{}, errors.New("token has no subject")
	}
	return AccessToken{
		Subject: verified.Subject, Username: claims.Username, Audience: verified.Audience,
		AuthorizedParty: claims.Party, Scopes: strings.Fields(claims.Scope), Expiry: verified.Expiry,
	}, nil
}

// tokenType reads the JOSE header's typ before any signature work, so an ID
// token is refused for what it is rather than for whatever else it lacks.
func tokenType(raw string) (string, error) {
	encoded := strings.SplitN(raw, ".", 2)[0]
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", errors.New("token header is not base64url")
	}
	var header struct {
		Type string `json:"typ"`
	}
	if err := json.Unmarshal(decoded, &header); err != nil {
		return "", errors.New("token header is not JSON")
	}
	return header.Type, nil
}
