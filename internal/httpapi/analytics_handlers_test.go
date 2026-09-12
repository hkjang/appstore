package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/hkjang/appstore/internal/analytics"
)

func trackedMomento(proxy bool) analytics.Config {
	return analytics.Config{Enabled: true, Provider: analytics.ProviderMomento, MomentoURL: "https://momento.corp.example", MomentoSiteID: "appstore", MomentoProxy: proxy}
}

// servePage runs a request through the policy middleware and the SPA handler,
// which is the path a browser takes for index.html.
func servePage(t *testing.T, config analytics.Config, target string) *httptest.ResponseRecorder {
	t.Helper()
	spa, err := NewSPAHandler()
	if err != nil {
		t.Fatal(err)
	}
	spa.Decorate = decorateIndex
	handler := SecurityHeaders(contentSecurityPolicy(func(context.Context) analytics.Config { return config }, spa))
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

var noncePattern = regexp.MustCompile(`'nonce-([A-Za-z0-9_-]+)'`)

func TestPageWithoutTrackingIsUnchanged(t *testing.T) {
	w := servePage(t, analytics.Default(), "/apps")
	if w.Code != http.StatusOK || w.Header().Get("Content-Security-Policy") != basePagePolicy {
		t.Fatalf("status=%d policy=%q", w.Code, w.Header().Get("Content-Security-Policy"))
	}
	if strings.Contains(w.Body.String(), "<script nonce") || strings.Contains(w.Body.String(), "momento") {
		t.Fatalf("no snippet expected: %s", w.Body.String())
	}
}

func TestTrackedPageCarriesNonceAndSnippet(t *testing.T) {
	w := servePage(t, trackedMomento(false), "/apps/agent-hub")
	policy := w.Header().Get("Content-Security-Policy")
	match := noncePattern.FindStringSubmatch(policy)
	if match == nil {
		t.Fatalf("policy has no nonce: %q", policy)
	}
	for _, directive := range strings.Split(policy, ";") {
		directive = strings.TrimSpace(directive)
		if strings.HasPrefix(directive, "script-src ") && strings.Contains(directive, "'unsafe-inline'") {
			t.Fatalf("script-src must not be unsafe-inline: %q", policy)
		}
	}
	for _, want := range []string{
		"script-src 'self' 'nonce-" + match[1] + "' https://momento.corp.example",
		"connect-src 'self' https://momento.corp.example",
		"img-src 'self' data: blob: https://momento.corp.example",
		"report-uri " + cspReportPath,
	} {
		if !strings.Contains(policy, want) {
			t.Fatalf("policy %q lacks %q", policy, want)
		}
	}
	body := w.Body.String()
	if !strings.Contains(body, `nonce="`+match[1]+`"`) || !strings.Contains(body, `src="https://momento.corp.example/tracker.js"`) {
		t.Fatalf("snippet missing or nonce mismatch: %s", body)
	}
	if !strings.Contains(body, "</script>\n</head>") {
		t.Fatalf("head placement expected: %s", body)
	}
	// Every request gets its own nonce.
	second := noncePattern.FindStringSubmatch(servePage(t, trackedMomento(false), "/apps/agent-hub").Header().Get("Content-Security-Policy"))
	if second == nil || second[1] == match[1] {
		t.Fatal("nonce must differ per request")
	}
}

func TestProxiedMomentoAddsNoOrigin(t *testing.T) {
	w := servePage(t, trackedMomento(true), "/")
	policy := w.Header().Get("Content-Security-Policy")
	if strings.Contains(policy, "momento.corp.example") || !strings.Contains(policy, "'nonce-") {
		t.Fatalf("policy=%q", policy)
	}
	if !strings.Contains(w.Body.String(), `src="/momento/tracker.js"`) || !strings.Contains(w.Body.String(), `data-endpoint="/momento"`) {
		t.Fatalf("body=%s", w.Body.String())
	}
}

func TestBodyPlacementAndAdminExclusion(t *testing.T) {
	config := trackedMomento(true)
	config.Placement = analytics.PlacementBody
	if body := servePage(t, config, "/today").Body.String(); !strings.Contains(body, "</script>\n</body>") {
		t.Fatalf("body placement expected: %s", body)
	}
	w := servePage(t, config, "/admin/users")
	if w.Header().Get("Content-Security-Policy") != basePagePolicy || strings.Contains(w.Body.String(), "momento") {
		t.Fatalf("admin page must stay untracked: policy=%q", w.Header().Get("Content-Security-Policy"))
	}
	config.IncludeAdmin = true
	if body := servePage(t, config, "/admin/users").Body.String(); !strings.Contains(body, "/momento/tracker.js") {
		t.Fatal("includeAdmin must track the console")
	}
}

func TestMachinePathsGetTheNarrowPolicy(t *testing.T) {
	reads := 0
	handler := contentSecurityPolicy(func(context.Context) analytics.Config { reads++; return trackedMomento(true) },
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, target := range []string{"/api/v1/apps", "/mcp", "/healthz", "/readyz", "/health/ready", "/momento/collect"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if got := w.Header().Get("Content-Security-Policy"); got != apiPagePolicy {
			t.Fatalf("%s policy=%q", target, got)
		}
	}
	w := httptest.NewRecorder()
	SecurityHeaders(handler).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/index-abc.js", nil))
	if got := w.Header().Get("Content-Security-Policy"); got != basePagePolicy {
		t.Fatalf("asset policy=%q", got)
	}
	if reads != 0 {
		t.Fatalf("machine and asset paths must not read settings, got %d reads", reads)
	}
}

func TestCSPReportIsRecorded(t *testing.T) {
	s := &Server{violations: analytics.NewRecorder()}
	post := func(body string) int {
		r := httptest.NewRequest(http.MethodPost, cspReportPath, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/csp-report")
		w := httptest.NewRecorder()
		s.receiveCSPReport(w, r)
		return w.Code
	}
	report := `{"csp-report":{"document-uri":"https://store.corp.example/apps","blocked-uri":"https://momento.corp.example/collect/v1/events","violated-directive":"connect-src 'self'","effective-directive":"connect-src"}}`
	if post(report) != http.StatusNoContent || post(report) != http.StatusNoContent {
		t.Fatal("reports are always accepted")
	}
	if post("not json") != http.StatusNoContent || post(strings.Repeat("x", maxReportBytes*4)) != http.StatusNoContent {
		t.Fatal("bad reports are dropped silently")
	}
	items := s.violations.List(analytics.Default())
	if len(items) != 1 || items[0].Origin != "https://momento.corp.example" || items[0].Directive != "connect-src" || items[0].Count != 2 {
		t.Fatalf("items=%+v", items)
	}
}

func TestMomentoProxyForwardsWithoutCredentials(t *testing.T) {
	var seen *http.Request
	var seenBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Clone(context.Background())
		body, _ := io.ReadAll(r.Body)
		seenBody = string(body)
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = w.Write([]byte("tracker()"))
	}))
	defer upstream.Close()
	config := analytics.Config{Enabled: true, Provider: analytics.ProviderMomento, MomentoURL: upstream.URL + "/base", MomentoSiteID: "appstore", MomentoProxy: true}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	r := httptest.NewRequest(http.MethodPost, "/momento/collect/v1/events?site=appstore", strings.NewReader(`{"e":1}`))
	r.Header.Set("Cookie", "appstore_session=secret")
	r.Header.Set("Authorization", "Bearer aps_secret")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	forwardMomento(w, r, config, logger)
	if w.Code != http.StatusOK || w.Body.String() != "tracker()" {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if seen == nil || seen.URL.Path != "/base/collect/v1/events" || seen.URL.RawQuery != "site=appstore" || seenBody != `{"e":1}` {
		t.Fatalf("upstream saw %v body=%q", seen, seenBody)
	}
	if seen.Header.Get("Cookie") != "" || seen.Header.Get("Authorization") != "" {
		t.Fatalf("credentials leaked upstream: %v", seen.Header)
	}
	if seen.Header.Get("X-Forwarded-For") == "" {
		t.Fatal("visitor address should be forwarded for the collector")
	}

	w = httptest.NewRecorder()
	forwardMomento(w, httptest.NewRequest(http.MethodDelete, "/momento/x", nil), config, logger)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("delete status=%d", w.Code)
	}

	for _, off := range []analytics.Config{analytics.Default(), trackedMomento(false)} {
		w = httptest.NewRecorder()
		forwardMomento(w, httptest.NewRequest(http.MethodGet, "/momento/tracker.js", nil), off, logger)
		if w.Code != http.StatusNotFound {
			t.Fatalf("proxy must be closed when not configured: status=%d", w.Code)
		}
	}
}
