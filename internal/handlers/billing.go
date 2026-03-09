package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"github.com/tron-legacy/api/internal/services"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Plan prices in BRL cents (monthly)
var planPrices = map[string]map[string]float64{
	"starter":    {"monthly": 49.0, "yearly": 39.0},
	"pro":        {"monthly": 149.0, "yearly": 119.0},
	"enterprise": {"monthly": 399.0, "yearly": 319.0},
}

type CheckoutRequest struct {
	PlanID       string `json:"plan_id"`
	BillingCycle string `json:"billing_cycle"` // "monthly" or "yearly"
}

type CheckoutResponse struct {
	Subscription models.Subscription `json:"subscription"`
	PaymentURL   string              `json:"payment_url,omitempty"`
}

// Checkout handles subscription creation/upgrade.
// POST /api/v1/orgs/current/subscription/checkout
func Checkout(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	if orgID == primitive.NilObjectID {
		http.Error(w, "No organization selected", http.StatusBadRequest)
		return
	}

	var req CheckoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate plan
	prices, ok := planPrices[req.PlanID]
	if !ok {
		http.Error(w, "Invalid plan. Must be starter, pro, or enterprise", http.StatusBadRequest)
		return
	}

	cycle := req.BillingCycle
	if cycle == "" {
		cycle = "monthly"
	}
	if cycle != "monthly" && cycle != "yearly" {
		http.Error(w, "Invalid billing cycle. Must be monthly or yearly", http.StatusBadRequest)
		return
	}

	price := prices[cycle]

	// Find the org owner's email for Asaas customer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get org to find owner
	var org models.Organization
	if err := database.DB.Collection("organizations").FindOne(ctx, bson.M{"_id": orgID}).Decode(&org); err != nil {
		http.Error(w, "Organization not found", http.StatusNotFound)
		return
	}

	// Get owner user
	var owner struct {
		Email string `bson:"email"`
		Name  string `bson:"name"`
	}
	if err := database.DB.Collection("users").FindOne(ctx, bson.M{"_id": org.OwnerUserID}).Decode(&owner); err != nil {
		http.Error(w, "Owner user not found", http.StatusInternalServerError)
		return
	}

	// Get current subscription
	var sub models.Subscription
	err := database.DB.Collection("subscriptions").FindOne(ctx, bson.M{"org_id": orgID}).Decode(&sub)
	if err != nil {
		http.Error(w, "Subscription not found", http.StatusNotFound)
		return
	}

	// Asaas integration
	asaas := services.NewAsaasClient()

	// Find or create customer
	var customerID string
	if sub.AsaasCustomerID != "" {
		customerID = sub.AsaasCustomerID
	} else {
		existing, _ := asaas.FindCustomerByEmail(owner.Email)
		if existing != nil {
			customerID = existing.ID
		} else {
			customer, err := asaas.CreateCustomer(services.CreateCustomerRequest{
				Name:  owner.Name,
				Email: owner.Email,
			})
			if err != nil {
				slog.Error("asaas_create_customer_failed", "error", err)
				http.Error(w, "Failed to create payment customer", http.StatusInternalServerError)
				return
			}
			customerID = customer.ID
		}
	}

	// Cancel existing Asaas subscription if upgrading
	if sub.AsaasSubscriptionID != "" {
		_ = asaas.CancelSubscription(sub.AsaasSubscriptionID)
	}

	// Create new subscription
	asaasCycle := "MONTHLY"
	if cycle == "yearly" {
		asaasCycle = "YEARLY"
	}

	asaasSub, err := asaas.CreateSubscription(services.CreateSubscriptionRequest{
		Customer:    customerID,
		BillingType: "UNDEFINED", // Let customer choose payment method
		Value:       price,
		Cycle:       asaasCycle,
		Description: "Whodo " + req.PlanID + " plan",
	})
	if err != nil {
		slog.Error("asaas_create_subscription_failed", "error", err)
		http.Error(w, "Failed to create subscription", http.StatusInternalServerError)
		return
	}

	// Update subscription in database
	now := time.Now()
	update := bson.M{
		"$set": bson.M{
			"plan_id":               req.PlanID,
			"status":                "active",
			"asaas_customer_id":     customerID,
			"asaas_subscription_id": asaasSub.ID,
			"updated_at":            now,
		},
	}

	_, err = database.DB.Collection("subscriptions").UpdateOne(ctx, bson.M{"org_id": orgID}, update)
	if err != nil {
		slog.Error("update_subscription_failed", "error", err)
		http.Error(w, "Failed to update subscription", http.StatusInternalServerError)
		return
	}

	sub.PlanID = req.PlanID
	sub.Status = "active"
	sub.AsaasCustomerID = customerID
	sub.AsaasSubscriptionID = asaasSub.ID
	sub.UpdatedAt = now

	slog.Info("checkout_completed",
		"org_id", orgID.Hex(),
		"plan", req.PlanID,
		"cycle", cycle,
		"asaas_sub", asaasSub.ID,
	)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(CheckoutResponse{
		Subscription: sub,
	})
}

// CancelSubscription handles plan cancellation.
// POST /api/v1/orgs/current/subscription/cancel
func CancelSubscription(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	if orgID == primitive.NilObjectID {
		http.Error(w, "No organization selected", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var sub models.Subscription
	err := database.DB.Collection("subscriptions").FindOne(ctx, bson.M{"org_id": orgID}).Decode(&sub)
	if err != nil {
		http.Error(w, "Subscription not found", http.StatusNotFound)
		return
	}

	if sub.PlanID == "free" {
		http.Error(w, "Cannot cancel free plan", http.StatusBadRequest)
		return
	}

	// Cancel on Asaas
	if sub.AsaasSubscriptionID != "" {
		asaas := services.NewAsaasClient()
		if err := asaas.CancelSubscription(sub.AsaasSubscriptionID); err != nil {
			slog.Error("asaas_cancel_failed", "error", err)
			// Continue anyway — update local status
		}
	}

	// Update local subscription — keep access until period end, downgrade to free
	now := time.Now()
	update := bson.M{
		"$set": bson.M{
			"status":     "canceled",
			"updated_at": now,
		},
	}

	database.DB.Collection("subscriptions").UpdateOne(ctx, bson.M{"org_id": orgID}, update)

	slog.Info("subscription_canceled", "org_id", orgID.Hex(), "plan", sub.PlanID)

	json.NewEncoder(w).Encode(map[string]string{"message": "Subscription canceled. Access continues until period end."})
}
