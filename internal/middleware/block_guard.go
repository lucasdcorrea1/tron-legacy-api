package middleware

import (
	"net/http"

	"github.com/tron-legacy/api/internal/security"
)

// BlockGuard rejects requests from blocked IPs with 403 before they reach any
// handler or the rate limiter, reading the block set from memory (no DB hit on
// the hot path). Health and metrics are exempt so a block can never take down
// platform liveness probes. Pass-through if the guard isn't running.
func BlockGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if security.Default == nil || r.URL.Path == "/api/v1/health" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		ip := extractIP(r)
		if security.Default.IsBlocked(ip) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
