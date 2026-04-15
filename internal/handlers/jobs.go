package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// JobInfo describes a registered background job.
type JobInfo struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Interval    string    `json:"interval"`
	LastRunAt   time.Time `json:"last_run_at"`
	LastStatus  string    `json:"last_status"` // "ok", "error", "running", "idle"
	LastError   string    `json:"last_error,omitempty"`
	RunCount    int64     `json:"run_count"`
	Running     bool      `json:"running"`
}

type jobEntry struct {
	info    JobInfo
	handler func()
	mu      sync.Mutex
}

var (
	jobRegistry = map[string]*jobEntry{}
	jobsMu      sync.RWMutex
)

// RegisterJob registers a background job so it can be listed and triggered via API.
func RegisterJob(id, name, description, interval string, handler func()) {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	jobRegistry[id] = &jobEntry{
		info: JobInfo{
			ID:          id,
			Name:        name,
			Description: description,
			Interval:    interval,
			LastStatus:  "idle",
		},
		handler: handler,
	}
}

// RunJobWithTracking wraps a job handler to track execution time and status.
// Use this in the scheduler ticker loop instead of calling the handler directly.
func RunJobWithTracking(id string) {
	jobsMu.RLock()
	entry, ok := jobRegistry[id]
	jobsMu.RUnlock()
	if !ok {
		return
	}

	entry.mu.Lock()
	entry.info.Running = true
	entry.info.LastStatus = "running"
	entry.mu.Unlock()

	start := time.Now()
	func() {
		defer func() {
			if r := recover(); r != nil {
				entry.mu.Lock()
				entry.info.LastStatus = "error"
				entry.info.LastError = "panic recovered"
				entry.info.Running = false
				entry.info.LastRunAt = time.Now()
				entry.info.RunCount++
				entry.mu.Unlock()
				slog.Error("job_panic", "job_id", id, "panic", r)
			}
		}()
		entry.handler()
	}()

	entry.mu.Lock()
	entry.info.LastRunAt = time.Now()
	entry.info.RunCount++
	entry.info.Running = false
	if entry.info.LastStatus == "running" {
		entry.info.LastStatus = "ok"
		entry.info.LastError = ""
	}
	entry.mu.Unlock()

	slog.Info("job_executed", "job_id", id, "duration", time.Since(start).String())
}

// PlatformListJobs returns all registered background jobs.
func PlatformListJobs(w http.ResponseWriter, r *http.Request) {
	jobsMu.RLock()
	defer jobsMu.RUnlock()

	jobs := make([]JobInfo, 0, len(jobRegistry))
	for _, entry := range jobRegistry {
		entry.mu.Lock()
		info := entry.info // copy
		entry.mu.Unlock()
		jobs = append(jobs, info)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"jobs": jobs})
}

// PlatformTriggerJob manually triggers a background job by ID.
func PlatformTriggerJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")

	jobsMu.RLock()
	entry, ok := jobRegistry[jobID]
	jobsMu.RUnlock()

	if !ok {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	entry.mu.Lock()
	if entry.info.Running {
		entry.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Job is already running",
			"status":  "already_running",
		})
		return
	}
	entry.mu.Unlock()

	// Run in background so the HTTP response returns immediately
	go RunJobWithTracking(jobID)

	json.NewEncoder(w).Encode(map[string]string{
		"message": "Job triggered",
		"job_id":  jobID,
		"status":  "started",
	})
}
