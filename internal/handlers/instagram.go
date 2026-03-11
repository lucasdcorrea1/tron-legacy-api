package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/config"
	"github.com/tron-legacy/api/internal/crypto"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/image/draw"
)

// ExchangeForLongLivedToken exchanges a short-lived Meta token for a long-lived one (~60 days).
func ExchangeForLongLivedToken(shortToken string) (string, error) {
	cfg := config.Get()
	if cfg.MetaAppID == "" || cfg.MetaAppSecret == "" {
		return "", fmt.Errorf("META_APP_ID or META_APP_SECRET not configured")
	}

	url := fmt.Sprintf(
		"https://graph.facebook.com/v21.0/oauth/access_token?grant_type=fb_exchange_token&client_id=%s&client_secret=%s&fb_exchange_token=%s",
		cfg.MetaAppID, cfg.MetaAppSecret, shortToken,
	)

	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
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

	slog.Info("long_lived_token_obtained", "expires_in_seconds", result.ExpiresIn)
	return result.AccessToken, nil
}

// instagramCredentials holds resolved Instagram API credentials.
type instagramCredentials struct {
	AccountID string
	Token     string
	Source    string // "user" or "env"
	OrgID     primitive.ObjectID
}

// getInstagramCredentials resolves credentials: DB per-org config first, then env vars fallback (only when no org context).
func getInstagramCredentials(ctx context.Context, userID, orgID primitive.ObjectID) (*instagramCredentials, error) {
	// Try per-org config from DB
	if orgID != primitive.NilObjectID && crypto.Available() {
		var cfg models.InstagramConfig
		err := database.InstagramConfigs().FindOne(ctx, bson.M{"org_id": orgID}).Decode(&cfg)
		if err == nil {
			token, err := crypto.Decrypt(cfg.AccessTokenEnc)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt token: %w", err)
			}
			return &instagramCredentials{
				AccountID: cfg.InstagramAccountID,
				Token:     token,
				Source:    "user",
				OrgID:     cfg.OrgID,
			}, nil
		}
		if err != mongo.ErrNoDocuments {
			return nil, fmt.Errorf("db error: %w", err)
		}
		// org specified but no config found — don't fallback to env vars
		return nil, nil
	}

	// Fallback to env vars only when there's no org context
	envCfg := config.Get()
	if envCfg.InstagramAccountID != "" && envCfg.InstagramToken != "" {
		return &instagramCredentials{
			AccountID: envCfg.InstagramAccountID,
			Token:     envCfg.InstagramToken,
			Source:    "env",
		}, nil
	}

	return nil, nil // not configured
}

// maskAccountID masks the middle of an account ID string.
func maskAccountID(id string) string {
	if len(id) > 8 {
		return id[:4] + "****" + id[len(id)-4:]
	}
	return "****"
}

// requireInstagramCreds is a helper that extracts user/org and resolves IG credentials.
// Supports override via query param "instagram_account_id" for multi-account switching.
func requireInstagramCreds(w http.ResponseWriter, r *http.Request) (primitive.ObjectID, *instagramCredentials, bool) {
	userID := middleware.GetUserID(r)
	orgID := middleware.GetOrgID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return primitive.NilObjectID, nil, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	creds, err := getInstagramCredentials(ctx, userID, orgID)
	if err != nil {
		slog.Error("instagram_creds_error", "error", err)
		http.Error(w, "Error getting credentials", http.StatusInternalServerError)
		return primitive.NilObjectID, nil, false
	}
	if creds == nil {
		http.Error(w, "Instagram not configured", http.StatusBadRequest)
		return primitive.NilObjectID, nil, false
	}

	// Allow override via query param (multi-account support)
	if override := r.URL.Query().Get("instagram_account_id"); override != "" {
		creds.AccountID = override
	}

	return userID, creds, true
}

// ListInstagramAccounts returns all Instagram Business accounts accessible via the stored token.
func ListInstagramAccounts(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireInstagramCreds(w, r)
	if !ok {
		return
	}

	accounts, reason, err := fetchInstagramAccounts(creds.Token)
	if err != nil {
		slog.Error("list_instagram_accounts_error", "error", err)
		http.Error(w, "Error fetching Instagram accounts", http.StatusBadGateway)
		return
	}

	if accounts == nil {
		accounts = []igAccount{}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"data":   accounts,
		"reason": reason,
	})
}

// GetInstagramConfig returns whether Instagram is configured (DB first, then env fallback)
func GetInstagramConfig(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	creds, err := getInstagramCredentials(ctx, userID, orgID)
	if err != nil {
		slog.Error("get_instagram_config_error", "error", err)
		http.Error(w, "Error checking config", http.StatusInternalServerError)
		return
	}

	resp := models.InstagramConfigResponse{
		Configured: creds != nil,
		HasToken:   creds != nil,
	}
	if creds != nil {
		resp.AccountID = maskAccountID(creds.AccountID)
		resp.Source = creds.Source
	}

	// If source is "user", also load Meta Ads fields from the same config
	if creds != nil && creds.Source == "user" && orgID != primitive.NilObjectID {
		var cfg models.InstagramConfig
		err := database.InstagramConfigs().FindOne(ctx, bson.M{"org_id": orgID}).Decode(&cfg)
		if err == nil {
			if cfg.AdAccountID != "" {
				resp.AdAccountID = maskAccountID(cfg.AdAccountID)
			}
			if cfg.BusinessID != "" {
				resp.BusinessID = maskAccountID(cfg.BusinessID)
			}
		}
	}

	json.NewEncoder(w).Encode(resp)
}

// SaveInstagramConfig saves or updates per-user Instagram credentials
func SaveInstagramConfig(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	orgID := middleware.GetOrgID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if !crypto.Available() {
		http.Error(w, "Encryption not configured (ENCRYPTION_KEY missing)", http.StatusServiceUnavailable)
		return
	}

	var req models.SaveInstagramConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Check if config already exists (DB or env)
	var existing models.InstagramConfig
	existsErr := database.InstagramConfigs().FindOne(ctx, bson.M{"org_id": orgID}).Decode(&existing)
	dbConfigExists := existsErr == nil

	// Also check if env credentials are available
	creds, _ := getInstagramCredentials(ctx, userID, orgID)
	hasAnyCreds := dbConfigExists || creds != nil

	// For brand new configs (no DB, no env), require full credentials
	if !hasAnyCreds && (req.InstagramAccountID == "" || req.AccessToken == "") {
		http.Error(w, "instagram_account_id and access_token are required", http.StatusBadRequest)
		return
	}

	now := time.Now()
	filter := bson.M{"org_id": orgID}
	setFields := bson.M{
		"updated_at": now,
	}

	// Update credentials if provided
	var plainToken string
	if req.InstagramAccountID != "" {
		setFields["instagram_account_id"] = req.InstagramAccountID
	}
	if req.AccessToken != "" {
		// Try to exchange for a long-lived token
		longToken, err := ExchangeForLongLivedToken(req.AccessToken)
		if err != nil {
			slog.Warn("long_lived_token_exchange_failed, using original token", "error", err)
			longToken = req.AccessToken
		} else {
			slog.Info("successfully exchanged for long-lived token")
		}
		plainToken = longToken
		encToken, err := crypto.Encrypt(longToken)
		if err != nil {
			slog.Error("encrypt_token_error", "error", err)
			http.Error(w, "Error encrypting token", http.StatusInternalServerError)
			return
		}
		setFields["access_token_enc"] = encToken
	}

	// If creating a new DB record from env credentials, seed with env values
	if !dbConfigExists && creds != nil {
		if req.InstagramAccountID == "" {
			setFields["instagram_account_id"] = creds.AccountID
		}
		if req.AccessToken == "" {
			encToken, err := crypto.Encrypt(creds.Token)
			if err != nil {
				slog.Error("encrypt_token_error", "error", err)
				http.Error(w, "Error encrypting token", http.StatusInternalServerError)
				return
			}
			setFields["access_token_enc"] = encToken
		}
	}

	// Update Meta Ads fields
	if req.AdAccountID != "" {
		setFields["ad_account_id"] = req.AdAccountID
	}
	if req.BusinessID != "" {
		setFields["business_id"] = req.BusinessID
	}

	// Fetch IG username and page_name from Meta API if we have a token
	if plainToken != "" {
		effectiveIGID := req.InstagramAccountID
		if effectiveIGID == "" && dbConfigExists {
			effectiveIGID = existing.InstagramAccountID
		}
		if effectiveIGID != "" {
			if accounts, _, fetchErr := fetchInstagramAccounts(plainToken); fetchErr == nil {
				for _, acc := range accounts {
					if acc.IGAccountID == effectiveIGID {
						if acc.Username != "" {
							setFields["username"] = acc.Username
						}
						if acc.PageName != "" {
							setFields["page_name"] = acc.PageName
						}
						break
					}
				}
			} else {
				slog.Warn("save_config_fetch_ig_profile_failed", "error", fetchErr)
			}
		}
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

	_, dbErr := database.InstagramConfigs().UpdateOne(ctx, filter, update, opts)
	if dbErr != nil {
		slog.Error("save_instagram_config_error", "error", dbErr)
		http.Error(w, "Error saving config", http.StatusInternalServerError)
		return
	}

	slog.Info("instagram_config_saved",
		"user_id", userID.Hex(),
		"account_id", maskAccountID(req.InstagramAccountID),
	)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":    "Config saved",
		"account_id": maskAccountID(req.InstagramAccountID),
	})
}

// DeleteInstagramConfig removes per-org Instagram credentials
func DeleteInstagramConfig(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	orgID := middleware.GetOrgID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if orgID == primitive.NilObjectID {
		http.Error(w, "Organization context required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{"org_id": orgID}

	slog.Info("instagram_config_deleting",
		"user_id", userID.Hex(),
		"org_id", orgID.Hex(),
	)

	result, err := database.InstagramConfigs().DeleteOne(ctx, filter)
	if err != nil {
		slog.Error("delete_instagram_config_error", "error", err)
		http.Error(w, "Error deleting config", http.StatusInternalServerError)
		return
	}

	if result.DeletedCount == 0 {
		http.Error(w, "No config found", http.StatusNotFound)
		return
	}

	slog.Info("instagram_config_deleted",
		"user_id", userID.Hex(),
		"org_id", orgID.Hex(),
		"deleted_count", result.DeletedCount,
	)

	json.NewEncoder(w).Encode(map[string]string{"message": "Config deleted"})
}

// TestInstagramConnection verifies credentials by fetching account info (read-only, no publish)
func TestInstagramConnection(w http.ResponseWriter, r *http.Request) {
	userID, creds, ok := requireInstagramCreds(w, r)
	if !ok {
		return
	}

	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// GET account info — read-only, safe
	params := url.Values{}
	params.Set("fields", "id,username,name,profile_picture_url,followers_count,media_count")
	params.Set("access_token", creds.Token)
	apiURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s?%s", creds.AccountID, params.Encode())

	resp, err := http.Get(apiURL)
	if err != nil {
		http.Error(w, "Failed to reach Instagram API: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		http.Error(w, "Failed to parse Instagram response", http.StatusBadGateway)
		return
	}

	if errObj, ok := result["error"]; ok {
		slog.Error("instagram_test_failed", "error", errObj)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   errObj,
			"source":  creds.Source,
		})
		return
	}

	slog.Info("instagram_test_success",
		"user_id", userID.Hex(),
		"ig_username", result["username"],
	)

	response := map[string]interface{}{
		"success":             true,
		"source":              creds.Source,
		"id":                  result["id"],
		"username":            result["username"],
		"name":                result["name"],
		"profile_picture_url": result["profile_picture_url"],
		"followers_count":     result["followers_count"],
		"media_count":         result["media_count"],
	}

	// If AdAccountID is configured, also test the ads account
	if creds.Source == "user" && orgID != primitive.NilObjectID {
		var cfg models.InstagramConfig
		err := database.InstagramConfigs().FindOne(ctx, bson.M{"org_id": orgID}).Decode(&cfg)
		if err == nil && cfg.AdAccountID != "" {
			adAccountPath := cfg.AdAccountID
			if !strings.HasPrefix(adAccountPath, "act_") {
				adAccountPath = "act_" + adAccountPath
			}
			adsParams := url.Values{}
			adsParams.Set("fields", "name,account_status,currency,timezone_name,amount_spent")
			adsParams.Set("access_token", creds.Token)
			adsURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s?%s", adAccountPath, adsParams.Encode())
			adsResp, err := http.Get(adsURL)
			if err == nil {
				defer adsResp.Body.Close()
				var adsResult map[string]interface{}
				if json.NewDecoder(adsResp.Body).Decode(&adsResult) == nil {
					if _, hasErr := adsResult["error"]; !hasErr {
						response["ads_account"] = adsResult
					} else {
						response["ads_account_error"] = adsResult["error"]
					}
				}
			}
		}
	}

	json.NewEncoder(w).Encode(response)
}

// GetInstagramFeed fetches recent media from the Instagram account (read-only)
func GetInstagramFeed(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireInstagramCreds(w, r)
	if !ok {
		return
	}

	limit := r.URL.Query().Get("limit")
	if limit == "" {
		limit = "12"
	}

	feedParams := url.Values{}
	feedParams.Set("fields", "id,caption,media_type,media_url,thumbnail_url,permalink,timestamp,like_count,comments_count")
	feedParams.Set("limit", limit)
	feedParams.Set("access_token", creds.Token)
	apiURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/media?%s", creds.AccountID, feedParams.Encode())

	resp, err := http.Get(apiURL)
	if err != nil {
		http.Error(w, "Failed to reach Instagram API: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		http.Error(w, "Failed to parse Instagram response", http.StatusBadGateway)
		return
	}

	if errObj, ok := result["error"]; ok {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   errObj,
		})
		return
	}

	json.NewEncoder(w).Encode(result)
}

// CreateInstagramSchedule creates a new scheduled Instagram post
func CreateInstagramSchedule(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	orgID := middleware.GetOrgID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req models.CreateInstagramScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.ImageIDs) == 0 {
		http.Error(w, "At least one image is required", http.StatusBadRequest)
		return
	}

	if req.MediaType != "image" && req.MediaType != "carousel" {
		http.Error(w, "media_type must be 'image' or 'carousel'", http.StatusBadRequest)
		return
	}

	if req.MediaType == "image" && len(req.ImageIDs) > 1 {
		http.Error(w, "image type allows only one image; use 'carousel' for multiple", http.StatusBadRequest)
		return
	}

	if req.MediaType == "carousel" && len(req.ImageIDs) < 2 {
		http.Error(w, "carousel requires at least 2 images", http.StatusBadRequest)
		return
	}

	if len(req.Caption) > 2200 {
		http.Error(w, "Caption must be 2200 characters or less", http.StatusBadRequest)
		return
	}

	scheduledAt, err := time.Parse(time.RFC3339, req.ScheduledAt)
	if err != nil {
		http.Error(w, "scheduled_at must be a valid ISO 8601 date", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Verify all images exist
	for _, imgID := range req.ImageIDs {
		oid, err := primitive.ObjectIDFromHex(imgID)
		if err != nil {
			http.Error(w, "Invalid image ID: "+imgID, http.StatusBadRequest)
			return
		}
		count, err := database.Images().CountDocuments(ctx, bson.M{"_id": oid})
		if err != nil || count == 0 {
			http.Error(w, "Image not found: "+imgID, http.StatusBadRequest)
			return
		}
	}

	now := time.Now()
	schedule := models.InstagramSchedule{
		ID:          primitive.NewObjectID(),
		UserID:      userID,
		OrgID:       orgID,
		Caption:     req.Caption,
		MediaType:   req.MediaType,
		ImageIDs:    req.ImageIDs,
		ScheduledAt: scheduledAt,
		Status:      "scheduled",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err = database.InstagramSchedules().InsertOne(ctx, schedule)
	if err != nil {
		http.Error(w, "Error creating schedule", http.StatusInternalServerError)
		return
	}

	slog.Info("instagram_schedule_created",
		"schedule_id", schedule.ID.Hex(),
		"user_id", userID.Hex(),
		"scheduled_at", scheduledAt.Format(time.RFC3339),
	)

	resp := buildScheduleResponse(schedule)
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// ListInstagramSchedules lists scheduled posts with pagination and filtering
func ListInstagramSchedules(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 50 {
		limit = 10
	}

	filter := bson.M{"org_id": orgID}
	if status := r.URL.Query().Get("status"); status != "" {
		filter["status"] = status
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	total, err := database.InstagramSchedules().CountDocuments(ctx, filter)
	if err != nil {
		http.Error(w, "Error counting schedules", http.StatusInternalServerError)
		return
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().
		SetSort(bson.D{{Key: "scheduled_at", Value: -1}}).
		SetSkip(skip).
		SetLimit(int64(limit))

	cursor, err := database.InstagramSchedules().Find(ctx, filter, opts)
	if err != nil {
		http.Error(w, "Error fetching schedules", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var schedules []models.InstagramSchedule
	if err := cursor.All(ctx, &schedules); err != nil {
		http.Error(w, "Error decoding schedules", http.StatusInternalServerError)
		return
	}

	responses := make([]models.InstagramScheduleResponse, len(schedules))
	for i, s := range schedules {
		responses[i] = buildScheduleResponse(s)
	}

	json.NewEncoder(w).Encode(models.InstagramScheduleListResponse{
		Schedules: responses,
		Total:     total,
		Page:      page,
		Limit:     limit,
	})
}

// GetInstagramSchedule returns a single schedule by ID
func GetInstagramSchedule(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	idStr := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid schedule ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var schedule models.InstagramSchedule
	err = database.InstagramSchedules().FindOne(ctx, bson.M{"_id": oid, "org_id": orgID}).Decode(&schedule)
	if err != nil {
		http.Error(w, "Schedule not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(buildScheduleResponse(schedule))
}

// UpdateInstagramSchedule updates a scheduled post (only if status is "scheduled")
func UpdateInstagramSchedule(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	idStr := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid schedule ID", http.StatusBadRequest)
		return
	}

	var req models.UpdateInstagramScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var schedule models.InstagramSchedule
	err = database.InstagramSchedules().FindOne(ctx, bson.M{"_id": oid, "org_id": orgID}).Decode(&schedule)
	if err != nil {
		http.Error(w, "Schedule not found", http.StatusNotFound)
		return
	}

	if schedule.Status != "scheduled" && schedule.Status != "failed" {
		http.Error(w, "Can only edit scheduled or failed posts", http.StatusBadRequest)
		return
	}

	update := bson.M{"$set": bson.M{"updated_at": time.Now()}}
	setFields := update["$set"].(bson.M)

	if req.Caption != nil {
		if len(*req.Caption) > 2200 {
			http.Error(w, "Caption must be 2200 characters or less", http.StatusBadRequest)
			return
		}
		setFields["caption"] = *req.Caption
	}

	if req.MediaType != nil {
		if *req.MediaType != "image" && *req.MediaType != "carousel" {
			http.Error(w, "media_type must be 'image' or 'carousel'", http.StatusBadRequest)
			return
		}
		setFields["media_type"] = *req.MediaType
	}

	if req.ImageIDs != nil {
		if len(req.ImageIDs) == 0 {
			http.Error(w, "At least one image is required", http.StatusBadRequest)
			return
		}
		// Verify images exist
		for _, imgID := range req.ImageIDs {
			imgOID, err := primitive.ObjectIDFromHex(imgID)
			if err != nil {
				http.Error(w, "Invalid image ID: "+imgID, http.StatusBadRequest)
				return
			}
			count, err := database.Images().CountDocuments(ctx, bson.M{"_id": imgOID})
			if err != nil || count == 0 {
				http.Error(w, "Image not found: "+imgID, http.StatusBadRequest)
				return
			}
		}
		setFields["image_ids"] = req.ImageIDs
	}

	if req.ScheduledAt != nil {
		scheduledAt, err := time.Parse(time.RFC3339, *req.ScheduledAt)
		if err != nil {
			http.Error(w, "scheduled_at must be a valid ISO 8601 date", http.StatusBadRequest)
			return
		}
		setFields["scheduled_at"] = scheduledAt
	}

	// If re-scheduling a failed post, reset status
	if schedule.Status == "failed" {
		setFields["status"] = "scheduled"
		setFields["error_message"] = ""
	}

	_, err = database.InstagramSchedules().UpdateOne(ctx, bson.M{"_id": oid, "org_id": orgID}, update)
	if err != nil {
		http.Error(w, "Error updating schedule", http.StatusInternalServerError)
		return
	}

	var updated models.InstagramSchedule
	database.InstagramSchedules().FindOne(ctx, bson.M{"_id": oid, "org_id": orgID}).Decode(&updated)

	slog.Info("instagram_schedule_updated",
		"schedule_id", oid.Hex(),
	)

	json.NewEncoder(w).Encode(buildScheduleResponse(updated))
}

// DeleteInstagramSchedule deletes a scheduled post
func DeleteInstagramSchedule(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	idStr := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid schedule ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var schedule models.InstagramSchedule
	err = database.InstagramSchedules().FindOne(ctx, bson.M{"_id": oid, "org_id": orgID}).Decode(&schedule)
	if err != nil {
		http.Error(w, "Schedule not found", http.StatusNotFound)
		return
	}

	if schedule.Status == "publishing" {
		http.Error(w, "Cannot delete a post that is currently publishing", http.StatusBadRequest)
		return
	}

	_, err = database.InstagramSchedules().DeleteOne(ctx, bson.M{"_id": oid, "org_id": orgID})
	if err != nil {
		http.Error(w, "Error deleting schedule", http.StatusInternalServerError)
		return
	}

	slog.Info("instagram_schedule_deleted",
		"schedule_id", oid.Hex(),
	)

	json.NewEncoder(w).Encode(map[string]string{"message": "Schedule deleted"})
}

// UploadInstagramImage uploads an image for Instagram, resized to max 1080x1080
func UploadInstagramImage(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	orgID := middleware.GetOrgID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB limit

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "Image too large (max 10MB)", http.StatusRequestEntityTooLarge)
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "No image provided. Use field name 'image'", http.StatusBadRequest)
		return
	}
	defer file.Close()

	imgData, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Failed to read image", http.StatusBadRequest)
		return
	}

	detectedType := http.DetectContentType(imgData)
	if detectedType != "image/jpeg" && detectedType != "image/png" && detectedType != "image/webp" {
		http.Error(w, "Only JPEG, PNG and WebP images are allowed", http.StatusBadRequest)
		return
	}

	img, _, err := image.Decode(bytes.NewReader(imgData))
	if err != nil {
		http.Error(w, "Invalid image format", http.StatusBadRequest)
		return
	}

	// Resize to max 1080px width (Instagram requirement)
	resized := resizeInstagramImage(img, 1080)
	bounds := resized.Bounds()

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 85}); err != nil {
		http.Error(w, "Failed to process image", http.StatusInternalServerError)
		return
	}

	base64Img := base64.StdEncoding.EncodeToString(buf.Bytes())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	imgDoc := models.BlogImage{
		ID:         primitive.NewObjectID(),
		UploaderID: userID,
		OrgID:      orgID,
		Width:      bounds.Dx(),
		Data:       base64Img,
		Size:       buf.Len(),
		CreatedAt:  time.Now(),
	}

	_, err = database.Images().InsertOne(ctx, imgDoc)
	if err != nil {
		http.Error(w, "Error saving image", http.StatusInternalServerError)
		return
	}

	slog.Info("instagram_image_uploaded",
		"image_id", imgDoc.ID.Hex(),
		"user_id", userID.Hex(),
		"width", bounds.Dx(),
		"height", bounds.Dy(),
	)

	json.NewEncoder(w).Encode(map[string]string{
		"id":  imgDoc.ID.Hex(),
		"url": "/api/v1/blog/images/" + imgDoc.ID.Hex(),
	})
}

// resizeInstagramImage resizes image to max width and enforces Instagram feed
// aspect ratio limits (4:5 portrait to 1.91:1 landscape). Images taller than
// 4:5 are center-cropped before resizing.
func resizeInstagramImage(img image.Image, maxWidth int) image.Image {
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	// Enforce aspect ratio: min 4:5 (0.8), max 1.91:1
	// If image is taller than 4:5, center-crop height
	ratio := float64(srcW) / float64(srcH)
	if ratio < 0.8 {
		// Too tall (e.g. 9:16 stories) — crop to 4:5 from center
		newH := int(float64(srcW) / 0.8)
		top := (srcH - newH) / 2
		cropRect := image.Rect(bounds.Min.X, bounds.Min.Y+top, bounds.Max.X, bounds.Min.Y+top+newH)
		cropped := image.NewRGBA(image.Rect(0, 0, srcW, newH))
		draw.CatmullRom.Scale(cropped, cropped.Bounds(), img, cropRect, draw.Over, nil)
		img = cropped
		srcW = srcW
		srcH = newH
	} else if ratio > 1.91 {
		// Too wide — crop to 1.91:1 from center
		newW := int(float64(srcH) * 1.91)
		left := (srcW - newW) / 2
		cropRect := image.Rect(bounds.Min.X+left, bounds.Min.Y, bounds.Min.X+left+newW, bounds.Max.Y)
		cropped := image.NewRGBA(image.Rect(0, 0, newW, srcH))
		draw.CatmullRom.Scale(cropped, cropped.Bounds(), img, cropRect, draw.Over, nil)
		img = cropped
		srcW = newW
		srcH = srcH
	}

	if srcW <= maxWidth {
		return img
	}

	newW := maxWidth
	newH := int(float64(srcH) * float64(maxWidth) / float64(srcW))

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)

	return dst
}

// getPublicImageURL builds the public URL for serving an image
func getPublicImageURL(imageID string) string {
	// Use RENDER_EXTERNAL_URL (deployed) or FRONTEND_URL as base
	baseURL := os.Getenv("RENDER_EXTERNAL_URL")
	if baseURL == "" {
		baseURL = config.Get().FrontendURL
	}
	// Remove trailing slash
	baseURL = strings.TrimRight(baseURL, "/")
	return baseURL + "/api/v1/blog/images/" + imageID
}

// publishToInstagram publishes a scheduled post to Instagram via Graph API
func publishToInstagram(schedule models.InstagramSchedule) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	creds, err := getInstagramCredentials(ctx, schedule.UserID, schedule.OrgID)
	if err != nil {
		return "", fmt.Errorf("get credentials: %w", err)
	}
	if creds == nil {
		return "", fmt.Errorf("instagram not configured")
	}

	accountID := creds.AccountID
	token := creds.Token

	if schedule.MediaType == "image" {
		// Single image post
		imageURL := getPublicImageURL(schedule.ImageIDs[0])

		// Step 1: Create media container
		containerID, err := createMediaContainer(accountID, token, imageURL, schedule.Caption, false)
		if err != nil {
			return "", fmt.Errorf("create container: %w", err)
		}

		// Step 2: Publish
		mediaID, err := publishMediaContainer(accountID, token, containerID)
		if err != nil {
			return "", fmt.Errorf("publish: %w", err)
		}

		return mediaID, nil
	}

	// Carousel post
	var childIDs []string
	for _, imgID := range schedule.ImageIDs {
		imageURL := getPublicImageURL(imgID)
		childID, err := createMediaContainer(accountID, token, imageURL, "", true)
		if err != nil {
			return "", fmt.Errorf("create carousel item: %w", err)
		}
		childIDs = append(childIDs, childID)
	}

	// Create carousel container
	carouselID, err := createCarouselContainer(accountID, token, childIDs, schedule.Caption)
	if err != nil {
		return "", fmt.Errorf("create carousel container: %w", err)
	}

	// Publish carousel
	mediaID, err := publishMediaContainer(accountID, token, carouselID)
	if err != nil {
		return "", fmt.Errorf("publish carousel: %w", err)
	}

	return mediaID, nil
}

// createMediaContainer creates an IG media container for a single image or carousel item.
// Retries up to 3 times on transient errors.
func createMediaContainer(accountID, token, imageURL, caption string, isCarouselItem bool) (string, error) {
	apiURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/media", accountID)

	params := map[string]string{
		"image_url":    imageURL,
		"access_token": token,
	}

	if isCarouselItem {
		params["is_carousel_item"] = "true"
	} else {
		params["caption"] = caption
	}

	formValues := url.Values{}
	for k, v := range params {
		formValues.Set(k, v)
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*5) * time.Second)
		}

		resp, err := http.PostForm(apiURL, formValues)
		if err != nil {
			lastErr = err
			continue
		}

		var result map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			lastErr = err
			continue
		}
		resp.Body.Close()

		if errObj, ok := result["error"]; ok {
			errMap, _ := errObj.(map[string]interface{})
			isTransient, _ := errMap["is_transient"].(bool)
			if isTransient && attempt < 2 {
				slog.Warn("ig_container_transient_error", "attempt", attempt+1, "error", errObj)
				lastErr = fmt.Errorf("instagram API error: %v", errObj)
				continue
			}
			return "", fmt.Errorf("instagram API error: %v", errObj)
		}

		id, ok := result["id"].(string)
		if !ok {
			return "", fmt.Errorf("unexpected response: no id field")
		}

		return id, nil
	}
	return "", lastErr
}

// createCarouselContainer creates a carousel container with children
func createCarouselContainer(accountID, token string, childIDs []string, caption string) (string, error) {
	apiURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/media", accountID)

	formValues := url.Values{}
	formValues.Set("media_type", "CAROUSEL")
	formValues.Set("children", strings.Join(childIDs, ","))
	formValues.Set("caption", caption)
	formValues.Set("access_token", token)

	resp, err := http.PostForm(apiURL, formValues)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if errMsg, ok := result["error"]; ok {
		return "", fmt.Errorf("instagram API error: %v", errMsg)
	}

	id, ok := result["id"].(string)
	if !ok {
		return "", fmt.Errorf("unexpected response: no id field")
	}

	return id, nil
}

// publishMediaContainer publishes a created media container
func waitForContainerReady(containerID, token string) error {
	checkURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s?fields=status_code&access_token=%s", containerID, token)

	for i := 0; i < 30; i++ {
		resp, err := http.Get(checkURL)
		if err != nil {
			return err
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		statusCode, _ := result["status_code"].(string)
		switch statusCode {
		case "FINISHED":
			return nil
		case "ERROR":
			return fmt.Errorf("container processing failed")
		}
		// IN_PROGRESS or empty — wait and retry
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("container not ready after 60s")
}

func publishMediaContainer(accountID, token, creationID string) (string, error) {
	// Wait for container to be ready before publishing
	if err := waitForContainerReady(creationID, token); err != nil {
		return "", fmt.Errorf("wait for container: %w", err)
	}

	apiURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/media_publish", accountID)

	formValues := url.Values{}
	formValues.Set("creation_id", creationID)
	formValues.Set("access_token", token)

	resp, err := http.PostForm(apiURL, formValues)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if errMsg, ok := result["error"]; ok {
		return "", fmt.Errorf("instagram API error: %v", errMsg)
	}

	id, ok := result["id"].(string)
	if !ok {
		return "", fmt.Errorf("unexpected response: no id field")
	}

	return id, nil
}

// ProcessScheduledInstagramPosts checks for due posts and publishes them
func ProcessScheduledInstagramPosts() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	now := time.Now()
	filter := bson.M{
		"status":       "scheduled",
		"scheduled_at": bson.M{"$lte": now},
	}

	cursor, err := database.InstagramSchedules().Find(ctx, filter)
	if err != nil {
		slog.Error("instagram_scheduler_query_error", "error", err)
		return
	}
	defer cursor.Close(ctx)

	var schedules []models.InstagramSchedule
	if err := cursor.All(ctx, &schedules); err != nil {
		slog.Error("instagram_scheduler_decode_error", "error", err)
		return
	}

	for _, schedule := range schedules {
		// Set status to publishing
		database.InstagramSchedules().UpdateOne(ctx, bson.M{"_id": schedule.ID}, bson.M{
			"$set": bson.M{"status": "publishing", "updated_at": time.Now()},
		})

		mediaID, err := publishToInstagram(schedule)
		if err != nil {
			slog.Error("instagram_publish_failed",
				"schedule_id", schedule.ID.Hex(),
				"error", err,
			)
			database.InstagramSchedules().UpdateOne(ctx, bson.M{"_id": schedule.ID}, bson.M{
				"$set": bson.M{
					"status":        "failed",
					"error_message": err.Error(),
					"updated_at":    time.Now(),
				},
			})
			continue
		}

		database.InstagramSchedules().UpdateOne(ctx, bson.M{"_id": schedule.ID}, bson.M{
			"$set": bson.M{
				"status":      "published",
				"ig_media_id": mediaID,
				"updated_at":  time.Now(),
			},
		})

		middleware.IncInstagramPublished()

		slog.Info("instagram_published",
			"schedule_id", schedule.ID.Hex(),
			"ig_media_id", mediaID,
		)
	}
}

// ListAllOrgInstagramProfiles returns IG profiles across all orgs the user belongs to.
// Auth-only (no org context needed). Only reads from DB — no Meta API calls.
func ListAllOrgInstagramProfiles(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Current org from JWT (may be nil if no org selected)
	currentOrgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Find all org memberships for this user
	cursor, err := database.OrgMemberships().Find(ctx, bson.M{"user_id": userID})
	if err != nil {
		slog.Error("all_profiles_membership_error", "error", err)
		http.Error(w, "Error listing memberships", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	type membership struct {
		OrgID primitive.ObjectID `bson:"org_id"`
	}
	var memberships []membership
	if err := cursor.All(ctx, &memberships); err != nil {
		http.Error(w, "Error decoding memberships", http.StatusInternalServerError)
		return
	}

	type profileEntry struct {
		OrgID       string `json:"org_id"`
		OrgName     string `json:"org_name"`
		IGAccountID string `json:"ig_account_id"`
		Username    string `json:"username,omitempty"`
		PageName    string `json:"page_name,omitempty"`
		AdAccountID string `json:"ad_account_id,omitempty"`
		IsCurrent   bool   `json:"is_current"`
	}

	var profiles []profileEntry

	for _, m := range memberships {
		// Fetch instagram config for this org
		var cfg models.InstagramConfig
		err := database.InstagramConfigs().FindOne(ctx, bson.M{"org_id": m.OrgID}).Decode(&cfg)
		if err != nil {
			continue // no IG config for this org
		}

		// Fetch org name
		var org models.Organization
		err = database.Organizations().FindOne(ctx, bson.M{"_id": m.OrgID}).Decode(&org)
		if err != nil {
			continue
		}

		profiles = append(profiles, profileEntry{
			OrgID:       m.OrgID.Hex(),
			OrgName:     org.Name,
			IGAccountID: cfg.InstagramAccountID,
			Username:    cfg.Username,
			PageName:    cfg.PageName,
			AdAccountID: cfg.AdAccountID,
			IsCurrent:   m.OrgID == currentOrgID,
		})
	}

	if profiles == nil {
		profiles = []profileEntry{}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"profiles": profiles,
	})
}

// buildScheduleResponse creates a response with resolved image URLs
func buildScheduleResponse(s models.InstagramSchedule) models.InstagramScheduleResponse {
	imageURLs := make([]string, len(s.ImageIDs))
	for i, id := range s.ImageIDs {
		imageURLs[i] = "/api/v1/blog/images/" + id
	}
	return models.InstagramScheduleResponse{
		InstagramSchedule: s,
		ImageURLs:         imageURLs,
	}
}
