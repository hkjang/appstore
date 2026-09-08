package httpapi

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func spaAsset(t *testing.T, spa *SPAHandler, pattern string) string {
	t.Helper()
	matches, err := fs.Glob(spa.dist, pattern)
	if err != nil || len(matches) == 0 {
		t.Fatalf("no embedded asset matches %q: %v", pattern, err)
	}
	return matches[0]
}

func TestSPAServesHashedAssetsAsImmutable(t *testing.T) {
	spa, err := NewSPAHandler()
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		pattern string
		prefix  string
	}{
		{"assets/*.js", "text/javascript"},
		{"assets/*.css", "text/css"},
	} {
		name := spaAsset(t, spa, testCase.pattern)
		w := httptest.NewRecorder()
		spa.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/"+name, nil))
		if w.Code != http.StatusOK || w.Body.Len() == 0 {
			t.Fatalf("%s: status=%d bytes=%d", name, w.Code, w.Body.Len())
		}
		if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, testCase.prefix) {
			t.Fatalf("%s: content type = %q, want a %s", name, got, testCase.prefix)
		}
		// The file name carries the build hash, so the bundle can be cached
		// for as long as the browser likes; only index.html is revalidated.
		if got := w.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
			t.Fatalf("%s: cache control = %q", name, got)
		}
	}
}

// The container runtime is a bare Alpine image, so it has no /etc/mime.types
// and mime.TypeByExtension answers nothing for a web font. The response still
// has to name the font: net/http then falls back to sniffing the wOF2
// signature, which is why serveFile may leave the header unset.
func TestSPAFontsAreTypedWithoutASystemMimeTable(t *testing.T) {
	spa, err := NewSPAHandler()
	if err != nil {
		t.Fatal(err)
	}
	name := spaAsset(t, spa, "assets/*.woff2")
	w := httptest.NewRecorder()
	spa.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/"+name, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("%s: status=%d", name, w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "" && !strings.Contains(got, "woff2") {
		t.Fatalf("%s: content type = %q, want a woff2 font", name, got)
	}
	value, err := fs.ReadFile(spa.dist, name)
	if err != nil {
		t.Fatal(err)
	}
	if got := http.DetectContentType(value); got != "font/woff2" {
		t.Fatalf("%s: sniffed content type = %q, want font/woff2", name, got)
	}
}

func TestSPAIndexIsRevalidated(t *testing.T) {
	spa, err := NewSPAHandler()
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	spa.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("cache control = %q, want no-cache", got)
	}
	if got := w.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
}

func TestSPADoesNotAnswerMissingFilesWithTheDocument(t *testing.T) {
	spa, err := NewSPAHandler()
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name   string
		target string
		accept string
	}{
		// A stale bundle reference has to fail loudly instead of parsing the
		// document as JavaScript.
		{"missing asset", "/assets/index-deadbeef.js", "text/html,application/xhtml+xml"},
		{"missing font", "/assets/Pretendard-Gone.woff2", ""},
		{"non browser client", "/admin/users", "application/json"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, testCase.target, nil)
			if testCase.accept != "" {
				r.Header.Set("Accept", testCase.accept)
			}
			w := httptest.NewRecorder()
			spa.ServeHTTP(w, r)
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", w.Code)
			}
		})
	}
}

func TestSPAHeadKeepsTheLengthAndDropsTheBody(t *testing.T) {
	spa, err := NewSPAHandler()
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	spa.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/", nil))
	length, err := strconv.Atoi(w.Header().Get("Content-Length"))
	if err != nil || length <= 0 {
		t.Fatalf("content length = %q: %v", w.Header().Get("Content-Length"), err)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("HEAD wrote %d body bytes", w.Body.Len())
	}
}

func TestSPAKeepsATraversalInsideTheBundle(t *testing.T) {
	spa, err := NewSPAHandler()
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	spa.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/../../etc/passwd", nil))
	// The cleaned path names no embedded file, so the request is answered the
	// way any unknown route is; nothing outside the bundle is reachable.
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
