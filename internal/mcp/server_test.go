package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeTools struct{}

func (fakeTools) Tools(context.Context, Caller) []Tool {
	return []Tool{{Name: "apps_list", Description: "list", InputSchema: map[string]any{"type": "object"}}}
}
func (fakeTools) Call(_ context.Context, _ Caller, name string, _ map[string]any) (any, error) {
	return map[string]any{"tool": name}, nil
}

func validRequest(method, params string) *http.Request {
	body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":{` + params + `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"test","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}}`
	r := httptest.NewRequest(http.MethodPost, "http://appstore.test/mcp", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	r.Header.Set("Mcp-Method", method)
	return r
}

func TestDiscover(t *testing.T) {
	s := &Server{Version: "v2.0.0", Provider: fakeTools{}}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, validRequest("server/discover", ""))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"supportedVersions":["2026-07-28"]`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRejectsHeaderMismatch(t *testing.T) {
	s := &Server{Provider: fakeTools{}}
	r := validRequest("tools/list", "")
	r.Header.Set("Mcp-Method", "tools/call")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "-32020") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRejectsCrossOrigin(t *testing.T) {
	s := &Server{Provider: fakeTools{}}
	r := validRequest("tools/list", "")
	r.Header.Set("Origin", "https://evil.test")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestRejectsAnonymousCallerWhenPolicyDisablesAnonymousMCP(t *testing.T) {
	s := &Server{
		Provider: fakeTools{},
		Enabled: func(context.Context) (bool, bool, error) {
			return true, false, nil
		},
		Authenticate: func(*http.Request) (Caller, error) {
			return Caller{Permissions: map[string]bool{}}, ErrAnonymous
		},
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, validRequest("tools/list", ""))
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), `"code":-32001`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestToolCallRequiresNameHeader(t *testing.T) {
	s := &Server{Provider: fakeTools{}}
	r := validRequest("tools/call", `"name":"apps_list","arguments":{},`)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAnonymousCallerIsRejectedWhenPolicyDisablesAnonymous(t *testing.T) {
	s := &Server{
		Provider: fakeTools{},
		Enabled:  func(context.Context) (bool, bool, error) { return true, false, nil },
		Authenticate: func(*http.Request) (Caller, error) {
			return Caller{Permissions: map[string]bool{}}, ErrAnonymous
		},
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, validRequest("tools/list", ""))
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "-32001") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAppToolsAreFilteredByPermission(t *testing.T) {
	tools := (AppTools{}).Tools(context.Background(), Caller{Authenticated: true, Permissions: map[string]bool{"apps:read": true, "mcp:read": true}})
	seen := map[string]bool{}
	for _, item := range tools {
		seen[item.Name] = true
	}
	if !seen["apps_list"] || !seen["my_apps"] || seen["app_submit"] || seen["settings_get"] {
		t.Fatalf("unexpected tool grants: %#v", seen)
	}
}
