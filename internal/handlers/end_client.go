package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ── EndClient CRUD (org-scoped, gated by contaazul:manage_clients) ──

// ListEndClients godoc
// @Summary Listar clientes-finais (EndClients) da org
// @Tags conta-azul
// @Produce json
// @Security BearerAuth
// @Success 200 {object} models.EndClientListResponse
// @Router /admin/conta-azul/clients [get]
func ListEndClients(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	if orgID == primitive.NilObjectID {
		http.Error(w, "Organization context required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cur, err := database.EndClients().Find(ctx, bson.M{"org_id": orgID}, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}))
	if err != nil {
		http.Error(w, "Failed to list clients", http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	items := []models.EndClientListItem{}
	for cur.Next(ctx) {
		var ec models.EndClient
		if err := cur.Decode(&ec); err != nil {
			continue
		}
		items = append(items, models.EndClientListItem{
			ID:              ec.ID,
			Name:            ec.Name,
			Email:           ec.Email,
			IsActive:        ec.IsActive,
			HasContaAzul:    ec.ContaAzulConn != nil && ec.ContaAzulConn.AccessTokenEnc != "",
			DashboardAccess: ec.DashboardAccess,
			PortalTheme:     ec.PortalTheme,
			LastLoginAt:     ec.LastLoginAt,
			CreatedAt:       ec.CreatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.EndClientListResponse{
		EndClients: items,
		Total:      int64(len(items)),
	})
}

// CreateEndClient godoc
// @Summary Cadastrar novo cliente-final
// @Tags conta-azul
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body models.CreateEndClientRequest true "Dados do cliente"
// @Success 201 {object} models.EndClientListItem
// @Router /admin/conta-azul/clients [post]
func CreateEndClient(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	if orgID == primitive.NilObjectID {
		http.Error(w, "Organization context required", http.StatusBadRequest)
		return
	}

	var req models.CreateEndClientRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Name == "" || req.Email == "" {
		http.Error(w, "Nome e email são obrigatórios", http.StatusBadRequest)
		return
	}
	if msg := models.ValidatePassword(req.Password); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	access := req.DashboardAccess
	if len(access) == 0 {
		access = append([]string{}, models.AllDashboardAccess...)
	} else {
		for _, k := range access {
			if !models.ValidDashboardAccess(k) {
				http.Error(w, "dashboard_access inválido: "+k, http.StatusBadRequest)
				return
			}
		}
	}

	hash, err := models.HashPassword(req.Password)
	if err != nil {
		http.Error(w, "Erro ao processar senha", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC()
	ec := models.EndClient{
		ID:              primitive.NewObjectID(),
		OrgID:           orgID,
		Name:            req.Name,
		Email:           req.Email,
		PasswordHash:    hash,
		Phone:           strings.TrimSpace(req.Phone),
		Document:        strings.TrimSpace(req.Document),
		DashboardAccess: access,
		IsActive:        true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := database.EndClients().InsertOne(ctx, ec); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			http.Error(w, "Já existe um cliente com este email nesta organização", http.StatusConflict)
			return
		}
		http.Error(w, "Erro ao salvar cliente", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(models.EndClientListItem{
		ID:              ec.ID,
		Name:            ec.Name,
		Email:           ec.Email,
		IsActive:        ec.IsActive,
		HasContaAzul:    false,
		DashboardAccess: ec.DashboardAccess,
		CreatedAt:       ec.CreatedAt,
	})
}

// ToggleEndClientActive flips the IsActive flag.
// PATCH /api/v1/admin/conta-azul/clients/{id}/active
func ToggleEndClientActive(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	if orgID == primitive.NilObjectID {
		http.Error(w, "Organization context required", http.StatusBadRequest)
		return
	}

	idStr := r.PathValue("id")
	id, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	var req struct {
		IsActive bool `json:"is_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := database.EndClients().UpdateOne(
		ctx,
		bson.M{"_id": id, "org_id": orgID},
		bson.M{"$set": bson.M{"is_active": req.IsActive, "updated_at": time.Now()}},
	)
	if err != nil {
		http.Error(w, "Erro ao atualizar cliente", http.StatusInternalServerError)
		return
	}
	if res.MatchedCount == 0 {
		http.Error(w, "Cliente não encontrado", http.StatusNotFound)
		return
	}

	// Revoke all active portal sessions when deactivating so the user is
	// kicked out as soon as their current access token expires (≤12h).
	if !req.IsActive {
		_, _ = database.EndClientRefreshTokens().DeleteMany(ctx, bson.M{"end_client_id": id})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"is_active": req.IsActive})
}

// SetEndClientPassword lets the org admin reset a client's portal password.
// PATCH /api/v1/admin/conta-azul/clients/{id}/password
func SetEndClientPassword(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	if orgID == primitive.NilObjectID {
		http.Error(w, "Organization context required", http.StatusBadRequest)
		return
	}

	idStr := r.PathValue("id")
	id, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if msg := models.ValidatePassword(req.Password); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	hash, err := models.HashPassword(req.Password)
	if err != nil {
		http.Error(w, "Erro ao processar senha", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := database.EndClients().UpdateOne(
		ctx,
		bson.M{"_id": id, "org_id": orgID},
		bson.M{"$set": bson.M{"password_hash": hash, "updated_at": time.Now()}},
	)
	if err != nil {
		http.Error(w, "Erro ao atualizar senha", http.StatusInternalServerError)
		return
	}
	if res.MatchedCount == 0 {
		http.Error(w, "Cliente não encontrado", http.StatusNotFound)
		return
	}

	// Revoke existing sessions so the client must log in again with the new password.
	_, _ = database.EndClientRefreshTokens().DeleteMany(ctx, bson.M{"end_client_id": id})

	w.WriteHeader(http.StatusNoContent)
}

// DeleteEndClient godoc
// @Summary Remover cliente-final
// @Tags conta-azul
// @Security BearerAuth
// @Param id path string true "EndClient ID"
// @Success 204
// @Router /admin/conta-azul/clients/{id} [delete]
func DeleteEndClient(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	if orgID == primitive.NilObjectID {
		http.Error(w, "Organization context required", http.StatusBadRequest)
		return
	}

	idStr := r.PathValue("id")
	id, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := database.EndClients().DeleteOne(ctx, bson.M{"_id": id, "org_id": orgID})
	if err != nil {
		http.Error(w, "Erro ao remover cliente", http.StatusInternalServerError)
		return
	}
	if res.DeletedCount == 0 {
		http.Error(w, "Cliente não encontrado", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
