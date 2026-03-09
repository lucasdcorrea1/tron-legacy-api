// Migration script: single-tenant → multi-tenant
//
// Usage:
//   go run cmd/migrate/main.go
//
// What it does:
//   1. For each user with role admin/superuser: create an Organization + OrgMembership (owner)
//   2. For each user with role author: create OrgMembership (member) under their admin's org, or create own org
//   3. For each user with role user: create OrgMembership (viewer) or create own org
//   4. Migrate platform roles: superuser → superadmin, admin → user, author → user, user → user
//   5. Update ALL business documents with $set: {org_id: orgID}
//   6. Create free Subscription for each new org
//   7. Validate: count documents without org_id (should be 0)
//
// Idempotent: safe to run multiple times. Skips users who already have an org.

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func main() {
	godotenv.Load()

	mongoURI := os.Getenv("MONGO_URI")
	dbName := os.Getenv("DB_NAME")
	if dbName == "" {
		dbName = "tron_legacy"
	}
	if mongoURI == "" {
		log.Fatal("MONGO_URI environment variable is required")
	}

	if err := database.Connect(mongoURI, dbName); err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer database.Disconnect()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	log.Println("=== Multi-Tenant Migration Started ===")

	// Step 1: Ensure indexes exist
	log.Println("[1/6] Ensuring indexes...")
	if err := database.EnsureIndexes(); err != nil {
		log.Printf("Warning: index creation had issues: %v", err)
	}

	// Step 2: Create organizations for users who don't have one yet
	log.Println("[2/6] Creating organizations for existing users...")
	orgMap := createOrganizations(ctx)

	// Step 3: Migrate platform roles
	log.Println("[3/6] Migrating platform roles...")
	migrateRoles(ctx)

	// Step 4: Update business documents with org_id
	log.Println("[4/6] Updating business documents with org_id...")
	updateBusinessDocs(ctx, orgMap)

	// Step 5: Create subscriptions for orgs without one
	log.Println("[5/6] Creating subscriptions...")
	createSubscriptions(ctx)

	// Step 6: Validate
	log.Println("[6/6] Validating migration...")
	validate(ctx)

	log.Println("=== Migration Complete ===")
}

// orgMap: userID → orgID
func createOrganizations(ctx context.Context) map[primitive.ObjectID]primitive.ObjectID {
	orgMap := make(map[primitive.ObjectID]primitive.ObjectID)

	// Get all profiles
	cursor, err := database.Profiles().Find(ctx, bson.M{})
	if err != nil {
		log.Fatalf("Failed to fetch profiles: %v", err)
	}
	defer cursor.Close(ctx)

	var profiles []models.Profile
	if err := cursor.All(ctx, &profiles); err != nil {
		log.Fatalf("Failed to decode profiles: %v", err)
	}

	for _, profile := range profiles {
		// Check if user already has a membership
		count, _ := database.OrgMemberships().CountDocuments(ctx, bson.M{"user_id": profile.UserID})
		if count > 0 {
			// Already has an org — get their first org
			var membership models.OrgMembership
			database.OrgMemberships().FindOne(ctx, bson.M{"user_id": profile.UserID}).Decode(&membership)
			orgMap[profile.UserID] = membership.OrgID
			continue
		}

		// Create org based on existing role
		var orgRole string
		switch profile.Role {
		case "superuser", "admin":
			orgRole = "owner"
		case "author":
			orgRole = "member"
		default:
			orgRole = "viewer"
		}

		// For now, create an individual org for each user
		// In production, you might want to group authors under their admin's org
		slug := generateMigrationSlug(profile.Name)
		slug = ensureMigrationUniqueSlug(ctx, slug)

		now := time.Now()
		org := models.Organization{
			ID:          primitive.NewObjectID(),
			Name:        profile.Name,
			Slug:        slug,
			OwnerUserID: profile.UserID,
			Settings: models.OrgSettings{
				DefaultLanguage: "pt-BR",
				DefaultCurrency: "BRL",
			},
			CreatedAt: now,
			UpdatedAt: now,
		}

		_, err := database.Organizations().InsertOne(ctx, org)
		if err != nil {
			log.Printf("  Warning: failed to create org for user %s: %v", profile.UserID.Hex(), err)
			continue
		}

		membership := models.OrgMembership{
			ID:       primitive.NewObjectID(),
			OrgID:    org.ID,
			UserID:   profile.UserID,
			OrgRole:  orgRole,
			JoinedAt: now,
		}
		database.OrgMemberships().InsertOne(ctx, membership)

		orgMap[profile.UserID] = org.ID
		log.Printf("  Created org '%s' (slug: %s) for user %s (role: %s → org_role: %s)",
			org.Name, org.Slug, profile.UserID.Hex(), profile.Role, orgRole)
	}

	log.Printf("  Total organizations: %d", len(orgMap))
	return orgMap
}

func migrateRoles(ctx context.Context) {
	// superuser → superadmin (platform level)
	result, _ := database.Profiles().UpdateMany(ctx,
		bson.M{"role": "superuser"},
		bson.M{"$set": bson.M{"role": "superadmin"}},
	)
	log.Printf("  superuser → superadmin: %d profiles", result.ModifiedCount)

	// admin → user (platform level; they're org owners)
	result, _ = database.Profiles().UpdateMany(ctx,
		bson.M{"role": "admin"},
		bson.M{"$set": bson.M{"role": "user"}},
	)
	log.Printf("  admin → user: %d profiles", result.ModifiedCount)

	// author → user (platform level; they're org members)
	result, _ = database.Profiles().UpdateMany(ctx,
		bson.M{"role": "author"},
		bson.M{"$set": bson.M{"role": "user"}},
	)
	log.Printf("  author → user: %d profiles", result.ModifiedCount)
}

func updateBusinessDocs(ctx context.Context, orgMap map[primitive.ObjectID]primitive.ObjectID) {
	type collectionInfo struct {
		name       string
		userField  string
		collection func() interface{ UpdateMany(context.Context, interface{}, interface{}) (interface{}, error) }
	}

	// Collections that use "user_id" field
	userIDCollections := []struct {
		name      string
		userField string
	}{
		{"instagram_schedules", "user_id"},
		{"instagram_configs", "user_id"},
		{"auto_reply_rules", "user_id"},
		{"auto_boost_rules", "user_id"},
		{"auto_boost_logs", "user_id"},
		{"integrated_publishes", "user_id"},
		{"meta_ads_campaigns", "user_id"},
		{"meta_ads_adsets", "user_id"},
		{"meta_ads_ads", "user_id"},
		{"meta_ads_targeting_presets", "user_id"},
		{"meta_ads_campaign_templates", "user_id"},
		{"meta_ads_budget_alerts", "user_id"},
	}

	// Collections that use "author_id"
	authorIDCollections := []struct {
		name      string
		userField string
	}{
		{"posts", "author_id"},
	}

	// Collections that use "uploader_id"
	uploaderIDCollections := []struct {
		name      string
		userField string
	}{
		{"images", "uploader_id"},
	}

	// Update all user_id collections
	for _, c := range userIDCollections {
		updateCollection(ctx, c.name, c.userField, orgMap)
	}

	// Update author_id collections
	for _, c := range authorIDCollections {
		updateCollection(ctx, c.name, c.userField, orgMap)
	}

	// Update uploader_id collections
	for _, c := range uploaderIDCollections {
		updateCollection(ctx, c.name, c.userField, orgMap)
	}

	// Auto-reply logs use rule_id, need special handling
	updateAutoReplyLogs(ctx, orgMap)

	// Instagram leads don't have user_id — skip or handle specially
	log.Printf("  instagram_leads: skipped (no user_id, will be org-scoped via webhook)")
}

func updateCollection(ctx context.Context, collName, userField string, orgMap map[primitive.ObjectID]primitive.ObjectID) {
	coll := database.DB.Collection(collName)

	totalUpdated := int64(0)
	for userID, orgID := range orgMap {
		result, err := coll.UpdateMany(ctx,
			bson.M{userField: userID, "org_id": bson.M{"$exists": false}},
			bson.M{"$set": bson.M{"org_id": orgID}},
		)
		if err != nil {
			log.Printf("  Warning: error updating %s for user %s: %v", collName, userID.Hex(), err)
			continue
		}
		totalUpdated += result.ModifiedCount
	}

	if totalUpdated > 0 {
		log.Printf("  %s: updated %d documents", collName, totalUpdated)
	}
}

func updateAutoReplyLogs(ctx context.Context, orgMap map[primitive.ObjectID]primitive.ObjectID) {
	// Auto-reply logs reference rules, not users directly.
	// We update them by matching rules to their user_id's org.
	coll := database.DB.Collection("auto_reply_logs")

	// Get all rules to build rule_id → org_id map
	rulesCursor, err := database.AutoReplyRules().Find(ctx, bson.M{})
	if err != nil {
		log.Printf("  Warning: failed to fetch auto_reply_rules: %v", err)
		return
	}
	defer rulesCursor.Close(ctx)

	var rules []models.AutoReplyRule
	rulesCursor.All(ctx, &rules)

	totalUpdated := int64(0)
	for _, rule := range rules {
		orgID, ok := orgMap[rule.UserID]
		if !ok {
			continue
		}
		result, _ := coll.UpdateMany(ctx,
			bson.M{"rule_id": rule.ID, "org_id": bson.M{"$exists": false}},
			bson.M{"$set": bson.M{"org_id": orgID}},
		)
		totalUpdated += result.ModifiedCount
	}

	if totalUpdated > 0 {
		log.Printf("  auto_reply_logs: updated %d documents", totalUpdated)
	}
}

func createSubscriptions(ctx context.Context) {
	// For each org without a subscription, create a free one
	cursor, err := database.Organizations().Find(ctx, bson.M{})
	if err != nil {
		log.Printf("Warning: failed to fetch organizations: %v", err)
		return
	}
	defer cursor.Close(ctx)

	var orgs []models.Organization
	cursor.All(ctx, &orgs)

	created := 0
	for _, org := range orgs {
		count, _ := database.Subscriptions().CountDocuments(ctx, bson.M{"org_id": org.ID})
		if count > 0 {
			continue
		}

		now := time.Now()
		sub := models.Subscription{
			ID:               primitive.NewObjectID(),
			OrgID:            org.ID,
			PlanID:           "free",
			Status:           "active",
			CurrentPeriodEnd: now.AddDate(100, 0, 0),
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		database.Subscriptions().InsertOne(ctx, sub)
		created++
	}

	log.Printf("  Created %d new subscriptions", created)
}

func validate(ctx context.Context) {
	collections := []string{
		"instagram_schedules", "instagram_configs",
		"auto_reply_rules", "auto_boost_rules", "auto_boost_logs",
		"integrated_publishes",
		"meta_ads_campaigns", "meta_ads_adsets", "meta_ads_ads",
		"meta_ads_targeting_presets", "meta_ads_campaign_templates", "meta_ads_budget_alerts",
		"posts", "images",
	}

	allClean := true
	for _, collName := range collections {
		coll := database.DB.Collection(collName)
		total, _ := coll.CountDocuments(ctx, bson.M{})
		if total == 0 {
			continue
		}
		missing, _ := coll.CountDocuments(ctx, bson.M{"org_id": bson.M{"$exists": false}})
		if missing > 0 {
			log.Printf("  WARNING: %s has %d/%d documents without org_id", collName, missing, total)
			allClean = false
		} else {
			log.Printf("  OK: %s (%d documents, all have org_id)", collName, total)
		}
	}

	// Check org counts
	orgCount, _ := database.Organizations().CountDocuments(ctx, bson.M{})
	memberCount, _ := database.OrgMemberships().CountDocuments(ctx, bson.M{})
	subCount, _ := database.Subscriptions().CountDocuments(ctx, bson.M{})
	log.Printf("  Organizations: %d, Memberships: %d, Subscriptions: %d", orgCount, memberCount, subCount)

	if allClean {
		log.Println("  VALIDATION PASSED: All business documents have org_id")
	} else {
		log.Println("  VALIDATION FAILED: Some documents are missing org_id")
	}
}

func generateMigrationSlug(name string) string {
	slug := strings.ToLower(name)
	replacer := strings.NewReplacer(
		"á", "a", "à", "a", "ã", "a", "â", "a",
		"é", "e", "ê", "e", "í", "i", "ó", "o",
		"ô", "o", "õ", "o", "ú", "u", "ç", "c",
	)
	slug = replacer.Replace(slug)

	var b strings.Builder
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	slug = b.String()

	// Collapse multiple dashes
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")

	if slug == "" {
		slug = "org"
	}
	if len(slug) > 50 {
		slug = slug[:50]
	}
	return slug
}

func ensureMigrationUniqueSlug(ctx context.Context, slug string) string {
	candidate := slug
	for i := 1; ; i++ {
		count, _ := database.Organizations().CountDocuments(ctx, bson.M{"slug": candidate})
		if count == 0 {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", slug, i)
	}
}
