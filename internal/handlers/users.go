package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ListUsers godoc
// @Summary Listar usuários
// @Description Retorna lista paginada de todos os usuários com perfil. Requer role admin.
// @Tags users
// @Produce json
// @Security BearerAuth
// @Param page query int false "Página" default(1)
// @Param limit query int false "Itens por página" default(20)
// @Param search query string false "Buscar por nome ou email"
// @Param role query string false "Filtrar por role (admin, author, user)"
// @Success 200 {object} models.UserListResponse
// @Failure 401 {string} string "Unauthorized"
// @Failure 403 {string} string "Forbidden"
// @Router /users [get]
func ListUsers(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Build profile filter
	filter := bson.M{}

	if role := r.URL.Query().Get("role"); role != "" {
		filter["role"] = role
	}

	if search := r.URL.Query().Get("search"); search != "" {
		filter["$or"] = []bson.M{
			{"name": bson.M{"$regex": search, "$options": "i"}},
		}
	}

	// Count total
	total, err := database.Profiles().CountDocuments(ctx, filter)
	if err != nil {
		http.Error(w, "Error counting users", http.StatusInternalServerError)
		return
	}

	// Fetch profiles paginated
	skip := int64((page - 1) * limit)
	opts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}}).
		SetSkip(skip).
		SetLimit(int64(limit))

	cursor, err := database.Profiles().Find(ctx, filter, opts)
	if err != nil {
		http.Error(w, "Error fetching users", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var profiles []models.Profile
	if err := cursor.All(ctx, &profiles); err != nil {
		http.Error(w, "Error decoding users", http.StatusInternalServerError)
		return
	}

	// Collect user IDs to fetch emails
	userIDs := make([]primitive.ObjectID, len(profiles))
	for i, p := range profiles {
		userIDs[i] = p.UserID
	}

	// Fetch users for emails
	emailMap := make(map[primitive.ObjectID]string)
	if len(userIDs) > 0 {
		userCursor, err := database.Users().Find(ctx, bson.M{"_id": bson.M{"$in": userIDs}})
		if err == nil {
			defer userCursor.Close(ctx)
			var users []models.User
			if userCursor.All(ctx, &users) == nil {
				for _, u := range users {
					emailMap[u.ID] = u.Email
				}
			}
		}
	}

	// Build response
	items := make([]models.UserListItem, len(profiles))
	for i, p := range profiles {
		items[i] = models.UserListItem{
			ID:        p.UserID,
			Email:     emailMap[p.UserID],
			Name:      p.Name,
			Avatar:    p.Avatar,
			Role:      p.Role,
			CreatedAt: p.CreatedAt,
		}
	}

	response := models.UserListResponse{
		Users: items,
		Total: total,
		Page:  page,
		Limit: limit,
	}

	json.NewEncoder(w).Encode(response)
}

// UpdateUserRole godoc
// @Summary Alterar role de um usuário
// @Description Altera a role de um usuário. Requer role admin.
// @Tags users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "User ID"
// @Param request body models.UpdateUserRoleRequest true "Nova role"
// @Success 200 {object} models.UserListItem
// @Failure 400 {string} string "Invalid request"
// @Failure 401 {string} string "Unauthorized"
// @Failure 403 {string} string "Forbidden"
// @Failure 404 {string} string "User not found"
// @Router /users/{id}/role [put]
func UpdateUserRole(w http.ResponseWriter, r *http.Request) {
	adminID := middleware.GetUserID(r)
	if adminID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	targetIDStr := r.PathValue("id")
	targetID, err := primitive.ObjectIDFromHex(targetIDStr)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	var req models.UpdateUserRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Role != "admin" && req.Role != "author" && req.Role != "user" {
		http.Error(w, "Role must be 'admin', 'author' or 'user'", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := database.Profiles().UpdateOne(
		ctx,
		bson.M{"user_id": targetID},
		bson.M{"$set": bson.M{"role": req.Role, "updated_at": time.Now()}},
	)
	if err != nil {
		http.Error(w, "Error updating role", http.StatusInternalServerError)
		return
	}
	if result.MatchedCount == 0 {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	// Fetch updated profile + email
	var profile models.Profile
	database.Profiles().FindOne(ctx, bson.M{"user_id": targetID}).Decode(&profile)

	var user models.User
	database.Users().FindOne(ctx, bson.M{"_id": targetID}).Decode(&user)

	slog.Info("user_role_updated",
		"target_user_id", targetID.Hex(),
		"new_role", req.Role,
		"admin_id", adminID.Hex(),
	)

	item := models.UserListItem{
		ID:        profile.UserID,
		Email:     user.Email,
		Name:      profile.Name,
		Avatar:    profile.Avatar,
		Role:      profile.Role,
		CreatedAt: profile.CreatedAt,
	}

	json.NewEncoder(w).Encode(item)
}
