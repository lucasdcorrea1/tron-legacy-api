package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// PlatformListOrgs lists all organizations (superadmin only).
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
