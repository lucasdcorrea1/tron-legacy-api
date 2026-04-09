package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// RegisterAndSubscribeRequest is the combined signup + plan selection request.
// Payment is handled separately via the checkout endpoint after registration.
type RegisterAndSubscribeRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	CpfCnpj  string `json:"cpf_cnpj"`
	PlanID   string `json:"plan_id"`       // kept for frontend compatibility, but ignored (starts free)
}

// RegisterAndSubscribeResponse returns auth tokens after registration.
type RegisterAndSubscribeResponse struct {
	User         models.UserResponse `json:"user"`
	Profile      models.Profile      `json:"profile"`
	Token        string              `json:"token"`
	RefreshToken string              `json:"refresh_token,omitempty"`
	PlanID       string              `json:"plan_id"`
}

// RegisterAndSubscribe godoc
// @Summary Registrar usuario com organizacao
// @Description Cria conta, perfil e organizacao com plano free. Pagamento feito separadamente via checkout.
// @Tags auth
// @Accept json
// @Produce json
// @Param request body RegisterAndSubscribeRequest true "Dados de registro"
// @Success 201 {object} RegisterAndSubscribeResponse
// @Failure 400 {string} string "Dados inválidos"
// @Failure 409 {string} string "Email already exists"
// @Router /auth/register-and-subscribe [post]
func RegisterAndSubscribe(w http.ResponseWriter, r *http.Request) {
	var req RegisterAndSubscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"Invalid request body"}`, http.StatusBadRequest)
		return
	}

	// ── Validation ──────────────────────────────────────────────────
	if req.Email == "" || req.Password == "" || req.Name == "" {
		http.Error(w, `{"message":"Nome, email e senha são obrigatórios"}`, http.StatusBadRequest)
		return
	}
	if msg := models.ValidatePassword(req.Password); msg != "" {
		http.Error(w, `{"message":"`+msg+`"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// ── 1. Check duplicate email ────────────────────────────────────
	var existingUser models.User
	if err := database.Users().FindOne(ctx, bson.M{"email": req.Email}).Decode(&existingUser); err == nil {
		http.Error(w, `{"message":"Este email já está cadastrado"}`, http.StatusConflict)
		return
	}

	// ── 2. Create user ──────────────────────────────────────────────
	passwordHash, err := models.HashPassword(req.Password)
	if err != nil {
		http.Error(w, `{"message":"Erro ao processar senha"}`, http.StatusInternalServerError)
		return
	}

	user := models.User{
		ID:           primitive.NewObjectID(),
		Email:        req.Email,
		PasswordHash: passwordHash,
		CreatedAt:    time.Now(),
	}
	if _, err := database.Users().InsertOne(ctx, user); err != nil {
		http.Error(w, `{"message":"Erro ao criar usuário"}`, http.StatusInternalServerError)
		return
	}

	// ── 3. Create profile ───────────────────────────────────────────
	profile := models.Profile{
		ID:     primitive.NewObjectID(),
		UserID: user.ID,
		Name:   req.Name,
		Role:   "user",
		Settings: models.ProfileSettings{
			Currency: "BRL",
			Language: "pt-BR",
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if _, err := database.Profiles().InsertOne(ctx, profile); err != nil {
		database.Users().DeleteOne(ctx, bson.M{"_id": user.ID})
		http.Error(w, `{"message":"Erro ao criar perfil"}`, http.StatusInternalServerError)
		return
	}

	// ── 4. Create org + membership + free subscription ──────────────
	orgID, err := CreateOrgForUser(ctx, user.ID, req.Name)
	if err != nil {
		database.Profiles().DeleteOne(ctx, bson.M{"_id": profile.ID})
		database.Users().DeleteOne(ctx, bson.M{"_id": user.ID})
		http.Error(w, `{"message":"Erro ao criar organização"}`, http.StatusInternalServerError)
		return
	}

	// ── 5. Generate auth tokens ─────────────────────────────────────
	token, err := GenerateTokenWithOrg(user, orgID.Hex())
	if err != nil {
		http.Error(w, `{"message":"Erro ao gerar token de acesso"}`, http.StatusInternalServerError)
		return
	}

	rawRefresh, err := generateRefreshToken(ctx, user.ID)
	if err != nil {
		http.Error(w, `{"message":"Erro ao gerar refresh token"}`, http.StatusInternalServerError)
		return
	}

	// ── Response ────────────────────────────────────────────────────
	middleware.IncUserRegistered()
	slog.Info("register_and_subscribe",
		"user_id", user.ID.Hex(),
		"email", user.Email,
		"org_id", orgID.Hex(),
	)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(RegisterAndSubscribeResponse{
		User:         user.ToResponse(),
		Profile:      profile,
		Token:        token,
		RefreshToken: rawRefresh,
		PlanID:       "free",
	})
}
