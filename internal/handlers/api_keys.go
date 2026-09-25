package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// APIKeyScopeInstagram lets a key upload images and manage Instagram schedules.
const APIKeyScopeInstagram = "instagram"

const maxActiveAPIKeys = 10

// ListAPIKeys lists the org's API keys (never the key itself).
// @Summary Listar chaves de API
// @Tags api-keys
// @Produce json
// @Security BearerAuth
// @Success 200 {array} models.APIKey
// @Router /admin/api-keys [get]
func ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	cursor, err := database.APIKeys().Find(ctx,
		bson.M{"org_id": orgID, "revoked_at": bson.M{"$exists": false}},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}),
	)
	if err != nil {
		http.Error(w, "Error listing API keys", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	keys := []models.APIKey{}
	if err := cursor.All(ctx, &keys); err != nil {
		http.Error(w, "Error listing API keys", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(keys)
}

// CreateAPIKey creates a key with the instagram scope. The plain key is
// returned only in this response.
// @Summary Criar chave de API
// @Tags api-keys
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body models.CreateAPIKeyRequest true "Nome da chave"
// @Success 201 {object} models.CreateAPIKeyResponse
// @Router /admin/api-keys [post]
func CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	orgID := middleware.GetOrgID(r)

	var req models.CreateAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 60 {
		http.Error(w, "name is required (max 60 characters)", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	active, err := database.APIKeys().CountDocuments(ctx, bson.M{"org_id": orgID, "revoked_at": bson.M{"$exists": false}})
	if err != nil {
		http.Error(w, "Error creating API key", http.StatusInternalServerError)
		return
	}
	if active >= maxActiveAPIKeys {
		http.Error(w, "Too many active API keys; revoke one first", http.StatusConflict)
		return
	}

	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		http.Error(w, "Error creating API key", http.StatusInternalServerError)
		return
	}
	key := middleware.APIKeyPrefix + hex.EncodeToString(raw)

	doc := models.APIKey{
		ID:        primitive.NewObjectID(),
		OrgID:     orgID,
		UserID:    userID,
		Name:      name,
		Prefix:    key[:len(middleware.APIKeyPrefix)+6],
		KeyHash:   middleware.HashAPIKey(key),
		Scopes:    []string{APIKeyScopeInstagram},
		CreatedAt: time.Now(),
	}
	if _, err := database.APIKeys().InsertOne(ctx, doc); err != nil {
		http.Error(w, "Error creating API key", http.StatusInternalServerError)
		return
	}

	slog.Info("api_key_created", "key_id", doc.ID.Hex(), "org_id", orgID.Hex(), "user_id", userID.Hex())

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(models.CreateAPIKeyResponse{APIKey: doc, Key: key})
}

// RevokeAPIKey revokes a key; requests using it fail right away.
// @Summary Revogar chave de API
// @Tags api-keys
// @Security BearerAuth
// @Param id path string true "ID da chave"
// @Success 204
// @Router /admin/api-keys/{id} [delete]
func RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	id, err := primitive.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	res, err := database.APIKeys().UpdateOne(ctx,
		bson.M{"_id": id, "org_id": orgID, "revoked_at": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"revoked_at": time.Now()}},
	)
	if err != nil {
		http.Error(w, "Error revoking API key", http.StatusInternalServerError)
		return
	}
	if res.MatchedCount == 0 {
		http.Error(w, "API key not found", http.StatusNotFound)
		return
	}

	slog.Info("api_key_revoked", "key_id", id.Hex(), "org_id", orgID.Hex())
	w.WriteHeader(http.StatusNoContent)
}
