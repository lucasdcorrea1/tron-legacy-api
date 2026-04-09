package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/tron-legacy/api/internal/config"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// AsaasWebhookEvent represents an incoming Asaas webhook payload.
// Payment events send data in "payment", subscription events in "subscription".
type AsaasWebhookEvent struct {
	Event        string          `json:"event"`
	Payment      json.RawMessage `json:"payment"`
	Subscription json.RawMessage `json:"subscription"`
}

// AsaasPaymentData holds the parsed payment fields we care about.
type AsaasPaymentData struct {
	ID           string  `json:"id"`
	Customer     string  `json:"customer"`
	Subscription string  `json:"subscription"`
	Value        float64 `json:"value"`
	Status       string  `json:"status"`
	BillingType  string  `json:"billingType"`
	DueDate      string  `json:"dueDate"`
}

// AsaasSubscriptionData holds parsed subscription event fields.
type AsaasSubscriptionData struct {
	ID          string  `json:"id"`
	Customer    string  `json:"customer"`
	Value       float64 `json:"value"`
	Status      string  `json:"status"`
	BillingType string  `json:"billingType"`
	Description string  `json:"description"`
}

// WebhookLog persists every webhook event for audit trail.
type WebhookLog struct {
	ID               primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	Event            string             `json:"event" bson:"event"`
	PaymentID        string             `json:"payment_id" bson:"payment_id"`
	SubscriptionID   string             `json:"subscription_id" bson:"subscription_id"`
	CustomerID       string             `json:"customer_id" bson:"customer_id"`
	Value            float64            `json:"value" bson:"value"`
	PaymentStatus    string             `json:"payment_status" bson:"payment_status"`
	BillingType      string             `json:"billing_type" bson:"billing_type"`
	ProcessingResult string             `json:"processing_result" bson:"processing_result"` // "ok", "ignored", "error", "duplicate"
	ErrorMessage     string             `json:"error_message,omitempty" bson:"error_message,omitempty"`
	OrgID            primitive.ObjectID `json:"org_id,omitempty" bson:"org_id,omitempty"`
	OrgName          string             `json:"org_name,omitempty" bson:"org_name,omitempty"`
	PreviousPlan     string             `json:"previous_plan,omitempty" bson:"previous_plan,omitempty"`
	NewPlan          string             `json:"new_plan,omitempty" bson:"new_plan,omitempty"`
	RawPayload       json.RawMessage    `json:"raw_payload" bson:"raw_payload"`
	CreatedAt        time.Time          `json:"created_at" bson:"created_at"`
}

// AsaasWebhook handles incoming Asaas webhook events.
func AsaasWebhook(w http.ResponseWriter, r *http.Request) {
	// ── Validate webhook token ──────────────────────────────────────
	cfg := config.Get()
	token := r.Header.Get("asaas-access-token")
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if cfg.AsaasWebhookToken != "" && token != cfg.AsaasWebhookToken {
		http.Error(w, "Invalid webhook token", http.StatusUnauthorized)
		return
	}

	// ── Parse event ─────────────────────────────────────────────────
	var event AsaasWebhookEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Parse payment data (payment events)
	var payment AsaasPaymentData
	if event.Payment != nil {
		json.Unmarshal(event.Payment, &payment)
	}

	// Parse subscription data (subscription events like SUBSCRIPTION_CREATED/DELETED)
	var subEvent AsaasSubscriptionData
	if event.Subscription != nil {
		json.Unmarshal(event.Subscription, &subEvent)
	}

	// Merge: for subscription events, fill in missing fields from subEvent
	subID := payment.Subscription
	customerID := payment.Customer
	eventID := payment.ID
	rawPayload := event.Payment

	if subEvent.ID != "" {
		if subID == "" {
			subID = subEvent.ID
		}
		if customerID == "" {
			customerID = subEvent.Customer
		}
		if eventID == "" {
			eventID = subEvent.ID
		}
		if payment.Value == 0 {
			payment.Value = subEvent.Value
		}
		if rawPayload == nil {
			rawPayload = event.Subscription
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// ── Build log entry ─────────────────────────────────────────────
	logEntry := WebhookLog{
		ID:             primitive.NewObjectID(),
		Event:          event.Event,
		PaymentID:      eventID,
		SubscriptionID: subID,
		CustomerID:     customerID,
		Value:          payment.Value,
		PaymentStatus:  payment.Status,
		BillingType:    payment.BillingType,
		RawPayload:     rawPayload,
		CreatedAt:      time.Now(),
	}

	// ── Idempotency check ───────────────────────────────────────────
	if eventID != "" {
		count, _ := database.WebhookLogs().CountDocuments(ctx, bson.M{
			"payment_id": eventID,
			"event":      event.Event,
		})
		if count > 0 {
			logEntry.ProcessingResult = "duplicate"
			slog.Info("webhook_duplicate_ignored", "event", event.Event, "payment_id", eventID)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "duplicate"})
			return
		}
	}

	// ── Find matching subscription ──────────────────────────────────
	if subID == "" && customerID == "" {
		logEntry.ProcessingResult = "ignored"
		database.WebhookLogs().InsertOne(ctx, logEntry)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ignored"})
		return
	}

	// Look up the subscription: try by asaas_subscription_id first, then by asaas_customer_id
	var sub models.Subscription
	found := false
	if subID != "" {
		if err := database.Subscriptions().FindOne(ctx, bson.M{"asaas_subscription_id": subID}).Decode(&sub); err == nil {
			found = true
		}
	}
	if !found && customerID != "" {
		// Fallback: find by customer ID (handles cases where subscription ID was replaced)
		if err := database.Subscriptions().FindOne(ctx, bson.M{"asaas_customer_id": customerID}).Decode(&sub); err == nil {
			found = true
			subID = sub.AsaasSubscriptionID // use the current sub ID for processing
		}
	}

	if found {
		logEntry.OrgID = sub.OrgID
		logEntry.PreviousPlan = sub.PlanID

		var org models.Organization
		if err := database.Organizations().FindOne(ctx, bson.M{"_id": sub.OrgID}).Decode(&org); err == nil {
			logEntry.OrgName = org.Name
		}
	}

	processErr := processWebhookEvent(ctx, event.Event, subID, customerID, &logEntry)
	if processErr != nil {
		logEntry.ProcessingResult = "error"
		logEntry.ErrorMessage = processErr.Error()
		slog.Error("webhook_processing_failed", "event", event.Event, "error", processErr)
	} else {
		logEntry.ProcessingResult = "ok"
	}

	// ── Persist log ─────────────────────────────────────────────────
	database.WebhookLogs().InsertOne(ctx, logEntry)

	slog.Info("asaas_webhook_processed",
		"event", event.Event,
		"payment_id", payment.ID,
		"subscription_id", subID,
		"result", logEntry.ProcessingResult,
	)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": logEntry.ProcessingResult})
}

func processWebhookEvent(ctx context.Context, event, subID, customerID string, logEntry *WebhookLog) error {
	now := time.Now()

	// Build filter: prefer subscription ID, fallback to customer ID
	filter := bson.M{}
	if subID != "" {
		filter["asaas_subscription_id"] = subID
	} else if customerID != "" {
		filter["asaas_customer_id"] = customerID
	} else {
		logEntry.ProcessingResult = "ignored"
		return nil
	}

	switch event {
	case "PAYMENT_CONFIRMED", "PAYMENT_RECEIVED":
		update := bson.M{
			"status":             "active",
			"current_period_end": now.AddDate(0, 1, 0),
			"updated_at":         now,
		}
		result, err := database.Subscriptions().UpdateOne(ctx, filter, bson.M{"$set": update})
		if err != nil {
			return err
		}
		if result.MatchedCount == 0 {
			logEntry.ProcessingResult = "ignored"
		}
		logEntry.NewPlan = logEntry.PreviousPlan

	case "PAYMENT_OVERDUE":
		_, err := database.Subscriptions().UpdateOne(ctx, filter,
			bson.M{"$set": bson.M{"status": "past_due", "updated_at": now}},
		)
		if err != nil {
			return err
		}

	case "PAYMENT_DELETED", "PAYMENT_REFUNDED":
		// Only mark past_due if currently pending (don't touch active subscriptions)
		_, err := database.Subscriptions().UpdateOne(ctx,
			bson.M{"$and": []bson.M{filter, {"status": "pending"}}},
			bson.M{"$set": bson.M{"status": "past_due", "updated_at": now}},
		)
		if err != nil {
			return err
		}

	case "SUBSCRIPTION_CREATED", "SUBSCRIPTION_UPDATED":
		// Just log, subscription was already created by our checkout
		logEntry.NewPlan = logEntry.PreviousPlan

	case "SUBSCRIPTION_DELETED", "SUBSCRIPTION_INACTIVATED":
		logEntry.NewPlan = "free"
		_, err := database.Subscriptions().UpdateOne(ctx, filter,
			bson.M{"$set": bson.M{
				"plan_id":    "free",
				"status":     "canceled",
				"updated_at": now,
			}},
		)
		if err != nil {
			return err
		}

	case "PAYMENT_CREATED":
		// Just log, no action needed

	default:
		// Unknown event, just log
		logEntry.ProcessingResult = "ignored"
	}

	return nil
}

// ── Webhook Logs listing (superadmin only) ──────────────────────────

type WebhookLogListResponse struct {
	Logs  []WebhookLog `json:"logs"`
	Total int64        `json:"total"`
}

// ListWebhookLogs returns paginated webhook logs for the financial dashboard.
func ListWebhookLogs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Parse query params
	page := 1
	limit := 50
	if p := r.URL.Query().Get("page"); p != "" {
		var v int
		if _, err := parseIntParam(p, &v); err == nil && v > 0 {
			page = v
		}
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		var v int
		if _, err := parseIntParam(l, &v); err == nil && v > 0 && v <= 100 {
			limit = v
		}
	}

	// Build filter
	filter := bson.M{}
	if event := r.URL.Query().Get("event"); event != "" {
		filter["event"] = event
	}
	if result := r.URL.Query().Get("result"); result != "" {
		filter["processing_result"] = result
	}
	if search := r.URL.Query().Get("search"); search != "" {
		filter["$or"] = []bson.M{
			{"org_name": bson.M{"$regex": search, "$options": "i"}},
			{"payment_id": bson.M{"$regex": search, "$options": "i"}},
			{"subscription_id": bson.M{"$regex": search, "$options": "i"}},
		}
	}

	total, _ := database.WebhookLogs().CountDocuments(ctx, filter)

	skip := int64((page - 1) * limit)
	opts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}}).
		SetSkip(skip).
		SetLimit(int64(limit))

	cursor, err := database.WebhookLogs().Find(ctx, filter, opts)
	if err != nil {
		http.Error(w, "Error listing webhook logs", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var logs []WebhookLog
	cursor.All(ctx, &logs)
	if logs == nil {
		logs = []WebhookLog{}
	}

	json.NewEncoder(w).Encode(WebhookLogListResponse{
		Logs:  logs,
		Total: total,
	})
}

// WebhookStats returns summary stats for the webhook health dashboard.
func WebhookStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	total, _ := database.WebhookLogs().CountDocuments(ctx, bson.M{})

	// Count by result
	okCount, _ := database.WebhookLogs().CountDocuments(ctx, bson.M{"processing_result": "ok"})
	errorCount, _ := database.WebhookLogs().CountDocuments(ctx, bson.M{"processing_result": "error"})
	dupCount, _ := database.WebhookLogs().CountDocuments(ctx, bson.M{"processing_result": "duplicate"})
	ignoredCount, _ := database.WebhookLogs().CountDocuments(ctx, bson.M{"processing_result": "ignored"})

	// Last 24h
	last24h := time.Now().Add(-24 * time.Hour)
	recent, _ := database.WebhookLogs().CountDocuments(ctx, bson.M{"created_at": bson.M{"$gte": last24h}})

	// Last event
	var lastLog WebhookLog
	opts := options.FindOne().SetSort(bson.D{{Key: "created_at", Value: -1}})
	database.WebhookLogs().FindOne(ctx, bson.M{}, opts).Decode(&lastLog)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"total":          total,
		"ok":             okCount,
		"errors":         errorCount,
		"duplicates":     dupCount,
		"ignored":        ignoredCount,
		"last_24h":       recent,
		"last_event":     lastLog.Event,
		"last_event_at":  lastLog.CreatedAt,
		"last_result":    lastLog.ProcessingResult,
	})
}

func parseIntParam(s string, out *int) (int, error) {
	var v int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, nil
		}
		v = v*10 + int(c-'0')
	}
	*out = v
	return v, nil
}
