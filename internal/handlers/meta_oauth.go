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

const metaOAuthScopes = "pages_show_list,pages_read_engagement,instagram_basic,instagram_content_publish,instagram_manage_comments,instagram_manage_insights,ads_management,ads_read,business_management"

// MetaOAuthURL godoc
// @Summary Obter URL de autenticação Meta OAuth
// @Description Retorna a URL de autorização do Facebook OAuth para conectar conta Meta
// @Tags auth
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param org_id query string true "ID da organização"
// @Success 200 {object} map[string]string
// @Failure 400 {string} string "org_id is required"
// @Failure 401 {string} string "Unauthorized"
// @Failure 500 {string} string "META_APP_ID not configured"
// @Router /auth/meta/url [get]
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

// MetaOAuthCallback godoc
// @Summary Callback de autenticação Meta OAuth
// @Description Troca o código de autorização do Facebook por tokens e busca informações das contas
// @Tags auth
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body object true "Código OAuth e ID da organização" example({"code":"abc123","org_id":"60d5ec49f1b2c72d88c1e4a1"})
// @Success 200 {object} map[string]interface{}
// @Failure 400 {string} string "Invalid request body"
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Falha ao trocar codigo por token"
// @Router /auth/meta/callback [post]
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

	// Step 3: Fetch pages + instagram_business_account(s)
	igAccounts, igReason, igErr := fetchInstagramAccounts(longToken)
	if igErr != nil {
		slog.Warn("meta_oauth_fetch_ig_accounts_failed", "error", igErr)
		// Not fatal — user can configure manually later
	}

	// Pick first account to auto-configure (if multiple, frontend lets user choose)
	var igAccountID, businessID, igUsername, igPageName string
	if len(igAccounts) > 0 {
		igAccountID = igAccounts[0].IGAccountID
		businessID = igAccounts[0].PageID
		igUsername = igAccounts[0].Username
		igPageName = igAccounts[0].PageName
	}

	// Step 4: Fetch ad accounts (all of them)
	adAccounts, err := fetchAdAccounts(longToken)
	if err != nil {
		slog.Warn("meta_oauth_fetch_ad_accounts_failed", "error", err)
		// Not fatal — user might not have ad accounts
	}

	// Pick first as default
	var adAccountID string
	if len(adAccounts) > 0 {
		adAccountID = adAccounts[0].AccountID
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
	if igUsername != "" {
		setFields["username"] = igUsername
	}
	if igPageName != "" {
		setFields["page_name"] = igPageName
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

	// Step 6: Also save Facebook Page config (using page access token for direct posting)
	fbPages, fbErr := fetchFacebookPagesWithTokens(longToken)
	var fbPageID, fbPageName string
	if fbErr == nil && len(fbPages) > 0 {
		// Use first page by default (same as IG behavior)
		fbPageID = fbPages[0].PageID
		fbPageName = fbPages[0].PageName
		pageToken := fbPages[0].AccessToken

		if pageToken != "" {
			pageTokenEnc, encErr := crypto.Encrypt(pageToken)
			if encErr == nil {
				fbFilter := bson.M{"org_id": orgID}
				fbSetFields := bson.M{
					"page_id":               fbPageID,
					"page_access_token_enc": pageTokenEnc,
					"page_name":             fbPageName,
					"updated_at":            now,
				}
				fbUpdate := bson.M{
					"$set": fbSetFields,
					"$setOnInsert": bson.M{
						"user_id":    userID,
						"org_id":     orgID,
						"created_at": now,
					},
				}
				_, fbSaveErr := database.FacebookConfigs().UpdateOne(ctx, fbFilter, fbUpdate, opts)
				if fbSaveErr != nil {
					slog.Warn("meta_oauth_fb_save_failed", "error", fbSaveErr)
				} else {
					slog.Info("facebook_config_auto_saved",
						"org_id", orgID.Hex(),
						"page_id", fbPageID,
						"page_name", fbPageName,
					)
				}
			}
		}
	}

	needsManualConfig := igAccountID == ""
	needsSelection := len(igAccounts) > 1
	needsAdAccountSelection := len(adAccounts) > 1

	slog.Info("meta_oauth_connected",
		"org_id", orgID.Hex(),
		"ig_account_id", igAccountID,
		"ig_accounts_found", len(igAccounts),
		"ad_accounts_found", len(adAccounts),
		"has_ad_account", adAccountID != "",
		"needs_manual_config", needsManualConfig,
	)

	// Return info
	respData := map[string]interface{}{
		"success":                    true,
		"instagram_account_id":       maskID(igAccountID),
		"ad_account_id":              maskID(adAccountID),
		"business_id":                maskID(businessID),
		"needs_manual_config":        needsManualConfig,
		"needs_selection":            needsSelection,
		"needs_ad_account_selection": needsAdAccountSelection,
	}
	if igReason != "" {
		respData["ig_reason"] = igReason
	}
	if needsSelection {
		respData["ig_accounts"] = igAccounts
	}
	if len(adAccounts) > 0 {
		respData["ad_accounts"] = adAccounts
		respData["ad_accounts_count"] = len(adAccounts)
	}
	// Include Facebook pages info
	if fbErr == nil && len(fbPages) > 0 {
		// Return pages without access_token for security
		safeFbPages := make([]map[string]string, len(fbPages))
		for i, p := range fbPages {
			safeFbPages[i] = map[string]string{
				"page_id":   p.PageID,
				"page_name": p.PageName,
				"category":  p.Category,
			}
		}
		respData["fb_pages"] = safeFbPages
		respData["fb_pages_count"] = len(fbPages)
		respData["fb_page_id"] = maskID(fbPageID)
		respData["fb_page_name"] = fbPageName
		respData["needs_fb_page_selection"] = len(fbPages) > 1
	}
	json.NewEncoder(w).Encode(respData)
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

// igAccount represents a found Instagram Business account linked to a Facebook Page.
type igAccount struct {
	IGAccountID       string `json:"ig_account_id"`
	PageID            string `json:"page_id"`
	PageName          string `json:"page_name"`
	Username          string `json:"username,omitempty"`
	ProfilePictureURL string `json:"profile_picture_url,omitempty"`
}

// fetchInstagramAccounts fetches the user's pages and returns all that have an instagram_business_account.
// Returns (accounts, reason, error). reason is "no_pages" or "no_ig_linked" when no accounts found.
func fetchInstagramAccounts(token string) ([]igAccount, string, error) {
	apiURL := fmt.Sprintf(
		"https://graph.facebook.com/v21.0/me/accounts?fields=id,name,instagram_business_account&access_token=%s",
		url.QueryEscape(token),
	)

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, "", fmt.Errorf("request failed: %w", err)
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
		return nil, "", fmt.Errorf("decode failed: %w", err)
	}

	if result.Error != nil {
		return nil, "", fmt.Errorf("meta error: %s", result.Error.Message)
	}

	if len(result.Data) == 0 {
		slog.Warn("meta_oauth_no_pages_found", "hint", "user has no Facebook Pages")
		return nil, "no_pages", nil
	}

	var accounts []igAccount
	for _, page := range result.Data {
		if page.InstagramBusinessAccount != nil && page.InstagramBusinessAccount.ID != "" {
			igID := page.InstagramBusinessAccount.ID
			slog.Info("meta_oauth_found_ig_account",
				"page_id", page.ID,
				"page_name", page.Name,
				"ig_account_id", igID,
			)
			acc := igAccount{
				IGAccountID: igID,
				PageID:      page.ID,
				PageName:    page.Name,
			}

			// Fetch username and profile picture from IG account
			igURL := fmt.Sprintf(
				"https://graph.facebook.com/v21.0/%s?fields=username,profile_picture_url&access_token=%s",
				url.QueryEscape(igID), url.QueryEscape(token),
			)
			igResp, igErr := http.Get(igURL)
			if igErr == nil {
				var igInfo struct {
					Username          string `json:"username"`
					ProfilePictureURL string `json:"profile_picture_url"`
				}
				if json.NewDecoder(igResp.Body).Decode(&igInfo) == nil {
					acc.Username = igInfo.Username
					acc.ProfilePictureURL = igInfo.ProfilePictureURL
				}
				igResp.Body.Close()
			}

			accounts = append(accounts, acc)
		}
	}

	if len(accounts) == 0 {
		pageNames := make([]string, 0, len(result.Data))
		for _, page := range result.Data {
			pageNames = append(pageNames, page.Name)
		}
		slog.Warn("meta_oauth_pages_without_ig",
			"page_count", len(result.Data),
			"page_names", pageNames,
		)
		return nil, "no_ig_linked", nil
	}

	return accounts, "", nil
}

// adAccount represents a found Meta Ads account.
type adAccount struct {
	AccountID string `json:"account_id"`
	Name      string `json:"name"`
}

// fetchAdAccounts fetches all ad accounts the user has access to.
func fetchAdAccounts(token string) ([]adAccount, error) {
	apiURL := fmt.Sprintf(
		"https://graph.facebook.com/v21.0/me/adaccounts?fields=account_id,name&access_token=%s",
		url.QueryEscape(token),
	)

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
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
		return nil, fmt.Errorf("decode failed: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("meta error: %s", result.Error.Message)
	}

	var accounts []adAccount
	for _, d := range result.Data {
		slog.Info("meta_oauth_found_ad_account",
			"account_id", d.AccountID,
			"name", d.Name,
		)
		accounts = append(accounts, adAccount{
			AccountID: d.AccountID,
			Name:      d.Name,
		})
	}

	return accounts, nil
}

// fbPage represents a Facebook Page with its access token for posting.
type fbPage struct {
	PageID      string `json:"page_id"`
	PageName    string `json:"page_name"`
	AccessToken string `json:"access_token"`
	Category    string `json:"category,omitempty"`
}

// fetchFacebookPagesWithTokens fetches all Facebook Pages the user has access to with their page access tokens.
func fetchFacebookPagesWithTokens(userToken string) ([]fbPage, error) {
	apiURL := fmt.Sprintf(
		"https://graph.facebook.com/v21.0/me/accounts?fields=id,name,access_token,category&access_token=%s",
		url.QueryEscape(userToken),
	)

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			AccessToken string `json:"access_token"`
			Category    string `json:"category"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("meta error: %s", result.Error.Message)
	}

	var pages []fbPage
	for _, p := range result.Data {
		pages = append(pages, fbPage{
			PageID:      p.ID,
			PageName:    p.Name,
			AccessToken: p.AccessToken,
			Category:    p.Category,
		})
	}

	return pages, nil
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
