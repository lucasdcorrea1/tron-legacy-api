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

	// instagram_schedules: index on {user_id, created_at} for user listing
	_, err = InstagramSchedules().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}},
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

	// auto_reply_rules: compound index on {user_id, active} for active rules lookup
	_, err = AutoReplyRules().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "active", Value: 1}},
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

	log.Println("Engagement indexes ensured")
	return nil
}
