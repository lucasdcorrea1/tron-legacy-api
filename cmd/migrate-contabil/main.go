package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ContabilUser represents a user from the whodo_contabil database.
type ContabilUser struct {
	ID             string `bson:"_id"`
	OrganizationID string `bson:"organization_id"`
	Email          string `bson:"email"`
	Name           string `bson:"name"`
	Role           string `bson:"role"`
	IsActive       bool   `bson:"is_active"`
}

// TronUser represents a user from the tron_legacy database.
type TronUser struct {
	ID    primitive.ObjectID `bson:"_id"`
	Email string             `bson:"email"`
}

// ContabilUserMapping is the mapping record inserted into tron_legacy.
type ContabilUserMapping struct {
	TronUserID   primitive.ObjectID `bson:"tron_user_id"`
	TronEmail    string             `bson:"tron_email"`
	ContabilRole string             `bson:"contabil_role"`
	OrgID        primitive.ObjectID `bson:"org_id"`
	ContabilOrgID string            `bson:"contabil_org_id"`
	IsActive     bool               `bson:"is_active"`
	CreatedAt    time.Time          `bson:"created_at"`
	UpdatedAt    time.Time          `bson:"updated_at"`
}

func main() {
	godotenv.Load()

	mongoURI := getEnv("MONGO_URI", "mongodb://localhost:27017")
	tronDB := getEnv("TRON_DB_NAME", "tron_legacy")
	contabilDB := getEnv("CONTABIL_DB_NAME", "whodo_contabil")
	targetOrgID := getEnv("TARGET_ORG_ID", "") // tron org ID to link contabil users to

	if targetOrgID == "" {
		log.Fatal("TARGET_ORG_ID is required. Set it to the tron-legacy organization ID to map contabil users to.")
	}

	orgID, err := primitive.ObjectIDFromHex(targetOrgID)
	if err != nil {
		log.Fatalf("Invalid TARGET_ORG_ID: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer client.Disconnect(ctx)

	tron := client.Database(tronDB)
	contabil := client.Database(contabilDB)

	log.Printf("Connected to MongoDB")
	log.Printf("Tron DB: %s", tronDB)
	log.Printf("Contabil DB: %s", contabilDB)
	log.Printf("Target Org ID: %s", targetOrgID)

	// 1. Get all contabil users
	cursor, err := contabil.Collection("users").Find(ctx, bson.M{})
	if err != nil {
		log.Fatalf("Failed to query contabil users: %v", err)
	}
	defer cursor.Close(ctx)

	var contabilUsers []ContabilUser
	if err := cursor.All(ctx, &contabilUsers); err != nil {
		log.Fatalf("Failed to decode contabil users: %v", err)
	}

	log.Printf("Found %d contabil users to migrate", len(contabilUsers))

	created := 0
	skipped := 0
	notFound := 0

	for _, cu := range contabilUsers {
		// 2. Try to find matching user in tron by email
		var tronUser TronUser
		err := tron.Collection("users").FindOne(ctx, bson.M{"email": cu.Email}).Decode(&tronUser)

		if err == mongo.ErrNoDocuments {
			log.Printf("  SKIP: No tron user found for email %s (contabil user %s)", cu.Email, cu.ID)
			notFound++
			continue
		}
		if err != nil {
			log.Printf("  ERROR: Failed to query tron user for %s: %v", cu.Email, err)
			continue
		}

		// 3. Check if mapping already exists
		count, _ := tron.Collection("contabil_user_mappings").CountDocuments(ctx, bson.M{
			"tron_user_id": tronUser.ID,
			"org_id":       orgID,
		})
		if count > 0 {
			log.Printf("  SKIP: Mapping already exists for %s", cu.Email)
			skipped++
			continue
		}

		// 4. Create mapping
		now := time.Now()
		mapping := ContabilUserMapping{
			TronUserID:    tronUser.ID,
			TronEmail:     cu.Email,
			ContabilRole:  cu.Role,
			OrgID:         orgID,
			ContabilOrgID: cu.OrganizationID,
			IsActive:      cu.IsActive,
			CreatedAt:     now,
			UpdatedAt:     now,
		}

		_, err = tron.Collection("contabil_user_mappings").InsertOne(ctx, mapping)
		if err != nil {
			log.Printf("  ERROR: Failed to create mapping for %s: %v", cu.Email, err)
			continue
		}

		log.Printf("  OK: Created mapping for %s (tron=%s, role=%s)", cu.Email, tronUser.ID.Hex(), cu.Role)
		created++
	}

	fmt.Println("\n========== Migration Summary ==========")
	fmt.Printf("Total contabil users:  %d\n", len(contabilUsers))
	fmt.Printf("Mappings created:      %d\n", created)
	fmt.Printf("Already existed:       %d\n", skipped)
	fmt.Printf("No tron user found:    %d\n", notFound)
	fmt.Println("========================================")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
