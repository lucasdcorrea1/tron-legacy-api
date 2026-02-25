package handlers

import (
	"context"
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

	if len(req.Password) < 6 {
		http.Error(w, "Password must be at least 6 characters", http.StatusBadRequest)
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

	// Generate JWT token
	token, err := generateToken(user)
	if err != nil {
		http.Error(w, "Error generating token", http.StatusInternalServerError)
		return
	}

	response := models.AuthResponse{
		User:    user.ToResponse(),
		Profile: profile,
		Token:   token,
	}

	// Increment metrics and log event
	middleware.IncUserRegistered()
	slog.Info("user_registered",
		"user_id", user.ID.Hex(),
		"email", user.Email,
		"name", profile.Name,
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

	// Generate JWT token
	token, err := generateToken(user)
	if err != nil {
		http.Error(w, "Error generating token", http.StatusInternalServerError)
		return
	}

	response := models.AuthResponse{
		User:    user.ToResponse(),
		Profile: profile,
		Token:   token,
	}

	// Increment metrics and log event
	middleware.IncLoginSuccess()
	slog.Info("user_login",
		"user_id", user.ID.Hex(),
		"email", user.Email,
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

// generateToken creates a JWT token for the user
func generateToken(user models.User) (string, error) {
	cfg := config.Get()

	claims := middleware.Claims{
		UserID: user.ID.Hex(),
		Email:  user.Email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(cfg.JWTExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "tron-legacy-api",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(cfg.JWTSecret))
}
