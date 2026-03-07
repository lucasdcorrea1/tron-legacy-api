package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ══════════════════════════════════════════════════════════════════════
// AUTO-BOOST RULES CRUD
// ══════════════════════════════════════════════════════════════════════

func ListAutoBoostRules(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	cursor, err := database.AutoBoostRules().Find(ctx, bson.M{"user_id": userID}, opts)
	if err != nil {
		slog.Error("auto_boost_list_rules_error", "error", err)
		http.Error(w, "Error listing rules", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var rules []models.AutoBoostRule
	if err := cursor.All(ctx, &rules); err != nil {
		slog.Error("auto_boost_decode_rules_error", "error", err)
		http.Error(w, "Error decoding rules", http.StatusInternalServerError)
		return
	}

	if rules == nil {
		rules = []models.AutoBoostRule{}
	}

	json.NewEncoder(w).Encode(rules)
}

func CreateAutoBoostRule(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req models.CreateAutoBoostRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	validMetrics := map[string]bool{"likes": true, "comments": true, "engagement_rate": true}
	if !validMetrics[req.Metric] {
		http.Error(w, "metric must be one of: likes, comments, engagement_rate", http.StatusBadRequest)
		return
	}
	if req.Threshold <= 0 {
		http.Error(w, "threshold must be greater than 0", http.StatusBadRequest)
		return
	}
	if req.DailyBudget <= 0 {
		http.Error(w, "daily_budget must be greater than 0", http.StatusBadRequest)
		return
	}
	if req.DurationDays <= 0 {
		http.Error(w, "duration_days must be greater than 0", http.StatusBadRequest)
		return
	}
	if req.Objective == "" {
		http.Error(w, "objective is required", http.StatusBadRequest)
		return
	}

	// Defaults
	if req.CooldownHours == 0 {
		req.CooldownHours = 72
	}
	if req.MaxPostAgeHours == 0 {
		req.MaxPostAgeHours = 48
	}
	if req.BillingEvent == "" {
		req.BillingEvent = "IMPRESSIONS"
	}
	if req.OptimizationGoal == "" {
		req.OptimizationGoal = "POST_ENGAGEMENT"
	}

	now := time.Now()
	rule := models.AutoBoostRule{
		ID:               primitive.NewObjectID(),
		UserID:           userID,
		Name:             req.Name,
		Active:           true,
		Metric:           req.Metric,
		Threshold:        req.Threshold,
		DailyBudget:      req.DailyBudget,
		DurationDays:     req.DurationDays,
		Targeting:        req.Targeting,
		Objective:        req.Objective,
		OptimizationGoal: req.OptimizationGoal,
		BillingEvent:     req.BillingEvent,
		CallToAction:     req.CallToAction,
		LinkURL:          req.LinkURL,
		CooldownHours:    req.CooldownHours,
		MaxPostAgeHours:  req.MaxPostAgeHours,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := database.AutoBoostRules().InsertOne(ctx, rule); err != nil {
		slog.Error("auto_boost_create_rule_error", "error", err)
		http.Error(w, "Error creating rule", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(rule)
}

func GetAutoBoostRule(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	oid, err := primitive.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var rule models.AutoBoostRule
	err = database.AutoBoostRules().FindOne(ctx, bson.M{"_id": oid, "user_id": userID}).Decode(&rule)
	if err != nil {
		http.Error(w, "Rule not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(rule)
}

func UpdateAutoBoostRule(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	oid, err := primitive.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	var req models.UpdateAutoBoostRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	update := bson.M{"updated_at": time.Now()}
	if req.Name != nil {
		update["name"] = *req.Name
	}
	if req.Active != nil {
		update["active"] = *req.Active
	}
	if req.Metric != nil {
		validMetrics := map[string]bool{"likes": true, "comments": true, "engagement_rate": true}
		if !validMetrics[*req.Metric] {
			http.Error(w, "metric must be one of: likes, comments, engagement_rate", http.StatusBadRequest)
			return
		}
		update["metric"] = *req.Metric
	}
	if req.Threshold != nil {
		update["threshold"] = *req.Threshold
	}
	if req.DailyBudget != nil {
		update["daily_budget"] = *req.DailyBudget
	}
	if req.DurationDays != nil {
		update["duration_days"] = *req.DurationDays
	}
	if req.Targeting != nil {
		update["targeting"] = *req.Targeting
	}
	if req.Objective != nil {
		update["objective"] = *req.Objective
	}
	if req.OptimizationGoal != nil {
		update["optimization_goal"] = *req.OptimizationGoal
	}
	if req.BillingEvent != nil {
		update["billing_event"] = *req.BillingEvent
	}
	if req.CallToAction != nil {
		update["call_to_action"] = *req.CallToAction
	}
	if req.LinkURL != nil {
		update["link_url"] = *req.LinkURL
	}
	if req.CooldownHours != nil {
		update["cooldown_hours"] = *req.CooldownHours
	}
	if req.MaxPostAgeHours != nil {
		update["max_post_age_hours"] = *req.MaxPostAgeHours
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := database.AutoBoostRules().UpdateOne(
		ctx,
		bson.M{"_id": oid, "user_id": userID},
		bson.M{"$set": update},
	)
	if err != nil {
		slog.Error("auto_boost_update_rule_error", "error", err)
		http.Error(w, "Error updating rule", http.StatusInternalServerError)
		return
	}
	if result.MatchedCount == 0 {
		http.Error(w, "Rule not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"message": "Rule updated"})
}

func ToggleAutoBoostRule(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	oid, err := primitive.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	var req struct {
		Active *bool `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Active == nil {
		http.Error(w, "active field is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := database.AutoBoostRules().UpdateOne(
		ctx,
		bson.M{"_id": oid, "user_id": userID},
		bson.M{"$set": bson.M{"active": *req.Active, "updated_at": time.Now()}},
	)
	if err != nil {
		slog.Error("auto_boost_toggle_error", "error", err)
		http.Error(w, "Error toggling rule", http.StatusInternalServerError)
		return
	}
	if result.MatchedCount == 0 {
		http.Error(w, "Rule not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"message": "Rule toggled", "active": *req.Active})
}

func DeleteAutoBoostRule(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	oid, err := primitive.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := database.AutoBoostRules().DeleteOne(ctx, bson.M{"_id": oid, "user_id": userID})
	if err != nil {
		slog.Error("auto_boost_delete_rule_error", "error", err)
		http.Error(w, "Error deleting rule", http.StatusInternalServerError)
		return
	}
	if result.DeletedCount == 0 {
		http.Error(w, "Rule not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"message": "Rule deleted"})
}

// ══════════════════════════════════════════════════════════════════════
// AUTO-BOOST LOGS
// ══════════════════════════════════════════════════════════════════════

func ListAutoBoostLogs(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	filter := bson.M{"user_id": userID}

	// Optional filters
	if ruleID := r.URL.Query().Get("rule_id"); ruleID != "" {
		if oid, err := primitive.ObjectIDFromHex(ruleID); err == nil {
			filter["rule_id"] = oid
		}
	}
	if status := r.URL.Query().Get("status"); status != "" {
		filter["status"] = status
	}

	limit := int64(50)
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.ParseInt(l, 10, 64); err == nil && parsed > 0 {
			limit = parsed
			if limit > 200 {
				limit = 200
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}}).
		SetLimit(limit)

	cursor, err := database.AutoBoostLogs().Find(ctx, filter, opts)
	if err != nil {
		slog.Error("auto_boost_list_logs_error", "error", err)
		http.Error(w, "Error listing logs", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var logs []models.AutoBoostLog
	if err := cursor.All(ctx, &logs); err != nil {
		slog.Error("auto_boost_decode_logs_error", "error", err)
		http.Error(w, "Error decoding logs", http.StatusInternalServerError)
		return
	}

	if logs == nil {
		logs = []models.AutoBoostLog{}
	}

	json.NewEncoder(w).Encode(logs)
}

// ══════════════════════════════════════════════════════════════════════
// BACKGROUND JOB — ProcessAutoBoosts
// ══════════════════════════════════════════════════════════════════════

func ProcessAutoBoosts() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("auto_boost_panic_recovered", "panic", r)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// 1. Fetch all active rules
	cursor, err := database.AutoBoostRules().Find(ctx, bson.M{"active": true})
	if err != nil {
		slog.Error("auto_boost_fetch_rules_error", "error", err)
		return
	}
	defer cursor.Close(ctx)

	var allRules []models.AutoBoostRule
	if err := cursor.All(ctx, &allRules); err != nil {
		slog.Error("auto_boost_decode_rules_error", "error", err)
		return
	}

	if len(allRules) == 0 {
		return
	}

	// 2. Group rules by user_id
	rulesByUser := make(map[primitive.ObjectID][]models.AutoBoostRule)
	for _, rule := range allRules {
		rulesByUser[rule.UserID] = append(rulesByUser[rule.UserID], rule)
	}

	var totalBoosts, totalErrors int

	// 3. Process each user
	for userID, rules := range rulesByUser {
		boosts, errors := processAutoBoostForUser(ctx, userID, rules)
		totalBoosts += boosts
		totalErrors += errors
	}

	if totalBoosts > 0 || totalErrors > 0 {
		slog.Info("auto_boost_cycle_complete",
			"rules_processed", len(allRules),
			"boosts_created", totalBoosts,
			"errors", totalErrors,
		)
	}
}

func processAutoBoostForUser(ctx context.Context, userID primitive.ObjectID, rules []models.AutoBoostRule) (int, int) {
	// Get Instagram credentials
	igCreds, err := getInstagramCredentials(ctx, userID)
	if err != nil || igCreds == nil {
		if err != nil {
			slog.Warn("auto_boost_ig_creds_error", "user_id", userID.Hex(), "error", err)
		}
		return 0, 0
	}

	// Get Meta Ads credentials
	adsCreds, err := getMetaAdsCredentials(ctx, userID)
	if err != nil || adsCreds == nil {
		if err != nil {
			slog.Warn("auto_boost_ads_creds_error", "user_id", userID.Hex(), "error", err)
		}
		return 0, 0
	}

	// Fetch recent posts from Instagram
	params := url.Values{}
	params.Set("fields", "id,caption,media_type,media_url,thumbnail_url,permalink,timestamp,like_count,comments_count")
	params.Set("limit", "25")

	postsResult, err := metaGraphGet("/"+igCreds.AccountID+"/media", igCreds.Token, params)
	if err != nil {
		slog.Error("auto_boost_fetch_posts_error", "user_id", userID.Hex(), "error", err)
		return 0, 0
	}

	postsData, ok := postsResult["data"].([]interface{})
	if !ok || len(postsData) == 0 {
		return 0, 0
	}

	// Check if any rule needs engagement_rate — if so, fetch followers_count
	var followersCount float64
	needsFollowers := false
	for _, rule := range rules {
		if rule.Metric == "engagement_rate" {
			needsFollowers = true
			break
		}
	}

	if needsFollowers {
		accountParams := url.Values{}
		accountParams.Set("fields", "followers_count")
		accountResult, err := metaGraphGet("/"+igCreds.AccountID, igCreds.Token, accountParams)
		if err != nil {
			slog.Error("auto_boost_fetch_followers_error", "user_id", userID.Hex(), "error", err)
		} else if fc, ok := accountResult["followers_count"].(float64); ok {
			followersCount = fc
		}
	}

	var boosts, errors int

	// Process each rule against each post
	for _, rule := range rules {
		for _, postRaw := range postsData {
			post, ok := postRaw.(map[string]interface{})
			if !ok {
				continue
			}

			boosted, failed := processAutoBoostPost(ctx, rule, post, followersCount, igCreds, adsCreds, userID)
			if boosted {
				boosts++
			}
			if failed {
				errors++
			}
		}
	}

	return boosts, errors
}

func processAutoBoostPost(
	ctx context.Context,
	rule models.AutoBoostRule,
	post map[string]interface{},
	followersCount float64,
	igCreds *instagramCredentials,
	adsCreds *metaAdsCredentials,
	userID primitive.ObjectID,
) (boosted bool, failed bool) {
	postID, _ := post["id"].(string)
	if postID == "" {
		return false, false
	}

	// Check post age
	timestampStr, _ := post["timestamp"].(string)
	if timestampStr != "" {
		postTime, err := time.Parse("2006-01-02T15:04:05-0700", timestampStr)
		if err != nil {
			postTime, err = time.Parse("2006-01-02T15:04:05+0000", timestampStr)
		}
		if err == nil {
			maxAge := time.Duration(rule.MaxPostAgeHours) * time.Hour
			if time.Since(postTime) > maxAge {
				return false, false
			}
		}
	}

	// Calculate metric value
	likeCount, _ := post["like_count"].(float64)
	commentsCount, _ := post["comments_count"].(float64)

	var metricValue float64
	switch rule.Metric {
	case "likes":
		metricValue = likeCount
	case "comments":
		metricValue = commentsCount
	case "engagement_rate":
		if followersCount > 0 {
			metricValue = (likeCount + commentsCount) / followersCount * 100
		}
	}

	// Check threshold
	if metricValue < rule.Threshold {
		return false, false
	}

	// Check cooldown
	cooldownCutoff := time.Now().Add(-time.Duration(rule.CooldownHours) * time.Hour)
	count, err := database.AutoBoostLogs().CountDocuments(ctx, bson.M{
		"rule_id":    rule.ID,
		"ig_media_id": postID,
		"status":     "success",
		"created_at": bson.M{"$gt": cooldownCutoff},
	})
	if err != nil {
		slog.Error("auto_boost_cooldown_check_error", "error", err)
		return false, true
	}
	if count > 0 {
		// Skipped due to cooldown — log it
		caption, _ := post["caption"].(string)
		if len(caption) > 200 {
			caption = caption[:200]
		}
		permalink, _ := post["permalink"].(string)
		mediaType, _ := post["media_type"].(string)

		database.AutoBoostLogs().InsertOne(ctx, models.AutoBoostLog{
			ID:          primitive.NewObjectID(),
			RuleID:      rule.ID,
			RuleName:    rule.Name,
			UserID:      userID,
			IGMediaID:   postID,
			IGPermalink: permalink,
			IGMediaType: mediaType,
			IGCaption:   caption,
			Metric:      rule.Metric,
			MetricValue: metricValue,
			Threshold:   rule.Threshold,
			DailyBudget: rule.DailyBudget,
			DurationDays: rule.DurationDays,
			Status:      "skipped_cooldown",
			CreatedAt:   time.Now(),
		})
		return false, false
	}

	// Create the campaign
	caption, _ := post["caption"].(string)
	if len(caption) > 200 {
		caption = caption[:200]
	}
	permalink, _ := post["permalink"].(string)
	mediaType, _ := post["media_type"].(string)

	campaignID, adSetID, creativeID, adID, err := createAutoBoostCampaign(ctx, rule, postID, igCreds, adsCreds, userID)
	if err != nil {
		slog.Error("auto_boost_campaign_error",
			"rule_id", rule.ID.Hex(),
			"post_id", postID,
			"error", err,
		)
		// Log failure
		database.AutoBoostLogs().InsertOne(ctx, models.AutoBoostLog{
			ID:           primitive.NewObjectID(),
			RuleID:       rule.ID,
			RuleName:     rule.Name,
			UserID:       userID,
			IGMediaID:    postID,
			IGPermalink:  permalink,
			IGMediaType:  mediaType,
			IGCaption:    caption,
			Metric:       rule.Metric,
			MetricValue:  metricValue,
			Threshold:    rule.Threshold,
			DailyBudget:  rule.DailyBudget,
			DurationDays: rule.DurationDays,
			Status:       "failed",
			ErrorMessage: err.Error(),
			CreatedAt:    time.Now(),
		})
		return false, true
	}

	// Log success
	database.AutoBoostLogs().InsertOne(ctx, models.AutoBoostLog{
		ID:             primitive.NewObjectID(),
		RuleID:         rule.ID,
		RuleName:       rule.Name,
		UserID:         userID,
		IGMediaID:      postID,
		IGPermalink:    permalink,
		IGMediaType:    mediaType,
		IGCaption:      caption,
		Metric:         rule.Metric,
		MetricValue:    metricValue,
		Threshold:      rule.Threshold,
		MetaCampaignID: campaignID,
		MetaAdSetID:    adSetID,
		MetaCreativeID: creativeID,
		MetaAdID:       adID,
		DailyBudget:    rule.DailyBudget,
		DurationDays:   rule.DurationDays,
		Status:         "success",
		CreatedAt:      time.Now(),
	})

	slog.Info("auto_boost_created",
		"rule", rule.Name,
		"post_id", postID,
		"metric", rule.Metric,
		"value", metricValue,
		"campaign_id", campaignID,
	)

	return true, false
}

func createAutoBoostCampaign(
	ctx context.Context,
	rule models.AutoBoostRule,
	postID string,
	igCreds *instagramCredentials,
	adsCreds *metaAdsCredentials,
	userID primitive.ObjectID,
) (campaignID, adSetID, creativeID, adID string, err error) {
	accountPath := adAccountPath(adsCreds.AdAccountID)
	now := time.Now()

	// 1. Create Campaign
	campaignParams := url.Values{}
	campaignParams.Set("name", fmt.Sprintf("AutoBoost: %s - %s", rule.Name, postID))
	campaignParams.Set("objective", rule.Objective)
	campaignParams.Set("status", "PAUSED")
	campaignParams.Set("special_ad_categories", "NONE")

	campaignResult, err := metaGraphPost(accountPath+"/campaigns", adsCreds.Token, campaignParams)
	if err != nil {
		return "", "", "", "", fmt.Errorf("create campaign: %w", err)
	}
	campaignID, _ = campaignResult["id"].(string)
	if campaignID == "" {
		return "", "", "", "", fmt.Errorf("create campaign: empty ID returned")
	}

	// 2. Create Ad Set
	startTime := now.Format(time.RFC3339)
	endTime := now.Add(time.Duration(rule.DurationDays) * 24 * time.Hour).Format(time.RFC3339)

	targetingJSON, _ := json.Marshal(rule.Targeting)

	adSetParams := url.Values{}
	adSetParams.Set("campaign_id", campaignID)
	adSetParams.Set("name", fmt.Sprintf("AutoBoost AdSet - %s", postID))
	adSetParams.Set("daily_budget", strconv.FormatInt(rule.DailyBudget, 10))
	adSetParams.Set("billing_event", rule.BillingEvent)
	adSetParams.Set("optimization_goal", rule.OptimizationGoal)
	adSetParams.Set("targeting", string(targetingJSON))
	adSetParams.Set("status", "PAUSED")
	adSetParams.Set("start_time", startTime)
	adSetParams.Set("end_time", endTime)

	adSetResult, err := metaGraphPost(accountPath+"/adsets", adsCreds.Token, adSetParams)
	if err != nil {
		return campaignID, "", "", "", fmt.Errorf("create adset: %w", err)
	}
	adSetID, _ = adSetResult["id"].(string)
	if adSetID == "" {
		return campaignID, "", "", "", fmt.Errorf("create adset: empty ID returned")
	}

	// 3. Create Ad Creative
	objectStorySpec := fmt.Sprintf(`{"instagram_actor_id":"%s","source_instagram_media_id":"%s"}`, igCreds.AccountID, postID)

	creativeParams := url.Values{}
	creativeParams.Set("name", fmt.Sprintf("AutoBoost Creative - %s", postID))
	creativeParams.Set("object_story_spec", objectStorySpec)

	creativeResult, err := metaGraphPost(accountPath+"/adcreatives", adsCreds.Token, creativeParams)
	if err != nil {
		return campaignID, adSetID, "", "", fmt.Errorf("create creative: %w", err)
	}
	creativeID, _ = creativeResult["id"].(string)
	if creativeID == "" {
		return campaignID, adSetID, "", "", fmt.Errorf("create creative: empty ID returned")
	}

	// 4. Create Ad
	adParams := url.Values{}
	adParams.Set("adset_id", adSetID)
	adParams.Set("name", fmt.Sprintf("AutoBoost Ad - %s", postID))
	adParams.Set("creative", fmt.Sprintf(`{"creative_id":"%s"}`, creativeID))
	adParams.Set("status", "PAUSED")

	adResult, err := metaGraphPost(accountPath+"/ads", adsCreds.Token, adParams)
	if err != nil {
		return campaignID, adSetID, creativeID, "", fmt.Errorf("create ad: %w", err)
	}
	adID, _ = adResult["id"].(string)
	if adID == "" {
		return campaignID, adSetID, creativeID, "", fmt.Errorf("create ad: empty ID returned")
	}

	// 5. Activate all objects
	activateParams := url.Values{}
	activateParams.Set("status", "ACTIVE")

	if _, err := metaGraphPost("/"+campaignID, adsCreds.Token, activateParams); err != nil {
		slog.Warn("auto_boost_activate_campaign_error", "campaign_id", campaignID, "error", err)
	}
	if _, err := metaGraphPost("/"+adSetID, adsCreds.Token, activateParams); err != nil {
		slog.Warn("auto_boost_activate_adset_error", "adset_id", adSetID, "error", err)
	}
	if _, err := metaGraphPost("/"+adID, adsCreds.Token, activateParams); err != nil {
		slog.Warn("auto_boost_activate_ad_error", "ad_id", adID, "error", err)
	}

	// 6. Save locally in MongoDB (following existing meta_ads pattern)
	database.MetaAdsCampaigns().InsertOne(ctx, models.MetaAdsCampaign{
		ID:             primitive.NewObjectID(),
		UserID:         userID,
		MetaCampaignID: campaignID,
		Name:           fmt.Sprintf("AutoBoost: %s - %s", rule.Name, postID),
		Objective:      rule.Objective,
		Status:         "ACTIVE",
		BuyingType:     "AUCTION",
		DailyBudget:    rule.DailyBudget,
		SpecialAdCategories: []string{"NONE"},
		CreatedAt:      now,
		UpdatedAt:      now,
	})

	database.MetaAdsAdSets().InsertOne(ctx, models.MetaAdsAdSet{
		ID:               primitive.NewObjectID(),
		UserID:           userID,
		MetaAdSetID:      adSetID,
		CampaignID:       campaignID,
		Name:             fmt.Sprintf("AutoBoost AdSet - %s", postID),
		Status:           "ACTIVE",
		DailyBudget:      rule.DailyBudget,
		BillingEvent:     rule.BillingEvent,
		OptimizationGoal: rule.OptimizationGoal,
		StartTime:        startTime,
		EndTime:          endTime,
		Targeting:        rule.Targeting,
		CreatedAt:        now,
		UpdatedAt:        now,
	})

	database.MetaAdsAds().InsertOne(ctx, models.MetaAdsAd{
		ID:       primitive.NewObjectID(),
		UserID:   userID,
		MetaAdID: adID,
		AdSetID:  adSetID,
		Name:     fmt.Sprintf("AutoBoost Ad - %s", postID),
		Status:   "ACTIVE",
		Creative: models.AdCreative{
			Name: fmt.Sprintf("AutoBoost Creative - %s", postID),
		},
		CreatedAt: now,
		UpdatedAt: now,
	})

	return campaignID, adSetID, creativeID, adID, nil
}
