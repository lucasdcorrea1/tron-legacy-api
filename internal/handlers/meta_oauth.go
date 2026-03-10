package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/config"
	"github.com/tron-legacy/api/internal/crypto"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const metaOAuthScopes = "pages_show_list,pages_read_engagement,pages_manage_posts,instagram_basic,instagram_content_publish,instagram_manage_comments,instagram_manage_insights,ads_read,business_management"

// MetaOAuthURL returns the Facebook OAuth authorization URL.
// GET /api/v1/auth/meta/url
func MetaOAuthURL(w http.ResponseWriter, r *http.Request) {
	cfg := config.Get()
	if cfg.MetaAppID == "" {
		http.Error(w, "META_APP_ID not configured", http.StatusInternalServerError)
		return
	}

	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	orgID := r.URL.Query().Get("org_id")
	if orgID == "" {
		http.Error(w, "org_id is required", http.StatusBadRequest)
		return
	}

	redirectURI := fmt.Sprintf("%s/meta/callback", strings.TrimRight(cfg.FrontendURL, "/"))

	// state encodes org_id so the callback can associate with the right org
	state := orgID

	oauthURL := fmt.Sprintf(
		"https://www.facebook.com/v21.0/dialog/oauth?client_id=%s&redirect_uri=%s&scope=%s&state=%s&response_type=code",
		url.QueryEscape(cfg.MetaAppID),
		url.QueryEscape(redirectURI),
		url.QueryEscape(metaOAuthScopes),
		url.QueryEscape(state),
	)

	json.NewEncoder(w).Encode(map[string]string{"url": oauthURL})
}

// MetaOAuthCallback exchanges the Facebook OAuth code for tokens and fetches account info.
// POST /api/v1/auth/meta/callback
func MetaOAuthCallback(w http.ResponseWriter, r *http.Request) {
	cfg := config.Get()
	if cfg.MetaAppID == "" || cfg.MetaAppSecret == "" {
		http.Error(w, "Meta app credentials not configured", http.StatusInternalServerError)
		return
	}

	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Code  string `json:"code"`
		OrgID string `json:"org_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Code == "" || req.OrgID == "" {
		http.Error(w, "code and org_id are required", http.StatusBadRequest)
		return
	}

	orgID, err := primitive.ObjectIDFromHex(req.OrgID)
	if err != nil {
		http.Error(w, "Invalid org_id", http.StatusBadRequest)
		return
	}

	if !crypto.Available() {
		http.Error(w, "Encryption not configured", http.StatusInternalServerError)
		return
	}

	redirectURI := fmt.Sprintf("%s/meta/callback", strings.TrimRight(cfg.FrontendURL, "/"))

	// Step 1: Exchange code for short-lived token
	shortToken, err := exchangeCodeForToken(cfg.MetaAppID, cfg.MetaAppSecret, req.Code, redirectURI)
	if err != nil {
		slog.Error("meta_oauth_code_exchange_failed", "error", err)
		http.Error(w, "Falha ao trocar codigo por token: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Step 2: Exchange for long-lived token
	longToken, err := ExchangeForLongLivedToken(shortToken)
	if err != nil {
		slog.Warn("meta_oauth_long_token_failed, using short token", "error", err)
		longToken = shortToken
	}

	// Step 3: Fetch pages + instagram_business_account
	igAccountID, businessID, err := fetchInstagramAccount(longToken)
	if err != nil {
		slog.Warn("meta_oauth_fetch_ig_account_failed", "error", err)
		// Not fatal — user can configure manually later
	}

	// Step 4: Fetch ad accounts
	adAccountID, err := fetchAdAccount(longToken)
	if err != nil {
		slog.Warn("meta_oauth_fetch_ad_account_failed", "error", err)
		// Not fatal — user might not have ad accounts
	}

	// Step 5: Encrypt and save
	tokenEnc, err := crypto.Encrypt(longToken)
	if err != nil {
		slog.Error("meta_oauth_encrypt_failed", "error", err)
		http.Error(w, "Falha ao encriptar token", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	now := time.Now()
	filter := bson.M{"org_id": orgID}
	setFields := bson.M{
		"access_token_enc": tokenEnc,
		"updated_at":       now,
	}
	if igAccountID != "" {
		setFields["instagram_account_id"] = igAccountID
	}
	if businessID != "" {
		setFields["business_id"] = businessID
	}
	if adAccountID != "" {
		setFields["ad_account_id"] = adAccountID
	}

	update := bson.M{
		"$set": setFields,
		"$setOnInsert": bson.M{
			"user_id":    userID,
			"org_id":     orgID,
			"created_at": now,
		},
	}
	opts := options.Update().SetUpsert(true)
	_, err = database.InstagramConfigs().UpdateOne(ctx, filter, update, opts)
	if err != nil {
		slog.Error("meta_oauth_save_failed", "error", err)
		http.Error(w, "Falha ao salvar configuracao", http.StatusInternalServerError)
		return
	}

	needsManualConfig := igAccountID == ""

	slog.Info("meta_oauth_connected",
		"org_id", orgID.Hex(),
		"ig_account_id", igAccountID,
		"has_ad_account", adAccountID != "",
		"needs_manual_config", needsManualConfig,
	)

	// Return masked info
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":              true,
		"instagram_account_id": maskID(igAccountID),
		"ad_account_id":        maskID(adAccountID),
		"business_id":          maskID(businessID),
		"needs_manual_config":  needsManualConfig,
	})
}

// exchangeCodeForToken exchanges an OAuth authorization code for a short-lived access token.
func exchangeCodeForToken(appID, appSecret, code, redirectURI string) (string, error) {
	tokenURL := fmt.Sprintf(
		"https://graph.facebook.com/v21.0/oauth/access_token?client_id=%s&client_secret=%s&code=%s&redirect_uri=%s",
		url.QueryEscape(appID),
		url.QueryEscape(appSecret),
		url.QueryEscape(code),
		url.QueryEscape(redirectURI),
	)

	resp, err := http.Get(tokenURL)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		Error       *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode failed: %w", err)
	}

	if result.Error != nil {
		return "", fmt.Errorf("meta error %d: %s", result.Error.Code, result.Error.Message)
	}

	if result.AccessToken == "" {
		return "", fmt.Errorf("empty access_token in response")
	}

	return result.AccessToken, nil
}

// fetchInstagramAccount fetches the user's pages and finds the one with an instagram_business_account.
// Returns (instagramAccountID, pageID/businessID, error).
func fetchInstagramAccount(token string) (string, string, error) {
	apiURL := fmt.Sprintf(
		"https://graph.facebook.com/v21.0/me/accounts?fields=id,name,instagram_business_account&access_token=%s",
		url.QueryEscape(token),
	)

	resp, err := http.Get(apiURL)
	if err != nil {
		return "", "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			ID                       string `json:"id"`
			Name                     string `json:"name"`
			InstagramBusinessAccount *struct {
				ID string `json:"id"`
			} `json:"instagram_business_account"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("decode failed: %w", err)
	}

	if result.Error != nil {
		return "", "", fmt.Errorf("meta error: %s", result.Error.Message)
	}

	for _, page := range result.Data {
		if page.InstagramBusinessAccount != nil && page.InstagramBusinessAccount.ID != "" {
			slog.Info("meta_oauth_found_ig_account",
				"page_id", page.ID,
				"page_name", page.Name,
				"ig_account_id", page.InstagramBusinessAccount.ID,
			)
			return page.InstagramBusinessAccount.ID, page.ID, nil
		}
	}

	return "", "", fmt.Errorf("nenhuma pagina com conta Instagram Business encontrada")
}

// fetchAdAccount fetches the user's ad accounts and returns the first one.
func fetchAdAccount(token string) (string, error) {
	apiURL := fmt.Sprintf(
		"https://graph.facebook.com/v21.0/me/adaccounts?fields=account_id,name&access_token=%s",
		url.QueryEscape(token),
	)

	resp, err := http.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			AccountID string `json:"account_id"`
			Name      string `json:"name"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode failed: %w", err)
	}

	if result.Error != nil {
		return "", fmt.Errorf("meta error: %s", result.Error.Message)
	}

	if len(result.Data) > 0 {
		slog.Info("meta_oauth_found_ad_account",
			"account_id", result.Data[0].AccountID,
			"name", result.Data[0].Name,
		)
		return result.Data[0].AccountID, nil
	}

	return "", nil
}

// maskID returns a masked version of an ID string for display.
func maskID(id string) string {
	if id == "" {
		return ""
	}
	if len(id) <= 6 {
		return id[:1] + "****" + id[len(id)-1:]
	}
	return id[:3] + "****" + id[len(id)-3:]
}
