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
// INTEGRATED PUBLISH HANDLERS
// ══════════════════════════════════════════════════════════════════════

func CreateIntegratedPublish(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	orgID := middleware.GetOrgID(r)
	if userID == primitive.NilObjectID || orgID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req models.CreateIntegratedPublishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate Instagram fields
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

	// Validate campaign fields
	if req.Campaign.Name == "" {
		http.Error(w, "campaign.name is required", http.StatusBadRequest)
		return
	}
	if req.Campaign.Objective == "" {
		http.Error(w, "campaign.objective is required", http.StatusBadRequest)
		return
	}
	if req.Campaign.DailyBudget <= 0 {
		http.Error(w, "campaign.daily_budget must be > 0 (in cents)", http.StatusBadRequest)
		return
	}
	if req.Campaign.DurationDays <= 0 {
		http.Error(w, "campaign.duration_days must be > 0", http.StatusBadRequest)
		return
	}
	if req.Campaign.Objective == "OUTCOME_TRAFFIC" && req.Campaign.Creative.LinkURL == "" {
		http.Error(w, "campaign.creative.link_url is required for TRAFFIC objective", http.StatusBadRequest)
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

	// Verify credentials exist
	igCreds, err := getInstagramCredentials(ctx, userID)
	if err != nil || igCreds == nil {
		http.Error(w, "Instagram not configured. Configure in Settings first.", http.StatusBadRequest)
		return
	}
	adsCreds, err := getMetaAdsCredentials(ctx, userID)
	if err != nil || adsCreds == nil {
		http.Error(w, "Meta Ads not configured. Configure in Settings first.", http.StatusBadRequest)
		return
	}

	now := time.Now()
	pub := models.IntegratedPublish{
		ID:          primitive.NewObjectID(),
		UserID:      userID,
		OrgID:       orgID,
		Caption:     req.Caption,
		MediaType:   req.MediaType,
		ImageIDs:    req.ImageIDs,
		ScheduledAt: scheduledAt,
		Status:      "scheduled",
		Campaign:    req.Campaign,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if _, err := database.IntegratedPublishes().InsertOne(ctx, pub); err != nil {
		slog.Error("integrated_publish_create_error", "error", err)
		http.Error(w, "Error creating integrated publish", http.StatusInternalServerError)
		return
	}

	slog.Info("integrated_publish_created",
		"id", pub.ID.Hex(),
		"user_id", userID.Hex(),
		"scheduled_at", scheduledAt.Format(time.RFC3339),
	)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(buildIPResponse(pub))
}

func ListIntegratedPublishes(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	if orgID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

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

	total, err := database.IntegratedPublishes().CountDocuments(ctx, filter)
	if err != nil {
		http.Error(w, "Error counting records", http.StatusInternalServerError)
		return
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}}).
		SetSkip(skip).
		SetLimit(int64(limit))

	cursor, err := database.IntegratedPublishes().Find(ctx, filter, opts)
	if err != nil {
		http.Error(w, "Error fetching records", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var items []models.IntegratedPublish
	if err := cursor.All(ctx, &items); err != nil {
		http.Error(w, "Error decoding records", http.StatusInternalServerError)
		return
	}

	responses := make([]models.IntegratedPublishResponse, len(items))
	for i, item := range items {
		responses[i] = buildIPResponse(item)
	}

	json.NewEncoder(w).Encode(models.IntegratedPublishListResponse{
		Items: responses,
		Total: total,
		Page:  page,
		Limit: limit,
	})
}

func GetIntegratedPublish(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	if orgID == primitive.NilObjectID {
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

	var pub models.IntegratedPublish
	err = database.IntegratedPublishes().FindOne(ctx, bson.M{"_id": oid, "org_id": orgID}).Decode(&pub)
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(buildIPResponse(pub))
}

func UpdateIntegratedPublish(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	if orgID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	oid, err := primitive.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	var body struct {
		ScheduledAt string `json:"scheduled_at"`
		Status      string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var pub models.IntegratedPublish
	err = database.IntegratedPublishes().FindOne(ctx, bson.M{"_id": oid, "org_id": orgID}).Decode(&pub)
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	if pub.Status == "publishing_ig" || pub.Status == "publishing_ads" {
		http.Error(w, "Cannot update while publishing is in progress", http.StatusBadRequest)
		return
	}

	set := bson.M{"updated_at": time.Now()}

	if body.ScheduledAt != "" {
		scheduledAt, err := time.Parse(time.RFC3339, body.ScheduledAt)
		if err != nil {
			http.Error(w, "scheduled_at must be a valid ISO 8601 date", http.StatusBadRequest)
			return
		}
		set["scheduled_at"] = scheduledAt
	}

	if body.Status != "" {
		if body.Status != "scheduled" {
			http.Error(w, "Only 'scheduled' status is allowed for rescheduling", http.StatusBadRequest)
			return
		}
		set["status"] = body.Status
		set["error_message"] = ""
		set["error_phase"] = ""
	}

	if _, err := database.IntegratedPublishes().UpdateOne(ctx, bson.M{"_id": oid, "org_id": orgID}, bson.M{"$set": set}); err != nil {
		http.Error(w, "Error updating record", http.StatusInternalServerError)
		return
	}

	slog.Info("integrated_publish_updated", "id", oid.Hex(), "org_id", orgID.Hex())

	// Return updated document
	database.IntegratedPublishes().FindOne(ctx, bson.M{"_id": oid}).Decode(&pub)
	json.NewEncoder(w).Encode(buildIPResponse(pub))
}

func DeleteIntegratedPublish(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	if orgID == primitive.NilObjectID {
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

	var pub models.IntegratedPublish
	err = database.IntegratedPublishes().FindOne(ctx, bson.M{"_id": oid, "org_id": orgID}).Decode(&pub)
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	if pub.Status == "publishing_ig" || pub.Status == "publishing_ads" {
		http.Error(w, "Cannot delete while publishing is in progress", http.StatusBadRequest)
		return
	}

	if _, err := database.IntegratedPublishes().DeleteOne(ctx, bson.M{"_id": oid, "org_id": orgID}); err != nil {
		http.Error(w, "Error deleting record", http.StatusInternalServerError)
		return
	}

	slog.Info("integrated_publish_deleted", "id", oid.Hex(), "org_id", orgID.Hex())
	json.NewEncoder(w).Encode(map[string]string{"message": "Integrated publish deleted"})
}

// ══════════════════════════════════════════════════════════════════════
// BACKGROUND JOB
// ══════════════════════════════════════════════════════════════════════

func ProcessScheduledIntegratedPublishes() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("integrated_publish_panic_recovered", "panic", r)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	now := time.Now()
	filter := bson.M{
		"status":       "scheduled",
		"scheduled_at": bson.M{"$lte": now},
	}

	cursor, err := database.IntegratedPublishes().Find(ctx, filter)
	if err != nil {
		slog.Error("integrated_publish_scheduler_error", "error", err)
		return
	}
	defer cursor.Close(ctx)

	var items []models.IntegratedPublish
	if err := cursor.All(ctx, &items); err != nil {
		slog.Error("integrated_publish_decode_error", "error", err)
		return
	}

	for _, pub := range items {
		processIntegratedPublish(ctx, pub)
	}
}

func processIntegratedPublish(ctx context.Context, pub models.IntegratedPublish) {
	// PHASE 1: Publish to Instagram
	ipUpdateStatus(ctx, pub.ID, "publishing_ig", "", "")

	igSchedule := models.InstagramSchedule{
		ID:        pub.ID,
		UserID:    pub.UserID,
		Caption:   pub.Caption,
		MediaType: pub.MediaType,
		ImageIDs:  pub.ImageIDs,
	}

	mediaID, err := publishToInstagram(igSchedule)
	if err != nil {
		slog.Error("integrated_publish_ig_failed", "id", pub.ID.Hex(), "error", err)
		ipUpdateStatus(ctx, pub.ID, "failed", err.Error(), "ig")
		return
	}

	// Save ig_media_id
	database.IntegratedPublishes().UpdateOne(ctx, bson.M{"_id": pub.ID}, bson.M{
		"$set": bson.M{"ig_media_id": mediaID, "updated_at": time.Now()},
	})

	slog.Info("integrated_publish_ig_done", "id", pub.ID.Hex(), "ig_media_id", mediaID)

	// PHASE 2: Create Meta Ads Campaign
	ipUpdateStatus(ctx, pub.ID, "publishing_ads", "", "")

	adsCreds, err := getMetaAdsCredentials(ctx, pub.UserID)
	if err != nil || adsCreds == nil {
		slog.Error("integrated_publish_ads_creds_error", "id", pub.ID.Hex(), "error", err)
		ipUpdateStatus(ctx, pub.ID, "failed", "Meta Ads not configured", "ads")
		return
	}

	igCreds, err := getInstagramCredentials(ctx, pub.UserID)
	if err != nil || igCreds == nil {
		slog.Error("integrated_publish_ig_creds_error", "id", pub.ID.Hex(), "error", err)
		ipUpdateStatus(ctx, pub.ID, "failed", "Instagram not configured", "ads")
		return
	}

	accountPath := adAccountPath(adsCreds.AdAccountID)

	// For TRAFFIC objective, resolve the real Facebook Page ID
	var fbPageID string
	if pub.Campaign.Objective == "OUTCOME_TRAFFIC" {
		fbPageID, err = resolveFacebookPageID(adsCreds.Token, igCreds.AccountID)
		if err != nil {
			slog.Error("integrated_publish_resolve_page_error", "id", pub.ID.Hex(), "error", err)
			ipUpdateStatus(ctx, pub.ID, "failed", "Could not resolve Facebook Page ID: "+err.Error(), "ads")
			return
		}
		slog.Info("integrated_publish_resolved_page", "id", pub.ID.Hex(), "fb_page_id", fbPageID)
	}

	// Step 2a: Create Campaign (with Campaign Budget Optimization)
	campaignParams := url.Values{}
	campaignParams.Set("name", pub.Campaign.Name)
	campaignParams.Set("objective", pub.Campaign.Objective)
	campaignParams.Set("status", "ACTIVE")
	campaignParams.Set("special_ad_categories", "NONE")
	campaignParams.Set("daily_budget", fmt.Sprintf("%d", pub.Campaign.DailyBudget))
	campaignParams.Set("bid_strategy", "LOWEST_COST_WITHOUT_CAP")

	campaignResult, err := metaGraphPost(accountPath+"/campaigns", adsCreds.Token, campaignParams)
	if err != nil {
		slog.Error("integrated_publish_campaign_error", "id", pub.ID.Hex(), "error", err)
		ipUpdateStatus(ctx, pub.ID, "failed", "Campaign creation failed: "+err.Error(), "ads")
		return
	}
	metaCampaignID, _ := campaignResult["id"].(string)

	database.IntegratedPublishes().UpdateOne(ctx, bson.M{"_id": pub.ID}, bson.M{
		"$set": bson.M{"meta_campaign_id": metaCampaignID, "updated_at": time.Now()},
	})

	// Step 2b: Create Ad Set
	startTime := time.Now().Add(1 * time.Hour)
	endTime := startTime.AddDate(0, 0, pub.Campaign.DurationDays)

	targetingJSON, _ := json.Marshal(buildMetaTargeting(pub.Campaign.Targeting))

	adsetParams := url.Values{}
	adsetParams.Set("campaign_id", metaCampaignID)
	adsetParams.Set("name", pub.Campaign.Name+" - Ad Set")
	adsetParams.Set("billing_event", "IMPRESSIONS")
	// Match optimization goal to campaign objective
	optGoal := "REACH"
	switch pub.Campaign.Objective {
	case "OUTCOME_TRAFFIC":
		optGoal = "LANDING_PAGE_VIEWS"
		promotedObj, _ := json.Marshal(map[string]string{"page_id": fbPageID})
		adsetParams.Set("promoted_object", string(promotedObj))
		adsetParams.Set("destination_type", "WEBSITE")
	case "OUTCOME_ENGAGEMENT":
		optGoal = "POST_ENGAGEMENT"
	case "OUTCOME_AWARENESS":
		optGoal = "REACH"
	}
	adsetParams.Set("optimization_goal", optGoal)
	adsetParams.Set("status", "ACTIVE")
	adsetParams.Set("start_time", startTime.Format(time.RFC3339))
	adsetParams.Set("end_time", endTime.Format(time.RFC3339))
	adsetParams.Set("targeting", string(targetingJSON))

	adsetResult, err := metaGraphPost(accountPath+"/adsets", adsCreds.Token, adsetParams)
	if err != nil {
		slog.Error("integrated_publish_adset_error", "id", pub.ID.Hex(), "error", err)
		ipUpdateStatus(ctx, pub.ID, "failed", "Ad Set creation failed: "+err.Error(), "ads")
		return
	}
	metaAdSetID, _ := adsetResult["id"].(string)

	database.IntegratedPublishes().UpdateOne(ctx, bson.M{"_id": pub.ID}, bson.M{
		"$set": bson.M{"meta_adset_id": metaAdSetID, "updated_at": time.Now()},
	})

	// Wait for Instagram to fully process the published media before creating ad creative
	slog.Info("integrated_publish_waiting_media_sync", "id", pub.ID.Hex(), "seconds", 15)
	time.Sleep(15 * time.Second)

	// Step 2c: Create Ad Creative using the published IG post
	creativeParams := url.Values{}
	creativeParams.Set("name", pub.Campaign.Name+" Creative")

	if pub.Campaign.Objective == "OUTCOME_TRAFFIC" {
		// TRAFFIC: use object_story_spec with page_id + instagram_actor_id + link_data
		cta := pub.Campaign.Creative.CallToAction
		if cta == "" {
			cta = "LEARN_MORE"
		}
		objectStorySpec := map[string]interface{}{
			"page_id":            fbPageID,
			"instagram_actor_id": igCreds.AccountID,
			"link_data": map[string]interface{}{
				"link":    pub.Campaign.Creative.LinkURL,
				"message": pub.Caption,
				"call_to_action": map[string]interface{}{
					"type":  cta,
					"value": map[string]string{"link": pub.Campaign.Creative.LinkURL},
				},
			},
		}
		specJSON, _ := json.Marshal(objectStorySpec)
		creativeParams.Set("object_story_spec", string(specJSON))

		slog.Info("integrated_publish_creative_attempt",
			"id", pub.ID.Hex(),
			"approach", "object_story_spec_traffic",
			"fb_page_id", fbPageID,
			"link_url", pub.Campaign.Creative.LinkURL,
		)
	} else {
		// ENGAGEMENT / AWARENESS: promote existing IG post via object_story_spec
		objectStorySpec := map[string]interface{}{
			"instagram_actor_id":         igCreds.AccountID,
			"source_instagram_media_id":  mediaID,
		}
		specJSON, _ := json.Marshal(objectStorySpec)
		creativeParams.Set("object_story_spec", string(specJSON))

		slog.Info("integrated_publish_creative_attempt",
			"id", pub.ID.Hex(),
			"approach", "object_story_spec_boost",
			"ig_account_id", igCreds.AccountID,
			"ig_media_id", mediaID,
		)
	}

	creativeResult, err := metaGraphPost(accountPath+"/adcreatives", adsCreds.Token, creativeParams)
	if err != nil {
		slog.Error("integrated_publish_creative_error", "id", pub.ID.Hex(), "error", err)
		ipUpdateStatus(ctx, pub.ID, "failed", "Ad creative creation failed: "+err.Error(), "ads")
		return
	}
	creativeID, _ := creativeResult["id"].(string)

	// Step 2d: Create Ad
	adParams := url.Values{}
	adParams.Set("name", pub.Campaign.Name+" - Ad")
	adParams.Set("adset_id", metaAdSetID)
	adParams.Set("status", "ACTIVE")
	adParams.Set("creative", fmt.Sprintf(`{"creative_id":"%s"}`, creativeID))

	adResult, err := metaGraphPost(accountPath+"/ads", adsCreds.Token, adParams)
	if err != nil {
		slog.Error("integrated_publish_ad_error", "id", pub.ID.Hex(), "error", err)
		ipUpdateStatus(ctx, pub.ID, "failed", "Ad creation failed: "+err.Error(), "ads")
		return
	}
	metaAdID, _ := adResult["id"].(string)

	// SUCCESS
	database.IntegratedPublishes().UpdateOne(ctx, bson.M{"_id": pub.ID}, bson.M{
		"$set": bson.M{
			"meta_ad_id": metaAdID,
			"status":     "completed",
			"updated_at": time.Now(),
		},
	})

	slog.Info("integrated_publish_completed",
		"id", pub.ID.Hex(),
		"ig_media_id", mediaID,
		"meta_campaign_id", metaCampaignID,
		"meta_adset_id", metaAdSetID,
		"meta_ad_id", metaAdID,
	)
}

// resolveFacebookPageID finds the Facebook Page ID linked to an Instagram Business Account
// by querying GET /me/accounts and matching instagram_business_account.id.
func resolveFacebookPageID(token, igAccountID string) (string, error) {
	apiURL := "https://graph.facebook.com/v21.0/me/accounts?fields=id,instagram_business_account&access_token=" + url.QueryEscape(token)

	resp, err := http.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("http error: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			ID                       string `json:"id"`
			InstagramBusinessAccount struct {
				ID string `json:"id"`
			} `json:"instagram_business_account"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode error: %w", err)
	}

	for _, page := range result.Data {
		if page.InstagramBusinessAccount.ID == igAccountID {
			return page.ID, nil
		}
	}

	return "", fmt.Errorf("no Facebook Page found linked to Instagram account %s", igAccountID)
}

// buildMetaTargeting converts targeting to Meta API format (interest IDs as numbers).
func buildMetaTargeting(t models.AdSetTargeting) map[string]interface{} {
	m := map[string]interface{}{}

	if t.GeoLocations != nil {
		m["geo_locations"] = t.GeoLocations
	}
	if t.AgeMin > 0 {
		m["age_min"] = t.AgeMin
	}
	if t.AgeMax > 0 {
		m["age_max"] = t.AgeMax
	}
	if len(t.Genders) > 0 {
		m["genders"] = t.Genders
	}
	if len(t.Interests) > 0 {
		interests := make([]map[string]interface{}, len(t.Interests))
		for i, interest := range t.Interests {
			numID, _ := strconv.ParseInt(interest.ID, 10, 64)
			interests[i] = map[string]interface{}{
				"id":   numID,
				"name": interest.Name,
			}
		}
		m["flexible_spec"] = []map[string]interface{}{
			{"interests": interests},
		}
	}
	// Disable Advantage Audience — use exact targeting as specified
	m["targeting_automation"] = map[string]interface{}{
		"advantage_audience": 0,
	}
	return m
}

// ipUpdateStatus updates the status and error fields of an integrated publish.
func ipUpdateStatus(ctx context.Context, id primitive.ObjectID, status, errMsg, errPhase string) {
	set := bson.M{
		"status":     status,
		"updated_at": time.Now(),
	}
	if errMsg != "" {
		set["error_message"] = errMsg
		set["error_phase"] = errPhase
	}
	database.IntegratedPublishes().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": set})
}

// buildIPResponse creates a response with resolved image URLs.
func buildIPResponse(p models.IntegratedPublish) models.IntegratedPublishResponse {
	imageURLs := make([]string, len(p.ImageIDs))
	for i, id := range p.ImageIDs {
		imageURLs[i] = "/api/v1/blog/images/" + id
	}
	return models.IntegratedPublishResponse{
		IntegratedPublish: p,
		ImageURLs:         imageURLs,
	}
}
