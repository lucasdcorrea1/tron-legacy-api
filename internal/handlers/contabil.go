package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ── Contabil User Mapping Handlers ──────────────────────────────────

// ListContabilMappings lists all contabil user mappings for the current org.
// @Summary Listar mapeamentos contábeis
// @Description Lista todos os mapeamentos de usuários contábeis da organização
// @Tags contabil
// @Produce json
// @Security BearerAuth
// @Success 200 {object} models.ContabilMappingListResponse
// @Failure 401 {string} string "Unauthorized"
// @Failure 500 {string} string "Failed to list contabil mappings"
// @Router /admin/contabil/mappings [get]
func ListContabilMappings(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	cursor, err := database.ContabilUserMappings().Find(ctx, bson.M{"org_id": orgID})
	if err != nil {
		http.Error(w, `{"message":"Failed to list contabil mappings"}`, http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var mappings []models.ContabilUserMapping
	if err := cursor.All(ctx, &mappings); err != nil {
		http.Error(w, `{"message":"Failed to decode contabil mappings"}`, http.StatusInternalServerError)
		return
	}

	// Enrich with user info
	var response []models.ContabilMappingResponse
	for _, m := range mappings {
		resp := models.ContabilMappingResponse{ContabilUserMapping: m}

		var profile models.Profile
		if err := database.Profiles().FindOne(ctx, bson.M{"user_id": m.TronUserID}).Decode(&profile); err == nil {
			resp.UserName = profile.Name
		}

		var user models.User
		if err := database.Users().FindOne(ctx, bson.M{"_id": m.TronUserID}).Decode(&user); err == nil {
			resp.UserEmail = user.Email
		}

		response = append(response, resp)
	}

	if response == nil {
		response = []models.ContabilMappingResponse{}
	}

	json.NewEncoder(w).Encode(response)
}

// CreateContabilMapping creates a new contabil user mapping.
// @Summary Criar mapeamento contábil
// @Description Cria um novo mapeamento de usuário contábil
// @Tags contabil
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body models.CreateContabilMappingRequest true "Dados do mapeamento"
// @Success 201 {object} models.ContabilUserMapping
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "User not found"
// @Failure 409 {string} string "Mapping already exists"
// @Failure 500 {string} string "Failed to create contabil mapping"
// @Router /admin/contabil/mappings [post]
func CreateContabilMapping(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	var req models.CreateContabilMappingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"Invalid request body"}`, http.StatusBadRequest)
		return
	}

	if !models.ValidContabilRole(req.ContabilRole) {
		http.Error(w, `{"message":"Invalid contabil role. Must be ADMIN, OPERATOR, or VIEWER"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Resolve user: by ID or by email
	var user models.User
	if req.TronUserID != "" {
		oid, err := primitive.ObjectIDFromHex(req.TronUserID)
		if err != nil {
			http.Error(w, `{"message":"Invalid tronUserId"}`, http.StatusBadRequest)
			return
		}
		if err := database.Users().FindOne(ctx, bson.M{"_id": oid}).Decode(&user); err != nil {
			http.Error(w, `{"message":"User not found"}`, http.StatusNotFound)
			return
		}
	} else if req.Email != "" {
		if err := database.Users().FindOne(ctx, bson.M{"email": req.Email}).Decode(&user); err != nil {
			http.Error(w, `{"message":"User not found with this email"}`, http.StatusNotFound)
			return
		}
	} else {
		http.Error(w, `{"message":"Either tronUserId or email is required"}`, http.StatusBadRequest)
		return
	}

	tronUserID := user.ID

	// Check if user is a member of the org
	var membership models.OrgMembership
	err := database.OrgMemberships().FindOne(ctx, bson.M{
		"org_id":  orgID,
		"user_id": tronUserID,
	}).Decode(&membership)
	if err != nil {
		http.Error(w, `{"message":"User is not a member of this organization"}`, http.StatusBadRequest)
		return
	}

	// Check if mapping already exists
	count, _ := database.ContabilUserMappings().CountDocuments(ctx, bson.M{
		"tron_user_id": tronUserID,
		"org_id":       orgID,
	})
	if count > 0 {
		http.Error(w, `{"message":"Mapping already exists for this user"}`, http.StatusConflict)
		return
	}

	now := time.Now()
	mapping := models.ContabilUserMapping{
		TronUserID:   tronUserID,
		TronEmail:    user.Email,
		ContabilRole: req.ContabilRole,
		OrgID:        orgID,
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	result, err := database.ContabilUserMappings().InsertOne(ctx, mapping)
	if err != nil {
		http.Error(w, `{"message":"Failed to create contabil mapping"}`, http.StatusInternalServerError)
		return
	}

	mapping.ID = result.InsertedID.(primitive.ObjectID)
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(mapping)
}

// UpdateContabilMapping updates a contabil user mapping.
// @Summary Atualizar mapeamento contábil
// @Description Atualiza um mapeamento de usuário contábil existente
// @Tags contabil
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Mapping ID"
// @Param body body models.UpdateContabilMappingRequest true "Dados para atualização"
// @Success 200 {object} map[string]string
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Mapping not found"
// @Router /admin/contabil/mappings/{id} [put]
func UpdateContabilMapping(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	mappingID, err := primitive.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		http.Error(w, `{"message":"Invalid mapping ID"}`, http.StatusBadRequest)
		return
	}

	var req models.UpdateContabilMappingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"Invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.ContabilRole != "" && !models.ValidContabilRole(req.ContabilRole) {
		http.Error(w, `{"message":"Invalid contabil role"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	update := bson.M{"updated_at": time.Now()}
	if req.ContabilRole != "" {
		update["contabil_role"] = req.ContabilRole
	}
	if req.IsActive != nil {
		update["is_active"] = *req.IsActive
	}

	result, err := database.ContabilUserMappings().UpdateOne(ctx,
		bson.M{"_id": mappingID, "org_id": orgID},
		bson.M{"$set": update},
	)
	if err != nil || result.MatchedCount == 0 {
		http.Error(w, `{"message":"Mapping not found"}`, http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"message": "Mapping updated"})
}

// DeleteContabilMapping removes a contabil user mapping.
// @Summary Remover mapeamento contábil
// @Description Remove um mapeamento de usuário contábil
// @Tags contabil
// @Produce json
// @Security BearerAuth
// @Param id path string true "Mapping ID"
// @Success 200 {object} map[string]string
// @Failure 400 {string} string "Invalid mapping ID"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Mapping not found"
// @Router /admin/contabil/mappings/{id} [delete]
func DeleteContabilMapping(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	mappingID, err := primitive.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		http.Error(w, `{"message":"Invalid mapping ID"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	result, err := database.ContabilUserMappings().DeleteOne(ctx, bson.M{
		"_id":    mappingID,
		"org_id": orgID,
	})
	if err != nil || result.DeletedCount == 0 {
		http.Error(w, `{"message":"Mapping not found"}`, http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"message": "Mapping deleted"})
}

// ── Contabil Proxy ──────────────────────────────────────────────────

// ContabilProxy creates a reverse proxy handler that forwards requests
// to the contabil API, preserving auth headers.
func ContabilProxy(contabilBaseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Build target URL: strip the /api/v1/admin/contabil prefix and forward to contabil API
		originalPath := r.URL.Path
		contabilPath := strings.TrimPrefix(originalPath, "/api/v1/admin/contabil")
		if contabilPath == "" {
			contabilPath = "/"
		}

		targetURL, err := url.Parse(contabilBaseURL + "/api/v1/contabil" + contabilPath)
		if err != nil {
			slog.Error("contabil_proxy_url_parse", "error", err.Error())
			http.Error(w, `{"message":"Internal proxy error"}`, http.StatusInternalServerError)
			return
		}

		// Preserve query parameters
		targetURL.RawQuery = r.URL.RawQuery

		// Create proxy request
		proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL.String(), r.Body)
		if err != nil {
			slog.Error("contabil_proxy_request", "error", err.Error())
			http.Error(w, `{"message":"Failed to create proxy request"}`, http.StatusInternalServerError)
			return
		}

		// Copy headers from original request
		for key, values := range r.Header {
			for _, value := range values {
				proxyReq.Header.Add(key, value)
			}
		}

		// Add tron context headers for the contabil API
		userID := middleware.GetUserID(r)
		orgID := middleware.GetOrgID(r)
		orgRole := middleware.GetOrgRole(r)

		proxyReq.Header.Set("X-Tron-User-ID", userID.Hex())
		proxyReq.Header.Set("X-Tron-Org-ID", orgID.Hex())
		proxyReq.Header.Set("X-Tron-Org-Role", orgRole)

		// Forward request
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(proxyReq)
		if err != nil {
			slog.Error("contabil_proxy_forward", "error", err.Error(), "target", targetURL.String())
			http.Error(w, `{"message":"Contabil service unavailable"}`, http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// Copy response headers (skip CORS — handled by our middleware)
		for key, values := range resp.Header {
			if strings.HasPrefix(strings.ToLower(key), "access-control-") {
				continue
			}
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}

		// Copy status code and body
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}
