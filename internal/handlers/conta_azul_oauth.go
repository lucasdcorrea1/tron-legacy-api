package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/tron-legacy/api/internal/config"
	"github.com/tron-legacy/api/internal/crypto"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	contaazul "github.com/tron-legacy/api/internal/services/conta_azul"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

const contaAzulOAuthScope = "openid+profile+aws.cognito.signin.user.admin"

// portalCallbackPath is the front-end route that finalizes the OAuth dance.
// The actual exchange happens in PortalContaAzulCallback after the front
// posts the code back to the API.
const portalCallbackPath = "/portal/conectar/callback"

func contaAzulRedirectURI() string {
	return strings.TrimRight(config.Get().FrontendURL, "/") + portalCallbackPath
}

// signOAuthState signs an opaque state containing the EndClient and Org IDs
// using the JWT_SECRET. Lives only 10 minutes to prevent replay.
func signOAuthState(ecID, orgID primitive.ObjectID) (string, error) {
	claims := middleware.EndClientClaims{
		EndClientID: ecID.Hex(),
		OrgID:       orgID.Hex(),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   middleware.ContaAzulOAuthStateSubject,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(config.Get().JWTSecret))
}

func parseOAuthState(raw string) (primitive.ObjectID, primitive.ObjectID, error) {
	claims := &middleware.EndClientClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(config.Get().JWTSecret), nil
	})
	if err != nil || !token.Valid {
		return primitive.NilObjectID, primitive.NilObjectID, fmt.Errorf("invalid state")
	}
	if claims.Subject != middleware.ContaAzulOAuthStateSubject {
		return primitive.NilObjectID, primitive.NilObjectID, fmt.Errorf("wrong state subject")
	}
	ecID, err := primitive.ObjectIDFromHex(claims.EndClientID)
	if err != nil {
		return primitive.NilObjectID, primitive.NilObjectID, err
	}
	orgID, err := primitive.ObjectIDFromHex(claims.OrgID)
	if err != nil {
		return primitive.NilObjectID, primitive.NilObjectID, err
	}
	return ecID, orgID, nil
}

// PortalContaAzulConnectURL returns the URL the EndClient must visit to
// authorize their Conta Azul account.
// GET /api/v1/portal/conta-azul/connect-url
func PortalContaAzulConnectURL(w http.ResponseWriter, r *http.Request) {
	cfg := config.Get()
	if cfg.ContaAzulClientID == "" {
		http.Error(w, "Conta Azul não configurada no servidor", http.StatusInternalServerError)
		return
	}

	ecID := middleware.GetEndClientID(r)
	orgID := middleware.GetEndClientOrgID(r)
	if ecID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	state, err := signOAuthState(ecID, orgID)
	if err != nil {
		http.Error(w, "Erro ao gerar state", http.StatusInternalServerError)
		return
	}

	q := url.Values{}
	q.Set("client_id", cfg.ContaAzulClientID)
	q.Set("redirect_uri", contaAzulRedirectURI())
	q.Set("response_type", "code")
	q.Set("state", state)

	// Scope is space-separated per OAuth2 spec; Conta Azul docs show it with
	// '+' characters in the example, but url.Values will percent-encode the
	// spaces, which the auth server accepts.
	scopes := strings.ReplaceAll(contaAzulOAuthScope, "+", " ")
	q.Set("scope", scopes)

	authURL := strings.TrimRight(cfg.ContaAzulAuthURL, "/") + "?" + q.Encode()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.ContaAzulConnectURLResponse{URL: authURL})
}

// PortalContaAzulCallback receives the authorization code from the front,
// exchanges it for tokens, encrypts and persists them on the EndClient.
// POST /api/v1/portal/conta-azul/callback
func PortalContaAzulCallback(w http.ResponseWriter, r *http.Request) {
	if !crypto.Available() {
		http.Error(w, "Encryption not initialized (set ENCRYPTION_KEY)", http.StatusInternalServerError)
		return
	}

	authedID := middleware.GetEndClientID(r)
	if authedID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req models.ContaAzulCallbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.Code == "" || req.State == "" {
		http.Error(w, "code e state são obrigatórios", http.StatusBadRequest)
		return
	}

	stateECID, _, err := parseOAuthState(req.State)
	if err != nil {
		http.Error(w, "State inválido ou expirado", http.StatusBadRequest)
		return
	}
	if stateECID != authedID {
		// State belongs to a different EndClient than the one calling us —
		// either CSRF attempt or a stale link from another session.
		http.Error(w, "State não corresponde ao cliente autenticado", http.StatusForbidden)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tok, err := contaazul.ExchangeCode(ctx, req.Code, contaAzulRedirectURI())
	if err != nil {
		http.Error(w, "Falha ao trocar code por token: "+err.Error(), http.StatusBadGateway)
		return
	}

	accessEnc, err := crypto.Encrypt(tok.AccessToken)
	if err != nil {
		http.Error(w, "Erro ao encriptar access token", http.StatusInternalServerError)
		return
	}
	refreshEnc, err := crypto.Encrypt(tok.RefreshToken)
	if err != nil {
		http.Error(w, "Erro ao encriptar refresh token", http.StatusInternalServerError)
		return
	}

	now := time.Now()
	expiresAt := now.Add(time.Duration(tok.ExpiresIn) * time.Second)

	conn := models.ContaAzulConnection{
		AccessTokenEnc:  accessEnc,
		RefreshTokenEnc: refreshEnc,
		ExpiresAt:       expiresAt,
		Scope:           tok.Scope,
		ConnectedAt:     now,
	}

	_, err = database.EndClients().UpdateOne(
		ctx,
		bson.M{"_id": authedID},
		bson.M{"$set": bson.M{"conta_azul_conn": conn, "updated_at": now}},
	)
	if err != nil {
		http.Error(w, "Erro ao salvar conexão", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"connected":  true,
		"expires_at": expiresAt,
	})
}

// AdminImportContaAzulTokens lets an org admin paste already-obtained
// Conta Azul tokens directly onto an EndClient, bypassing the OAuth flow.
// This is a dev/testing shortcut — production should always use the
// portal-side OAuth.
// POST /api/v1/admin/conta-azul/clients/{id}/import-tokens
func AdminImportContaAzulTokens(w http.ResponseWriter, r *http.Request) {
	if !crypto.Available() {
		http.Error(w, "Encryption not initialized (set ENCRYPTION_KEY)", http.StatusInternalServerError)
		return
	}

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
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"` // seconds from now
		Scope        string `json:"scope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	req.AccessToken = strings.TrimSpace(req.AccessToken)
	if req.AccessToken == "" {
		http.Error(w, "access_token é obrigatório", http.StatusBadRequest)
		return
	}
	if req.ExpiresIn <= 0 {
		req.ExpiresIn = 3600 // assume 1h if not provided
	}

	accessEnc, err := crypto.Encrypt(req.AccessToken)
	if err != nil {
		http.Error(w, "Erro ao encriptar access token", http.StatusInternalServerError)
		return
	}
	var refreshEnc string
	if req.RefreshToken != "" {
		refreshEnc, err = crypto.Encrypt(strings.TrimSpace(req.RefreshToken))
		if err != nil {
			http.Error(w, "Erro ao encriptar refresh token", http.StatusInternalServerError)
			return
		}
	}

	now := time.Now()
	conn := models.ContaAzulConnection{
		AccessTokenEnc:  accessEnc,
		RefreshTokenEnc: refreshEnc,
		ExpiresAt:       now.Add(time.Duration(req.ExpiresIn) * time.Second),
		Scope:           req.Scope,
		ConnectedAt:     now,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := database.EndClients().UpdateOne(
		ctx,
		bson.M{"_id": id, "org_id": orgID},
		bson.M{"$set": bson.M{"conta_azul_conn": conn, "updated_at": now}},
	)
	if err != nil {
		http.Error(w, "Erro ao salvar conexão", http.StatusInternalServerError)
		return
	}
	if res.MatchedCount == 0 {
		http.Error(w, "Cliente não encontrado", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"connected":  true,
		"expires_at": conn.ExpiresAt,
	})
}

// PortalContaAzulDisconnect removes the stored OAuth tokens for the
// authenticated EndClient.
// DELETE /api/v1/portal/conta-azul/connection
func PortalContaAzulDisconnect(w http.ResponseWriter, r *http.Request) {
	ecID := middleware.GetEndClientID(r)
	if ecID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := database.EndClients().UpdateOne(
		ctx,
		bson.M{"_id": ecID},
		bson.M{"$unset": bson.M{"conta_azul_conn": ""}, "$set": bson.M{"updated_at": time.Now()}},
	)
	if err != nil {
		http.Error(w, "Erro ao desconectar", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
