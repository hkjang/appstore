package analytics

import (
	"fmt"
	"testing"
	"time"
)

func TestRecorderKeepsDistinctOrigins(t *testing.T) {
	recorder := NewRecorder()
	clock := time.Date(2026, 9, 12, 14, 0, 0, 0, time.UTC)
	recorder.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	for i := 0; i < 5; i++ {
		recorder.Record("https://momento.corp.example/collect/v1/events", "connect-src", "https://store.corp.example/apps")
	}
	recorder.Record("https://momento.corp.example/tracker.js", "script-src-elem 'self'", "https://store.corp.example/")
	recorder.Record("chrome-extension://abc/inject.js", "script-src", "/")
	recorder.Record("data:text/javascript,1", "script-src", "/")
	recorder.Record("", "connect-src", "/")
	items := recorder.List(Default())
	if len(items) != 2 {
		t.Fatalf("expected two distinct origin+directive entries, got %+v", items)
	}
	if items[0].Directive != "script-src-elem" || items[1].Count != 5 || items[1].Directive != "connect-src" {
		t.Fatalf("items=%+v", items)
	}
	if items[0].Allowed || items[1].Allowed {
		t.Fatal("nothing is allowed by the default config")
	}
}

func TestRecorderMarksAllowedOrigins(t *testing.T) {
	recorder := NewRecorder()
	recorder.Record("https://momento.corp.example/collect", "connect-src", "/")
	recorder.Record("https://region1.google-analytics.com/g/collect", "connect-src", "/")
	recorder.Record("https://other.example/x.js", "script-src", "/")
	config := Config{Enabled: true, Provider: ProviderGA4, MeasurementID: "G-1", AllowedHosts: "https://momento.corp.example"}
	allowed := map[string]bool{}
	for _, item := range recorder.List(config) {
		allowed[item.Origin] = item.Allowed
	}
	if !allowed["https://momento.corp.example"] || !allowed["https://region1.google-analytics.com"] || allowed["https://other.example"] {
		t.Fatalf("allowed=%v", allowed)
	}
}

func TestRecorderIsBoundedAndForgets(t *testing.T) {
	recorder := NewRecorder()
	clock := time.Unix(0, 0)
	recorder.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	for i := 0; i < MaxViolations+10; i++ {
		recorder.Record(fmt.Sprintf("https://host%03d.example/x", i), "connect-src", "/")
	}
	items := recorder.List(Default())
	if len(items) != MaxViolations {
		t.Fatalf("expected %d entries, got %d", MaxViolations, len(items))
	}
	for _, item := range items {
		if item.Origin == "https://host000.example" {
			t.Fatal("the oldest entry should have been evicted")
		}
	}
	recorder.Forget()
	if len(recorder.List(Default())) != 0 {
		t.Fatal("forget must clear the buffer")
	}
}
