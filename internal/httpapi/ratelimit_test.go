package httpapi

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFixedWindowLimiter(t *testing.T) {
	limiter := newFixedWindowLimiter()
	now := time.Date(2026, 9, 1, 0, 0, 30, 0, time.UTC)
	for index := 0; index < 2; index++ {
		allowed, remaining := limiter.allow("client", 2, now)
		if !allowed || remaining != 1-index {
			t.Fatalf("request %d: allowed=%v remaining=%d", index, allowed, remaining)
		}
	}
	if allowed, _ := limiter.allow("client", 2, now); allowed {
		t.Fatal("third request in the same window must be rejected")
	}
	if allowed, remaining := limiter.allow("client", 2, now.Add(time.Minute)); !allowed || remaining != 1 {
		t.Fatalf("new window: allowed=%v remaining=%d", allowed, remaining)
	}
}

func TestFixedWindowLimiterDropsFinishedWindowsOnLowTraffic(t *testing.T) {
	limiter := newFixedWindowLimiter()
	now := time.Date(2026, 9, 1, 0, 0, 30, 0, time.UTC)
	for index := 0; index < 50; index++ {
		limiter.allow(fmt.Sprintf("anonymous:198.51.100.%d", index), 10, now)
	}
	if got := len(limiter.buckets); got != 50 {
		t.Fatalf("buckets in the first window = %d, want 50", got)
	}
	// A single request in the next window is enough to release the previous
	// one, even though the limiter has been called far fewer than 1024 times.
	limiter.allow("anonymous:203.0.113.7", 10, now.Add(time.Minute))
	if got := len(limiter.buckets); got != 1 {
		t.Fatalf("buckets after the window rolled over = %d, want 1", got)
	}
	if allowed, remaining := limiter.allow("anonymous:203.0.113.7", 2, now.Add(time.Minute)); !allowed || remaining != 0 {
		t.Fatalf("the sweep must keep the current window: allowed=%v remaining=%d", allowed, remaining)
	}
}

func TestRetryAfterSecondsCountsDownTheCurrentWindow(t *testing.T) {
	seoul := time.FixedZone("KST", 9*60*60)
	for _, testCase := range []struct {
		name string
		now  time.Time
		want int
	}{
		{"window start", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), 60},
		{"one nanosecond in", time.Date(2026, 9, 1, 0, 0, 0, 1, time.UTC), 60},
		{"half way", time.Date(2026, 9, 1, 0, 30, 30, 0, time.UTC), 30},
		{"a fraction past the middle", time.Date(2026, 9, 1, 0, 30, 30, 400_000_000, time.UTC), 30},
		{"last whole second", time.Date(2026, 9, 1, 0, 30, 59, 0, time.UTC), 1},
		{"last nanosecond", time.Date(2026, 9, 1, 0, 30, 59, 999_999_999, time.UTC), 1},
		{"a location other than UTC", time.Date(2026, 9, 1, 9, 30, 45, 0, seoul), 15},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := retryAfterSeconds(testCase.now); got != testCase.want {
				t.Fatalf("retryAfterSeconds(%s) = %d, want %d", testCase.now, got, testCase.want)
			}
		})
	}
}

// The header must never promise a wait shorter than the window it belongs to:
// a client that comes back exactly when told has to find the bucket reset.
func TestRetryAfterSecondsOutlastsTheRejectingWindow(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for offset := 0; offset < 60_000; offset += 137 {
		now := start.Add(time.Duration(offset) * time.Millisecond)
		limiter := newFixedWindowLimiter()
		limiter.allow("client", 1, now)
		if allowed, _ := limiter.allow("client", 1, now); allowed {
			t.Fatalf("the second request at %s must be rejected", now)
		}
		wait := time.Duration(retryAfterSeconds(now)) * time.Second
		if allowed, _ := limiter.allow("client", 1, now.Add(wait)); !allowed {
			t.Fatalf("still rejected at %s after the advertised %s", now, wait)
		}
	}
}

func TestFixedWindowLimiterIsConcurrentAndKeyScoped(t *testing.T) {
	limiter := newFixedWindowLimiter()
	now := time.Date(2026, 9, 1, 0, 0, 30, 0, time.UTC)
	const limit = 50
	const attempts = 500
	var allowed atomic.Int64
	var wait sync.WaitGroup
	for index := 0; index < attempts; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if ok, _ := limiter.allow("shared-client", limit, now); ok {
				allowed.Add(1)
			}
		}()
	}
	wait.Wait()
	if got := allowed.Load(); got != limit {
		t.Fatalf("concurrent allowed requests = %d, want %d", got, limit)
	}
	if ok, remaining := limiter.allow("independent-client", 1, now); !ok || remaining != 0 {
		t.Fatalf("independent key: allowed=%v remaining=%d", ok, remaining)
	}
}
