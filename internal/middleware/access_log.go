package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/models"
	"github.com/tron-legacy/api/internal/security"
)

// alResponseWriter captures the status code for logging.
type alResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *alResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *alResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// alSkip is true for paths we never log (noise / high-frequency liveness).
func alSkip(path string) bool {
	switch {
	case path == "/api/v1/health", path == "/metrics":
		return true
	case strings.HasPrefix(path, "/swagger/"):
		return true
	case strings.HasPrefix(path, "/api/v1/blog/images/"):
		return true
	}
	return false
}

// AccessLog records every request (method, path, status, duration, IP, UA) for
// abuse analysis. It never stores the query string or body. Entries are written
// in batches in the background by the security guard; if the guard isn't running
// the middleware is a pass-through.
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if security.Default == nil || alSkip(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		r, ident := withIdentity(r)
		sw := &alResponseWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)

		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		ua := r.Header.Get("User-Agent")
		if len(ua) > 300 {
			ua = ua[:300]
		}
		security.Default.RecordAccess(models.AccessLog{
			Method:     r.Method,
			Path:       r.URL.Path,
			Status:     status,
			DurationMs: time.Since(start).Milliseconds(),
			IP:         extractIP(r),
			UserAgent:  ua,
			UserID:     ident.userID,
			OrgID:      ident.orgID,
			CreatedAt:  time.Now(),
		})
	})
}
