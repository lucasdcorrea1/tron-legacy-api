package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/tron-legacy/api/internal/config"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/models"
	"github.com/tron-legacy/api/internal/services"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ProcessBillingGracePeriod checks for past_due subscriptions whose grace period
// has expired and auto-downgrades them to the free plan.
func ProcessBillingGracePeriod() {
	cfg := config.Get()
	graceDays := cfg.BillingGracePeriodDays
	if graceDays <= 0 {
		graceDays = 5
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cutoff := time.Now().AddDate(0, 0, -graceDays)

	// Find subscriptions that are past_due with overdue_since before the cutoff
	filter := bson.M{
		"status":       "past_due",
		"overdue_since": bson.M{"$ne": time.Time{}, "$lt": cutoff},
	}

	cursor, err := database.Subscriptions().Find(ctx, filter)
	if err != nil {
		slog.Error("billing_grace_query_failed", "error", err)
		return
	}
	defer cursor.Close(ctx)

	var subs []models.Subscription
	if err := cursor.All(ctx, &subs); err != nil {
		slog.Error("billing_grace_decode_failed", "error", err)
		return
	}

	now := time.Now()

	for _, sub := range subs {
		if sub.PlanID == "free" {
			continue // already free, skip
		}

		previousPlan := sub.PlanID

		// Auto-downgrade to free
		update := bson.M{"$set": bson.M{
			"plan_id":          "free",
			"status":           "active", // now a legitimate free user
			"previous_plan_id": previousPlan,
			"downgraded_at":    now,
			"overdue_since":    time.Time{},
			"updated_at":       now,
		}}

		_, err := database.Subscriptions().UpdateOne(ctx, bson.M{"_id": sub.ID}, update)
		if err != nil {
			slog.Error("billing_auto_downgrade_failed", "error", err, "org_id", sub.OrgID)
			continue
		}

		// Get org name for the audit log
		orgName := ""
		var org models.Organization
		if err := database.Organizations().FindOne(ctx, bson.M{"_id": sub.OrgID}).Decode(&org); err == nil {
			orgName = org.Name
		}

		// Insert audit log entry (visible in Financeiro webhook logs tab)
		logEntry := WebhookLog{
			ID:               primitive.NewObjectID(),
			Event:            "AUTO_DOWNGRADE",
			PaymentID:        "grace_expired",
			SubscriptionID:   sub.AsaasSubscriptionID,
			CustomerID:       sub.AsaasCustomerID,
			ProcessingResult: "ok",
			OrgID:            sub.OrgID,
			OrgName:          orgName,
			PreviousPlan:     previousPlan,
			NewPlan:          "free",
			RawPayload:       json.RawMessage(`{"reason":"grace_period_expired","grace_days":` + intToStr(graceDays) + `}`),
			CreatedAt:        now,
		}
		database.WebhookLogs().InsertOne(ctx, logEntry)

		slog.Warn("billing_auto_downgrade",
			"org_id", sub.OrgID.Hex(),
			"org_name", orgName,
			"previous_plan", previousPlan,
			"grace_days", graceDays,
		)
	}

	if len(subs) > 0 {
		slog.Info("billing_grace_enforcer_completed", "processed", len(subs))
	}
}

// SyncBillingWithAsaas verifies local subscription state against Asaas
// to catch missed webhooks or state drift.
func SyncBillingWithAsaas() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sixHoursAgo := time.Now().Add(-6 * time.Hour)

	// Find subscriptions with Asaas IDs that haven't been synced recently
	filter := bson.M{
		"asaas_subscription_id": bson.M{"$ne": ""},
		"$or": []bson.M{
			{"last_sync_at": bson.M{"$exists": false}},
			{"last_sync_at": time.Time{}},
			{"last_sync_at": bson.M{"$lt": sixHoursAgo}},
		},
	}

	opts := options.Find().SetLimit(50) // avoid Asaas rate limits
	cursor, err := database.Subscriptions().Find(ctx, filter, opts)
	if err != nil {
		slog.Error("billing_sync_query_failed", "error", err)
		return
	}
	defer cursor.Close(ctx)

	var subs []models.Subscription
	if err := cursor.All(ctx, &subs); err != nil {
		slog.Error("billing_sync_decode_failed", "error", err)
		return
	}

	if len(subs) == 0 {
		return
	}

	asaas := services.NewAsaasClient()
	now := time.Now()
	corrected := 0

	for _, sub := range subs {
		asaasSub, err := asaas.GetSubscription(sub.AsaasSubscriptionID)
		if err != nil {
			slog.Warn("billing_sync_fetch_failed",
				"error", err,
				"org_id", sub.OrgID.Hex(),
				"asaas_sub_id", sub.AsaasSubscriptionID,
			)
			continue // skip, will retry next cycle
		}

		updateFields := bson.M{
			"last_sync_at": now,
			"updated_at":   now,
		}

		// Update next_due_date from Asaas
		if asaasSub.NextDueDate != "" {
			if parsed, err := time.Parse("2006-01-02", asaasSub.NextDueDate); err == nil {
				updateFields["next_due_date"] = parsed
			}
		}

		// Detect and correct state mismatches
		asaasStatus := asaasSub.Status // ACTIVE, INACTIVE, EXPIRED

		switch {
		case asaasStatus == "ACTIVE" && sub.Status == "past_due":
			// Asaas says active but we think it's overdue — webhook was missed
			updateFields["status"] = "active"
			updateFields["overdue_since"] = time.Time{}
			corrected++
			slog.Warn("billing_sync_corrected",
				"org_id", sub.OrgID.Hex(),
				"correction", "past_due→active",
				"reason", "asaas_active_local_past_due",
			)

		case (asaasStatus == "INACTIVE" || asaasStatus == "EXPIRED") && sub.Status == "active" && sub.PlanID != "free":
			// Asaas canceled but we still think it's active — downgrade
			updateFields["plan_id"] = "free"
			updateFields["status"] = "canceled"
			corrected++
			slog.Warn("billing_sync_corrected",
				"org_id", sub.OrgID.Hex(),
				"correction", "active→canceled",
				"reason", "asaas_inactive_local_active",
				"previous_plan", sub.PlanID,
			)

		case asaasStatus == "ACTIVE" && sub.Status == "pending":
			// Payment confirmed but webhook missed
			updateFields["status"] = "active"
			corrected++
			slog.Warn("billing_sync_corrected",
				"org_id", sub.OrgID.Hex(),
				"correction", "pending→active",
				"reason", "asaas_active_local_pending",
			)
		}

		database.Subscriptions().UpdateOne(ctx, bson.M{"_id": sub.ID}, bson.M{"$set": updateFields})
	}

	slog.Info("billing_sync_completed", "checked", len(subs), "corrected", corrected)
}

// intToStr is a small helper to avoid importing strconv for a single use.
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}
