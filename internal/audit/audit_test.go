package audit

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEntryNeverSerializesGoErrorsOrOversizedUserAgent(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/v1/apps", strings.NewReader(""))
	r.RemoteAddr = "10.0.0.8:1234"
	r.Header.Set("User-Agent", strings.Repeat("x", 600))
	entry := NewEntry(r, nil, "", "app.create", "app", "id", "request", nil, map[string]any{"name": "Demo"})
	if entry.Actor != "system" || entry.IP != "10.0.0.8" || len(entry.UserAgent) != 512 || !strings.Contains(string(entry.After), "Demo") {
		t.Fatalf("entry = %#v", entry)
	}
}
