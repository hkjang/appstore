// Package analytics injects a visitor tracking snippet into the served pages.
//
// The content security policy shipped with AppStore allows scripts from the
// application origin only, so a tracking snippet cannot simply be pasted in.
// This package produces both halves of the answer: the markup to inject and the
// policy sources it needs, with a per-request nonce so inline code runs without
// weakening the policy for everything else.
package analytics

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
)

// identifierPattern is what a site or measurement id looks like. The ids are
// written into the snippet, so keeping them to this alphabet is what makes
// the generated markup safe without a second escaping step.
var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

const (
	ProviderNone    = "none"
	ProviderMomento = "momento"
	ProviderGA4     = "ga4"
	ProviderGTM     = "gtm"
	ProviderMatomo  = "matomo"
	ProviderCustom  = "custom"

	PlacementHead = "head"
	PlacementBody = "body"

	// MaxSnippetBytes bounds a pasted snippet. A tracker loader is a few
	// hundred bytes; anything past this is not a snippet.
	MaxSnippetBytes = 8 * 1024

	// MomentoProxyPath is the same-origin prefix the service forwards to the
	// Momento collector, so the tracker and its events never leave this origin
	// as far as the browser policy is concerned.
	MomentoProxyPath = "/momento"
)

// Providers lists the supported providers in the order the console shows
// them. Momento comes first: it is the self-hosted collector, the one choice
// that keeps visit data inside the network.
var Providers = []string{ProviderNone, ProviderMomento, ProviderGA4, ProviderGTM, ProviderMatomo, ProviderCustom}

// Config is the tracking configuration an administrator edits. It is stored
// as one system_settings row, so the collector address can change at runtime
// without a rebuild or a restart.
type Config struct {
	Enabled       bool   `json:"enabled"`
	Provider      string `json:"provider"`
	MomentoURL    string `json:"momentoUrl"`
	MomentoSiteID string `json:"momentoSiteId"`
	// MomentoProxy routes the tracker through MomentoProxyPath on this origin
	// instead of naming the collector in the policy.
	MomentoProxy  bool   `json:"momentoProxy"`
	MeasurementID string `json:"measurementId"`
	MatomoURL     string `json:"matomoUrl"`
	MatomoSiteID  string `json:"matomoSiteId"`
	CustomSnippet string `json:"customSnippet"`
	AllowedHosts  string `json:"allowedHosts"`
	IncludeAdmin  bool   `json:"includeAdmin"`
	Placement     string `json:"placement"`
}

// Default is the configuration of a fresh installation: nothing is tracked and
// no policy changes, so a new install behaves exactly as before.
func Default() Config {
	return Config{Provider: ProviderNone, MomentoProxy: true, Placement: PlacementHead}
}

// Normalize trims every field and lowers the enumerations so the rest of the
// package can compare them directly.
func (c Config) Normalize() Config {
	c.Provider = strings.ToLower(strings.TrimSpace(c.Provider))
	if c.Provider == "" {
		c.Provider = ProviderNone
	}
	c.Placement = strings.ToLower(strings.TrimSpace(c.Placement))
	if c.Placement != PlacementBody {
		c.Placement = PlacementHead
	}
	c.MomentoURL = strings.TrimRight(strings.TrimSpace(c.MomentoURL), "/")
	c.MomentoSiteID = strings.TrimSpace(c.MomentoSiteID)
	c.MeasurementID = strings.TrimSpace(c.MeasurementID)
	c.MatomoURL = strings.TrimRight(strings.TrimSpace(c.MatomoURL), "/")
	c.MatomoSiteID = strings.TrimSpace(c.MatomoSiteID)
	c.CustomSnippet = strings.TrimSpace(c.CustomSnippet)
	c.AllowedHosts = strings.TrimSpace(c.AllowedHosts)
	return c
}

// FieldError names the field an administrator has to fix.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Message }

// Validate reports what is missing for the chosen provider. Size limits apply
// even while tracking is off, so an oversized snippet is never stored; the
// per-provider requirements apply only when tracking is switched on, so a
// half-filled form can be saved and finished later.
func (c Config) Validate() error {
	c = c.Normalize()
	known := false
	for _, provider := range Providers {
		if c.Provider == provider {
			known = true
		}
	}
	if !known {
		return &FieldError{"provider", "provider는 none, momento, ga4, gtm, matomo, custom 중 하나여야 합니다."}
	}
	if len(c.CustomSnippet) > MaxSnippetBytes {
		return &FieldError{"customSnippet", fmt.Sprintf("추적 코드는 %d바이트를 넘을 수 없습니다.", MaxSnippetBytes)}
	}
	for _, host := range AllowedHostList(c.AllowedHosts) {
		if originOf(host) == "" {
			return &FieldError{"allowedHosts", "허용 출처는 https://host 형식이어야 합니다: " + host}
		}
	}
	for _, id := range []struct{ field, value string }{
		{"momentoSiteId", c.MomentoSiteID}, {"measurementId", c.MeasurementID}, {"matomoSiteId", c.MatomoSiteID},
	} {
		if id.value != "" && !identifierPattern.MatchString(id.value) {
			return &FieldError{id.field, "ID는 영문·숫자·'_ . : -' 64자 이내여야 합니다."}
		}
	}
	if c.MomentoURL != "" && !validHTTPURL(c.MomentoURL) {
		return &FieldError{"momentoUrl", "Momento 주소는 http(s):// 로 시작하는 URL이어야 합니다."}
	}
	if c.MatomoURL != "" && !validHTTPURL(c.MatomoURL) {
		return &FieldError{"matomoUrl", "Matomo 주소는 http(s):// 로 시작하는 URL이어야 합니다."}
	}
	if !c.Enabled {
		return nil
	}
	switch c.Provider {
	case ProviderNone:
		return &FieldError{"provider", "추적을 켜려면 provider를 선택하세요."}
	case ProviderMomento:
		if c.MomentoURL == "" || c.MomentoSiteID == "" {
			return &FieldError{"momentoUrl", "Momento 수집기 주소와 사이트 ID가 필요합니다."}
		}
	case ProviderGA4, ProviderGTM:
		if c.MeasurementID == "" {
			return &FieldError{"measurementId", "GA4 · GTM 측정 ID가 필요합니다."}
		}
	case ProviderMatomo:
		if c.MatomoURL == "" || c.MatomoSiteID == "" {
			return &FieldError{"matomoUrl", "Matomo 주소와 사이트 ID가 필요합니다."}
		}
	case ProviderCustom:
		if c.CustomSnippet == "" {
			return &FieldError{"customSnippet", "붙여 넣은 스니펫이 비어 있습니다."}
		}
	}
	return nil
}

func validHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.User == nil
}

// Active reports whether a page at the given path should carry the snippet.
// Console pages are excluded unless an administrator asks for them, because
// console traffic is rarely the visitor data anybody wants.
func (c Config) Active(path string) bool {
	c = c.Normalize()
	if !c.Enabled || c.Provider == ProviderNone {
		return false
	}
	if !c.IncludeAdmin && (path == "/admin" || strings.HasPrefix(path, "/admin/")) {
		return false
	}
	return c.Snippet("") != ""
}

// ProxyActive reports whether the same-origin Momento proxy should forward
// requests. It is independent of the page path: the tracker loaded on a public
// page keeps sending events after the visitor navigates.
func (c Config) ProxyActive() bool {
	c = c.Normalize()
	return c.Enabled && c.Provider == ProviderMomento && c.MomentoProxy && c.MomentoURL != ""
}

// Snippet renders the markup to inject. The nonce is applied to every script
// tag in the snippet so the policy can stay strict.
func (c Config) Snippet(nonce string) string {
	c = c.Normalize()
	switch c.Provider {
	case ProviderMomento:
		if c.MomentoURL == "" || c.MomentoSiteID == "" {
			return ""
		}
		base := html.EscapeString(c.MomentoURL)
		endpoint := ""
		if c.MomentoProxy {
			base = MomentoProxyPath
			endpoint = ` data-endpoint="` + MomentoProxyPath + `"`
		}
		return withNonce(fmt.Sprintf(`<script async src="%s/tracker.js" data-site-id="%s"%s data-environment="prd" data-contract-version="1"></script>`,
			base, html.EscapeString(c.MomentoSiteID), endpoint), nonce)
	case ProviderGA4:
		if c.MeasurementID == "" {
			return ""
		}
		id := html.EscapeString(c.MeasurementID)
		return withNonce(fmt.Sprintf(`<script async src="https://www.googletagmanager.com/gtag/js?id=%s"></script>
<script>window.dataLayer=window.dataLayer||[];function gtag(){dataLayer.push(arguments);}gtag('js',new Date());gtag('config','%s');</script>`, id, id), nonce)
	case ProviderGTM:
		if c.MeasurementID == "" {
			return ""
		}
		return withNonce(fmt.Sprintf(`<script>(function(w,d,s,l,i){w[l]=w[l]||[];w[l].push({'gtm.start':new Date().getTime(),event:'gtm.js'});var f=d.getElementsByTagName(s)[0],j=d.createElement(s),dl=l!='dataLayer'?'&l='+l:'';j.async=true;j.src='https://www.googletagmanager.com/gtm.js?id='+i+dl;f.parentNode.insertBefore(j,f);})(window,document,'script','dataLayer','%s');</script>`, html.EscapeString(c.MeasurementID)), nonce)
	case ProviderMatomo:
		if c.MatomoURL == "" || c.MatomoSiteID == "" {
			return ""
		}
		return withNonce(fmt.Sprintf(`<script>var _paq=window._paq=window._paq||[];_paq.push(['trackPageView']);_paq.push(['enableLinkTracking']);(function(){var u="%s/";_paq.push(['setTrackerUrl',u+'matomo.php']);_paq.push(['setSiteId','%s']);var d=document,g=d.createElement('script'),s=d.getElementsByTagName('script')[0];g.async=true;g.src=u+'matomo.js';s.parentNode.insertBefore(g,s);})();</script>`,
			html.EscapeString(c.MatomoURL), html.EscapeString(c.MatomoSiteID)), nonce)
	case ProviderCustom:
		return withNonce(c.CustomSnippet, nonce)
	}
	return ""
}

// withNonce adds the nonce to every script tag that does not already carry one,
// which is what lets a pasted snippet run under a strict policy unchanged.
func withNonce(snippet, nonce string) string {
	if nonce == "" || snippet == "" {
		return snippet
	}
	var builder strings.Builder
	remaining := snippet
	for {
		index := strings.Index(strings.ToLower(remaining), "<script")
		if index < 0 {
			builder.WriteString(remaining)
			return builder.String()
		}
		end := index + len("<script")
		builder.WriteString(remaining[:end])
		tag := remaining[end:]
		if closing := strings.Index(tag, ">"); closing >= 0 {
			tag = tag[:closing]
		}
		if !strings.Contains(strings.ToLower(tag), "nonce=") {
			builder.WriteString(` nonce="` + html.EscapeString(nonce) + `"`)
		}
		remaining = remaining[end:]
	}
}

// PolicySources lists the extra origins the snippet needs, derived from the
// provider so a common setup needs no policy knowledge at all. The Momento
// proxy contributes nothing: everything it loads is on this origin.
func (c Config) PolicySources() (scripts []string, connects []string, images []string) {
	c = c.Normalize()
	add := func(origin string) {
		scripts = append(scripts, origin)
		connects = append(connects, origin)
		images = append(images, origin)
	}
	switch c.Provider {
	case ProviderMomento:
		if !c.MomentoProxy {
			if origin := originOf(c.MomentoURL); origin != "" {
				add(origin)
			}
		}
	case ProviderGA4, ProviderGTM:
		scripts = append(scripts, "https://www.googletagmanager.com")
		connects = append(connects, "https://www.google-analytics.com", "https://analytics.google.com", "https://*.google-analytics.com")
		images = append(images, "https://www.google-analytics.com", "https://www.googletagmanager.com")
	case ProviderMatomo:
		if origin := originOf(c.MatomoURL); origin != "" {
			add(origin)
		}
	case ProviderCustom:
		// A pasted snippet names the addresses it loads and reports to, so
		// those origins are allowed without anybody reading a policy error.
		for _, origin := range SnippetOrigins(c.CustomSnippet) {
			add(origin)
		}
	}
	for _, host := range AllowedHostList(c.AllowedHosts) {
		add(host)
	}
	return scripts, connects, images
}

// SnippetOrigins lists every http(s) origin written into a tracking snippet:
// the script it loads, the endpoint it posts to, the pixel it requests.
func SnippetOrigins(snippet string) []string {
	origins := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	lower := strings.ToLower(snippet)
	for index := 0; index < len(snippet); {
		start := strings.Index(lower[index:], "http")
		if start < 0 {
			break
		}
		start += index
		end := start
		for end < len(snippet) && !isURLBoundary(snippet[end]) {
			end++
		}
		index = end
		origin := originOf(snippet[start:end])
		if origin == "" {
			continue
		}
		if _, duplicate := seen[origin]; duplicate {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	return origins
}

// isURLBoundary reports the characters that cannot appear in a URL written
// inside HTML or JavaScript, which is where each address ends.
func isURLBoundary(letter byte) bool {
	switch letter {
	case '"', '\'', '`', '<', '>', ' ', '\t', '\n', '\r', ')', ',', ';', '\\', '+':
		return true
	}
	return false
}

// originOf reduces an address to its http(s) origin, or "" when it is not one.
func originOf(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	return scheme + "://" + strings.ToLower(parsed.Host)
}

// AllowedHostList splits the administrator's allow list, which accepts commas,
// spaces or new lines between entries.
func AllowedHostList(value string) []string {
	fields := strings.FieldsFunc(value, func(letter rune) bool {
		return letter == ',' || letter == ' ' || letter == '\n' || letter == '\r' || letter == '\t'
	})
	hosts := make([]string, 0, len(fields))
	for _, host := range fields {
		if trimmed := strings.TrimSuffix(strings.TrimSpace(host), "/"); trimmed != "" {
			hosts = append(hosts, trimmed)
		}
	}
	return hosts
}

// AddAllowedHost appends an origin to the allow list, leaving the existing
// entries and their order alone.
func AddAllowedHost(existing, origin string) string {
	origin = strings.TrimSpace(strings.TrimSuffix(origin, "/"))
	if origin == "" {
		return existing
	}
	for _, host := range AllowedHostList(existing) {
		if strings.EqualFold(host, origin) {
			return existing
		}
	}
	if strings.TrimSpace(existing) == "" {
		return origin
	}
	return strings.TrimSpace(existing) + ", " + origin
}

// Inject places the markup just before the closing tag the placement names,
// falling back to the end of the document when the tag is missing.
func Inject(page []byte, snippet, placement string) []byte {
	marker := "</head>"
	if placement == PlacementBody {
		marker = "</body>"
	}
	text := string(page)
	index := strings.LastIndex(strings.ToLower(text), marker)
	if index < 0 {
		return []byte(text + "\n" + snippet + "\n")
	}
	return []byte(text[:index] + snippet + "\n" + text[index:])
}
