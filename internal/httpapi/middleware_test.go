package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRequestIDMiddlewareKeepsOnlyUsableClientIDs(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		supplied string
		kept     bool
	}{
		{"a correlation id from the caller", "trace-0123456789ab", true},
		{"too short to correlate anything", "abc", false},
		{"longer than the log field", strings.Repeat("a", 129), false},
		// The value is echoed into a response header and an error body, so a
		// line break from the caller must never reach either.
		{"a header injection attempt", "abcdefgh\r\nX-Admin: yes", false},
		{"absent", "", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var seen string
			handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = RequestID(r.Context())
			}))
			r := httptest.NewRequest(http.MethodGet, "/api/v1/apps", nil)
			if testCase.supplied != "" {
				r.Header.Set("X-Request-ID", testCase.supplied)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			header := w.Header().Get("X-Request-ID")
			if header != seen {
				t.Fatalf("header %q and context %q disagree", header, seen)
			}
			if testCase.kept {
				if header != testCase.supplied {
					t.Fatalf("request id = %q, want the supplied %q", header, testCase.supplied)
				}
				return
			}
			if header == testCase.supplied {
				t.Fatalf("request id %q must not be reused", header)
			}
			if len(header) != 32 || strings.ContainsAny(header, "\r\n") {
				t.Fatalf("generated request id = %q", header)
			}
		})
	}
}

func TestSecurityHeadersCoverEveryResponse(t *testing.T) {
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	for header, want := range map[string]string{
		"X-Content-Type-Options":     "nosniff",
		"X-Frame-Options":            "DENY",
		"Referrer-Policy":            "strict-origin-when-cross-origin",
		"Cross-Origin-Opener-Policy": "same-origin",
	} {
		if got := w.Header().Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
	// The bundle is served from this origin and the store front loads uploaded
	// branding as a data or blob URL; nothing else may be fetched.
	policy := w.Header().Get("Content-Security-Policy")
	for _, directive := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'", "script-src 'self'"} {
		if !strings.Contains(policy, directive) {
			t.Fatalf("content security policy %q is missing %q", policy, directive)
		}
	}
}

func TestRecovererAnswersWithTheErrorEnvelope(t *testing.T) {
	handler := RequestIDMiddleware(Recoverer(discardLogger(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("catalog exploded")
	})))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/apps", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"code":"INTERNAL_ERROR"`) || !strings.Contains(body, `"requestId":"`) {
		t.Fatalf("body = %s", body)
	}
	// The panic value names internals and must stay in the log.
	if strings.Contains(body, "catalog exploded") {
		t.Fatalf("the panic value leaked into the response: %s", body)
	}
}

func TestAccessLogRecordsTheAnsweredStatus(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		handler http.HandlerFunc
		status  int
		bytes   int
	}{
		{"an explicit status", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		}, http.StatusAccepted, 0},
		{"an implicit 200 from the first write", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("hello"))
		}, http.StatusOK, 5},
		{"a handler that writes nothing at all", func(w http.ResponseWriter, r *http.Request) {}, http.StatusOK, 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var recorded *responseRecorder
			handler := AccessLog(discardLogger(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				recorded = w.(*responseRecorder)
				testCase.handler(w, r)
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/apps", nil))
			// A handler that answers without touching the writer is still a
			// 200 on the wire, so the log line has to say so.
			if recorded.status != testCase.status {
				t.Fatalf("status = %d, want %d", recorded.status, testCase.status)
			}
			if recorded.bytes != testCase.bytes {
				t.Fatalf("bytes = %d, want %d", recorded.bytes, testCase.bytes)
			}
		})
	}
}

// The AI answer is streamed as server-sent events through this middleware, so
// the recorder has to keep the underlying writer reachable for flushing.
func TestAccessLogKeepsTheResponseFlushable(t *testing.T) {
	var flushErr error
	handler := AccessLog(discardLogger(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: chunk\n\n"))
		flushErr = http.NewResponseController(w).Flush()
	}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/ai/chat/stream", nil))
	if flushErr != nil {
		t.Fatalf("flush through the access log recorder: %v", flushErr)
	}
	if !w.Flushed {
		t.Fatal("the underlying writer was never flushed")
	}
}

func TestNoStoreKeepsAuthenticatedAnswersOutOfCaches(t *testing.T) {
	handler := NoStore(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache control = %q, want no-store", got)
	}
}
