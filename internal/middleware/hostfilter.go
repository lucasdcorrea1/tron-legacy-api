package middleware

import (
	"net/http"
	"strings"

	"github.com/tron-legacy/api/internal/config"
)

// HostFilter rejects requests whose Host header isn't in the configured
// allowlist (config.Get().AllowedHosts). This closes the bypass where an
// attacker floods the PaaS's default subdomain (e.g. *.onrender.com) directly,
// skipping any edge protection (Cloudflare) put in front of the custom domain.
// Health check and metrics are exempt: the PaaS's own liveness probe usually
// hits an internal hostname, not the custom domain, and blocking it would
// cause the platform to think the service is down and restart it.
func HostFilter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		host := r.Host
		if i := strings.IndexByte(host, ':'); i != -1 {
			host = host[:i]
		}

		for _, allowed := range config.Get().AllowedHosts {
			if host == allowed {
				next.ServeHTTP(w, r)
				return
			}
		}

		http.Error(w, "Forbidden", http.StatusForbidden)
	})
}
