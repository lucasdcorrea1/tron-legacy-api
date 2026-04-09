package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/tron-legacy/api/internal/config"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// WebhookSSEEvent is the payload broadcast to live-feed clients.
type WebhookSSEEvent struct {
	Type         string `json:"type"`
	RuleName     string `json:"rule_name"`
	Sender       string `json:"sender"`
	TriggerText  string `json:"trigger_text"`
	Response     string `json:"response"`
	CommentReply string `json:"comment_reply,omitempty"`
	Status       string `json:"status"`
	Timestamp    string `json:"timestamp"`
}

// ─── SSE Hub (goroutine-safe) ────────────────────────────────────────

var (
	sseMu      sync.RWMutex
	sseClients = make(map[chan WebhookSSEEvent]bool)
)

// BroadcastWebhookEvent sends an event to all connected SSE clients.
func BroadcastWebhookEvent(evt WebhookSSEEvent) {
	sseMu.RLock()
	defer sseMu.RUnlock()

	for ch := range sseClients {
		select {
		case ch <- evt:
		default:
			// Client too slow, skip
		}
	}
}

func sseRegister(ch chan WebhookSSEEvent) {
	sseMu.Lock()
	sseClients[ch] = true
	sseMu.Unlock()
}

func sseUnregister(ch chan WebhookSSEEvent) {
	sseMu.Lock()
	delete(sseClients, ch)
	close(ch)
	sseMu.Unlock()
}

// ─── SSE Handler ─────────────────────────────────────────────────────

// AutoReplySSE streams webhook events to the browser via Server-Sent Events.
// Auth is done via ?token= query param because EventSource doesn't support custom headers.
// @Summary Stream de eventos de auto-resposta (SSE)
// @Description Transmite eventos de webhook em tempo real via Server-Sent Events (autenticação via query param token)
// @Tags instagram-autoreply
// @Produce text/event-stream
// @Param token query string true "JWT token para autenticação"
// @Success 200 {string} string "Event stream"
// @Failure 401 {string} string "Token required or invalid"
// @Failure 403 {string} string "Forbidden"
// @Router /admin/instagram/autoreply/live [get]
func AutoReplySSE(w http.ResponseWriter, r *http.Request) {
	// 1. Validate JWT from query param
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		http.Error(w, "Token required", http.StatusUnauthorized)
		return
	}

	claims := &middleware.Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		return []byte(config.Get().JWTSecret), nil
	})
	if err != nil || !token.Valid {
		http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
		return
	}

	userID, err := primitive.ObjectIDFromHex(claims.UserID)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusUnauthorized)
		return
	}

	// 2. Check role (superuser or admin)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var profile models.Profile
	if err := database.Profiles().FindOne(ctx, bson.M{"user_id": userID}).Decode(&profile); err != nil {
		http.Error(w, "Profile not found", http.StatusForbidden)
		return
	}
	if profile.Role != "superuser" && profile.Role != "admin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// 3. SSE headers
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// 4. Register client
	ch := make(chan WebhookSSEEvent, 32)
	sseRegister(ch)
	defer sseUnregister(ch)

	slog.Info("sse_client_connected", "user_id", userID.Hex())

	// Send initial keepalive
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	// 5. Event loop
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			slog.Info("sse_client_disconnected", "user_id", userID.Hex())
			return
		case evt := <-ch:
			data, _ := json.Marshal(evt)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
