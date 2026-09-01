package httpapi

import (
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
