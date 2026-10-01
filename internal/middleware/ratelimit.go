package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/tron-legacy/api/internal/config"
	"github.com/tron-legacy/api/internal/security"
)

type rateBucket struct {
	count    int
	resetAt  time.Time
}

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rateBucket
	max     int
	window  time.Duration
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	rl := &rateLimiter{
		buckets: make(map[string]*rateBucket),
		max:     max,
		window:  window,
	}
	// Cleanup expired entries every 5 minutes
	go func() {
		for {
			time.Sleep(5 * time.Minute)
			rl.cleanup()
		}
	}()
	return rl
}

func (rl *rateLimiter) allow(key string) bool {
	return rl.allowMax(key, rl.max)
}

// allowMax is like allow but with a caller-supplied ceiling, so the limit can be
// driven by the live security policy. A max <= 0 disables the limit.
func (rl *rateLimiter) allowMax(key string, max int) bool {
	if max <= 0 {
		return true
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok || now.After(b.resetAt) {
		rl.buckets[key] = &rateBucket{count: 1, resetAt: now.Add(rl.window)}
		return true
	}
	b.count++
	return b.count <= max
}

func (rl *rateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	for k, b := range rl.buckets {
		if now.After(b.resetAt) {
			delete(rl.buckets, k)
		}
	}
}

// Auth endpoints: default 10 requests per 15 minutes per IP (the ceiling is
// overridden by the live security policy when the guard is running).
var authLimiter = newRateLimiter(10, 15*time.Minute)

// RateLimit wraps a handler with IP-based rate limiting for auth endpoints.
func RateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		max := authLimiter.max
		if security.Default != nil {
			if security.Default.Allows(ip) {
				next(w, r)
				return
			}
			max = security.Default.Policy().AuthPer15Min
		}
		if !authLimiter.allowMax(ip, max) {
			if security.Default != nil {
				security.Default.NoteRateLimited(ip)
			}
			http.Error(w, "Too many requests. Try again later.", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

// globalLimiter is created lazily (not at package-var init) because its
// limits come from config.Get(), which is only populated once main() calls
// config.Load() — before that it would be nil.
var (
	globalLimiter     *rateLimiter
	globalLimiterOnce sync.Once
)

func getGlobalLimiter() *rateLimiter {
	globalLimiterOnce.Do(func() {
		globalLimiter = newRateLimiter(config.Get().RateLimitGlobalMax, config.Get().RateLimitGlobalWindow)
	})
	return globalLimiter
}

// GlobalRateLimit wraps the whole mux with a looser IP-based rate limit,
// as defense-in-depth for routes that don't have their own limiter (RateLimit
// above stays in place, unchanged, for the stricter auth-specific limits).
// Health check and metrics are exempt since PaaS platforms poll them for
// liveness and a 429 there would trigger a restart loop.
func GlobalRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		ip := extractIP(r)
		max := getGlobalLimiter().max
		if security.Default != nil {
			if security.Default.Allows(ip) {
				next.ServeHTTP(w, r)
				return
			}
			if pm := security.Default.Policy().GlobalPerMin; pm > 0 {
				max = pm
			}
		}
		if !getGlobalLimiter().allowMax(ip, max) {
			if security.Default != nil {
				security.Default.NoteRateLimited(ip)
			}
			http.Error(w, "Too many requests. Try again later.", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func extractIP(r *http.Request) string {
	// Check common proxy headers
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take first IP (client)
		if i := len(xff); i > 0 {
			for j := 0; j < len(xff); j++ {
				if xff[j] == ',' {
					return xff[:j]
				}
			}
			return xff
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}
