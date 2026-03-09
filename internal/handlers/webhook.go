package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/tron-legacy/api/internal/config"
	"github.com/tron-legacy/api/internal/database"
	"go.mongodb.org/mongo-driver/bson"
)

// AsaasWebhookEvent represents an incoming Asaas webhook payload.
type AsaasWebhookEvent struct {
	Event   string `json:"event"`
	Payment struct {
		ID           string  `json:"id"`
		Customer     string  `json:"customer"`
		Subscription string  `json:"subscription"`
		Value        float64 `json:"value"`
		Status       string  `json:"status"`
		DueDate      string  `json:"dueDate"`
	} `json:"payment"`
}

// AsaasWebhook handles incoming Asaas webhook events.
// POST /api/v1/webhooks/asaas
func AsaasWebhook(w http.ResponseWriter, r *http.Request) {
	// Validate webhook token
	cfg := config.Get()
	token := r.Header.Get("asaas-access-token")
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if cfg.AsaasWebhookToken != "" && token != cfg.AsaasWebhookToken {
		http.Error(w, "Invalid webhook token", http.StatusUnauthorized)
		return
	}

	var event AsaasWebhookEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	slog.Info("asaas_webhook_received",
		"event", event.Event,
		"subscription_id", event.Payment.Subscription,
		"payment_status", event.Payment.Status,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	subID := event.Payment.Subscription
	if subID == "" {
		// Not a subscription payment, ignore
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ignored"})
		return
	}

	switch event.Event {
	case "PAYMENT_CONFIRMED", "PAYMENT_RECEIVED":
		// Payment confirmed — ensure subscription is active
		database.DB.Collection("subscriptions").UpdateOne(ctx,
			bson.M{"asaas_subscription_id": subID},
			bson.M{"$set": bson.M{
				"status":     "active",
				"updated_at": time.Now(),
			}},
		)
		slog.Info("subscription_activated_via_webhook", "asaas_sub", subID)

	case "PAYMENT_OVERDUE":
		// Payment overdue — mark as past_due
		database.DB.Collection("subscriptions").UpdateOne(ctx,
			bson.M{"asaas_subscription_id": subID},
			bson.M{"$set": bson.M{
				"status":     "past_due",
				"updated_at": time.Now(),
			}},
		)
		slog.Warn("subscription_past_due", "asaas_sub", subID)

	case "SUBSCRIPTION_DELETED", "SUBSCRIPTION_INACTIVATED":
		// Subscription canceled/deleted — downgrade to free
		database.DB.Collection("subscriptions").UpdateOne(ctx,
			bson.M{"asaas_subscription_id": subID},
			bson.M{"$set": bson.M{
				"plan_id":    "free",
				"status":     "canceled",
				"updated_at": time.Now(),
			}},
		)
		slog.Info("subscription_canceled_via_webhook", "asaas_sub", subID)
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
