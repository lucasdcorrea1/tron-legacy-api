package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/tron-legacy/api/internal/config"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Register godoc
// @Summary Registrar novo usuário
// @Description Cria uma nova conta com email e senha
// @Tags auth
// @Accept json
// @Produce json
// @Param request body models.RegisterRequest true "Dados de registro"
// @Success 201 {object} models.AuthResponse
// @Failure 400 {string} string "Invalid request body"
// @Failure 409 {string} string "Email already exists"
// @Router /auth/register [post]
func Register(w http.ResponseWriter, r *http.Request) {
	var req models.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.Email == "" || req.Password == "" || req.Name == "" {
		http.Error(w, "Email, password and name are required", http.StatusBadRequest)
		return
	}

	if msg := models.ValidatePassword(req.Password); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Check if email already exists
	var existingUser models.User
	err := database.Users().FindOne(ctx, bson.M{"email": req.Email}).Decode(&existingUser)
	if err == nil {
		http.Error(w, "Email already exists", http.StatusConflict)
		return
	}

	// Hash password
	passwordHash, err := models.HashPassword(req.Password)
	if err != nil {
		http.Error(w, "Error processing password", http.StatusInternalServerError)
		return
	}

	// Create user
	user := models.User{
		ID:           primitive.NewObjectID(),
		Email:        req.Email,
		PasswordHash: passwordHash,
		CreatedAt:    time.Now(),
	}

	_, err = database.Users().InsertOne(ctx, user)
	if err != nil {
		http.Error(w, "Error creating user", http.StatusInternalServerError)
		return
	}

	// Create profile
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

	_, err = database.Profiles().InsertOne(ctx, profile)
	if err != nil {
		// Rollback user creation
		database.Users().DeleteOne(ctx, bson.M{"_id": user.ID})
		http.Error(w, "Error creating profile", http.StatusInternalServerError)
		return
	}

	// Auto-create organization for the new user
	orgID, err := CreateOrgForUser(ctx, user.ID, req.Name)
	if err != nil {
		slog.Error("register_create_org_error", "error", err, "user_id", user.ID.Hex())
		// Non-fatal: user is created, they just won't have an org yet
	}

	// Auto-accept any pre-accepted invitations for this user
	AutoAcceptInvitations(ctx, user.ID, user.Email)

	// Generate JWT access token with org_id
	token, err := GenerateTokenWithOrg(user, orgID.Hex())
	if err != nil {
		http.Error(w, "Error generating token", http.StatusInternalServerError)
		return
	}

	// Generate refresh token
	rawRefresh, err := generateRefreshToken(ctx, user.ID)
	if err != nil {
		http.Error(w, "Error generating refresh token", http.StatusInternalServerError)
		return
	}

	response := models.AuthResponse{
		User:         user.ToResponse(),
		Profile:      profile,
		Token:        token,
		RefreshToken: rawRefresh,
	}

	// Increment metrics and log event
	middleware.IncUserRegistered()
	slog.Info("user_registered",
		"user_id", user.ID.Hex(),
		"email", user.Email,
		"name", profile.Name,
		"org_id", orgID.Hex(),
	)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

// Login godoc
// @Summary Login de usuário
// @Description Autentica usuário com email e senha
// @Tags auth
// @Accept json
// @Produce json
// @Param request body models.LoginRequest true "Dados de login"
// @Success 200 {object} models.AuthResponse
// @Failure 400 {string} string "Invalid request body"
// @Failure 401 {string} string "Invalid credentials"
// @Router /auth/login [post]
func Login(w http.ResponseWriter, r *http.Request) {
	var req models.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Email == "" || req.Password == "" {
		http.Error(w, "Email and password are required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Find user by email
	var user models.User
	err := database.Users().FindOne(ctx, bson.M{"email": req.Email}).Decode(&user)
	if err != nil {
		middleware.IncLoginFailed()
		slog.Warn("login_failed",
			"reason", "user_not_found",
			"email", req.Email,
		)
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	// Check password
	if !models.CheckPassword(req.Password, user.PasswordHash) {
		middleware.IncLoginFailed()
		slog.Warn("login_failed",
			"reason", "invalid_password",
			"user_id", user.ID.Hex(),
			"email", req.Email,
		)
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	// Get profile
	var profile models.Profile
	err = database.Profiles().FindOne(ctx, bson.M{"user_id": user.ID}).Decode(&profile)
	if err != nil {
		http.Error(w, "Profile not found", http.StatusInternalServerError)
		return
	}

	// Auto-accept any pre-accepted invitations for this user
	AutoAcceptInvitations(ctx, user.ID, user.Email)

	// Get default org for the user
	orgID, err := GetDefaultOrgForUser(ctx, user.ID)
	orgIDStr := ""
	if err == nil {
		orgIDStr = orgID.Hex()
	}

	// Generate JWT access token with org_id
	token, err := GenerateTokenWithOrg(user, orgIDStr)
	if err != nil {
		http.Error(w, "Error generating token", http.StatusInternalServerError)
		return
	}

	// Generate refresh token
	rawRefresh, err := generateRefreshToken(ctx, user.ID)
	if err != nil {
		http.Error(w, "Error generating refresh token", http.StatusInternalServerError)
		return
	}

	response := models.AuthResponse{
		User:         user.ToResponse(),
		Profile:      profile,
		Token:        token,
		RefreshToken: rawRefresh,
	}

	// Increment metrics and log event
	middleware.IncLoginSuccess()
	slog.Info("user_login",
		"user_id", user.ID.Hex(),
		"email", user.Email,
		"org_id", orgIDStr,
	)

	json.NewEncoder(w).Encode(response)
}

// Me godoc
// @Summary Dados do usuário logado
// @Description Retorna informações do usuário autenticado
// @Tags auth
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} models.AuthResponse
// @Failure 401 {string} string "Unauthorized"
// @Router /auth/me [get]
func Me(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get user
	var user models.User
	err := database.Users().FindOne(ctx, bson.M{"_id": userID}).Decode(&user)
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	// Get profile
	var profile models.Profile
	err = database.Profiles().FindOne(ctx, bson.M{"user_id": userID}).Decode(&profile)
	if err != nil {
		http.Error(w, "Profile not found", http.StatusNotFound)
		return
	}

	response := models.AuthResponse{
		User:    user.ToResponse(),
		Profile: profile,
		Token:   "", // Don't include token in /me response
	}

	json.NewEncoder(w).Encode(response)
}

// GenerateTokenWithOrg creates a JWT access token with org_id claim.
func GenerateTokenWithOrg(user models.User, orgID string) (string, error) {
	cfg := config.Get()

	claims := middleware.Claims{
		UserID: user.ID.Hex(),
		Email:  user.Email,
		OrgID:  orgID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(cfg.AccessTokenExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "tron-legacy-api",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(cfg.JWTSecret))
}

// generateToken creates a JWT access token for the user (backwards compatible).
func generateToken(user models.User) (string, error) {
	return GenerateTokenWithOrg(user, "")
}

// generateRefreshToken creates a random opaque refresh token, stores its SHA-256 hash
// in the database, and returns the raw token to be sent to the client.
func generateRefreshToken(ctx context.Context, userID primitive.ObjectID) (string, error) {
	cfg := config.Get()

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	rawHex := hex.EncodeToString(raw)

	hash := sha256.Sum256([]byte(rawHex))
	tokenHash := hex.EncodeToString(hash[:])

	rt := models.RefreshToken{
		ID:        primitive.NewObjectID(),
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(cfg.RefreshTokenExpiry),
		CreatedAt: time.Now(),
	}

	if _, err := database.RefreshTokens().InsertOne(ctx, rt); err != nil {
		return "", err
	}

	return rawHex, nil
}

// hashToken returns the hex-encoded SHA-256 hash of a raw token string.
func hashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Refresh godoc
// @Summary Renovar tokens de acesso
// @Description Troca um refresh token válido por um novo par de access + refresh token
// @Tags auth
// @Accept json
// @Produce json
// @Param request body refreshRequest true "Refresh token"
// @Success 200 {object} models.AuthResponse
// @Failure 400 {string} string "refresh_token is required"
// @Failure 401 {string} string "Invalid or expired refresh token"
// @Router /auth/refresh [post]
func Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
		http.Error(w, "refresh_token is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tokenHash := hashToken(req.RefreshToken)

	// Find the refresh token in DB
	var stored models.RefreshToken
	err := database.RefreshTokens().FindOne(ctx, bson.M{
		"token_hash": tokenHash,
		"expires_at": bson.M{"$gt": time.Now()},
	}).Decode(&stored)
	if err != nil {
		http.Error(w, "Invalid or expired refresh token", http.StatusUnauthorized)
		return
	}

	// Delete the consumed token (rotation)
	database.RefreshTokens().DeleteOne(ctx, bson.M{"_id": stored.ID})

	// Look up user
	var user models.User
	if err := database.Users().FindOne(ctx, bson.M{"_id": stored.UserID}).Decode(&user); err != nil {
		http.Error(w, "User not found", http.StatusUnauthorized)
		return
	}

	// Look up profile
	var profile models.Profile
	if err := database.Profiles().FindOne(ctx, bson.M{"user_id": stored.UserID}).Decode(&profile); err != nil {
		http.Error(w, "Profile not found", http.StatusInternalServerError)
		return
	}

	// Get default org for new token
	orgID, err := GetDefaultOrgForUser(ctx, stored.UserID)
	orgIDStr := ""
	if err == nil {
		orgIDStr = orgID.Hex()
	}

	// Issue new access token
	accessToken, err := GenerateTokenWithOrg(user, orgIDStr)
	if err != nil {
		http.Error(w, "Error generating token", http.StatusInternalServerError)
		return
	}

	// Issue new refresh token
	newRefresh, err := generateRefreshToken(ctx, stored.UserID)
	if err != nil {
		http.Error(w, "Error generating refresh token", http.StatusInternalServerError)
		return
	}

	response := models.AuthResponse{
		User:         user.ToResponse(),
		Profile:      profile,
		Token:        accessToken,
		RefreshToken: newRefresh,
	}

	slog.Info("token_refreshed", "user_id", stored.UserID.Hex())
	json.NewEncoder(w).Encode(response)
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Logout godoc
// @Summary Logout do usuário
// @Description Invalida o refresh token fornecido
// @Tags auth
// @Accept json
// @Produce json
// @Param request body logoutRequest true "Refresh token para invalidar"
// @Success 200 {object} map[string]string
// @Router /auth/logout [post]
func Logout(w http.ResponseWriter, r *http.Request) {
	var req logoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err == nil && req.RefreshToken != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		tokenHash := hashToken(req.RefreshToken)
		database.RefreshTokens().DeleteOne(ctx, bson.M{"token_hash": tokenHash})
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Logged out"})
}
