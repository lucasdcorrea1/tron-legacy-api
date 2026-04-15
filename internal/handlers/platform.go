package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/tron-legacy/api/internal/config"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/models"
	"github.com/tron-legacy/api/internal/services"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// PlatformListOrgs lists all organizations (superadmin only).
// @Summary Listar todas as organizações
// @Description Lista todas as organizações da plataforma (somente superadmin)
// @Tags platform
// @Produce json
// @Security BearerAuth
// @Param page query int false "Página (padrão 1)"
// @Param limit query int false "Itens por página (padrão 20, máx 100)"
// @Success 200 {object} map[string]interface{}
// @Failure 401 {string} string "Unauthorized"
// @Failure 500 {string} string "Error listing organizations"
// @Router /platform/orgs [get]
func PlatformListOrgs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}}).
		SetSkip(skip).
		SetLimit(int64(limit))

	total, _ := database.Organizations().CountDocuments(ctx, bson.M{})

	cursor, err := database.Organizations().Find(ctx, bson.M{}, opts)
	if err != nil {
		http.Error(w, "Error listing organizations", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var orgs []models.Organization
	cursor.All(ctx, &orgs)
	if orgs == nil {
		orgs = []models.Organization{}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"organizations": orgs,
		"total":         total,
		"page":          page,
		"limit":         limit,
	})
}

// PlatformStats returns platform-wide metrics (superadmin only).
// @Summary Estatísticas da plataforma
// @Description Retorna métricas globais da plataforma (somente superadmin)
// @Tags platform
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 401 {string} string "Unauthorized"
// @Router /platform/stats [get]
func PlatformStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	totalOrgs, _ := database.Organizations().CountDocuments(ctx, bson.M{})
	totalUsers, _ := database.Users().CountDocuments(ctx, bson.M{})
	totalPosts, _ := database.Posts().CountDocuments(ctx, bson.M{})

	// Count by plan
	planCounts := map[string]int64{}
	for plan := range models.Plans {
		count, _ := database.Subscriptions().CountDocuments(ctx, bson.M{"plan_id": plan})
		planCounts[plan] = count
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_organizations": totalOrgs,
		"total_users":         totalUsers,
		"total_posts":         totalPosts,
		"subscriptions_by_plan": planCounts,
	})
}

// PlatformOrgsWithMembers returns all organizations with their members (superadmin only).
// @Summary Organizações com membros
// @Description Retorna todas as organizações com seus membros (somente superadmin)
// @Tags platform
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 401 {string} string "Unauthorized"
// @Failure 500 {string} string "Error listing organizations"
// @Router /platform/orgs-with-members [get]
func PlatformOrgsWithMembers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Fetch all orgs
	cursor, err := database.Organizations().Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		http.Error(w, "Error listing organizations", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var allOrgs []models.Organization
	cursor.All(ctx, &allOrgs)
	if allOrgs == nil {
		allOrgs = []models.Organization{}
	}

	type memberInfo struct {
		UserID       primitive.ObjectID `json:"user_id"`
		Name         string             `json:"name"`
		Email        string             `json:"email"`
		Avatar       string             `json:"avatar,omitempty"`
		OrgRole      string             `json:"org_role"`
		PlatformRole string             `json:"platform_role"`
		JoinedAt     time.Time          `json:"joined_at"`
	}

	type orgWithMembers struct {
		ID      primitive.ObjectID `json:"id"`
		Name    string             `json:"name"`
		Slug    string             `json:"slug"`
		Members []memberInfo       `json:"members"`
	}

	result := make([]orgWithMembers, 0, len(allOrgs))

	for _, org := range allOrgs {
		memCursor, err := database.OrgMemberships().Find(ctx, bson.M{"org_id": org.ID})
		if err != nil {
			continue
		}
		var memberships []models.OrgMembership
		memCursor.All(ctx, &memberships)
		memCursor.Close(ctx)

		members := make([]memberInfo, 0, len(memberships))
		for _, m := range memberships {
			var user models.User
			database.Users().FindOne(ctx, bson.M{"_id": m.UserID}).Decode(&user)
			var profile models.Profile
			database.Profiles().FindOne(ctx, bson.M{"user_id": m.UserID}).Decode(&profile)

			members = append(members, memberInfo{
				UserID:       m.UserID,
				Name:         profile.Name,
				Email:        user.Email,
				Avatar:       profile.Avatar,
				OrgRole:      m.OrgRole,
				PlatformRole: profile.Role,
				JoinedAt:     m.JoinedAt,
			})
		}

		result = append(result, orgWithMembers{
			ID:      org.ID,
			Name:    org.Name,
			Slug:    org.Slug,
			Members: members,
		})
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"organizations": result,
		"total":         len(result),
	})
}

// PlatformUpdatePlan overrides an organization's subscription plan (superadmin only).
// @Summary Atualizar plano de organização
// @Description Sobrescreve o plano de assinatura de uma organização (somente superadmin)
// @Tags platform
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Organization ID"
// @Param body body object true "Novo plano (plan_id)"
// @Success 200 {object} map[string]string
// @Failure 400 {string} string "Invalid plan ID"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Subscription not found"
// @Router /platform/orgs/{id}/plan [put]
func PlatformUpdatePlan(w http.ResponseWriter, r *http.Request) {
	orgID, err := primitive.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		http.Error(w, "Invalid organization ID", http.StatusBadRequest)
		return
	}

	var req struct {
		PlanID string `json:"plan_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if _, ok := models.Plans[req.PlanID]; !ok {
		http.Error(w, "Invalid plan ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := database.Subscriptions().UpdateOne(ctx,
		bson.M{"org_id": orgID},
		bson.M{"$set": bson.M{
			"plan_id":    req.PlanID,
			"updated_at": time.Now(),
		}},
	)
	if err != nil || result.MatchedCount == 0 {
		http.Error(w, "Subscription not found for this organization", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"message": "Plan updated to " + req.PlanID,
	})
}

// PlatformListSubscriptions returns all organizations with their subscription details (superadmin only).
func PlatformListSubscriptions(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Fetch all orgs
	cursor, err := database.Organizations().Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		http.Error(w, "Error listing organizations", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var allOrgs []models.Organization
	cursor.All(ctx, &allOrgs)
	if allOrgs == nil {
		allOrgs = []models.Organization{}
	}

	type orgSubscription struct {
		OrgID        primitive.ObjectID  `json:"org_id"`
		OrgName      string              `json:"org_name"`
		OrgSlug      string              `json:"org_slug"`
		OwnerName    string              `json:"owner_name"`
		OwnerEmail   string              `json:"owner_email"`
		MemberCount  int                 `json:"member_count"`
		Subscription *models.Subscription `json:"subscription"`
	}

	result := make([]orgSubscription, 0, len(allOrgs))

	for _, org := range allOrgs {
		item := orgSubscription{
			OrgID:   org.ID,
			OrgName: org.Name,
			OrgSlug: org.Slug,
		}

		// Get owner info
		var ownerProfile models.Profile
		if err := database.Profiles().FindOne(ctx, bson.M{"user_id": org.OwnerUserID}).Decode(&ownerProfile); err == nil {
			item.OwnerName = ownerProfile.Name
		}
		var ownerUser models.User
		if err := database.Users().FindOne(ctx, bson.M{"_id": org.OwnerUserID}).Decode(&ownerUser); err == nil {
			item.OwnerEmail = ownerUser.Email
		}

		// Count members
		count, _ := database.OrgMemberships().CountDocuments(ctx, bson.M{"org_id": org.ID})
		item.MemberCount = int(count)

		// Get subscription
		var sub models.Subscription
		if err := database.Subscriptions().FindOne(ctx, bson.M{"org_id": org.ID}).Decode(&sub); err == nil {
			item.Subscription = &sub
		}

		result = append(result, item)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"subscriptions": result,
		"total":         len(result),
	})
}

// PlatformUpdateSubscriptionStatus activates or deactivates a subscription (superadmin only).
func PlatformUpdateSubscriptionStatus(w http.ResponseWriter, r *http.Request) {
	orgID, err := primitive.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		http.Error(w, "Invalid organization ID", http.StatusBadRequest)
		return
	}

	var req struct {
		Status string `json:"status"` // "active" or "canceled"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Status != "active" && req.Status != "canceled" {
		http.Error(w, "Status must be 'active' or 'canceled'", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := bson.M{
		"$set": bson.M{
			"status":     req.Status,
			"updated_at": time.Now(),
		},
	}

	// If canceling, also downgrade to free
	if req.Status == "canceled" {
		update["$set"].(bson.M)["plan_id"] = "free"
	}

	result, err := database.Subscriptions().UpdateOne(ctx, bson.M{"org_id": orgID}, update)
	if err != nil || result.MatchedCount == 0 {
		http.Error(w, "Subscription not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"message": "Subscription status updated to " + req.Status,
	})
}

// PlatformGetOrgPaymentHistory returns payment history for an organization from Asaas.
func PlatformGetOrgPaymentHistory(w http.ResponseWriter, r *http.Request) {
	orgID, err := primitive.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		http.Error(w, "Invalid organization ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var sub models.Subscription
	if err := database.Subscriptions().FindOne(ctx, bson.M{"org_id": orgID}).Decode(&sub); err != nil {
		http.Error(w, "Subscription not found", http.StatusNotFound)
		return
	}

	if sub.AsaasSubscriptionID == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{"payments": []interface{}{}})
		return
	}

	asaas := services.NewAsaasClient()
	payments, err := asaas.GetSubscriptionPayments(sub.AsaasSubscriptionID)
	if err != nil {
		slog.Error("platform_get_payments_failed", "error", err, "org_id", orgID.Hex())
		http.Error(w, "Failed to fetch payments from Asaas", http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"payments": payments})
}

// PlatformRevenueMetrics returns revenue metrics for the platform.
func PlatformRevenueMetrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Plan prices (monthly / yearly-per-month)
	prices := map[string]map[string]float64{
		"starter":    {"monthly": 49.0, "yearly": 39.0},
		"pro":        {"monthly": 149.0, "yearly": 119.0},
		"enterprise": {"monthly": 399.0, "yearly": 319.0},
	}

	// Get all active paid subscriptions
	cursor, err := database.Subscriptions().Find(ctx, bson.M{
		"status":  "active",
		"plan_id": bson.M{"$ne": "free"},
	})
	if err != nil {
		http.Error(w, "Error fetching subscriptions", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var activeSubs []models.Subscription
	cursor.All(ctx, &activeSubs)

	var mrr float64
	activePaid := len(activeSubs)
	byPlan := map[string]struct {
		Count   int     `json:"count"`
		Revenue float64 `json:"revenue"`
	}{}

	for _, sub := range activeSubs {
		planPrices, ok := prices[sub.PlanID]
		if !ok {
			continue
		}
		cycle := sub.BillingCycle
		if cycle == "" {
			cycle = "monthly"
		}
		monthlyPrice := planPrices[cycle]
		if cycle == "yearly" {
			// yearly price is per-month, so it's already monthly
		}
		mrr += monthlyPrice

		entry := byPlan[sub.PlanID]
		entry.Count++
		entry.Revenue += monthlyPrice
		byPlan[sub.PlanID] = entry
	}

	// Count overdue
	overdueCount, _ := database.Subscriptions().CountDocuments(ctx, bson.M{"status": "past_due"})

	// Count churn (canceled in last 30 days via webhook logs)
	thirtyDaysAgo := time.Now().AddDate(0, 0, -30)
	churnCount, _ := database.WebhookLogs().CountDocuments(ctx, bson.M{
		"event":      bson.M{"$in": []string{"SUBSCRIPTION_DELETED", "SUBSCRIPTION_INACTIVATED", "AUTO_DOWNGRADE"}},
		"created_at": bson.M{"$gte": thirtyDaysAgo},
	})

	// Churn rate = churn / (active + churn) to avoid division by zero
	var churnRate float64
	total := int64(activePaid) + churnCount
	if total > 0 {
		churnRate = math.Round(float64(churnCount)/float64(total)*10000) / 100 // percentage with 2 decimals
	}

	// Convert byPlan to serializable map
	byPlanResult := map[string]interface{}{}
	for plan, data := range byPlan {
		byPlanResult[plan] = data
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"mrr":              math.Round(mrr*100) / 100,
		"arr":              math.Round(mrr*12*100) / 100,
		"active_paid":      activePaid,
		"overdue_count":    overdueCount,
		"churn_last_30d":   churnCount,
		"churn_rate":       churnRate,
		"by_plan":          byPlanResult,
	})
}

// PlatformListOverdue returns subscriptions that are past_due with grace period info.
func PlatformListOverdue(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cfg := config.Get()
	defaultGrace := cfg.BillingGracePeriodDays
	if defaultGrace <= 0 {
		defaultGrace = 5
	}

	cursor, err := database.Subscriptions().Find(ctx, bson.M{
		"status":        "past_due",
		"overdue_since": bson.M{"$ne": time.Time{}},
	}, options.Find().SetSort(bson.D{{Key: "overdue_since", Value: 1}}))
	if err != nil {
		http.Error(w, "Error fetching overdue subscriptions", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var subs []models.Subscription
	cursor.All(ctx, &subs)

	now := time.Now()

	type overdueItem struct {
		OrgID           primitive.ObjectID `json:"org_id"`
		OrgName         string             `json:"org_name"`
		OwnerEmail      string             `json:"owner_email"`
		OwnerName       string             `json:"owner_name"`
		PlanID          string             `json:"plan_id"`
		BillingCycle    string             `json:"billing_cycle"`
		DaysOverdue     int                `json:"days_overdue"`
		GraceDaysLeft   int                `json:"grace_days_left"`
		GracePeriodDays int                `json:"grace_period_days"`
		OverdueSince    time.Time          `json:"overdue_since"`
	}

	result := make([]overdueItem, 0, len(subs))

	for _, sub := range subs {
		graceDays := sub.GracePeriodDays
		if graceDays <= 0 {
			graceDays = defaultGrace
		}

		daysOverdue := int(now.Sub(sub.OverdueSince).Hours() / 24)
		graceLeft := graceDays - daysOverdue
		if graceLeft < 0 {
			graceLeft = 0
		}

		item := overdueItem{
			OrgID:           sub.OrgID,
			PlanID:          sub.PlanID,
			BillingCycle:    sub.BillingCycle,
			DaysOverdue:     daysOverdue,
			GraceDaysLeft:   graceLeft,
			GracePeriodDays: graceDays,
			OverdueSince:    sub.OverdueSince,
		}

		// Get org info
		var org models.Organization
		if err := database.Organizations().FindOne(ctx, bson.M{"_id": sub.OrgID}).Decode(&org); err == nil {
			item.OrgName = org.Name

			var ownerProfile models.Profile
			if err := database.Profiles().FindOne(ctx, bson.M{"user_id": org.OwnerUserID}).Decode(&ownerProfile); err == nil {
				item.OwnerName = ownerProfile.Name
			}
			var ownerUser models.User
			if err := database.Users().FindOne(ctx, bson.M{"_id": org.OwnerUserID}).Decode(&ownerUser); err == nil {
				item.OwnerEmail = ownerUser.Email
			}
		}

		result = append(result, item)
	}

	// Count how many will be downgraded in next 24h
	imminentCount := 0
	for _, item := range result {
		if item.GraceDaysLeft <= 1 {
			imminentCount++
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"overdue":         result,
		"total":           len(result),
		"imminent_count":  imminentCount,
	})
}

// PlatformExtendGrace sets a custom grace period for an organization.
func PlatformExtendGrace(w http.ResponseWriter, r *http.Request) {
	orgID, err := primitive.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		http.Error(w, "Invalid organization ID", http.StatusBadRequest)
		return
	}

	var req struct {
		GracePeriodDays int `json:"grace_period_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.GracePeriodDays < 1 {
		http.Error(w, "grace_period_days must be >= 1", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := database.Subscriptions().UpdateOne(ctx,
		bson.M{"org_id": orgID},
		bson.M{"$set": bson.M{
			"grace_period_days": req.GracePeriodDays,
			"updated_at":       time.Now(),
		}},
	)
	if err != nil || result.MatchedCount == 0 {
		http.Error(w, "Subscription not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":          "Grace period updated",
		"grace_period_days": req.GracePeriodDays,
	})
}

// PlatformSyncOrg forces an immediate Asaas sync for a single organization.
func PlatformSyncOrg(w http.ResponseWriter, r *http.Request) {
	orgID, err := primitive.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		http.Error(w, "Invalid organization ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var sub models.Subscription
	if err := database.Subscriptions().FindOne(ctx, bson.M{"org_id": orgID}).Decode(&sub); err != nil {
		http.Error(w, "Subscription not found", http.StatusNotFound)
		return
	}

	if sub.AsaasSubscriptionID == "" {
		json.NewEncoder(w).Encode(map[string]string{
			"message": "No Asaas subscription linked",
			"status":  "skipped",
		})
		return
	}

	asaas := services.NewAsaasClient()
	asaasSub, err := asaas.GetSubscription(sub.AsaasSubscriptionID)
	if err != nil {
		slog.Error("platform_sync_fetch_failed", "error", err, "org_id", orgID.Hex())
		http.Error(w, "Failed to fetch subscription from Asaas", http.StatusBadGateway)
		return
	}

	now := time.Now()
	updateFields := bson.M{
		"last_sync_at": now,
		"updated_at":   now,
	}

	if asaasSub.NextDueDate != "" {
		if parsed, err := time.Parse("2006-01-02", asaasSub.NextDueDate); err == nil {
			updateFields["next_due_date"] = parsed
		}
	}

	correction := "none"
	asaasStatus := asaasSub.Status

	switch {
	case asaasStatus == "ACTIVE" && sub.Status == "past_due":
		updateFields["status"] = "active"
		updateFields["overdue_since"] = time.Time{}
		correction = "past_due→active"

	case (asaasStatus == "INACTIVE" || asaasStatus == "EXPIRED") && sub.Status == "active" && sub.PlanID != "free":
		updateFields["plan_id"] = "free"
		updateFields["status"] = "canceled"
		correction = "active→canceled"

	case asaasStatus == "ACTIVE" && sub.Status == "pending":
		updateFields["status"] = "active"
		correction = "pending→active"
	}

	database.Subscriptions().UpdateOne(ctx, bson.M{"_id": sub.ID}, bson.M{"$set": updateFields})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":      "Sync completed",
		"asaas_status": asaasStatus,
		"local_status": sub.Status,
		"correction":   correction,
	})
}
