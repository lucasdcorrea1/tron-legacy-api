package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Metrics stores HTTP metrics
type Metrics struct {
	mu              sync.RWMutex
	requestsTotal   map[string]int64
	requestDuration map[string][]float64
	responseSizes   map[string][]int
	activeRequests  int64
	startTime       time.Time

	// User metrics
	usersRegistered   int64
	usersLoginSuccess int64
	usersLoginFailed  int64
	authErrors        int64
	profileUpdates    int64
	avatarUploads     int64

	// Blog metrics
	postsCreated int64
	postsUpdated int64
	postsDeleted int64
}

var metrics = &Metrics{
	requestsTotal:   make(map[string]int64),
	requestDuration: make(map[string][]float64),
	responseSizes:   make(map[string][]int),
	startTime:       time.Now(),
}

// GetMetrics returns the global metrics instance
func GetMetrics() *Metrics {
	return metrics
}

// User metrics increment functions
func IncUserRegistered() {
	metrics.mu.Lock()
	metrics.usersRegistered++
	metrics.mu.Unlock()
}

func IncLoginSuccess() {
	metrics.mu.Lock()
	metrics.usersLoginSuccess++
	metrics.mu.Unlock()
}

func IncLoginFailed() {
	metrics.mu.Lock()
	metrics.usersLoginFailed++
	metrics.mu.Unlock()
}

func IncAuthError() {
	metrics.mu.Lock()
	metrics.authErrors++
	metrics.mu.Unlock()
}

func IncProfileUpdate() {
	metrics.mu.Lock()
	metrics.profileUpdates++
	metrics.mu.Unlock()
}

func IncAvatarUpload() {
	metrics.mu.Lock()
	metrics.avatarUploads++
	metrics.mu.Unlock()
}

func IncPostCreated() {
	metrics.mu.Lock()
	metrics.postsCreated++
	metrics.mu.Unlock()
}

func IncPostUpdated() {
	metrics.mu.Lock()
	metrics.postsUpdated++
	metrics.mu.Unlock()
}

func IncPostDeleted() {
	metrics.mu.Lock()
	metrics.postsDeleted++
	metrics.mu.Unlock()
}

// MetricsMiddleware collects HTTP metrics
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip metrics endpoint itself
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()

		metrics.mu.Lock()
		metrics.activeRequests++
		metrics.mu.Unlock()

		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)

		duration := time.Since(start).Seconds()

		metrics.mu.Lock()
		metrics.activeRequests--

		key := r.Method + "_" + normalizePathForMetrics(r.URL.Path) + "_" + strconv.Itoa(rw.status)
		metrics.requestsTotal[key]++
		metrics.requestDuration[key] = append(metrics.requestDuration[key], duration)
		metrics.responseSizes[key] = append(metrics.responseSizes[key], rw.size)
		metrics.mu.Unlock()
	})
}

// normalizePathForMetrics removes IDs from paths for grouping
func normalizePathForMetrics(path string) string {
	segments := []string{}
	for _, seg := range splitPath(path) {
		if isID(seg) {
			segments = append(segments, ":id")
		} else {
			segments = append(segments, seg)
		}
	}
	if len(segments) == 0 {
		return "/"
	}
	result := ""
	for _, s := range segments {
		result += "/" + s
	}
	return result
}

func splitPath(path string) []string {
	var result []string
	current := ""
	for _, c := range path {
		if c == '/' {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

func isID(s string) bool {
	// Check if it's a MongoDB ObjectID (24 hex chars)
	if len(s) == 24 {
		for _, c := range s {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
		return true
	}
	return false
}

// PrometheusHandler returns metrics in Prometheus format
func PrometheusHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics.mu.RLock()
		defer metrics.mu.RUnlock()

		w.Header().Set("Content-Type", "text/plain; version=0.0.4")

		// Help and type declarations
		w.Write([]byte("# HELP http_requests_total Total number of HTTP requests\n"))
		w.Write([]byte("# TYPE http_requests_total counter\n"))

		for key, count := range metrics.requestsTotal {
			method, path, status := parseKey(key)
			line := "http_requests_total{method=\"" + method + "\",path=\"" + path + "\",status=\"" + status + "\"} " + strconv.FormatInt(count, 10) + "\n"
			w.Write([]byte(line))
		}

		w.Write([]byte("\n# HELP http_request_duration_seconds HTTP request duration in seconds\n"))
		w.Write([]byte("# TYPE http_request_duration_seconds summary\n"))

		for key, durations := range metrics.requestDuration {
			if len(durations) == 0 {
				continue
			}
			method, path, status := parseKey(key)
			avg := average(durations)
			line := "http_request_duration_seconds{method=\"" + method + "\",path=\"" + path + "\",status=\"" + status + "\"} " + strconv.FormatFloat(avg, 'f', 6, 64) + "\n"
			w.Write([]byte(line))
		}

		w.Write([]byte("\n# HELP http_active_requests Current number of active requests\n"))
		w.Write([]byte("# TYPE http_active_requests gauge\n"))
		w.Write([]byte("http_active_requests " + strconv.FormatInt(metrics.activeRequests, 10) + "\n"))

		w.Write([]byte("\n# HELP app_uptime_seconds Application uptime in seconds\n"))
		w.Write([]byte("# TYPE app_uptime_seconds counter\n"))
		uptime := time.Since(metrics.startTime).Seconds()
		w.Write([]byte("app_uptime_seconds " + strconv.FormatFloat(uptime, 'f', 0, 64) + "\n"))

		// User metrics
		w.Write([]byte("\n# HELP users_registered_total Total number of user registrations\n"))
		w.Write([]byte("# TYPE users_registered_total counter\n"))
		w.Write([]byte("users_registered_total " + strconv.FormatInt(metrics.usersRegistered, 10) + "\n"))

		w.Write([]byte("\n# HELP users_login_total Total number of login attempts\n"))
		w.Write([]byte("# TYPE users_login_total counter\n"))
		w.Write([]byte("users_login_total{result=\"success\"} " + strconv.FormatInt(metrics.usersLoginSuccess, 10) + "\n"))
		w.Write([]byte("users_login_total{result=\"failed\"} " + strconv.FormatInt(metrics.usersLoginFailed, 10) + "\n"))

		w.Write([]byte("\n# HELP auth_errors_total Total number of authentication errors\n"))
		w.Write([]byte("# TYPE auth_errors_total counter\n"))
		w.Write([]byte("auth_errors_total " + strconv.FormatInt(metrics.authErrors, 10) + "\n"))

		w.Write([]byte("\n# HELP profile_updates_total Total number of profile updates\n"))
		w.Write([]byte("# TYPE profile_updates_total counter\n"))
		w.Write([]byte("profile_updates_total " + strconv.FormatInt(metrics.profileUpdates, 10) + "\n"))

		w.Write([]byte("\n# HELP avatar_uploads_total Total number of avatar uploads\n"))
		w.Write([]byte("# TYPE avatar_uploads_total counter\n"))
		w.Write([]byte("avatar_uploads_total " + strconv.FormatInt(metrics.avatarUploads, 10) + "\n"))

		// Blog metrics
		w.Write([]byte("\n# HELP blog_posts_created_total Total number of blog posts created\n"))
		w.Write([]byte("# TYPE blog_posts_created_total counter\n"))
		w.Write([]byte("blog_posts_created_total " + strconv.FormatInt(metrics.postsCreated, 10) + "\n"))

		w.Write([]byte("\n# HELP blog_posts_updated_total Total number of blog posts updated\n"))
		w.Write([]byte("# TYPE blog_posts_updated_total counter\n"))
		w.Write([]byte("blog_posts_updated_total " + strconv.FormatInt(metrics.postsUpdated, 10) + "\n"))

		w.Write([]byte("\n# HELP blog_posts_deleted_total Total number of blog posts deleted\n"))
		w.Write([]byte("# TYPE blog_posts_deleted_total counter\n"))
		w.Write([]byte("blog_posts_deleted_total " + strconv.FormatInt(metrics.postsDeleted, 10) + "\n"))
	})
}

func parseKey(key string) (method, path, status string) {
	first := -1
	last := -1
	for i, c := range key {
		if c == '_' {
			if first == -1 {
				first = i
			} else {
				last = i
			}
		}
	}
	if first > 0 && last > first {
		method = key[:first]
		path = key[first+1 : last]
		status = key[last+1:]
	}
	return
}

func average(nums []float64) float64 {
	if len(nums) == 0 {
		return 0
	}
	sum := 0.0
	for _, n := range nums {
		sum += n
	}
	return sum / float64(len(nums))
}
