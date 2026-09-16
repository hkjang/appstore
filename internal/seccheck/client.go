package seccheck

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// maxResponseBytes bounds a SecCheck answer. A review detail is a few
// kilobytes; anything near this is a misconfigured address, not a review.
const maxResponseBytes = 2 << 20

type Config struct {
	BaseURL string
	APIKey  string
	Timeout time.Duration
}

// Client owns its HTTP client rather than borrowing one: redirects, cookies
// and timeouts set elsewhere must not be able to loosen how the credential
// travels.
type Client struct {
	baseURL string
	apiKey  string
	timeout time.Duration
	http    *http.Client
}

// ValidateBaseURL accepts only a plain origin (optionally with a path prefix).
// Credentials, queries and fragments in the configured address would end up in
// every request this client makes.
func ValidateBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, "\\\r\n\t#") ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return failure(ReasonInvalidConfig)
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return failure(ReasonInvalidConfig)
		}
	}
	trimmed := strings.TrimRight(parsed.Path, "/")
	if trimmed != "" && path.Clean(trimmed) != trimmed {
		return failure(ReasonInvalidConfig)
	}
	return nil
}

func NewClient(config Config, injected *http.Client) (*Client, error) {
	if err := ValidateBaseURL(config.BaseURL); err != nil {
		return nil, err
	}
	if config.APIKey == "" || len(config.APIKey) > 4096 {
		return nil, failure(ReasonInvalidConfig)
	}
	for _, character := range config.APIKey {
		if character <= 32 || character >= 127 {
			return nil, failure(ReasonInvalidConfig)
		}
	}
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}
	if config.Timeout < 0 || config.Timeout > 60*time.Second {
		return nil, failure(ReasonInvalidConfig)
	}
	client := &http.Client{}
	if injected != nil {
		*client = *injected
	}
	client.Jar = nil
	client.Timeout = config.Timeout
	// A redirect would carry the Authorization header somewhere the operator
	// never configured, so the answer to one is an error rather than a hop.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		baseURL: strings.TrimRight(config.BaseURL, "/"),
		apiKey:  config.APIKey, timeout: config.Timeout, http: client,
	}, nil
}

var reviewIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidReviewID holds the review identifier to the canonical UUID SecCheck
// generates, so no path or query smuggled into it can change where a request
// goes.
func ValidReviewID(id string) bool { return reviewIDPattern.MatchString(id) }

// Fetch reads one review. SecCheck answers 404 for a review the key may not
// see, which is what an owner guessing identifiers should get.
func (c *Client) Fetch(ctx context.Context, reviewID string) (*Review, error) {
	if !ValidReviewID(reviewID) {
		return nil, failure(ReasonInvalidReviewID)
	}
	body, err := c.get(ctx, "/api/v1/review-requests/"+reviewID)
	if err != nil {
		return nil, err
	}
	review, err := parseReview(body)
	if err != nil {
		return nil, err
	}
	if review.ID != reviewID {
		return nil, detailed(ReasonInvalidResponse, "review id does not match the request")
	}
	return review, nil
}

// TestConnection proves the configured key reaches a real SecCheck account
// that may read reviews. It cannot prove the key is read-only: SecCheck does
// not report a key's scope, so that stays the operator's responsibility.
func (c *Client) TestConnection(ctx context.Context) (*Identity, error) {
	body, err := c.get(ctx, "/api/v1/me")
	if err != nil {
		return nil, err
	}
	identity, err := parseIdentity(body)
	if err != nil {
		return nil, err
	}
	for _, role := range identity.Roles {
		if role == "AUDITOR" || role == "SECURITY_REVIEWER" {
			return identity, nil
		}
	}
	return nil, detailed(ReasonForbidden, "the account holds neither AUDITOR nor SECURITY_REVIEWER")
}

func (c *Client) get(ctx context.Context, endpoint string) ([]byte, error) {
	if c == nil || c.http == nil {
		return nil, failure(ReasonInvalidConfig)
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, c.baseURL+endpoint, nil)
	if err != nil {
		return nil, failure(ReasonInvalidConfig)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, requestFailure(requestCtx, err)
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode >= 300 && response.StatusCode < 400:
		return nil, failure(ReasonRedirect)
	case response.StatusCode == http.StatusUnauthorized:
		return nil, failure(ReasonUnauthorized)
	case response.StatusCode == http.StatusForbidden:
		return nil, failure(ReasonForbidden)
	case response.StatusCode == http.StatusNotFound:
		return nil, failure(ReasonNotFound)
	case response.StatusCode == http.StatusTooManyRequests:
		return nil, failure(ReasonRateLimited)
	case response.StatusCode != http.StatusOK:
		return nil, detailed(ReasonUnavailable, "HTTP "+strconv.Itoa(response.StatusCode))
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, detailed(ReasonInvalidResponse, "unexpected content type")
	}
	if response.ContentLength > maxResponseBytes {
		return nil, failure(ReasonTooLarge)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, requestFailure(requestCtx, err)
	}
	if len(body) > maxResponseBytes {
		return nil, failure(ReasonTooLarge)
	}
	if !json.Valid(body) {
		return nil, detailed(ReasonInvalidResponse, "body is not JSON")
	}
	return body, nil
}

func requestFailure(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return detailed(ReasonUnavailable, "request canceled")
	}
	var timeout interface{ Timeout() bool }
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return failure(ReasonTimeout)
	}
	return detailed(ReasonUnavailable, err.Error())
}
