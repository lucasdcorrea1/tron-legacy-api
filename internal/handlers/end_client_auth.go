package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/tron-legacy/api/internal/config"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	endClientAccessTokenTTL  = 12 * time.Hour
	endClientRefreshTokenTTL = 30 * 24 * time.Hour
)

// ── Portal session helpers ────────────────────────────────────────────

func issueEndClientToken(ec *models.EndClient) (string, error) {
	now := time.Now()
	claims := middleware.EndClientClaims{
		EndClientID: ec.ID.Hex(),
		OrgID:       ec.OrgID.Hex(),
		Email:       ec.Email,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   middleware.EndClientTokenSubject,
			ExpiresAt: jwt.NewNumericDate(now.Add(endClientAccessTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(config.Get().JWTSecret))
}

func generateRandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashEndClientToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func issueRefreshToken(ctx context.Context, endClientID primitive.ObjectID) (string, error) {
	raw, err := generateRandomToken(32)
	if err != nil {
		return "", err
	}
	rt := models.EndClientRefreshToken{
		EndClientID: endClientID,
		TokenHash:   hashEndClientToken(raw),
		ExpiresAt:   time.Now().Add(endClientRefreshTokenTTL),
		CreatedAt:   time.Now(),
	}
	if _, err := database.EndClientRefreshTokens().InsertOne(ctx, rt); err != nil {
		return "", err
	}
	return raw, nil
}

func toSession(ec *models.EndClient) models.EndClientSession {
	return models.EndClientSession{
		ID:              ec.ID,
		OrgID:           ec.OrgID,
		Name:            ec.Name,
		Email:           ec.Email,
		DashboardAccess: ec.DashboardAccess,
		HasContaAzul:    ec.ContaAzulConn != nil && ec.ContaAzulConn.AccessTokenEnc != "",
		IsActive:        ec.IsActive,
	}
}

// ── Handlers ──────────────────────────────────────────────────────────

// PortalLogin authenticates an EndClient and returns a portal session token.
// POST /api/v1/portal/auth/login
func PortalLogin(w http.ResponseWriter, r *http.Request) {
	var req models.EndClientLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" || req.Password == "" {
		http.Error(w, "Email e senha são obrigatórios", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var ec models.EndClient
	if err := database.EndClients().FindOne(ctx, bson.M{"email": req.Email}).Decode(&ec); err != nil {
		http.Error(w, "Credenciais inválidas", http.StatusUnauthorized)
		return
	}
	if !ec.IsActive {
		http.Error(w, "Acesso desativado. Entre em contato com seu administrador.", http.StatusForbidden)
		return
	}
	if !models.CheckPassword(req.Password, ec.PasswordHash) {
		http.Error(w, "Credenciais inválidas", http.StatusUnauthorized)
		return
	}

	tok, err := issueEndClientToken(&ec)
	if err != nil {
		http.Error(w, "Erro ao gerar token", http.StatusInternalServerError)
		return
	}
	refresh, err := issueRefreshToken(ctx, ec.ID)
	if err != nil {
		http.Error(w, "Erro ao gerar refresh token", http.StatusInternalServerError)
		return
	}

	// Update last login (best effort)
	_, _ = database.EndClients().UpdateOne(ctx, bson.M{"_id": ec.ID}, bson.M{"$set": bson.M{"last_login_at": time.Now()}})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.EndClientAuthResponse{
		EndClient:    toSession(&ec),
		Token:        tok,
		RefreshToken: refresh,
	})
}

// PortalRefresh exchanges a refresh token for a new access token.
// POST /api/v1/portal/auth/refresh
func PortalRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
		http.Error(w, "refresh_token is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hash := hashEndClientToken(req.RefreshToken)
	var rt models.EndClientRefreshToken
	err := database.EndClientRefreshTokens().FindOne(ctx, bson.M{
		"token_hash": hash,
		"expires_at": bson.M{"$gt": time.Now()},
	}).Decode(&rt)
	if err != nil {
		http.Error(w, "Invalid or expired refresh token", http.StatusUnauthorized)
		return
	}

	var ec models.EndClient
	if err := database.EndClients().FindOne(ctx, bson.M{"_id": rt.EndClientID}).Decode(&ec); err != nil {
		http.Error(w, "End client not found", http.StatusUnauthorized)
		return
	}
	if !ec.IsActive {
		http.Error(w, "Acesso desativado", http.StatusForbidden)
		return
	}

	tok, err := issueEndClientToken(&ec)
	if err != nil {
		http.Error(w, "Erro ao gerar token", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": tok})
}

// PortalLogout invalidates the refresh token.
// POST /api/v1/portal/auth/logout
func PortalLogout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.RefreshToken != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = database.EndClientRefreshTokens().DeleteOne(ctx, bson.M{"token_hash": hashEndClientToken(req.RefreshToken)})
	}
	w.WriteHeader(http.StatusNoContent)
}

// PortalMe returns the current EndClient session.
// GET /api/v1/portal/me
func PortalMe(w http.ResponseWriter, r *http.Request) {
	ecID := middleware.GetEndClientID(r)
	if ecID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var ec models.EndClient
	if err := database.EndClients().FindOne(ctx, bson.M{"_id": ecID}).Decode(&ec); err != nil {
		http.Error(w, "End client not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toSession(&ec))
}
