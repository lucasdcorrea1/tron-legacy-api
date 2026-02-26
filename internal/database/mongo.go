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

	log.Println("Engagement indexes ensured")
	return nil
}
