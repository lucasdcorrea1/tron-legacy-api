package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
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
)

const metaAdsAPIBase = "https://graph.facebook.com/v21.0"

// ── Credentials resolution (same pattern as Instagram) ──────────────

type metaAdsCredentials struct {
	AdAccountID string
	Token       string
	BusinessID  string
	Source      string // "user" or "env"
}

func getMetaAdsCredentials(ctx context.Context, userID, orgID primitive.ObjectID) (*metaAdsCredentials, error) {
	// 1. Try per-org Meta Ads config from DB
	if orgID != primitive.NilObjectID && crypto.Available() {
		var cfg models.MetaAdsConfig
		err := database.MetaAdsConfigs().FindOne(ctx, bson.M{"org_id": orgID}).Decode(&cfg)
		if err == nil {
			token, err := crypto.Decrypt(cfg.AccessTokenEnc)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt token: %w", err)
			}
			return &metaAdsCredentials{
				AdAccountID: cfg.AdAccountID,
				Token:       token,
				BusinessID:  cfg.BusinessID,
				Source:      "user",
			}, nil
		}
		if err != mongo.ErrNoDocuments {
			return nil, fmt.Errorf("db error: %w", err)
		}

		// 2. Fallback to Instagram config (unified token) — same org
		var igCfg models.InstagramConfig
		err = database.InstagramConfigs().FindOne(ctx, bson.M{"org_id": orgID}).Decode(&igCfg)
		if err == nil && igCfg.AdAccountID != "" {
			token, err := crypto.Decrypt(igCfg.AccessTokenEnc)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt instagram token: %w", err)
			}
			return &metaAdsCredentials{
				AdAccountID: igCfg.AdAccountID,
				Token:       token,
				BusinessID:  igCfg.BusinessID,
				Source:      "instagram",
			}, nil
		}

		// org specified but no config found — don't fallback to env vars
		return nil, nil
	}

	// 3. Fallback to env vars only when there's no org context
	envCfg := config.Get()
	if envCfg.MetaAdsAccountID != "" && envCfg.MetaAdsAccessToken != "" {
		return &metaAdsCredentials{
			AdAccountID: envCfg.MetaAdsAccountID,
			Token:       envCfg.MetaAdsAccessToken,
			Source:      "env",
		}, nil
	}

	return nil, nil
}

// requireMetaAdsCreds is a helper that extracts user and credentials or writes an error.
func requireMetaAdsCreds(w http.ResponseWriter, r *http.Request) (primitive.ObjectID, *metaAdsCredentials, bool) {
	userID := middleware.GetUserID(r)
	orgID := middleware.GetOrgID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return primitive.NilObjectID, nil, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	creds, err := getMetaAdsCredentials(ctx, userID, orgID)
	if err != nil {
		slog.Error("meta_ads_creds_error", "error", err)
		http.Error(w, "Error getting credentials", http.StatusInternalServerError)
		return primitive.NilObjectID, nil, false
	}
	if creds == nil {
		http.Error(w, "Meta Ads not configured", http.StatusBadRequest)
		return primitive.NilObjectID, nil, false
	}

	return userID, creds, true
}

// metaGraphGet does a GET to the Meta Graph API and decodes the response.
func metaGraphGet(endpoint, token string, params url.Values) (map[string]interface{}, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("access_token", token)
	apiURL := metaAdsAPIBase + endpoint + "?" + params.Encode()

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("http error: %w", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode error: %w", err)
	}

	if errObj, ok := result["error"]; ok {
		return nil, fmt.Errorf("meta API error: %v", errObj)
	}

	return result, nil
}

// metaGraphPost does a POST to the Meta Graph API with form-encoded body.
func metaGraphPost(endpoint, token string, params url.Values) (map[string]interface{}, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("access_token", token)
	apiURL := metaAdsAPIBase + endpoint

	resp, err := http.PostForm(apiURL, params)
	if err != nil {
		return nil, fmt.Errorf("http error: %w", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode error: %w", err)
	}

	if errObj, ok := result["error"]; ok {
		return nil, fmt.Errorf("meta API error: %v", errObj)
	}

	return result, nil
}

// metaGraphDelete does a DELETE to the Meta Graph API.
func metaGraphDelete(endpoint, token string) (map[string]interface{}, error) {
	params := url.Values{}
	params.Set("access_token", token)
	apiURL := metaAdsAPIBase + endpoint + "?" + params.Encode()

	req, err := http.NewRequest(http.MethodDelete, apiURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http error: %w", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode error: %w", err)
	}

	if errObj, ok := result["error"]; ok {
		return nil, fmt.Errorf("meta API error: %v", errObj)
	}

	return result, nil
}

// adAccountPath returns /act_<id> ensuring the act_ prefix.
func adAccountPath(adAccountID string) string {
	if strings.HasPrefix(adAccountID, "act_") {
		return "/" + adAccountID
	}
	return "/act_" + adAccountID
}

// ══════════════════════════════════════════════════════════════════════
// CAMPAIGNS
// ══════════════════════════════════════════════════════════════════════

func ListMetaAdsCampaigns(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}

	params := url.Values{}
	params.Set("fields", "id,name,objective,status,daily_budget,lifetime_budget,bid_strategy,buying_type,special_ad_categories,created_time,updated_time")
	params.Set("limit", "100")

	if status := r.URL.Query().Get("status"); status != "" {
		params.Set("effective_status", fmt.Sprintf(`["%s"]`, status))
	}

	result, err := metaGraphGet(adAccountPath(creds.AdAccountID)+"/campaigns", creds.Token, params)
	if err != nil {
		slog.Error("meta_ads_list_campaigns_error", "error", err)
		http.Error(w, "Error fetching campaigns: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(result)
}

func CreateMetaAdsCampaign(w http.ResponseWriter, r *http.Request) {
	userID, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}
	orgID := middleware.GetOrgID(r)

	var req models.CreateCampaignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" || req.Objective == "" {
		http.Error(w, "name and objective are required", http.StatusBadRequest)
		return
	}

	params := url.Values{}
	params.Set("name", req.Name)
	params.Set("objective", req.Objective)

	status := "PAUSED"
	if req.Status != "" {
		status = req.Status
	}
	params.Set("status", status)

	if req.BuyingType != "" {
		params.Set("buying_type", req.BuyingType)
	}
	if req.DailyBudget > 0 {
		params.Set("daily_budget", fmt.Sprintf("%d", req.DailyBudget))
	}
	if req.LifetimeBudget > 0 {
		params.Set("lifetime_budget", fmt.Sprintf("%d", req.LifetimeBudget))
	}
	if req.BidStrategy != "" {
		params.Set("bid_strategy", req.BidStrategy)
	}

	categories := "NONE"
	if len(req.SpecialAdCategories) > 0 {
		catJSON, _ := json.Marshal(req.SpecialAdCategories)
		categories = string(catJSON)
	}
	params.Set("special_ad_categories", categories)

	result, err := metaGraphPost(adAccountPath(creds.AdAccountID)+"/campaigns", creds.Token, params)
	if err != nil {
		slog.Error("meta_ads_create_campaign_error", "error", err)
		http.Error(w, "Error creating campaign: "+err.Error(), http.StatusBadGateway)
		return
	}

	metaID, _ := result["id"].(string)

	// Save locally
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now()
	campaign := models.MetaAdsCampaign{
		ID:                  primitive.NewObjectID(),
		UserID:              userID,
		OrgID:               orgID,
		MetaCampaignID:      metaID,
		Name:                req.Name,
		Objective:           req.Objective,
		Status:              status,
		BuyingType:          req.BuyingType,
		DailyBudget:         req.DailyBudget,
		LifetimeBudget:      req.LifetimeBudget,
		BidStrategy:         req.BidStrategy,
		SpecialAdCategories: req.SpecialAdCategories,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	database.MetaAdsCampaigns().InsertOne(ctx, campaign)

	slog.Info("meta_ads_campaign_created", "meta_id", metaID, "user_id", userID.Hex())

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":      metaID,
		"name":    req.Name,
		"status":  status,
		"message": "Campaign created",
	})
}

func GetMetaAdsCampaign(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}

	campaignID := r.PathValue("id")
	if campaignID == "" {
		http.Error(w, "Campaign ID required", http.StatusBadRequest)
		return
	}

	params := url.Values{}
	params.Set("fields", "id,name,objective,status,daily_budget,lifetime_budget,bid_strategy,buying_type,special_ad_categories,created_time,updated_time")

	result, err := metaGraphGet("/"+campaignID, creds.Token, params)
	if err != nil {
		http.Error(w, "Error fetching campaign: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(result)
}

func UpdateMetaAdsCampaign(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}
	orgID := middleware.GetOrgID(r)

	campaignID := r.PathValue("id")
	if campaignID == "" {
		http.Error(w, "Campaign ID required", http.StatusBadRequest)
		return
	}

	var req models.UpdateCampaignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	params := url.Values{}
	if req.Name != nil {
		params.Set("name", *req.Name)
	}
	if req.Status != nil {
		params.Set("status", *req.Status)
	}
	if req.DailyBudget != nil {
		params.Set("daily_budget", fmt.Sprintf("%d", *req.DailyBudget))
	}
	if req.LifetimeBudget != nil {
		params.Set("lifetime_budget", fmt.Sprintf("%d", *req.LifetimeBudget))
	}
	if req.BidStrategy != nil {
		params.Set("bid_strategy", *req.BidStrategy)
	}

	_, err := metaGraphPost("/"+campaignID, creds.Token, params)
	if err != nil {
		http.Error(w, "Error updating campaign: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Update local record
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	localUpdate := bson.M{"$set": bson.M{"updated_at": time.Now()}}
	setFields := localUpdate["$set"].(bson.M)
	if req.Name != nil {
		setFields["name"] = *req.Name
	}
	if req.Status != nil {
		setFields["status"] = *req.Status
	}
	if req.DailyBudget != nil {
		setFields["daily_budget"] = *req.DailyBudget
	}
	if req.LifetimeBudget != nil {
		setFields["lifetime_budget"] = *req.LifetimeBudget
	}
	database.MetaAdsCampaigns().UpdateOne(ctx, bson.M{"meta_campaign_id": campaignID, "org_id": orgID}, localUpdate)

	json.NewEncoder(w).Encode(map[string]string{"message": "Campaign updated"})
}

func DeleteMetaAdsCampaign(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}
	orgID := middleware.GetOrgID(r)

	campaignID := r.PathValue("id")
	if campaignID == "" {
		http.Error(w, "Campaign ID required", http.StatusBadRequest)
		return
	}

	_, err := metaGraphDelete("/"+campaignID, creds.Token)
	if err != nil {
		http.Error(w, "Error deleting campaign: "+err.Error(), http.StatusBadGateway)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	database.MetaAdsCampaigns().DeleteOne(ctx, bson.M{"meta_campaign_id": campaignID, "org_id": orgID})

	json.NewEncoder(w).Encode(map[string]string{"message": "Campaign deleted"})
}

func UpdateMetaAdsCampaignStatus(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}
	orgID := middleware.GetOrgID(r)

	campaignID := r.PathValue("id")
	if campaignID == "" {
		http.Error(w, "Campaign ID required", http.StatusBadRequest)
		return
	}

	var req models.UpdateStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Status == "" {
		http.Error(w, "status is required", http.StatusBadRequest)
		return
	}

	params := url.Values{}
	params.Set("status", req.Status)

	_, err := metaGraphPost("/"+campaignID, creds.Token, params)
	if err != nil {
		http.Error(w, "Error updating status: "+err.Error(), http.StatusBadGateway)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	database.MetaAdsCampaigns().UpdateOne(ctx,
		bson.M{"meta_campaign_id": campaignID, "org_id": orgID},
		bson.M{"$set": bson.M{"status": req.Status, "updated_at": time.Now()}},
	)

	json.NewEncoder(w).Encode(map[string]string{"message": "Status updated", "status": req.Status})
}

// ══════════════════════════════════════════════════════════════════════
// AD SETS
// ══════════════════════════════════════════════════════════════════════

func ListMetaAdsAdSets(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}

	params := url.Values{}
	params.Set("fields", "id,name,campaign_id,status,daily_budget,lifetime_budget,bid_amount,billing_event,optimization_goal,targeting,start_time,end_time,created_time")
	params.Set("limit", "100")

	if campaignID := r.URL.Query().Get("campaign_id"); campaignID != "" {
		// List ad sets for a specific campaign
		result, err := metaGraphGet("/"+campaignID+"/adsets", creds.Token, params)
		if err != nil {
			http.Error(w, "Error fetching ad sets: "+err.Error(), http.StatusBadGateway)
			return
		}
		json.NewEncoder(w).Encode(result)
		return
	}

	result, err := metaGraphGet(adAccountPath(creds.AdAccountID)+"/adsets", creds.Token, params)
	if err != nil {
		http.Error(w, "Error fetching ad sets: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(result)
}

func CreateMetaAdsAdSet(w http.ResponseWriter, r *http.Request) {
	userID, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}
	orgID := middleware.GetOrgID(r)

	var req models.CreateAdSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.CampaignID == "" || req.Name == "" {
		http.Error(w, "campaign_id and name are required", http.StatusBadRequest)
		return
	}

	params := url.Values{}
	params.Set("campaign_id", req.CampaignID)
	params.Set("name", req.Name)
	params.Set("billing_event", req.BillingEvent)
	params.Set("optimization_goal", req.OptimizationGoal)

	status := "PAUSED"
	if req.Status != "" {
		status = req.Status
	}
	params.Set("status", status)

	if req.DailyBudget > 0 {
		params.Set("daily_budget", fmt.Sprintf("%d", req.DailyBudget))
	}
	if req.LifetimeBudget > 0 {
		params.Set("lifetime_budget", fmt.Sprintf("%d", req.LifetimeBudget))
	}
	if req.BidAmount > 0 {
		params.Set("bid_amount", fmt.Sprintf("%d", req.BidAmount))
	}
	if req.StartTime != "" {
		params.Set("start_time", req.StartTime)
	}
	if req.EndTime != "" {
		params.Set("end_time", req.EndTime)
	}

	// Build targeting JSON
	targetingJSON, _ := json.Marshal(req.Targeting)
	params.Set("targeting", string(targetingJSON))

	result, err := metaGraphPost(adAccountPath(creds.AdAccountID)+"/adsets", creds.Token, params)
	if err != nil {
		slog.Error("meta_ads_create_adset_error", "error", err)
		http.Error(w, "Error creating ad set: "+err.Error(), http.StatusBadGateway)
		return
	}

	metaID, _ := result["id"].(string)

	// Save locally
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now()
	adset := models.MetaAdsAdSet{
		ID:               primitive.NewObjectID(),
		UserID:           userID,
		OrgID:            orgID,
		MetaAdSetID:      metaID,
		CampaignID:       req.CampaignID,
		Name:             req.Name,
		Status:           status,
		DailyBudget:      req.DailyBudget,
		LifetimeBudget:   req.LifetimeBudget,
		BidAmount:        req.BidAmount,
		BillingEvent:     req.BillingEvent,
		OptimizationGoal: req.OptimizationGoal,
		StartTime:        req.StartTime,
		EndTime:          req.EndTime,
		Targeting:        req.Targeting,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	database.MetaAdsAdSets().InsertOne(ctx, adset)

	slog.Info("meta_ads_adset_created", "meta_id", metaID, "user_id", userID.Hex())

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":      metaID,
		"name":    req.Name,
		"message": "Ad set created",
	})
}

func GetMetaAdsAdSet(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}

	adsetID := r.PathValue("id")
	if adsetID == "" {
		http.Error(w, "Ad Set ID required", http.StatusBadRequest)
		return
	}

	params := url.Values{}
	params.Set("fields", "id,name,campaign_id,status,daily_budget,lifetime_budget,bid_amount,billing_event,optimization_goal,targeting,start_time,end_time")

	result, err := metaGraphGet("/"+adsetID, creds.Token, params)
	if err != nil {
		http.Error(w, "Error fetching ad set: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(result)
}

func UpdateMetaAdsAdSet(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}
	orgID := middleware.GetOrgID(r)

	adsetID := r.PathValue("id")
	if adsetID == "" {
		http.Error(w, "Ad Set ID required", http.StatusBadRequest)
		return
	}

	var req models.UpdateAdSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	params := url.Values{}
	if req.Name != nil {
		params.Set("name", *req.Name)
	}
	if req.Status != nil {
		params.Set("status", *req.Status)
	}
	if req.DailyBudget != nil {
		params.Set("daily_budget", fmt.Sprintf("%d", *req.DailyBudget))
	}
	if req.LifetimeBudget != nil {
		params.Set("lifetime_budget", fmt.Sprintf("%d", *req.LifetimeBudget))
	}
	if req.BidAmount != nil {
		params.Set("bid_amount", fmt.Sprintf("%d", *req.BidAmount))
	}
	if req.StartTime != nil {
		params.Set("start_time", *req.StartTime)
	}
	if req.EndTime != nil {
		params.Set("end_time", *req.EndTime)
	}
	if req.Targeting != nil {
		targetingJSON, _ := json.Marshal(req.Targeting)
		params.Set("targeting", string(targetingJSON))
	}

	_, err := metaGraphPost("/"+adsetID, creds.Token, params)
	if err != nil {
		http.Error(w, "Error updating ad set: "+err.Error(), http.StatusBadGateway)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	database.MetaAdsAdSets().UpdateOne(ctx,
		bson.M{"meta_adset_id": adsetID, "org_id": orgID},
		bson.M{"$set": bson.M{"updated_at": time.Now()}},
	)

	json.NewEncoder(w).Encode(map[string]string{"message": "Ad set updated"})
}

func DeleteMetaAdsAdSet(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}
	orgID := middleware.GetOrgID(r)

	adsetID := r.PathValue("id")
	if adsetID == "" {
		http.Error(w, "Ad Set ID required", http.StatusBadRequest)
		return
	}

	_, err := metaGraphDelete("/"+adsetID, creds.Token)
	if err != nil {
		http.Error(w, "Error deleting ad set: "+err.Error(), http.StatusBadGateway)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	database.MetaAdsAdSets().DeleteOne(ctx, bson.M{"meta_adset_id": adsetID, "org_id": orgID})

	json.NewEncoder(w).Encode(map[string]string{"message": "Ad set deleted"})
}

func UpdateMetaAdsAdSetStatus(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}
	orgID := middleware.GetOrgID(r)

	adsetID := r.PathValue("id")
	if adsetID == "" {
		http.Error(w, "Ad Set ID required", http.StatusBadRequest)
		return
	}

	var req models.UpdateStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Status == "" {
		http.Error(w, "status is required", http.StatusBadRequest)
		return
	}

	params := url.Values{}
	params.Set("status", req.Status)

	_, err := metaGraphPost("/"+adsetID, creds.Token, params)
	if err != nil {
		http.Error(w, "Error updating status: "+err.Error(), http.StatusBadGateway)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	database.MetaAdsAdSets().UpdateOne(ctx,
		bson.M{"meta_adset_id": adsetID, "org_id": orgID},
		bson.M{"$set": bson.M{"status": req.Status, "updated_at": time.Now()}},
	)

	json.NewEncoder(w).Encode(map[string]string{"message": "Status updated", "status": req.Status})
}

// ══════════════════════════════════════════════════════════════════════
// ADS
// ══════════════════════════════════════════════════════════════════════

func ListMetaAdsAds(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}

	params := url.Values{}
	params.Set("fields", "id,name,adset_id,status,creative{id,name,title,body,image_url,thumbnail_url,call_to_action_type,link_url},created_time")
	params.Set("limit", "100")

	if adsetID := r.URL.Query().Get("adset_id"); adsetID != "" {
		result, err := metaGraphGet("/"+adsetID+"/ads", creds.Token, params)
		if err != nil {
			http.Error(w, "Error fetching ads: "+err.Error(), http.StatusBadGateway)
			return
		}
		json.NewEncoder(w).Encode(result)
		return
	}

	result, err := metaGraphGet(adAccountPath(creds.AdAccountID)+"/ads", creds.Token, params)
	if err != nil {
		http.Error(w, "Error fetching ads: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(result)
}

func CreateMetaAdsAd(w http.ResponseWriter, r *http.Request) {
	userID, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}
	orgID := middleware.GetOrgID(r)

	var req models.CreateAdRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.AdSetID == "" || req.Name == "" {
		http.Error(w, "adset_id and name are required", http.StatusBadRequest)
		return
	}

	// First create the ad creative
	creativeParams := url.Values{}
	creativeParams.Set("name", req.Name+" Creative")

	// Build object_story_spec based on format
	objectStorySpec := buildObjectStorySpec(creds, req.Creative)
	specJSON, _ := json.Marshal(objectStorySpec)
	creativeParams.Set("object_story_spec", string(specJSON))

	creativeResult, err := metaGraphPost(adAccountPath(creds.AdAccountID)+"/adcreatives", creds.Token, creativeParams)
	if err != nil {
		slog.Error("meta_ads_create_creative_error", "error", err)
		http.Error(w, "Error creating ad creative: "+err.Error(), http.StatusBadGateway)
		return
	}

	creativeID, _ := creativeResult["id"].(string)

	// Now create the ad
	adParams := url.Values{}
	adParams.Set("name", req.Name)
	adParams.Set("adset_id", req.AdSetID)

	status := "PAUSED"
	if req.Status != "" {
		status = req.Status
	}
	adParams.Set("status", status)
	adParams.Set("creative", fmt.Sprintf(`{"creative_id":"%s"}`, creativeID))

	adResult, err := metaGraphPost(adAccountPath(creds.AdAccountID)+"/ads", creds.Token, adParams)
	if err != nil {
		slog.Error("meta_ads_create_ad_error", "error", err)
		http.Error(w, "Error creating ad: "+err.Error(), http.StatusBadGateway)
		return
	}

	metaAdID, _ := adResult["id"].(string)

	// Save locally
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now()
	ad := models.MetaAdsAd{
		ID:        primitive.NewObjectID(),
		UserID:    userID,
		OrgID:     orgID,
		MetaAdID:  metaAdID,
		AdSetID:   req.AdSetID,
		Name:      req.Name,
		Status:    status,
		Creative:  req.Creative,
		CreatedAt: now,
		UpdatedAt: now,
	}
	database.MetaAdsAds().InsertOne(ctx, ad)

	slog.Info("meta_ads_ad_created", "meta_id", metaAdID, "user_id", userID.Hex())

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":          metaAdID,
		"creative_id": creativeID,
		"name":        req.Name,
		"message":     "Ad created",
	})
}

func buildObjectStorySpec(creds *metaAdsCredentials, creative models.AdCreative) map[string]interface{} {
	spec := map[string]interface{}{}

	pageID := creds.BusinessID
	if pageID == "" {
		pageID = creds.AdAccountID
	}

	switch creative.Format {
	case "video":
		videoData := map[string]interface{}{
			"video_id": creative.VideoID,
			"message":  creative.Body,
		}
		if creative.Title != "" {
			videoData["title"] = creative.Title
		}
		if creative.CallToAction != "" {
			videoData["call_to_action"] = map[string]interface{}{
				"type":  creative.CallToAction,
				"value": map[string]string{"link": creative.LinkURL},
			}
		}
		spec["page_id"] = pageID
		spec["video_data"] = videoData

	case "carousel":
		childAttachments := make([]map[string]interface{}, len(creative.CarouselCards))
		for i, card := range creative.CarouselCards {
			attachment := map[string]interface{}{}
			if card.ImageHash != "" {
				attachment["image_hash"] = card.ImageHash
			}
			if card.Title != "" {
				attachment["name"] = card.Title
			}
			if card.Description != "" {
				attachment["description"] = card.Description
			}
			if card.LinkURL != "" {
				attachment["link"] = card.LinkURL
			}
			childAttachments[i] = attachment
		}
		spec["page_id"] = pageID
		spec["link_data"] = map[string]interface{}{
			"message":           creative.Body,
			"link":              creative.LinkURL,
			"child_attachments": childAttachments,
		}

	default: // "image"
		linkData := map[string]interface{}{
			"message": creative.Body,
			"link":    creative.LinkURL,
		}
		if creative.ImageHash != "" {
			linkData["image_hash"] = creative.ImageHash
		}
		if creative.Title != "" {
			linkData["name"] = creative.Title
		}
		if creative.Description != "" {
			linkData["description"] = creative.Description
		}
		if creative.CallToAction != "" {
			linkData["call_to_action"] = map[string]interface{}{
				"type":  creative.CallToAction,
				"value": map[string]string{"link": creative.LinkURL},
			}
		}
		spec["page_id"] = pageID
		spec["link_data"] = linkData
	}

	return spec
}

func GetMetaAdsAd(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}

	adID := r.PathValue("id")
	if adID == "" {
		http.Error(w, "Ad ID required", http.StatusBadRequest)
		return
	}

	params := url.Values{}
	params.Set("fields", "id,name,adset_id,status,creative{id,name,title,body,image_url,thumbnail_url,call_to_action_type,link_url}")

	result, err := metaGraphGet("/"+adID, creds.Token, params)
	if err != nil {
		http.Error(w, "Error fetching ad: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(result)
}

func UpdateMetaAdsAd(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}
	orgID := middleware.GetOrgID(r)

	adID := r.PathValue("id")
	if adID == "" {
		http.Error(w, "Ad ID required", http.StatusBadRequest)
		return
	}

	var req models.UpdateAdRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	params := url.Values{}
	if req.Name != nil {
		params.Set("name", *req.Name)
	}
	if req.Status != nil {
		params.Set("status", *req.Status)
	}

	_, err := metaGraphPost("/"+adID, creds.Token, params)
	if err != nil {
		http.Error(w, "Error updating ad: "+err.Error(), http.StatusBadGateway)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	database.MetaAdsAds().UpdateOne(ctx,
		bson.M{"meta_ad_id": adID, "org_id": orgID},
		bson.M{"$set": bson.M{"updated_at": time.Now()}},
	)

	json.NewEncoder(w).Encode(map[string]string{"message": "Ad updated"})
}

func DeleteMetaAdsAd(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}
	orgID := middleware.GetOrgID(r)

	adID := r.PathValue("id")
	if adID == "" {
		http.Error(w, "Ad ID required", http.StatusBadRequest)
		return
	}

	_, err := metaGraphDelete("/"+adID, creds.Token)
	if err != nil {
		http.Error(w, "Error deleting ad: "+err.Error(), http.StatusBadGateway)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	database.MetaAdsAds().DeleteOne(ctx, bson.M{"meta_ad_id": adID, "org_id": orgID})

	json.NewEncoder(w).Encode(map[string]string{"message": "Ad deleted"})
}

func UpdateMetaAdsAdStatus(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}
	orgID := middleware.GetOrgID(r)

	adID := r.PathValue("id")
	if adID == "" {
		http.Error(w, "Ad ID required", http.StatusBadRequest)
		return
	}

	var req models.UpdateStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Status == "" {
		http.Error(w, "status is required", http.StatusBadRequest)
		return
	}

	params := url.Values{}
	params.Set("status", req.Status)

	_, err := metaGraphPost("/"+adID, creds.Token, params)
	if err != nil {
		http.Error(w, "Error updating status: "+err.Error(), http.StatusBadGateway)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	database.MetaAdsAds().UpdateOne(ctx,
		bson.M{"meta_ad_id": adID, "org_id": orgID},
		bson.M{"$set": bson.M{"status": req.Status, "updated_at": time.Now()}},
	)

	json.NewEncoder(w).Encode(map[string]string{"message": "Status updated", "status": req.Status})
}

// ══════════════════════════════════════════════════════════════════════
// UPLOAD (images & videos)
// ══════════════════════════════════════════════════════════════════════

func UploadMetaAdsImage(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 30<<20) // 30MB
	if err := r.ParseMultipartForm(30 << 20); err != nil {
		http.Error(w, "File too large (max 30MB)", http.StatusRequestEntityTooLarge)
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "No image provided. Use field name 'image'", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Upload to Meta Ads API
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	writer.WriteField("access_token", creds.Token)

	part, err := writer.CreateFormFile("filename", header.Filename)
	if err != nil {
		http.Error(w, "Error preparing upload", http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(part, file); err != nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}
	writer.Close()

	apiURL := metaAdsAPIBase + adAccountPath(creds.AdAccountID) + "/adimages"
	resp, err := http.Post(apiURL, writer.FormDataContentType(), &buf)
	if err != nil {
		http.Error(w, "Error uploading to Meta: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		http.Error(w, "Error parsing Meta response", http.StatusBadGateway)
		return
	}

	if errObj, ok := result["error"]; ok {
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": errObj})
		return
	}

	json.NewEncoder(w).Encode(result)
}

func UploadMetaAdsVideo(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 100<<20) // 100MB
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		http.Error(w, "File too large (max 100MB)", http.StatusRequestEntityTooLarge)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		http.Error(w, "No video provided. Use field name 'video'", http.StatusBadRequest)
		return
	}
	defer file.Close()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	writer.WriteField("access_token", creds.Token)
	if title := r.FormValue("title"); title != "" {
		writer.WriteField("title", title)
	}

	part, err := writer.CreateFormFile("source", header.Filename)
	if err != nil {
		http.Error(w, "Error preparing upload", http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(part, file); err != nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}
	writer.Close()

	apiURL := metaAdsAPIBase + adAccountPath(creds.AdAccountID) + "/advideos"
	resp, err := http.Post(apiURL, writer.FormDataContentType(), &buf)
	if err != nil {
		http.Error(w, "Error uploading to Meta: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		http.Error(w, "Error parsing Meta response", http.StatusBadGateway)
		return
	}

	if errObj, ok := result["error"]; ok {
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": errObj})
		return
	}

	json.NewEncoder(w).Encode(result)
}

// ══════════════════════════════════════════════════════════════════════
// INSIGHTS
// ══════════════════════════════════════════════════════════════════════

func GetMetaAdsInsights(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}

	level := r.URL.Query().Get("level")
	if level == "" {
		level = "account"
	}

	params := url.Values{}
	params.Set("fields", "impressions,reach,clicks,spend,ctr,cpc,cpm,actions,campaign_name,adset_name,ad_name")
	params.Set("level", level)

	if ti := r.URL.Query().Get("time_increment"); ti != "" {
		params.Set("time_increment", ti)
		params.Set("limit", "100")
	}

	if dateStart := r.URL.Query().Get("date_start"); dateStart != "" {
		if dateStop := r.URL.Query().Get("date_stop"); dateStop != "" {
			params.Set("time_range", fmt.Sprintf(`{"since":"%s","until":"%s"}`, dateStart, dateStop))
		}
	} else {
		// Default: last 30 days
		now := time.Now()
		params.Set("time_range", fmt.Sprintf(`{"since":"%s","until":"%s"}`,
			now.AddDate(0, 0, -30).Format("2006-01-02"),
			now.Format("2006-01-02"),
		))
	}

	result, err := metaGraphGet(adAccountPath(creds.AdAccountID)+"/insights", creds.Token, params)
	if err != nil {
		http.Error(w, "Error fetching insights: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(result)
}

func GetMetaAdsCampaignInsights(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}

	campaignID := r.PathValue("id")
	if campaignID == "" {
		http.Error(w, "Campaign ID required", http.StatusBadRequest)
		return
	}

	params := url.Values{}
	params.Set("fields", "impressions,reach,clicks,spend,ctr,cpc,cpm,actions")

	if dateStart := r.URL.Query().Get("date_start"); dateStart != "" {
		if dateStop := r.URL.Query().Get("date_stop"); dateStop != "" {
			params.Set("time_range", fmt.Sprintf(`{"since":"%s","until":"%s"}`, dateStart, dateStop))
		}
	} else {
		// Default: last 30 days
		now := time.Now()
		params.Set("time_range", fmt.Sprintf(`{"since":"%s","until":"%s"}`,
			now.AddDate(0, 0, -30).Format("2006-01-02"),
			now.Format("2006-01-02"),
		))
	}

	result, err := metaGraphGet("/"+campaignID+"/insights", creds.Token, params)
	if err != nil {
		http.Error(w, "Error fetching insights: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(result)
}

// ══════════════════════════════════════════════════════════════════════
// ACCOUNT FINANCE
// ══════════════════════════════════════════════════════════════════════

func GetMetaAdsAccountFinance(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}

	accountPath := adAccountPath(creds.AdAccountID)

	// Fetch account-level financial fields
	acctParams := url.Values{}
	acctParams.Set("fields", "name,account_status,spend_cap,amount_spent,balance,currency")

	acctResult, err := metaGraphGet(accountPath, creds.Token, acctParams)
	if err != nil {
		http.Error(w, "Error fetching account info: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Fetch today's spend
	todayParams := url.Values{}
	todayParams.Set("fields", "spend")
	todayParams.Set("date_preset", "today")

	todayResult, err := metaGraphGet(accountPath+"/insights", creds.Token, todayParams)
	if err != nil {
		slog.Warn("meta_ads_finance_today_insights", "error", err)
	}

	// Fetch this month's spend
	monthParams := url.Values{}
	monthParams.Set("fields", "spend")
	monthParams.Set("date_preset", "this_month")

	monthResult, err := metaGraphGet(accountPath+"/insights", creds.Token, monthParams)
	if err != nil {
		slog.Warn("meta_ads_finance_month_insights", "error", err)
	}

	// Parse cents-string values from account object
	parseCents := func(v interface{}) float64 {
		s, _ := v.(string)
		if s == "" {
			return 0
		}
		f, _ := strconv.ParseFloat(s, 64)
		return f / 100.0
	}

	spendCap := parseCents(acctResult["spend_cap"])
	amountSpent := parseCents(acctResult["amount_spent"])
	balance := parseCents(acctResult["balance"])

	hasSpendCap := spendCap > 0
	remaining := -1.0
	if hasSpendCap {
		remaining = spendCap - amountSpent
	}

	// Extract spend from insights data arrays
	extractSpend := func(result map[string]interface{}) float64 {
		if result == nil {
			return 0
		}
		dataRaw, ok := result["data"]
		if !ok {
			return 0
		}
		dataArr, ok := dataRaw.([]interface{})
		if !ok || len(dataArr) == 0 {
			return 0
		}
		row, ok := dataArr[0].(map[string]interface{})
		if !ok {
			return 0
		}
		s, _ := row["spend"].(string)
		f, _ := strconv.ParseFloat(s, 64)
		return f
	}

	spendToday := extractSpend(todayResult)
	spendMonth := extractSpend(monthResult)

	resp := map[string]interface{}{
		"name":            acctResult["name"],
		"currency":        acctResult["currency"],
		"account_status":  acctResult["account_status"],
		"spend_cap":       spendCap,
		"amount_spent":    amountSpent,
		"balance":         balance,
		"has_spend_cap":   hasSpendCap,
		"remaining":       remaining,
		"spend_today":     spendToday,
		"spend_this_month": spendMonth,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ══════════════════════════════════════════════════════════════════════
// ACCOUNT RECOMMENDATIONS (opportunity score + recommendations)
// ══════════════════════════════════════════════════════════════════════

func GetMetaAdsRecommendations(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}

	accountPath := adAccountPath(creds.AdAccountID)

	// Fetch opportunity_score from ad account
	scoreParams := url.Values{}
	scoreParams.Set("fields", "opportunity_score")

	scoreResult, err := metaGraphGet(accountPath, creds.Token, scoreParams)
	if err != nil {
		slog.Warn("meta_ads_opportunity_score", "error", err)
	}

	// Fetch recommendations
	recsResult, err := metaGraphGet(accountPath+"/recommendations", creds.Token, nil)
	if err != nil {
		slog.Warn("meta_ads_recommendations", "error", err)
	}

	// Parse opportunity_score (float 0-100)
	var opportunityScore float64
	if scoreResult != nil {
		if v, ok := scoreResult["opportunity_score"].(float64); ok {
			opportunityScore = v
		}
	}

	// Parse recommendations array
	var recommendations []interface{}
	if recsResult != nil {
		if data, ok := recsResult["data"].([]interface{}); ok {
			recommendations = data
		}
	}
	if recommendations == nil {
		recommendations = []interface{}{}
	}

	resp := map[string]interface{}{
		"opportunity_score": opportunityScore,
		"recommendations":  recommendations,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ══════════════════════════════════════════════════════════════════════
// TARGETING (search interests, locations, audiences)
// ══════════════════════════════════════════════════════════════════════

func SearchMetaAdsInterests(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}

	q := r.URL.Query().Get("q")
	if q == "" {
		http.Error(w, "q parameter required", http.StatusBadRequest)
		return
	}

	params := url.Values{}
	params.Set("type", "adinterest")
	params.Set("q", q)
	params.Set("limit", "25")

	result, err := metaGraphGet("/search", creds.Token, params)
	if err != nil {
		http.Error(w, "Error searching interests: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(result)
}

func SearchMetaAdsLocations(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}

	q := r.URL.Query().Get("q")
	if q == "" {
		http.Error(w, "q parameter required", http.StatusBadRequest)
		return
	}

	locType := r.URL.Query().Get("type")
	if locType == "" {
		locType = "adgeolocation"
	}

	params := url.Values{}
	params.Set("type", locType)
	params.Set("q", q)
	params.Set("limit", "25")

	result, err := metaGraphGet("/search", creds.Token, params)
	if err != nil {
		http.Error(w, "Error searching locations: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(result)
}

func ListMetaAdsAudiences(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}

	params := url.Values{}
	params.Set("fields", "id,name,approximate_count,subtype,description")

	result, err := metaGraphGet(adAccountPath(creds.AdAccountID)+"/customaudiences", creds.Token, params)
	if err != nil {
		http.Error(w, "Error fetching audiences: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(result)
}

// ══════════════════════════════════════════════════════════════════════
// TARGETING PRESETS
// ══════════════════════════════════════════════════════════════════════

func ListMetaAdsPresets(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cursor, err := database.MetaAdsTargetingPresets().Find(ctx,
		bson.M{"org_id": orgID},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}),
	)
	if err != nil {
		http.Error(w, "Error fetching presets", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var presets []models.TargetingPreset
	if err := cursor.All(ctx, &presets); err != nil {
		http.Error(w, "Error decoding presets", http.StatusInternalServerError)
		return
	}

	if presets == nil {
		presets = []models.TargetingPreset{}
	}

	json.NewEncoder(w).Encode(presets)
}

func CreateMetaAdsPreset(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	orgID := middleware.GetOrgID(r)

	var req models.CreateTargetingPresetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	preset := models.TargetingPreset{
		ID:        primitive.NewObjectID(),
		UserID:    userID,
		OrgID:     orgID,
		Name:      req.Name,
		Targeting: req.Targeting,
		CreatedAt: time.Now(),
	}

	_, err := database.MetaAdsTargetingPresets().InsertOne(ctx, preset)
	if err != nil {
		http.Error(w, "Error creating preset", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(preset)
}

func DeleteMetaAdsPreset(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	orgID := middleware.GetOrgID(r)

	presetID := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(presetID)
	if err != nil {
		http.Error(w, "Invalid preset ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := database.MetaAdsTargetingPresets().DeleteOne(ctx, bson.M{"_id": oid, "org_id": orgID})
	if err != nil {
		http.Error(w, "Error deleting preset", http.StatusInternalServerError)
		return
	}

	if result.DeletedCount == 0 {
		http.Error(w, "Preset not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"message": "Preset deleted"})
}

// ══════════════════════════════════════════════════════════════════════
// CAMPAIGN TEMPLATES
// ══════════════════════════════════════════════════════════════════════

func ListMetaAdsTemplates(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cursor, err := database.MetaAdsCampaignTemplates().Find(ctx,
		bson.M{"org_id": orgID},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}),
	)
	if err != nil {
		http.Error(w, "Error fetching templates", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var templates []models.CampaignTemplate
	if err := cursor.All(ctx, &templates); err != nil {
		http.Error(w, "Error decoding templates", http.StatusInternalServerError)
		return
	}

	if templates == nil {
		templates = []models.CampaignTemplate{}
	}

	json.NewEncoder(w).Encode(templates)
}

func CreateMetaAdsTemplate(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	orgID := middleware.GetOrgID(r)

	var req models.CreateCampaignTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tpl := models.CampaignTemplate{
		ID:               primitive.NewObjectID(),
		UserID:           userID,
		OrgID:            orgID,
		Name:             req.Name,
		Objective:        req.Objective,
		BuyingType:       req.BuyingType,
		BidStrategy:      req.BidStrategy,
		DailyBudget:      req.DailyBudget,
		LifetimeBudget:   req.LifetimeBudget,
		Targeting:        req.Targeting,
		BillingEvent:     req.BillingEvent,
		OptimizationGoal: req.OptimizationGoal,
		CreatedAt:        time.Now(),
	}

	_, err := database.MetaAdsCampaignTemplates().InsertOne(ctx, tpl)
	if err != nil {
		http.Error(w, "Error creating template", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(tpl)
}

func DeleteMetaAdsTemplate(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	orgID := middleware.GetOrgID(r)

	tplID := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(tplID)
	if err != nil {
		http.Error(w, "Invalid template ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := database.MetaAdsCampaignTemplates().DeleteOne(ctx, bson.M{"_id": oid, "org_id": orgID})
	if err != nil {
		http.Error(w, "Error deleting template", http.StatusInternalServerError)
		return
	}

	if result.DeletedCount == 0 {
		http.Error(w, "Template not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"message": "Template deleted"})
}

// ══════════════════════════════════════════════════════════════════════
// BUDGET ALERTS
// ══════════════════════════════════════════════════════════════════════

func ListMetaAdsBudgetAlerts(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cursor, err := database.MetaAdsBudgetAlerts().Find(ctx,
		bson.M{"org_id": orgID},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}),
	)
	if err != nil {
		http.Error(w, "Error fetching alerts", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var alerts []models.BudgetAlert
	if err := cursor.All(ctx, &alerts); err != nil {
		http.Error(w, "Error decoding alerts", http.StatusInternalServerError)
		return
	}

	if alerts == nil {
		alerts = []models.BudgetAlert{}
	}

	json.NewEncoder(w).Encode(alerts)
}

func CreateMetaAdsBudgetAlert(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	orgID := middleware.GetOrgID(r)

	var req models.CreateBudgetAlertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.AlertType == "" || req.Threshold <= 0 {
		http.Error(w, "alert_type and threshold (>0) are required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now()
	alert := models.BudgetAlert{
		ID:         primitive.NewObjectID(),
		UserID:     userID,
		OrgID:      orgID,
		CampaignID: req.CampaignID,
		AlertType:  req.AlertType,
		Threshold:  req.Threshold,
		Active:     true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	_, err := database.MetaAdsBudgetAlerts().InsertOne(ctx, alert)
	if err != nil {
		http.Error(w, "Error creating alert", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(alert)
}

func UpdateMetaAdsBudgetAlert(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	orgID := middleware.GetOrgID(r)

	alertID := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(alertID)
	if err != nil {
		http.Error(w, "Invalid alert ID", http.StatusBadRequest)
		return
	}

	var req models.UpdateBudgetAlertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := bson.M{"$set": bson.M{"updated_at": time.Now()}}
	setFields := update["$set"].(bson.M)

	if req.AlertType != nil {
		setFields["alert_type"] = *req.AlertType
	}
	if req.Threshold != nil {
		setFields["threshold"] = *req.Threshold
	}
	if req.Active != nil {
		setFields["active"] = *req.Active
	}

	result, err := database.MetaAdsBudgetAlerts().UpdateOne(ctx, bson.M{"_id": oid, "org_id": orgID}, update)
	if err != nil {
		http.Error(w, "Error updating alert", http.StatusInternalServerError)
		return
	}

	if result.MatchedCount == 0 {
		http.Error(w, "Alert not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"message": "Alert updated"})
}

func DeleteMetaAdsBudgetAlert(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	orgID := middleware.GetOrgID(r)

	alertID := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(alertID)
	if err != nil {
		http.Error(w, "Invalid alert ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := database.MetaAdsBudgetAlerts().DeleteOne(ctx, bson.M{"_id": oid, "org_id": orgID})
	if err != nil {
		http.Error(w, "Error deleting alert", http.StatusInternalServerError)
		return
	}

	if result.DeletedCount == 0 {
		http.Error(w, "Alert not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"message": "Alert deleted"})
}

// CheckBudgetAlerts is called periodically by a goroutine to check spend against thresholds.
func CheckBudgetAlerts() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cursor, err := database.MetaAdsBudgetAlerts().Find(ctx, bson.M{"active": true})
	if err != nil {
		slog.Error("budget_alert_query_error", "error", err)
		return
	}
	defer cursor.Close(ctx)

	var alerts []models.BudgetAlert
	if err := cursor.All(ctx, &alerts); err != nil {
		slog.Error("budget_alert_decode_error", "error", err)
		return
	}

	for _, alert := range alerts {
		// Skip if triggered within last hour
		if alert.LastTriggered != nil && time.Since(*alert.LastTriggered) < time.Hour {
			continue
		}

		creds, err := getMetaAdsCredentials(ctx, alert.UserID, alert.OrgID)
		if err != nil || creds == nil {
			continue
		}

		params := url.Values{}
		params.Set("fields", "spend")

		endpoint := adAccountPath(creds.AdAccountID) + "/insights"
		if alert.CampaignID != "" {
			endpoint = "/" + alert.CampaignID + "/insights"
		}

		now := time.Now()
		switch alert.AlertType {
		case "daily_spend":
			today := now.Format("2006-01-02")
			params.Set("time_range", fmt.Sprintf(`{"since":"%s","until":"%s"}`, today, today))
		case "total_spend":
			// Last 30 days
			params.Set("time_range", fmt.Sprintf(`{"since":"%s","until":"%s"}`,
				now.AddDate(0, 0, -30).Format("2006-01-02"),
				now.Format("2006-01-02"),
			))
		}

		result, err := metaGraphGet(endpoint, creds.Token, params)
		if err != nil {
			continue
		}

		data, ok := result["data"].([]interface{})
		if !ok || len(data) == 0 {
			continue
		}

		firstRow, ok := data[0].(map[string]interface{})
		if !ok {
			continue
		}

		spendStr, _ := firstRow["spend"].(string)
		var spend float64
		fmt.Sscanf(spendStr, "%f", &spend)

		if spend >= alert.Threshold {
			slog.Warn("budget_alert_triggered",
				"alert_id", alert.ID.Hex(),
				"user_id", alert.UserID.Hex(),
				"type", alert.AlertType,
				"threshold", alert.Threshold,
				"spend", spend,
			)

			now := time.Now()
			database.MetaAdsBudgetAlerts().UpdateOne(ctx,
				bson.M{"_id": alert.ID},
				bson.M{"$set": bson.M{"last_triggered": now, "updated_at": now}},
			)
		}
	}
}
