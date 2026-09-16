package seccheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const reviewJSON = `{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","review_number":"SEC-2026-0001",
	"service_name":"Agent Hub","description":"설명","status":"APPROVED","final_result":"APPROVED",
	"approved_at":"2026-09-10T02:00:00Z",
	"completion_blockers":{"unreviewed_items":0,"unverified_changes":0,"stale_verdicts":0}}`

func testClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(Config{BaseURL: server.URL, APIKey: "sck_test_key", Timeout: 5 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return client, server
}

func TestFetchSendsTheKeyAndReadsTheReview(t *testing.T) {
	var authorization, path string
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		authorization, path = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reviewJSON))
	})
	review, err := client.Fetch(context.Background(), "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer sck_test_key" {
		t.Errorf("authorization = %q", authorization)
	}
	if path != "/api/v1/review-requests/cccccccc-cccc-4ccc-8ccc-cccccccccccc" {
		t.Errorf("path = %q", path)
	}
	if review.Number != "SEC-2026-0001" {
		t.Errorf("review = %#v", review)
	}
}

// An identifier is the one thing an owner types, so it must not be able to
// reach another endpoint or carry a query.
func TestFetchRefusesAnIdentifierThatIsNotAReviewID(t *testing.T) {
	reached := false
	client, _ := testClient(t, func(http.ResponseWriter, *http.Request) { reached = true })
	for _, id := range []string{
		"", "not-a-uuid", "../me", "cccccccc-cccc-4ccc-8ccc-cccccccccccc/items",
		"cccccccc-cccc-4ccc-8ccc-cccccccccccc?x=1", " cccccccc-cccc-4ccc-8ccc-cccccccccccc",
	} {
		if _, err := client.Fetch(context.Background(), id); Code(err) != ReasonInvalidReviewID {
			t.Errorf("Fetch(%q) error = %v", id, err)
		}
	}
	if reached {
		t.Error("a rejected identifier still reached SecCheck")
	}
}

func TestFetchTranslatesWhatSecCheckAnswers(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		reason  Reason
	}{
		{"unauthorized", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }, ReasonUnauthorized},
		{"forbidden", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }, ReasonForbidden},
		{"missing review", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }, ReasonNotFound},
		{"rate limited", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }, ReasonRateLimited},
		{"server error", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }, ReasonUnavailable},
		{"a login page instead of JSON", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>login</html>"))
		}, ReasonInvalidResponse},
		{"a redirect to somewhere else", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://elsewhere.example/api", http.StatusFound)
		}, ReasonRedirect},
		{"an answer that is too large", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"x":"` + strings.Repeat("a", maxResponseBytes+16) + `"}`))
		}, ReasonTooLarge},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, _ := testClient(t, test.handler)
			if _, err := client.Fetch(context.Background(), "cccccccc-cccc-4ccc-8ccc-cccccccccccc"); Code(err) != test.reason {
				t.Fatalf("error = %v, want %q", err, test.reason)
			}
		})
	}
}

// A redirect must never replay the Authorization header at another host.
func TestFetchDoesNotFollowARedirectWithTheCredential(t *testing.T) {
	var secondHost bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { secondHost = true }))
	defer elsewhere.Close()
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/api/v1/me", http.StatusTemporaryRedirect)
	})
	if _, err := client.Fetch(context.Background(), "cccccccc-cccc-4ccc-8ccc-cccccccccccc"); Code(err) != ReasonRedirect {
		t.Fatalf("error = %v", err)
	}
	if secondHost {
		t.Fatal("the credential followed the redirect")
	}
}

func TestTestConnectionRequiresAReadingRole(t *testing.T) {
	identity := func(roles string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/me" {
				t.Errorf("path = %q", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user":{"id":"u1","username":"svc","display_name":"연동","active":true,"roles":[` + roles + `]}}`))
		}
	}
	client, _ := testClient(t, identity(`"AUDITOR"`))
	if _, err := client.TestConnection(context.Background()); err != nil {
		t.Fatalf("an auditor key was refused: %v", err)
	}
	client, _ = testClient(t, identity(`"REQUESTER"`))
	if _, err := client.TestConnection(context.Background()); Code(err) != ReasonForbidden {
		t.Fatalf("a key without a reading role was accepted: %v", err)
	}
}

func TestNewClientRefusesAnUnusableConfiguration(t *testing.T) {
	cases := map[string]Config{
		"no address":             {APIKey: "k"},
		"not http":               {BaseURL: "ftp://sec.example", APIKey: "k"},
		"credentials in the URL": {BaseURL: "https://user:pw@sec.example", APIKey: "k"},
		"a query string":         {BaseURL: "https://sec.example?token=1", APIKey: "k"},
		"a fragment":             {BaseURL: "https://sec.example#x", APIKey: "k"},
		"a newline":              {BaseURL: "https://sec.example\n", APIKey: "k"},
		"no key":                 {BaseURL: "https://sec.example"},
		"a key with a newline":   {BaseURL: "https://sec.example", APIKey: "k\nv"},
		"an impossible timeout":  {BaseURL: "https://sec.example", APIKey: "k", Timeout: 5 * time.Minute},
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewClient(config, nil); Code(err) != ReasonInvalidConfig {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if _, err := NewClient(Config{BaseURL: "https://sec.example/seccheck", APIKey: "k"}, nil); err != nil {
		t.Fatalf("an address with a path prefix was refused: %v", err)
	}
}

func TestFetchGivesUpOnASlowSecCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reviewJSON))
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, APIKey: "k", Timeout: 50 * time.Millisecond}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fetch(context.Background(), "cccccccc-cccc-4ccc-8ccc-cccccccccccc"); Code(err) != ReasonTimeout {
		t.Fatalf("error = %v, want a timeout", err)
	}
}
