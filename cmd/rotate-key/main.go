package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// collection + encrypted field pairs
var targets = []struct {
	Collection string
	Field      string
}{
	{"instagram_configs", "access_token_enc"},
	{"facebook_configs", "page_access_token_enc"},
	{"ai_configs", "api_key_enc"},
	{"meta_ads_configs", "access_token_enc"},
}

func main() {
	godotenv.Load()

	oldKeyHex := os.Getenv("OLD_ENCRYPTION_KEY")
	newKeyHex := os.Getenv("NEW_ENCRYPTION_KEY")
	mongoURI := os.Getenv("MONGO_URI")
	dbName := os.Getenv("DB_NAME")

	if oldKeyHex == "" || newKeyHex == "" {
		log.Fatal("Set OLD_ENCRYPTION_KEY and NEW_ENCRYPTION_KEY environment variables")
	}
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}
	if dbName == "" {
		dbName = "tron_legacy"
	}

	oldGCM, err := makeGCM(oldKeyHex)
	if err != nil {
		log.Fatalf("Invalid OLD_ENCRYPTION_KEY: %v", err)
	}
	newGCM, err := makeGCM(newKeyHex)
	if err != nil {
		log.Fatalf("Invalid NEW_ENCRYPTION_KEY: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		log.Fatalf("MongoDB connect error: %v", err)
	}
	defer client.Disconnect(ctx)

	db := client.Database(dbName)

	totalRotated := 0
	totalSkipped := 0
	totalErrors := 0

	for _, t := range targets {
		fmt.Printf("\n=== %s.%s ===\n", t.Collection, t.Field)

		coll := db.Collection(t.Collection)
		filter := bson.M{t.Field: bson.M{"$exists": true, "$ne": ""}}
		cursor, err := coll.Find(ctx, filter)
		if err != nil {
			log.Printf("  ERROR finding docs: %v", err)
			totalErrors++
			continue
		}

		for cursor.Next(ctx) {
			var doc bson.M
			if err := cursor.Decode(&doc); err != nil {
				log.Printf("  ERROR decoding doc: %v", err)
				totalErrors++
				continue
			}

			id := doc["_id"]
			encValue, ok := doc[t.Field].(string)
			if !ok || encValue == "" {
				totalSkipped++
				continue
			}

			// Decrypt with old key
			plaintext, err := decrypt(oldGCM, encValue)
			if err != nil {
				log.Printf("  ERROR decrypt id=%v: %v", id, err)
				totalErrors++
				continue
			}

			// Re-encrypt with new key
			newEnc, err := encrypt(newGCM, plaintext)
			if err != nil {
				log.Printf("  ERROR encrypt id=%v: %v", id, err)
				totalErrors++
				continue
			}

			// Update in DB
			_, err = coll.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{t.Field: newEnc}})
			if err != nil {
				log.Printf("  ERROR update id=%v: %v", id, err)
				totalErrors++
				continue
			}

			fmt.Printf("  rotated id=%v\n", id)
			totalRotated++
		}
		cursor.Close(ctx)
	}

	fmt.Printf("\n--- Done ---\n")
	fmt.Printf("Rotated: %d | Skipped: %d | Errors: %d\n", totalRotated, totalSkipped, totalErrors)

	if totalErrors > 0 {
		os.Exit(1)
	}
}

func makeGCM(keyHex string) (cipher.AEAD, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid hex: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 64 hex chars (32 bytes), got %d", len(key)*2)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func decrypt(gcm cipher.AEAD, encoded string) (string, error) {
	data, err := hex.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func encrypt(gcm cipher.AEAD, plaintext string) (string, error) {
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}
