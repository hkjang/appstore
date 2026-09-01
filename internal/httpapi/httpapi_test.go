package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteErrorUsesStableEnvelope(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/apps/nope", nil)
	r = r.WithContext(contextWithRequestID(r.Context(), "request-123"))
	w := httptest.NewRecorder()
	WriteError(w, r, NotFound("APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"requestId":"request-123"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"ok","unknown":true}`))
	w := httptest.NewRecorder()
	var value struct {
		Name string `json:"name"`
	}
	if err := DecodeJSON(w, r, &value); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestSPARefreshFallsBackToIndex(t *testing.T) {
	spa, err := NewSPAHandler()
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	spa.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func contextWithRequestID(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, requestIDKey, value)
}
