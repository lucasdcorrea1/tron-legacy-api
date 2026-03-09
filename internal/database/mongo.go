package database

import (
	"context"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var Client *mongo.Client
var DB *mongo.Database

func Connect(uri, dbName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clientOptions := options.Client().ApplyURI(uri)
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return err
	}

	// Ping the database
	if err := client.Ping(ctx, nil); err != nil {
		return err
	}

	Client = client
	DB = client.Database(dbName)

	log.Printf("Connected to MongoDB: %s", dbName)
	return nil
}

func Disconnect() {
	if Client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		Client.Disconnect(ctx)
	}
}

// Collections
func Users() *mongo.Collection {
	return DB.Collection("users")
}

func Profiles() *mongo.Collection {
	return DB.Collection("profiles")
}

func Posts() *mongo.Collection {
	return DB.Collection("posts")
}

func Images() *mongo.Collection {
	return DB.Collection("images")
}

func PostViews() *mongo.Collection {
	return DB.Collection("post_views")
}

func PostLikes() *mongo.Collection {
	return DB.Collection("post_likes")
}

func PostComments() *mongo.Collection {
	return DB.Collection("post_comments")
}

func PasswordResets() *mongo.Collection {
	return DB.Collection("password_resets")
}

func InstagramSchedules() *mongo.Collection {
	return DB.Collection("instagram_schedules")
}

func InstagramConfigs() *mongo.Collection {
	return DB.Collection("instagram_configs")
}

func RefreshTokens() *mongo.Collection {
	return DB.Collection("refresh_tokens")
}

func AutoReplyRules() *mongo.Collection {
	return DB.Collection("auto_reply_rules")
}

func AutoReplyLogs() *mongo.Collection {
	return DB.Collection("auto_reply_logs")
}

func InstagramLeads() *mongo.Collection {
	return DB.Collection("instagram_leads")
}

func CTAClicks() *mongo.Collection {
	return DB.Collection("cta_clicks")
}

func MetaAdsConfigs() *mongo.Collection {
	return DB.Collection("meta_ads_configs")
}

func MetaAdsCampaigns() *mongo.Collection {
	return DB.Collection("meta_ads_campaigns")
}

func MetaAdsAdSets() *mongo.Collection {
	return DB.Collection("meta_ads_adsets")
}

func MetaAdsAds() *mongo.Collection {
	return DB.Collection("meta_ads_ads")
}

func MetaAdsTargetingPresets() *mongo.Collection {
	return DB.Collection("meta_ads_targeting_presets")
}

func MetaAdsCampaignTemplates() *mongo.Collection {
	return DB.Collection("meta_ads_campaign_templates")
}

func MetaAdsBudgetAlerts() *mongo.Collection {
	return DB.Collection("meta_ads_budget_alerts")
}

func AutoBoostRules() *mongo.Collection {
	return DB.Collection("auto_boost_rules")
}

func AutoBoostLogs() *mongo.Collection {
	return DB.Collection("auto_boost_logs")
}

func IntegratedPublishes() *mongo.Collection {
	return DB.Collection("integrated_publishes")
}

func AIConfigs() *mongo.Collection {
	return DB.Collection("ai_configs")
}

// ── Multi-tenant collections ─────────────────────────────────────────

func Organizations() *mongo.Collection {
	return DB.Collection("organizations")
}

func OrgMemberships() *mongo.Collection {
	return DB.Collection("org_memberships")
}

func OrgInvitations() *mongo.Collection {
	return DB.Collection("org_invitations")
}

func Subscriptions() *mongo.Collection {
	return DB.Collection("subscriptions")
}

// EnsureIndexes creates required indexes for engagement collections
func EnsureIndexes() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// post_views: unique index on {post_id, user_id} for dedup
	_, err := PostViews().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "post_id", Value: 1}, {Key: "user_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return err
	}

	// post_likes: unique index on {post_id, user_id} to prevent duplicate likes
	_, err = PostLikes().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "post_id", Value: 1}, {Key: "user_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return err
	}

	// post_comments: index on {post_id, created_at} for fast listing
	_, err = PostComments().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "post_id", Value: 1}, {Key: "created_at", Value: -1}},
	})
	if err != nil {
		return err
	}

	// images: compound index on {group_id, size_label} for multi-size image lookup
	_, err = Images().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "group_id", Value: 1}, {Key: "size_label", Value: 1}},
	})
	if err != nil {
		return err
	}

	// password_resets: TTL index to auto-delete expired tokens after 2 hours
	_, err = PasswordResets().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expires_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(7200),
	})
	if err != nil {
		return err
	}

	// password_resets: index on token for fast lookup
	_, err = PasswordResets().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "token", Value: 1}},
	})
	if err != nil {
		return err
	}

	// instagram_schedules: compound index on {status, scheduled_at} for scheduler queries
	_, err = InstagramSchedules().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "status", Value: 1}, {Key: "scheduled_at", Value: 1}},
	})
	if err != nil {
		return err
	}

	// instagram_schedules: index on {org_id, created_at} for org listing
	_, err = InstagramSchedules().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "org_id", Value: 1}, {Key: "created_at", Value: -1}},
	})
	if err != nil {
		return err
	}

	// refresh_tokens: TTL index on expires_at (auto-delete expired tokens)
	_, err = RefreshTokens().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expires_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	})
	if err != nil {
		return err
	}

	// refresh_tokens: index on token_hash for fast lookup
	_, err = RefreshTokens().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "token_hash", Value: 1}},
	})
	if err != nil {
		return err
	}

	// refresh_tokens: index on user_id for cleanup on logout/password reset
	_, err = RefreshTokens().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "user_id", Value: 1}},
	})
	if err != nil {
		return err
	}

	// instagram_configs: unique index on user_id (one config per user)
	_, err = InstagramConfigs().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "user_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return err
	}

	// auto_reply_rules: compound index on {org_id, active} for active rules lookup
	_, err = AutoReplyRules().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "org_id", Value: 1}, {Key: "active", Value: 1}},
	})
	if err != nil {
		return err
	}

	// auto_reply_logs: compound index on {sender_ig_id, created_at} for cooldown checks
	_, err = AutoReplyLogs().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "sender_ig_id", Value: 1}, {Key: "created_at", Value: -1}},
	})
	if err != nil {
		return err
	}

	// auto_reply_logs: TTL index — auto-delete logs after 90 days
	_, err = AutoReplyLogs().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "created_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(90 * 24 * 3600),
	})
	if err != nil {
		return err
	}

	// instagram_leads: unique index on sender_ig_id
	_, err = InstagramLeads().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "sender_ig_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return err
	}

	// instagram_leads: index on last_interaction for sorting
	_, err = InstagramLeads().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "last_interaction", Value: -1}},
	})
	if err != nil {
		return err
	}

	// instagram_leads: index on tags for filtering
	_, err = InstagramLeads().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "tags", Value: 1}},
	})
	if err != nil {
		return err
	}

	// cta_clicks: index on {post_id, cta, created_at} for stats queries
	_, err = CTAClicks().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "post_id", Value: 1}, {Key: "cta", Value: 1}, {Key: "created_at", Value: -1}},
	})
	if err != nil {
		return err
	}

	// cta_clicks: TTL index — auto-delete after 180 days
	_, err = CTAClicks().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "created_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(180 * 24 * 3600),
	})
	if err != nil {
		return err
	}

	// meta_ads_configs: unique index on user_id (one config per user)
	_, err = MetaAdsConfigs().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "user_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return err
	}

	// meta_ads_campaigns: index on org_id for listing
	_, err = MetaAdsCampaigns().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "org_id", Value: 1}, {Key: "created_at", Value: -1}},
	})
	if err != nil {
		return err
	}

	// meta_ads_adsets: index on {org_id, campaign_id}
	_, err = MetaAdsAdSets().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "org_id", Value: 1}, {Key: "campaign_id", Value: 1}},
	})
	if err != nil {
		return err
	}

	// meta_ads_ads: index on {org_id, adset_id}
	_, err = MetaAdsAds().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "org_id", Value: 1}, {Key: "adset_id", Value: 1}},
	})
	if err != nil {
		return err
	}

	// meta_ads_targeting_presets: index on org_id
	_, err = MetaAdsTargetingPresets().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "org_id", Value: 1}},
	})
	if err != nil {
		return err
	}

	// meta_ads_campaign_templates: index on org_id
	_, err = MetaAdsCampaignTemplates().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "org_id", Value: 1}},
	})
	if err != nil {
		return err
	}

	// meta_ads_budget_alerts: index on {org_id, active}
	_, err = MetaAdsBudgetAlerts().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "org_id", Value: 1}, {Key: "active", Value: 1}},
	})
	if err != nil {
		return err
	}

	// auto_boost_rules: compound index on {org_id, active} for active rules lookup
	_, err = AutoBoostRules().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "org_id", Value: 1}, {Key: "active", Value: 1}},
	})
	if err != nil {
		return err
	}

	// auto_boost_logs: compound index on {rule_id, ig_media_id} for cooldown check
	_, err = AutoBoostLogs().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "rule_id", Value: 1}, {Key: "ig_media_id", Value: 1}},
	})
	if err != nil {
		return err
	}

	// auto_boost_logs: index on {org_id, created_at} for listing history
	_, err = AutoBoostLogs().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "org_id", Value: 1}, {Key: "created_at", Value: -1}},
	})
	if err != nil {
		return err
	}

	// auto_boost_logs: TTL index — auto-delete logs after 180 days
	_, err = AutoBoostLogs().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "created_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(180 * 24 * 3600),
	})
	if err != nil {
		return err
	}

	// integrated_publishes: compound index on {status, scheduled_at} for scheduler queries
	_, err = IntegratedPublishes().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "status", Value: 1}, {Key: "scheduled_at", Value: 1}},
	})
	if err != nil {
		return err
	}

	// integrated_publishes: index on {org_id, created_at} for org listing
	_, err = IntegratedPublishes().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "org_id", Value: 1}, {Key: "created_at", Value: -1}},
	})
	if err != nil {
		return err
	}

	// ai_configs: unique index on org_id (one config per org)
	_, err = AIConfigs().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "org_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return err
	}

	// ── Multi-tenant indexes ─────────────────────────────────────────

	// organizations: unique index on slug
	_, err = Organizations().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "slug", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return err
	}

	// organizations: index on owner_user_id
	_, err = Organizations().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "owner_user_id", Value: 1}},
	})
	if err != nil {
		return err
	}

	// org_memberships: unique index on {org_id, user_id}
	_, err = OrgMemberships().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "org_id", Value: 1}, {Key: "user_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return err
	}

	// org_memberships: index on user_id for listing user's orgs
	_, err = OrgMemberships().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "user_id", Value: 1}},
	})
	if err != nil {
		return err
	}

	// org_invitations: index on token for fast lookup
	_, err = OrgInvitations().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "token", Value: 1}},
	})
	if err != nil {
		return err
	}

	// org_invitations: TTL index — auto-delete expired invitations
	_, err = OrgInvitations().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expires_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	})
	if err != nil {
		return err
	}

	// org_invitations: index on {org_id, email} for duplicate check
	_, err = OrgInvitations().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "org_id", Value: 1}, {Key: "email", Value: 1}},
	})
	if err != nil {
		return err
	}

	// subscriptions: unique index on org_id (one subscription per org)
	_, err = Subscriptions().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "org_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return err
	}

	log.Println("Engagement indexes ensured")
	return nil
}
