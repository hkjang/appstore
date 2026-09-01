package httpapi

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type rateBucket struct {
	window time.Time
	count  int
}

type fixedWindowLimiter struct {
	mu      sync.Mutex
	buckets map[string]rateBucket
	calls   int
}

func newFixedWindowLimiter() *fixedWindowLimiter {
	return &fixedWindowLimiter{buckets: map[string]rateBucket{}}
}

func (l *fixedWindowLimiter) allow(key string, limit int, now time.Time) (bool, int) {
	if limit < 1 {
		limit = 1
	}
	window := now.UTC().Truncate(time.Minute)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if l.calls%1024 == 0 {
		for candidate, bucket := range l.buckets {
			if bucket.window.Before(window) {
				delete(l.buckets, candidate)
			}
		}
	}
	bucket := l.buckets[key]
	if bucket.window != window {
		bucket = rateBucket{window: window}
	}
	if bucket.count >= limit {
		return false, 0
	}
	bucket.count++
	l.buckets[key] = bucket
	return true, limit - bucket.count
}

func (s *Server) apiPolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		settings, err := s.repository.GetAPISettings(r.Context())
		if err != nil {
			WriteError(w, r, &APIError{Status: http.StatusServiceUnavailable, Code: "API_POLICY_UNAVAILABLE", Message: "API 정책을 확인할 수 없습니다."})
			return
		}
		principal := CurrentPrincipal(r.Context())
		if principal == nil && isCatalogAPI(r.URL.Path) {
			system, systemErr := s.repository.GetSystemSettings(r.Context())
			if systemErr != nil {
				WriteError(w, r, &APIError{Status: http.StatusServiceUnavailable, Code: "SYSTEM_POLICY_UNAVAILABLE", Message: "공개 접근 정책을 확인할 수 없습니다."})
				return
			}
			if !settings.Anonymous || !system.PublicMode {
				WriteError(w, r, Unauthorized("익명 AppStore 탐색이 비활성화되어 있습니다."))
				return
			}
		}
		// The API enabled switch controls automation credentials without taking
		// the browser UI offline. Public catalog access is governed separately by
		// Anonymous and PublicMode above; both default to enabled.
		if principal != nil && principal.AuthMethod == "api_key" && !settings.Enabled {
			WriteError(w, r, &APIError{Status: http.StatusServiceUnavailable, Code: "API_DISABLED", Message: "자동화 API가 비활성화되어 있습니다."})
			return
		}
		key := "anonymous:" + clientAddress(r)
		if principal != nil {
			key = principal.AuthMethod + ":" + principal.User.ID.String()
		}
		allowed, remaining := s.apiLimiter.allow(key, settings.RateLimitPerMinute, time.Now())
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(settings.RateLimitPerMinute))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		if !allowed {
			w.Header().Set("Retry-After", "60")
			WriteError(w, r, &APIError{Status: http.StatusTooManyRequests, Code: "RATE_LIMITED", Message: "요청이 너무 많습니다. 잠시 후 다시 시도하세요."})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isCatalogAPI(path string) bool {
	return path == "/api/v1/apps" || strings.HasPrefix(path, "/api/v1/apps/") || path == "/api/v1/categories"
}

func (s *Server) mcpRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		settings, err := s.repository.GetMCPSettings(r.Context())
		if err != nil {
			http.Error(w, "MCP policy unavailable", http.StatusServiceUnavailable)
			return
		}
		allowed, remaining := s.mcpLimiter.allow(clientAddress(r), settings.RateLimitPerMinute, time.Now())
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(settings.RateLimitPerMinute))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		if !allowed {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "MCP rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}
