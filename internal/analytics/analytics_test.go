package analytics

import (
	"strings"
	"testing"
)

func momento(proxy bool) Config {
	return Config{Enabled: true, Provider: ProviderMomento, MomentoURL: "https://momento.corp.example/", MomentoSiteID: "appstore", MomentoProxy: proxy}
}

func TestDefaultIsOff(t *testing.T) {
	config := Default()
	if config.Enabled || config.Active("/") || config.ProxyActive() || config.Snippet("n") != "" {
		t.Fatalf("a fresh install must not track: %+v", config)
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("default must validate: %v", err)
	}
	scripts, connects, images := config.PolicySources()
	if len(scripts)+len(connects)+len(images) != 0 {
		t.Fatalf("default must add no policy sources: %v %v %v", scripts, connects, images)
	}
}

func TestActiveSkipsAdminUnlessAsked(t *testing.T) {
	config := momento(true)
	for _, path := range []string{"/", "/apps", "/apps/agent-hub", "/my/apps", "/administration"} {
		if !config.Active(path) {
			t.Fatalf("expected tracking on %s", path)
		}
	}
	for _, path := range []string{"/admin", "/admin/users"} {
		if config.Active(path) {
			t.Fatalf("admin page %s must not be tracked by default", path)
		}
	}
	config.IncludeAdmin = true
	if !config.Active("/admin/users") {
		t.Fatal("includeAdmin must track the console")
	}
	config.Enabled = false
	if config.Active("/") {
		t.Fatal("disabled config must not be active")
	}
}

func TestMomentoSnippetUsesProxyByDefault(t *testing.T) {
	snippet := momento(true).Snippet("abc")
	for _, want := range []string{`src="/momento/tracker.js"`, `data-endpoint="/momento"`, `data-site-id="appstore"`, `nonce="abc"`, `data-contract-version="1"`} {
		if !strings.Contains(snippet, want) {
			t.Fatalf("snippet %q lacks %q", snippet, want)
		}
	}
	if strings.Contains(snippet, "momento.corp.example") {
		t.Fatalf("proxied snippet must not name the collector: %q", snippet)
	}
	scripts, connects, images := momento(true).PolicySources()
	if len(scripts)+len(connects)+len(images) != 0 {
		t.Fatalf("proxied momento must add no policy sources: %v %v %v", scripts, connects, images)
	}
	if !momento(true).ProxyActive() || momento(false).ProxyActive() {
		t.Fatal("proxy must follow the momentoProxy switch")
	}
}

func TestMomentoDirectNamesTheCollector(t *testing.T) {
	config := momento(false)
	snippet := config.Snippet("abc")
	if !strings.Contains(snippet, `src="https://momento.corp.example/tracker.js"`) || strings.Contains(snippet, "data-endpoint") {
		t.Fatalf("direct snippet %q", snippet)
	}
	scripts, connects, images := config.PolicySources()
	for _, group := range [][]string{scripts, connects, images} {
		if len(group) != 1 || group[0] != "https://momento.corp.example" {
			t.Fatalf("expected collector origin in every directive: %v %v %v", scripts, connects, images)
		}
	}
}

func TestNonceOnEveryScriptTag(t *testing.T) {
	config := Config{Enabled: true, Provider: ProviderCustom, CustomSnippet: `<SCRIPT src="https://t.example/a.js"></SCRIPT>
<script nonce="keep">x()</script>
<script>y()</script>`}
	snippet := config.Snippet("n1")
	if strings.Count(snippet, `nonce="n1"`) != 2 || !strings.Contains(snippet, `nonce="keep"`) {
		t.Fatalf("nonce placement wrong: %q", snippet)
	}
	if config.Snippet("") != config.Normalize().CustomSnippet {
		t.Fatal("no nonce means the snippet is left alone")
	}
}

func TestSnippetOriginsAreReadFromCustomSnippet(t *testing.T) {
	snippet := `<script src="https://momento.corp.example/tracker.js"></script>
<script>window.__t={endpoint:"https://momento.corp.example/collect/v1/events",pixel:'http://pixel.corp.example:8080/p.gif?id=1'};fetch("HTTPS://Upper.Example/x")</script>`
	origins := SnippetOrigins(snippet)
	want := []string{"https://momento.corp.example", "http://pixel.corp.example:8080", "https://upper.example"}
	if strings.Join(origins, " ") != strings.Join(want, " ") {
		t.Fatalf("origins=%v want %v", origins, want)
	}
	config := Config{Enabled: true, Provider: ProviderCustom, CustomSnippet: snippet, AllowedHosts: "https://extra.example, https://momento.corp.example"}
	scripts, _, _ := config.PolicySources()
	if strings.Join(scripts, " ") != "https://momento.corp.example http://pixel.corp.example:8080 https://upper.example https://extra.example https://momento.corp.example" {
		t.Fatalf("scripts=%v", scripts)
	}
}

func TestGA4AndMatomoSources(t *testing.T) {
	ga := Config{Enabled: true, Provider: ProviderGA4, MeasurementID: "G-ABC123"}
	scripts, connects, _ := ga.PolicySources()
	if scripts[0] != "https://www.googletagmanager.com" || len(connects) != 3 {
		t.Fatalf("ga4 sources: %v %v", scripts, connects)
	}
	if !strings.Contains(ga.Snippet("z"), `gtag('config','G-ABC123')`) {
		t.Fatalf("ga4 snippet: %q", ga.Snippet("z"))
	}
	matomo := Config{Enabled: true, Provider: ProviderMatomo, MatomoURL: "https://matomo.corp.example/analytics/", MatomoSiteID: "3"}
	scripts, _, _ = matomo.PolicySources()
	if len(scripts) != 1 || scripts[0] != "https://matomo.corp.example" {
		t.Fatalf("matomo sources: %v", scripts)
	}
	if !strings.Contains(matomo.Snippet("z"), `u="https://matomo.corp.example/analytics/"`) {
		t.Fatalf("matomo snippet: %q", matomo.Snippet("z"))
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name   string
		config Config
		field  string
	}{
		{"off with nothing", Config{}, ""},
		{"off keeps half filled form", Config{Provider: ProviderMomento}, ""},
		{"unknown provider", Config{Provider: "piwik"}, "provider"},
		{"on without provider", Config{Enabled: true}, "provider"},
		{"momento without site", Config{Enabled: true, Provider: ProviderMomento, MomentoURL: "https://m.example"}, "momentoUrl"},
		{"momento bad url", Config{Provider: ProviderMomento, MomentoURL: "m.example"}, "momentoUrl"},
		{"momento credentials in url", Config{Provider: ProviderMomento, MomentoURL: "https://u:p@m.example"}, "momentoUrl"},
		{"momento ok", momento(true), ""},
		{"ga4 without id", Config{Enabled: true, Provider: ProviderGA4}, "measurementId"},
		{"ga4 quote in id", Config{Provider: ProviderGA4, MeasurementID: "G-1');alert(1);('"}, "measurementId"},
		{"matomo without site", Config{Enabled: true, Provider: ProviderMatomo, MatomoURL: "https://m.example"}, "matomoUrl"},
		{"custom empty", Config{Enabled: true, Provider: ProviderCustom}, "customSnippet"},
		{"custom too large even when off", Config{Provider: ProviderCustom, CustomSnippet: strings.Repeat("x", MaxSnippetBytes+1)}, "customSnippet"},
		{"custom at limit", Config{Provider: ProviderCustom, CustomSnippet: strings.Repeat("x", MaxSnippetBytes)}, ""},
		{"allowed host not an origin", Config{AllowedHosts: "pixel.example"}, "allowedHosts"},
		{"allowed hosts ok", Config{AllowedHosts: "https://a.example, https://b.example:8443\nhttp://c.example"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate()
			if tc.field == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			field, ok := err.(*FieldError)
			if !ok || field.Field != tc.field {
				t.Fatalf("err=%v want field %s", err, tc.field)
			}
		})
	}
}

func TestAddAllowedHost(t *testing.T) {
	if got := AddAllowedHost("", "https://a.example/"); got != "https://a.example" {
		t.Fatalf("got %q", got)
	}
	if got := AddAllowedHost("https://a.example", "https://A.example"); got != "https://a.example" {
		t.Fatalf("duplicate must not be appended: %q", got)
	}
	if got := AddAllowedHost("https://a.example", "https://b.example"); got != "https://a.example, https://b.example" {
		t.Fatalf("got %q", got)
	}
}

func TestInject(t *testing.T) {
	page := []byte("<html><head><title>x</title></head><body><div id=\"root\"></div></body></html>")
	head := string(Inject(page, "<script>h()</script>", PlacementHead))
	if !strings.Contains(head, "<script>h()</script>\n</head>") {
		t.Fatalf("head placement: %q", head)
	}
	body := string(Inject(page, "<script>b()</script>", PlacementBody))
	if !strings.Contains(body, "<script>b()</script>\n</body>") {
		t.Fatalf("body placement: %q", body)
	}
	bare := string(Inject([]byte("plain"), "<script>x()</script>", PlacementHead))
	if !strings.HasSuffix(bare, "<script>x()</script>\n") {
		t.Fatalf("fallback: %q", bare)
	}
}
