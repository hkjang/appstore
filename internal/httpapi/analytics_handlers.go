package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/hkjang/appstore/internal/analytics"
	appcrypto "github.com/hkjang/appstore/internal/crypto"
)

// cspReportPath is where browsers post the requests the content security
// policy refused. It is unauthenticated because the browser sends the report
// on its own, and it stores nothing but a bounded list of origins in memory.
const cspReportPath = "/api/v1/analytics/csp-report"

// maxReportBytes keeps an unauthenticated endpoint from being used to push
// large bodies at the server.
const maxReportBytes = 8 * 1024

// basePagePolicy is the policy every page has always shipped with. Tracking
// only ever adds a nonce, the snippet's origins and a report address to it;
// switching tracking off returns to exactly this string.
const basePagePolicy = "default-src 'self'; base-uri 'self'; frame-ancestors 'none'; object-src 'none'; img-src 'self' data: blob:; font-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; form-action 'self'"

// apiPagePolicy is what a non-page response carries. Nothing under these
// paths is rendered by a browser, so nothing needs to load at all.
const apiPagePolicy = "default-src 'none'; frame-ancestors 'none'"

const trackingKey contextKey = "page-tracking"

// pageTracking is what the policy middleware decided for one page request,
// handed to the SPA handler so the header and the injected markup agree.
type pageTracking struct {
	nonce     string
	snippet   string
	placement string
}

// isMachinePath reports the endpoints a browser never renders: API, MCP,
// health and the Momento proxy. They get the narrow policy.
func isMachinePath(requestPath string) bool {
	return strings.HasPrefix(requestPath, "/api/") || requestPath == "/mcp" || strings.HasPrefix(requestPath, "/mcp/") ||
		strings.HasPrefix(requestPath, "/health") || requestPath == "/healthz" || requestPath == "/readyz" ||
		requestPath == analytics.MomentoProxyPath || strings.HasPrefix(requestPath, analytics.MomentoProxyPath+"/")
}

// pagePolicy keeps the strict page policy and adds only what the configured
// tracking snippet needs, including a nonce for its inline code.
func pagePolicy(config analytics.Config, requestPath, nonce string) string {
	if !config.Active(requestPath) {
		return basePagePolicy
	}
	extraScripts, extraConnects, extraImages := config.PolicySources()
	scripts := append([]string{"'self'", "'nonce-" + nonce + "'"}, extraScripts...)
	connects := append([]string{"'self'"}, extraConnects...)
	images := append([]string{"'self'", "data:", "blob:"}, extraImages...)
	// While tracking is on, ask the browser to say what it refused. That
	// report is what turns a console error into a one-click fix.
	return "default-src 'self'; base-uri 'self'; frame-ancestors 'none'; object-src 'none'; img-src " + strings.Join(images, " ") +
		"; font-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src " + strings.Join(scripts, " ") +
		"; connect-src " + strings.Join(connects, " ") + "; form-action 'self'; report-uri " + cspReportPath
}

// analyticsConfig reads the tracking settings. Failures are treated as "no
// tracking" so a settings outage never breaks a page.
func (s *Server) analyticsConfig(ctx context.Context) analytics.Config {
	config, err := s.repository.GetAnalyticsSettings(ctx)
	if err != nil {
		return analytics.Default()
	}
	return config
}

// ContentSecurityPolicy replaces the static policy with one that knows about
// the tracking configuration: a nonce per page request while tracking is on,
// the narrow policy on machine endpoints, and the base policy everywhere else.
func (s *Server) ContentSecurityPolicy(next http.Handler) http.Handler {
	return contentSecurityPolicy(s.analyticsConfig, next)
}

func contentSecurityPolicy(read func(context.Context) analytics.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isMachinePath(r.URL.Path) {
			w.Header().Set("Content-Security-Policy", apiPagePolicy)
			next.ServeHTTP(w, r)
			return
		}
		if path.Ext(r.URL.Path) != "" {
			// A static asset keeps the base policy SecurityHeaders already
			// set; it is never a page, so no settings read is needed.
			next.ServeHTTP(w, r)
			return
		}
		config := read(r.Context())
		if !config.Active(r.URL.Path) {
			w.Header().Set("Content-Security-Policy", basePagePolicy)
			next.ServeHTTP(w, r)
			return
		}
		nonce, err := appcrypto.RandomToken(16)
		if err != nil {
			// Without a nonce the snippet cannot run; keep the page strict
			// rather than serve a script the policy will refuse anyway.
			w.Header().Set("Content-Security-Policy", basePagePolicy)
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", pagePolicy(config, r.URL.Path, nonce))
		tracking := pageTracking{nonce: nonce, snippet: config.Snippet(nonce), placement: config.Normalize().Placement}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), trackingKey, tracking)))
	})
}

// decorateIndex injects the snippet the policy middleware prepared into the
// single page shell. Pages without tracking pass through untouched.
func decorateIndex(r *http.Request, page []byte) []byte {
	tracking, ok := r.Context().Value(trackingKey).(pageTracking)
	if !ok || tracking.snippet == "" {
		return page
	}
	return analytics.Inject(page, tracking.snippet, tracking.placement)
}

type cspReport struct {
	Report struct {
		BlockedURI         string `json:"blocked-uri"`
		ViolatedDirective  string `json:"violated-directive"`
		EffectiveDirective string `json:"effective-directive"`
		DocumentURI        string `json:"document-uri"`
	} `json:"csp-report"`
}

// receiveCSPReport records what a browser refused to load. Reports are always
// answered with 204 so a misbehaving page never sees an error from us.
func (s *Server) receiveCSPReport(w http.ResponseWriter, r *http.Request) {
	defer w.WriteHeader(http.StatusNoContent)
	body, err := io.ReadAll(io.LimitReader(r.Body, maxReportBytes))
	if err != nil || len(body) == 0 {
		return
	}
	var report cspReport
	if json.Unmarshal(body, &report) != nil {
		return
	}
	directive := report.Report.EffectiveDirective
	if directive == "" {
		directive = report.Report.ViolatedDirective
	}
	s.violations.Record(report.Report.BlockedURI, directive, report.Report.DocumentURI)
}

func (s *Server) adminAnalytics(w http.ResponseWriter, r *http.Request) {
	value, err := s.repository.GetAnalyticsSettings(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, value)
}

func (s *Server) adminUpdateAnalytics(w http.ResponseWriter, r *http.Request) {
	var input analytics.Config
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	before, _ := s.repository.GetAnalyticsSettings(r.Context())
	principal := CurrentPrincipal(r.Context())
	after, err := s.repository.UpdateAnalyticsSettings(r.Context(), input, &principal.User.ID)
	if err != nil {
		WriteError(w, r, analyticsError(err))
		return
	}
	s.recordAudit(r, "analytics.setting.update", "analytics_settings", "default", before, after)
	WriteJSON(w, http.StatusOK, after)
}

// analyticsError turns a field problem into the 422 envelope the console
// already knows how to show next to the field.
func analyticsError(err error) error {
	var field *analytics.FieldError
	if errors.As(err, &field) {
		return Validation(field.Message, map[string]any{field.Field: field.Message})
	}
	return storeError(err, "ANALYTICS_SETTINGS_NOT_FOUND", "방문 추적 설정을 찾을 수 없습니다.")
}

// adminAnalyticsViolations shows the administrator which addresses the policy
// is blocking, so a tracking snippet can be fixed without reading the browser
// console.
func (s *Server) adminAnalyticsViolations(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{"items": s.violations.List(s.analyticsConfig(r.Context()))})
}

// adminClearAnalyticsViolations forgets the recorded reports, which is how an
// administrator checks whether a change actually fixed the snippet.
func (s *Server) adminClearAnalyticsViolations(w http.ResponseWriter, _ *http.Request) {
	s.violations.Forget()
	w.WriteHeader(http.StatusNoContent)
}

// adminAllowAnalyticsHost adds one blocked origin to the allow list. It is the
// one-click fix for the reports listed above.
func (s *Server) adminAllowAnalyticsHost(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Origin string `json:"origin"`
	}
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	origin := strings.TrimSpace(input.Origin)
	parsed, err := url.Parse(origin)
	if err != nil || origin == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		WriteError(w, r, Validation("허용할 출처는 https://host 형식이어야 합니다.", map[string]any{"origin": origin}))
		return
	}
	before, err := s.repository.GetAnalyticsSettings(r.Context())
	if err != nil {
		WriteError(w, r, err)
		return
	}
	input2 := before
	input2.AllowedHosts = analytics.AddAllowedHost(before.AllowedHosts, parsed.Scheme+"://"+parsed.Host)
	principal := CurrentPrincipal(r.Context())
	after, err := s.repository.UpdateAnalyticsSettings(r.Context(), input2, &principal.User.ID)
	if err != nil {
		WriteError(w, r, analyticsError(err))
		return
	}
	s.recordAudit(r, "analytics.setting.update", "analytics_settings", "default", before, after)
	WriteJSON(w, http.StatusOK, after)
}

// momentoProxyTimeout bounds one forwarded request so a stalled collector
// cannot hold service connections open.
const momentoProxyTimeout = 15 * time.Second

// maxMomentoEventBytes bounds a forwarded event body. Tracker events are a few
// hundred bytes; the limit keeps the open proxy from carrying anything else.
const maxMomentoEventBytes = 256 << 10

// momentoProxy forwards /momento/* to the configured Momento collector so the
// tracker and its events stay on this origin and never appear in the policy.
// Credentials for this service are stripped before forwarding: the collector
// gets the event, not the visitor's session.
func (s *Server) momentoProxy(w http.ResponseWriter, r *http.Request) {
	forwardMomento(w, r, s.analyticsConfig(r.Context()), s.logger)
}

func forwardMomento(w http.ResponseWriter, r *http.Request, config analytics.Config, logger *slog.Logger) {
	if !config.ProxyActive() {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	target, err := url.Parse(config.MomentoURL)
	if err != nil || target.Host == "" {
		http.NotFound(w, r)
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, analytics.MomentoProxyPath)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			request.Out.URL.Path = path.Join(target.Path, suffix)
			request.Out.URL.RawPath = ""
			request.Out.Host = target.Host
			request.Out.Header.Del("Cookie")
			request.Out.Header.Del("Authorization")
			request.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.WarnContext(r.Context(), "momento proxy failed", "error", err, "request_id", RequestID(r.Context()))
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	ctx, cancel := context.WithTimeout(r.Context(), momentoProxyTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	r.Body = http.MaxBytesReader(w, r.Body, maxMomentoEventBytes)
	w.Header().Set("Cache-Control", "no-store")
	proxy.ServeHTTP(w, r)
}
