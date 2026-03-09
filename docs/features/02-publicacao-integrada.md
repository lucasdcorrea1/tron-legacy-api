# 02 - Publicacao Integrada (Post + Campanha)

## 1. Visao Geral

### O que e

A Publicacao Integrada e um fluxo unificado que permite ao usuario, em uma unica operacao:

1. **Publicar um post no Instagram** (imagem ou carrossel) via Graph API
2. **Criar uma campanha completa no Meta Ads** (Campaign + AdSet + Ad) que promove esse mesmo post

### Por que

Atualmente, publicar no Instagram e criar uma campanha de ads sao fluxos separados no sistema. O usuario precisa:
- Agendar o post em `/admin/instagram/schedules`
- Esperar a publicacao
- Copiar o media ID
- Ir em `/admin/meta-ads/campaigns`, criar campanha, ad set, e ad manualmente

Isso gera friccao, perda de tempo e risco de erro.

### Valor de negocio

- **Reducao de tempo**: de ~15 minutos (fluxo manual) para ~2 minutos (formulario unico)
- **Menos erros**: configuracao integrada evita inconsistencias entre post e campanha
- **Automacao**: o background job garante que tudo acontece na ordem correta (IG primeiro, ads depois)
- **Rastreabilidade**: registro unico com status de ambas as etapas

---

## 2. Arquitetura

```
Usuario (Frontend)
    |
    | POST /api/v1/admin/integrated-publish
    v
[Handler: CreateIntegratedPublish]
    |
    | salva no MongoDB (status: "draft" ou "scheduled")
    v
[Collection: integrated_publishes]
    |
    | (a cada 1 minuto)
    v
[Background Job: ProcessScheduledIntegratedPublishes]
    |
    |-- FASE 1: Publicar no Instagram
    |   |
    |   | publishToInstagram(schedule)
    |   |   |-- createMediaContainer()
    |   |   |-- publishMediaContainer()
    |   |
    |   | status: "publishing_ig" -> sucesso -> armazena ig_media_id
    |   |                         -> falha   -> status: "failed"
    |
    |-- FASE 2: Criar Campanha Meta Ads
    |   |
    |   | metaGraphPost() x3 (campaign, adset, ad)
    |   |   |-- POST /act_{id}/campaigns
    |   |   |-- POST /act_{id}/adsets
    |   |   |-- POST /act_{id}/ads (com creative usando ig_media_id)
    |   |
    |   | status: "publishing_ads" -> sucesso -> status: "completed"
    |   |                           -> falha   -> status: "failed" (IG ja publicado)
    |
    v
[Resultado final: Post publicado + Campanha ativa]
```

---

## 3. Models (Go)

### `internal/models/integrated_publish.go`

```go
package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// IntegratedPublish represents a unified Instagram post + Meta Ads campaign publish.
type IntegratedPublish struct {
	ID        primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID    primitive.ObjectID `json:"user_id" bson:"user_id"`

	// ── Instagram fields ─────────────────────────────────────────
	Caption   string   `json:"caption" bson:"caption"`
	MediaType string   `json:"media_type" bson:"media_type"` // "image" or "carousel"
	ImageIDs  []string `json:"image_ids" bson:"image_ids"`   // IDs from images collection

	// ── Scheduling ───────────────────────────────────────────────
	ScheduledAt time.Time `json:"scheduled_at" bson:"scheduled_at"`
	Status      string    `json:"status" bson:"status"`
	// Possible statuses:
	//   "draft"          - saved but not scheduled
	//   "scheduled"      - waiting for scheduled_at
	//   "publishing_ig"  - publishing to Instagram in progress
	//   "publishing_ads" - creating Meta Ads campaign in progress
	//   "completed"      - both IG + Ads done
	//   "failed"         - error in either phase

	// ── Instagram result ─────────────────────────────────────────
	IGMediaID string `json:"ig_media_id,omitempty" bson:"ig_media_id,omitempty"`

	// ── Meta Ads campaign config ─────────────────────────────────
	Campaign IntegratedCampaignConfig `json:"campaign" bson:"campaign"`

	// ── Meta Ads result ──────────────────────────────────────────
	MetaCampaignID string `json:"meta_campaign_id,omitempty" bson:"meta_campaign_id,omitempty"`
	MetaAdSetID    string `json:"meta_adset_id,omitempty" bson:"meta_adset_id,omitempty"`
	MetaAdID       string `json:"meta_ad_id,omitempty" bson:"meta_ad_id,omitempty"`

	// ── Error tracking ───────────────────────────────────────────
	ErrorMessage string `json:"error_message,omitempty" bson:"error_message,omitempty"`
	ErrorPhase   string `json:"error_phase,omitempty" bson:"error_phase,omitempty"` // "ig" or "ads"

	// ── Timestamps ───────────────────────────────────────────────
	CreatedAt time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
}

// IntegratedCampaignConfig holds the Meta Ads campaign configuration
// for the integrated publish flow.
type IntegratedCampaignConfig struct {
	Name         string         `json:"name" bson:"name"`
	Objective    string         `json:"objective" bson:"objective"`       // e.g. "OUTCOME_AWARENESS", "OUTCOME_ENGAGEMENT"
	DailyBudget  int64          `json:"daily_budget" bson:"daily_budget"` // in cents (e.g. 2000 = R$20,00)
	DurationDays int            `json:"duration_days" bson:"duration_days"`
	Targeting    AdSetTargeting `json:"targeting" bson:"targeting"` // reuses existing AdSetTargeting
	Creative     IntegratedCreativeConfig `json:"creative" bson:"creative"`
}

// IntegratedCreativeConfig holds ad creative settings for the integrated flow.
type IntegratedCreativeConfig struct {
	Title        string `json:"title,omitempty" bson:"title,omitempty"`
	Body         string `json:"body,omitempty" bson:"body,omitempty"`               // ad copy (pode diferir da caption do IG)
	CallToAction string `json:"call_to_action,omitempty" bson:"call_to_action,omitempty"` // "LEARN_MORE", "SHOP_NOW", etc.
	LinkURL      string `json:"link_url,omitempty" bson:"link_url,omitempty"`
}

// CreateIntegratedPublishRequest is the request body for creating an integrated publish.
type CreateIntegratedPublishRequest struct {
	Caption      string                   `json:"caption"`
	MediaType    string                   `json:"media_type"`
	ImageIDs     []string                 `json:"image_ids"`
	ScheduledAt  string                   `json:"scheduled_at"` // ISO 8601
	Campaign     IntegratedCampaignConfig `json:"campaign"`
}

// IntegratedPublishResponse includes resolved image URLs.
type IntegratedPublishResponse struct {
	IntegratedPublish `json:",inline"`
	ImageURLs         []string `json:"image_urls"`
}

// IntegratedPublishListResponse is the paginated response.
type IntegratedPublishListResponse struct {
	Items []IntegratedPublishResponse `json:"items"`
	Total int64                       `json:"total"`
	Page  int                         `json:"page"`
	Limit int                         `json:"limit"`
}
```

### Notas sobre o modelo

- `AdSetTargeting` e reutilizado de `internal/models/meta_ads.go` (ja inclui `GeoLocations`, `AgeMin`, `AgeMax`, `Genders`, `Interests`, etc.)
- `DailyBudget` esta em centavos, consistente com o padrao da Meta API e dos modelos existentes (`MetaAdsCampaign.DailyBudget`)
- `DurationDays` define por quantos dias a campanha ficara ativa (o sistema calcula `start_time` e `end_time` automaticamente)
- `ErrorPhase` permite ao usuario saber se o erro foi na publicacao IG ou na criacao da campanha

---

## 4. Database

### Collection

```
integrated_publishes
```

### Funcao de acesso (adicionar em `internal/database/mongo.go`)

```go
func IntegratedPublishes() *mongo.Collection {
	return DB.Collection("integrated_publishes")
}
```

### Indices (adicionar em `EnsureIndexes()`)

```go
// integrated_publishes: compound index on {status, scheduled_at} for scheduler queries
_, err = IntegratedPublishes().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "status", Value: 1}, {Key: "scheduled_at", Value: 1}},
})
if err != nil {
	return err
}

// integrated_publishes: index on {user_id, created_at} for user listing
_, err = IntegratedPublishes().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}},
})
if err != nil {
	return err
}
```

### Justificativa dos indices

| Indice | Uso |
|--------|-----|
| `{status, scheduled_at}` | Background job busca `status="scheduled"` com `scheduled_at <= now` |
| `{user_id, created_at}` | Listagem paginada do historico por usuario, ordenado por data |

---

## 5. Handlers (Go)

### Arquivo: `internal/handlers/integrated_publish.go`

```go
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

// CreateIntegratedPublish creates a new integrated publish (IG post + Ads campaign).
func CreateIntegratedPublish(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req models.CreateIntegratedPublishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// ── Validate Instagram fields ────────────────────────────────
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

	// ── Validate campaign fields ─────────────────────────────────
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// ── Verify all images exist ──────────────────────────────────
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

	// ── Verify credentials exist (IG + Ads) ──────────────────────
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
		Caption:     req.Caption,
		MediaType:   req.MediaType,
		ImageIDs:    req.ImageIDs,
		ScheduledAt: scheduledAt,
		Status:      "scheduled",
		Campaign:    req.Campaign,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err = database.IntegratedPublishes().InsertOne(ctx, pub)
	if err != nil {
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
	json.NewEncoder(w).Encode(buildIntegratedPublishResponse(pub))
}

// ListIntegratedPublishes lists integrated publishes with pagination.
func ListIntegratedPublishes(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
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

	filter := bson.M{"user_id": userID}
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
		responses[i] = buildIntegratedPublishResponse(item)
	}

	json.NewEncoder(w).Encode(models.IntegratedPublishListResponse{
		Items: responses,
		Total: total,
		Page:  page,
		Limit: limit,
	})
}

// GetIntegratedPublish returns a single integrated publish by ID.
func GetIntegratedPublish(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var pub models.IntegratedPublish
	err = database.IntegratedPublishes().FindOne(ctx, bson.M{
		"_id":     oid,
		"user_id": userID,
	}).Decode(&pub)
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(buildIntegratedPublishResponse(pub))
}

// DeleteIntegratedPublish deletes an integrated publish (only draft/scheduled/failed).
func DeleteIntegratedPublish(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var pub models.IntegratedPublish
	err = database.IntegratedPublishes().FindOne(ctx, bson.M{
		"_id":     oid,
		"user_id": userID,
	}).Decode(&pub)
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	if pub.Status == "publishing_ig" || pub.Status == "publishing_ads" {
		http.Error(w, "Cannot delete while publishing is in progress", http.StatusBadRequest)
		return
	}

	_, err = database.IntegratedPublishes().DeleteOne(ctx, bson.M{"_id": oid, "user_id": userID})
	if err != nil {
		http.Error(w, "Error deleting record", http.StatusInternalServerError)
		return
	}

	slog.Info("integrated_publish_deleted", "id", oid.Hex(), "user_id", userID.Hex())
	json.NewEncoder(w).Encode(map[string]string{"message": "Integrated publish deleted"})
}

// ══════════════════════════════════════════════════════════════════════
// BACKGROUND JOB
// ══════════════════════════════════════════════════════════════════════

// ProcessScheduledIntegratedPublishes is called every 1 minute by the scheduler.
// It processes integrated publishes in two phases: IG first, then Ads.
func ProcessScheduledIntegratedPublishes() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	now := time.Now()
	filter := bson.M{
		"status":       "scheduled",
		"scheduled_at": bson.M{"$lte": now},
	}

	cursor, err := database.IntegratedPublishes().Find(ctx, filter)
	if err != nil {
		slog.Error("integrated_publish_scheduler_query_error", "error", err)
		return
	}
	defer cursor.Close(ctx)

	var items []models.IntegratedPublish
	if err := cursor.All(ctx, &items); err != nil {
		slog.Error("integrated_publish_scheduler_decode_error", "error", err)
		return
	}

	for _, pub := range items {
		processIntegratedPublish(ctx, pub)
	}
}

// processIntegratedPublish handles a single integrated publish (both phases).
func processIntegratedPublish(ctx context.Context, pub models.IntegratedPublish) {
	// ── PHASE 1: Publish to Instagram ────────────────────────────
	updateStatus(ctx, pub.ID, "publishing_ig", "", "")

	igSchedule := models.InstagramSchedule{
		ID:        pub.ID,
		UserID:    pub.UserID,
		Caption:   pub.Caption,
		MediaType: pub.MediaType,
		ImageIDs:  pub.ImageIDs,
	}

	mediaID, err := publishToInstagram(igSchedule)
	if err != nil {
		slog.Error("integrated_publish_ig_failed",
			"id", pub.ID.Hex(),
			"error", err,
		)
		updateStatus(ctx, pub.ID, "failed", err.Error(), "ig")
		return
	}

	// Save ig_media_id
	database.IntegratedPublishes().UpdateOne(ctx, bson.M{"_id": pub.ID}, bson.M{
		"$set": bson.M{"ig_media_id": mediaID, "updated_at": time.Now()},
	})

	slog.Info("integrated_publish_ig_done",
		"id", pub.ID.Hex(),
		"ig_media_id", mediaID,
	)

	// ── PHASE 2: Create Meta Ads Campaign ────────────────────────
	updateStatus(ctx, pub.ID, "publishing_ads", "", "")

	adsCreds, err := getMetaAdsCredentials(ctx, pub.UserID)
	if err != nil || adsCreds == nil {
		slog.Error("integrated_publish_ads_creds_error", "id", pub.ID.Hex(), "error", err)
		updateStatus(ctx, pub.ID, "failed", "Meta Ads not configured", "ads")
		return
	}

	igCreds, err := getInstagramCredentials(ctx, pub.UserID)
	if err != nil || igCreds == nil {
		slog.Error("integrated_publish_ig_creds_error", "id", pub.ID.Hex(), "error", err)
		updateStatus(ctx, pub.ID, "failed", "Instagram not configured", "ads")
		return
	}

	// Step 2a: Create Campaign
	campaignParams := url.Values{}
	campaignParams.Set("name", pub.Campaign.Name)
	campaignParams.Set("objective", pub.Campaign.Objective)
	campaignParams.Set("status", "PAUSED")
	campaignParams.Set("special_ad_categories", "NONE")

	campaignResult, err := metaGraphPost(
		adAccountPath(adsCreds.AdAccountID)+"/campaigns",
		adsCreds.Token,
		campaignParams,
	)
	if err != nil {
		slog.Error("integrated_publish_campaign_create_error", "id", pub.ID.Hex(), "error", err)
		updateStatus(ctx, pub.ID, "failed", "Campaign creation failed: "+err.Error(), "ads")
		return
	}
	metaCampaignID, _ := campaignResult["id"].(string)

	database.IntegratedPublishes().UpdateOne(ctx, bson.M{"_id": pub.ID}, bson.M{
		"$set": bson.M{"meta_campaign_id": metaCampaignID, "updated_at": time.Now()},
	})

	// Step 2b: Create Ad Set
	startTime := time.Now().Add(1 * time.Hour) // starts 1h from now
	endTime := startTime.AddDate(0, 0, pub.Campaign.DurationDays)

	adsetParams := url.Values{}
	adsetParams.Set("campaign_id", metaCampaignID)
	adsetParams.Set("name", pub.Campaign.Name+" - Ad Set")
	adsetParams.Set("daily_budget", fmt.Sprintf("%d", pub.Campaign.DailyBudget))
	adsetParams.Set("billing_event", "IMPRESSIONS")
	adsetParams.Set("optimization_goal", "REACH")
	adsetParams.Set("status", "PAUSED")
	adsetParams.Set("start_time", startTime.Format(time.RFC3339))
	adsetParams.Set("end_time", endTime.Format(time.RFC3339))

	targetingJSON, _ := json.Marshal(pub.Campaign.Targeting)
	adsetParams.Set("targeting", string(targetingJSON))

	adsetResult, err := metaGraphPost(
		adAccountPath(adsCreds.AdAccountID)+"/adsets",
		adsCreds.Token,
		adsetParams,
	)
	if err != nil {
		slog.Error("integrated_publish_adset_create_error", "id", pub.ID.Hex(), "error", err)
		updateStatus(ctx, pub.ID, "failed", "Ad Set creation failed: "+err.Error(), "ads")
		return
	}
	metaAdSetID, _ := adsetResult["id"].(string)

	database.IntegratedPublishes().UpdateOne(ctx, bson.M{"_id": pub.ID}, bson.M{
		"$set": bson.M{"meta_adset_id": metaAdSetID, "updated_at": time.Now()},
	})

	// Step 2c: Create Ad Creative + Ad
	// Build object_story_spec using the published IG media ID
	pageID := adsCreds.BusinessID
	if pageID == "" {
		pageID = igCreds.AccountID
	}

	objectStorySpec := map[string]interface{}{
		"page_id":            pageID,
		"instagram_actor_id": igCreds.AccountID,
	}

	// Use the IG post as the ad creative (existing_post approach)
	creativeParams := url.Values{}
	creativeParams.Set("name", pub.Campaign.Name+" Creative")

	// For ads that promote an existing IG post, we use object_story_id
	// Format: {instagram_account_id}_{media_id}
	objectStoryID := igCreds.AccountID + "_" + mediaID
	creativeParams.Set("object_story_id", objectStoryID)

	// If custom creative fields are provided, use object_story_spec instead
	if pub.Campaign.Creative.LinkURL != "" {
		specJSON, _ := json.Marshal(objectStorySpec)
		creativeParams.Del("object_story_id")
		creativeParams.Set("object_story_spec", string(specJSON))
	}

	creativeResult, err := metaGraphPost(
		adAccountPath(adsCreds.AdAccountID)+"/adcreatives",
		adsCreds.Token,
		creativeParams,
	)
	if err != nil {
		slog.Error("integrated_publish_creative_create_error", "id", pub.ID.Hex(), "error", err)
		updateStatus(ctx, pub.ID, "failed", "Ad creative creation failed: "+err.Error(), "ads")
		return
	}
	creativeID, _ := creativeResult["id"].(string)

	adParams := url.Values{}
	adParams.Set("name", pub.Campaign.Name+" - Ad")
	adParams.Set("adset_id", metaAdSetID)
	adParams.Set("status", "PAUSED")
	adParams.Set("creative", fmt.Sprintf(`{"creative_id":"%s"}`, creativeID))

	adResult, err := metaGraphPost(
		adAccountPath(adsCreds.AdAccountID)+"/ads",
		adsCreds.Token,
		adParams,
	)
	if err != nil {
		slog.Error("integrated_publish_ad_create_error", "id", pub.ID.Hex(), "error", err)
		updateStatus(ctx, pub.ID, "failed", "Ad creation failed: "+err.Error(), "ads")
		return
	}
	metaAdID, _ := adResult["id"].(string)

	// ── SUCCESS: Update final status ─────────────────────────────
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

// updateStatus is a helper to update status + error fields.
func updateStatus(ctx context.Context, id primitive.ObjectID, status, errMsg, errPhase string) {
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

// buildIntegratedPublishResponse creates a response with resolved image URLs.
func buildIntegratedPublishResponse(p models.IntegratedPublish) models.IntegratedPublishResponse {
	imageURLs := make([]string, len(p.ImageIDs))
	for i, id := range p.ImageIDs {
		imageURLs[i] = "/api/v1/blog/images/" + id
	}
	return models.IntegratedPublishResponse{
		IntegratedPublish: p,
		ImageURLs:         imageURLs,
	}
}
```

---

## 6. Rotas API

### Adicionar em `internal/router/router.go`

```go
// Integrated Publish routes (superuser + admin)
mux.Handle("GET /api/v1/admin/integrated-publish",
    middleware.Auth(middleware.RequireRole("superuser", "admin")(
        http.HandlerFunc(handlers.ListIntegratedPublishes))))
mux.Handle("POST /api/v1/admin/integrated-publish",
    middleware.Auth(middleware.RequireRole("superuser", "admin")(
        http.HandlerFunc(handlers.CreateIntegratedPublish))))
mux.Handle("GET /api/v1/admin/integrated-publish/{id}",
    middleware.Auth(middleware.RequireRole("superuser", "admin")(
        http.HandlerFunc(handlers.GetIntegratedPublish))))
mux.Handle("DELETE /api/v1/admin/integrated-publish/{id}",
    middleware.Auth(middleware.RequireRole("superuser", "admin")(
        http.HandlerFunc(handlers.DeleteIntegratedPublish))))
```

### Tabela de rotas

| Metodo | Path | Handler | Descricao |
|--------|------|---------|-----------|
| `POST` | `/api/v1/admin/integrated-publish` | `CreateIntegratedPublish` | Cria nova publicacao integrada (IG + Ads) |
| `GET` | `/api/v1/admin/integrated-publish` | `ListIntegratedPublishes` | Lista publicacoes com paginacao e filtro por status |
| `GET` | `/api/v1/admin/integrated-publish/{id}` | `GetIntegratedPublish` | Retorna uma publicacao especifica por ID |
| `DELETE` | `/api/v1/admin/integrated-publish/{id}` | `DeleteIntegratedPublish` | Deleta publicacao (apenas draft/scheduled/failed) |

### Query parameters para GET (listagem)

| Parametro | Tipo | Default | Descricao |
|-----------|------|---------|-----------|
| `page` | int | 1 | Pagina atual |
| `limit` | int | 10 | Itens por pagina (max 50) |
| `status` | string | - | Filtrar por status: `draft`, `scheduled`, `publishing_ig`, `publishing_ads`, `completed`, `failed` |

---

## 7. Background Jobs

### Registrar o scheduler em `cmd/api/main.go`

```go
// Start integrated publish scheduler
go integratedPublishScheduler()
```

```go
// integratedPublishScheduler runs every minute and processes due integrated publishes.
func integratedPublishScheduler() {
	// Wait for server to start
	time.Sleep(20 * time.Second)
	log.Println("Integrated publish scheduler started (1 min interval)")

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		handlers.ProcessScheduledIntegratedPublishes()
	}
}
```

### Fluxo do background job

```
Tick (1 min)
  |
  v
Query: status="scheduled" AND scheduled_at <= now
  |
  v
Para cada item:
  |
  |-- [1] status -> "publishing_ig"
  |       publishToInstagram(igSchedule)
  |       |
  |       |-- SUCESSO: salva ig_media_id
  |       |-- FALHA:   status -> "failed", error_phase -> "ig"
  |                    (PARA aqui, nao tenta ads)
  |
  |-- [2] status -> "publishing_ads"
  |       [2a] POST /act_{id}/campaigns    -> meta_campaign_id
  |       [2b] POST /act_{id}/adsets       -> meta_adset_id
  |       [2c] POST /act_{id}/adcreatives  -> creative_id
  |       [2d] POST /act_{id}/ads          -> meta_ad_id
  |       |
  |       |-- SUCESSO: status -> "completed"
  |       |-- FALHA:   status -> "failed", error_phase -> "ads"
  |                    (IG post JA FOI publicado; erro registrado)
  |
  v
Fim do ciclo
```

### Tratamento de erros

| Cenario | Comportamento |
|---------|---------------|
| Erro na publicacao IG | `status="failed"`, `error_phase="ig"`, `error_message` com detalhes. Ads NAO e criado. |
| IG OK, erro no Campaign | `status="failed"`, `error_phase="ads"`. IG post ja esta publicado. |
| IG OK, Campaign OK, erro no AdSet | Idem acima. Campaign foi criada mas AdSet falhou. |
| IG OK, Campaign OK, AdSet OK, erro no Ad | Campaign e AdSet existem, mas Ad nao foi criado. |
| Credenciais ausentes | `status="failed"` com mensagem descritiva. |
| Timeout do contexto | O proximo tick do scheduler tentara novamente (mas o item ja tera status "publishing_*", entao nao sera re-selecionado). Para retry, o usuario deve editar o status de volta para "scheduled". |

### Nota sobre idempotencia

O job NAO re-processa itens com status `publishing_ig` ou `publishing_ads`. Se o processo morrer no meio, o item ficara "travado". Uma melhoria futura pode adicionar um campo `locked_at` com TTL para detectar itens abandonados.

---

## 8. Frontend

### API Client (`tron-legacy-frontend/src/services/api.js`)

Adicionar o namespace `integratedPublish`:

```js
export const integratedPublish = {
  list: (params = {}) => {
    const query = new URLSearchParams();
    if (params.page) query.append('page', params.page);
    if (params.limit) query.append('limit', params.limit);
    if (params.status) query.append('status', params.status);
    const qs = query.toString();
    return api.get(`/api/v1/admin/integrated-publish${qs ? `?${qs}` : ''}`);
  },
  getById: (id) => api.get(`/api/v1/admin/integrated-publish/${id}`),
  create: (data) => api.post('/api/v1/admin/integrated-publish', data),
  delete: (id) => api.delete(`/api/v1/admin/integrated-publish/${id}`),
};
```

### Pagina: `IntegratedPublishPage.jsx`

Layout com dois paineis lado a lado (desktop) ou empilhados (mobile):

```
+-----------------------------------------------------+
|              Publicacao Integrada                     |
+-----------------------------------------------------+
|                                                       |
|  +--- Painel Esquerdo (IG) ---+  +--- Painel Direito (Ads) ---+
|  |                            |  |                              |
|  | [Upload de Imagens]        |  | Nome da Campanha:  [______] |
|  | [Preview das imagens]      |  | Objetivo:  [dropdown_____]  |
|  |                            |  | Orcamento diario: [R$___]   |
|  | Caption:                   |  | Duracao (dias):   [___]     |
|  | [___________________]      |  |                              |
|  | [___________________]      |  | -- Segmentacao --            |
|  |                            |  | Paises:    [______]          |
|  | Tipo: (o) Imagem           |  | Idade min: [__] max: [__]   |
|  |       (o) Carrossel        |  | Genero:    [dropdown]        |
|  |                            |  | Interesses: [busca___]       |
|  | Agendar para:              |  |                              |
|  | [____/____/____  __:__]    |  | -- Criativo do Ad --         |
|  |                            |  | (usa o post do IG como ad)   |
|  +----------------------------+  +------------------------------+
|                                                       |
|              [ Agendar Publicacao Integrada ]          |
|                                                       |
+-----------------------------------------------------+
|                                                       |
|  +--- Historico -----------------------------------+  |
|  | Status | Caption | Campanha | Data    | Acoes   |  |
|  |--------|---------|----------|---------|---------|  |
|  | OK     | Post... | Camp...  | 01/mar  | [X]     |  |
|  | Falha  | Post... | Camp...  | 28/fev  | [X][R]  |  |
|  | Agend. | Post... | Camp...  | 05/mar  | [X]     |  |
|  +------------------------------------------------+  |
|                                                       |
+-----------------------------------------------------+
```

### Componente React (estrutura simplificada)

```jsx
import { useState, useEffect } from 'react';
import { integratedPublish, instagram } from '../services/api';

export default function IntegratedPublishPage() {
  const [form, setForm] = useState({
    caption: '',
    media_type: 'image',
    image_ids: [],
    scheduled_at: '',
    campaign: {
      name: '',
      objective: 'OUTCOME_AWARENESS',
      daily_budget: 2000, // R$20,00
      duration_days: 7,
      targeting: {
        geo_locations: { countries: ['BR'] },
        age_min: 18,
        age_max: 65,
      },
      creative: {},
    },
  });

  const [items, setItems] = useState([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    loadHistory();
  }, []);

  const loadHistory = async () => {
    const data = await integratedPublish.list({ limit: 20 });
    setItems(data.items || []);
  };

  const handleSubmit = async (e) => {
    e.preventDefault();
    setLoading(true);
    try {
      await integratedPublish.create(form);
      await loadHistory();
      // reset form...
    } catch (err) {
      alert(err.message);
    } finally {
      setLoading(false);
    }
  };

  const handleDelete = async (id) => {
    if (!confirm('Excluir esta publicacao?')) return;
    await integratedPublish.delete(id);
    await loadHistory();
  };

  const handleImageUpload = async (file) => {
    const result = await instagram.uploadImage(file);
    setForm(prev => ({
      ...prev,
      image_ids: [...prev.image_ids, result.id],
    }));
  };

  const statusBadge = (status) => {
    const colors = {
      draft: 'gray',
      scheduled: 'blue',
      publishing_ig: 'yellow',
      publishing_ads: 'orange',
      completed: 'green',
      failed: 'red',
    };
    return <span className={`badge badge-${colors[status]}`}>{status}</span>;
  };

  return (
    <div className="integrated-publish-page">
      <h1>Publicacao Integrada</h1>

      <form onSubmit={handleSubmit} className="two-panel-form">
        {/* Left Panel: Instagram Config */}
        <div className="panel panel-ig">
          <h2>Instagram</h2>
          {/* Image upload, caption, media_type, scheduled_at */}
        </div>

        {/* Right Panel: Ads Config */}
        <div className="panel panel-ads">
          <h2>Campanha Meta Ads</h2>
          {/* Campaign name, objective, budget, duration, targeting */}
        </div>

        <button type="submit" disabled={loading}>
          {loading ? 'Agendando...' : 'Agendar Publicacao Integrada'}
        </button>
      </form>

      {/* History Table */}
      <div className="history-table">
        <h2>Historico</h2>
        <table>
          <thead>
            <tr>
              <th>Status</th>
              <th>Caption</th>
              <th>Campanha</th>
              <th>Agendado para</th>
              <th>Acoes</th>
            </tr>
          </thead>
          <tbody>
            {items.map(item => (
              <tr key={item.id}>
                <td>{statusBadge(item.status)}</td>
                <td>{item.caption?.substring(0, 50)}...</td>
                <td>{item.campaign?.name}</td>
                <td>{new Date(item.scheduled_at).toLocaleString('pt-BR')}</td>
                <td>
                  <button onClick={() => handleDelete(item.id)}>Excluir</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
```

### Rota no React Router

```jsx
<Route path="/admin/publicacao-integrada" element={<IntegratedPublishPage />} />
```

---

## 9. APIs Externas (Meta Graph API v21.0)

### Instagram Content Publishing API

| Endpoint | Metodo | Descricao | Parametros |
|----------|--------|-----------|------------|
| `POST /{ig-account-id}/media` | POST | Cria container de midia | `image_url`, `caption`, `access_token`, `is_carousel_item` |
| `POST /{ig-account-id}/media_publish` | POST | Publica o container | `creation_id`, `access_token` |

### Meta Marketing API

| Endpoint | Metodo | Descricao | Parametros principais |
|----------|--------|-----------|----------------------|
| `POST /act_{ad-account-id}/campaigns` | POST | Cria campanha | `name`, `objective`, `status`, `special_ad_categories` |
| `POST /act_{ad-account-id}/adsets` | POST | Cria conjunto de anuncios | `campaign_id`, `name`, `daily_budget`, `targeting`, `billing_event`, `optimization_goal`, `start_time`, `end_time` |
| `POST /act_{ad-account-id}/adcreatives` | POST | Cria criativo | `name`, `object_story_id` (para promover post existente) |
| `POST /act_{ad-account-id}/ads` | POST | Cria anuncio | `name`, `adset_id`, `creative`, `status` |

### Sobre `object_story_id`

Para promover um post existente do Instagram como anuncio, a Meta API aceita `object_story_id` no formato:

```
{instagram_account_id}_{ig_media_id}
```

Isso permite que o ad use exatamente o mesmo conteudo visual do post organico, sem precisar re-upload de imagens.

---

## 10. Codigo Reutilizado

### Funcoes existentes referenciadas pelo handler

| Funcao | Arquivo | Uso na feature |
|--------|---------|----------------|
| `publishToInstagram(schedule)` | `internal/handlers/instagram.go` | Fase 1: publica o post no IG |
| `createMediaContainer(accountID, token, imageURL, caption, isCarouselItem)` | `internal/handlers/instagram.go` | Chamada internamente por `publishToInstagram` |
| `publishMediaContainer(accountID, token, creationID)` | `internal/handlers/instagram.go` | Chamada internamente por `publishToInstagram` |
| `getInstagramCredentials(ctx, userID)` | `internal/handlers/instagram.go` | Obter credenciais IG do usuario |
| `getMetaAdsCredentials(ctx, userID)` | `internal/handlers/meta_ads.go` | Obter credenciais Meta Ads do usuario |
| `metaGraphPost(endpoint, token, params)` | `internal/handlers/meta_ads.go` | Chamadas POST para a Graph API |
| `adAccountPath(adAccountID)` | `internal/handlers/meta_ads.go` | Formata path `/act_{id}` |
| `buildObjectStorySpec(creds, creative)` | `internal/handlers/meta_ads.go` | Disponivel se necessario para criativos customizados |
| `getPublicImageURL(imageID)` | `internal/handlers/instagram.go` | Chamada internamente por `publishToInstagram` |
| `middleware.GetUserID(r)` | `internal/middleware/auth.go` | Extrai userID do contexto JWT |
| `middleware.Auth(handler)` | `internal/middleware/auth.go` | Middleware de autenticacao |
| `middleware.RequireRole(roles...)` | `internal/middleware/role.go` | Middleware de autorizacao por role |

### Models reutilizados

| Model | Arquivo | Uso |
|-------|---------|-----|
| `InstagramSchedule` | `internal/models/instagram.go` | Struct reutilizada para montar o "schedule" passado a `publishToInstagram()` |
| `AdSetTargeting` | `internal/models/meta_ads.go` | Struct de targeting reutilizada dentro de `IntegratedCampaignConfig` |
| `GeoLocation` | `internal/models/meta_ads.go` | Sub-struct de targeting |
| `TargetEntity` | `internal/models/meta_ads.go` | Sub-struct para interesses/audiencias |
| `LocationEntry` | `internal/models/meta_ads.go` | Sub-struct para cidades/regioes |

---

## 11. Fluxo Completo (Jornada do Usuario)

### Passo a passo

1. **Usuario acessa** `/admin/publicacao-integrada` no frontend

2. **Painel esquerdo (Instagram)**:
   - Faz upload de 1 ou mais imagens (usa `instagram.uploadImage`)
   - Escreve a caption do post
   - Seleciona tipo: imagem unica ou carrossel
   - Define data/hora de agendamento

3. **Painel direito (Meta Ads)**:
   - Define nome da campanha
   - Seleciona objetivo (Awareness, Engagement, Traffic, etc.)
   - Define orcamento diario em reais (convertido para centavos)
   - Define duracao em dias
   - Configura segmentacao (pais, idade, genero, interesses)

4. **Clica em "Agendar Publicacao Integrada"**
   - Frontend envia `POST /api/v1/admin/integrated-publish`
   - Backend valida tudo, salva com `status: "scheduled"`
   - Item aparece na tabela de historico com badge azul "scheduled"

5. **Background job processa** (quando `scheduled_at <= now`):
   - Status muda para "publishing_ig" (badge amarelo)
   - Post e publicado no Instagram via Graph API
   - Status muda para "publishing_ads" (badge laranja)
   - Campaign, AdSet, AdCreative e Ad sao criados na Meta Ads API
   - Status muda para "completed" (badge verde)

6. **Se ocorrer erro**:
   - Status muda para "failed" (badge vermelho)
   - `error_message` e `error_phase` indicam onde falhou
   - Usuario pode ver o erro na tabela e deletar/recriar

7. **Resultado final**:
   - Post organico publicado no Instagram
   - Campanha de ads criada (PAUSED) no Meta Ads Manager
   - Usuario pode ativar a campanha manualmente quando desejar

---

## 12. Verificacao (Teste End-to-End)

### Pre-requisitos

- [ ] Credenciais Instagram configuradas (via `/admin/instagram/config`)
- [ ] Credenciais Meta Ads configuradas (mesma config ou `/admin/meta-ads`)
- [ ] Pelo menos uma imagem ja uploaded via `/admin/instagram/upload`
- [ ] Servidor rodando com `ENCRYPTION_KEY` configurado

### Testes manuais

#### 1. Criacao (POST)

```bash
curl -X POST http://localhost:8088/api/v1/admin/integrated-publish \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "caption": "Teste de publicacao integrada! #tron",
    "media_type": "image",
    "image_ids": ["<IMAGE_OBJECT_ID>"],
    "scheduled_at": "2026-03-06T15:00:00Z",
    "campaign": {
      "name": "Campanha Teste Integrada",
      "objective": "OUTCOME_AWARENESS",
      "daily_budget": 2000,
      "duration_days": 3,
      "targeting": {
        "geo_locations": {
          "countries": ["BR"]
        },
        "age_min": 18,
        "age_max": 45
      },
      "creative": {}
    }
  }'
```

**Esperado**: 201 Created com o objeto completo e `status: "scheduled"`

#### 2. Listagem (GET)

```bash
curl http://localhost:8088/api/v1/admin/integrated-publish?page=1&limit=10 \
  -H "Authorization: Bearer <TOKEN>"
```

**Esperado**: JSON com `items`, `total`, `page`, `limit`

#### 3. Detalhes (GET por ID)

```bash
curl http://localhost:8088/api/v1/admin/integrated-publish/<ID> \
  -H "Authorization: Bearer <TOKEN>"
```

**Esperado**: JSON com todos os campos, incluindo `image_urls` resolvidas

#### 4. Processamento do scheduler

- Agendar para o passado (ou esperar o momento chegar)
- Verificar nos logs do servidor:
  ```
  integrated_publish_ig_done  id=xxx ig_media_id=yyy
  integrated_publish_completed id=xxx meta_campaign_id=zzz
  ```
- Confirmar no MongoDB: `status: "completed"`, campos `ig_media_id`, `meta_campaign_id`, `meta_adset_id`, `meta_ad_id` preenchidos
- Confirmar no Instagram: post aparece no feed
- Confirmar no Meta Ads Manager: campanha, ad set e ad criados com status PAUSED

#### 5. Teste de falha (IG)

- Configurar credenciais IG invalidas
- Agendar publicacao integrada
- Verificar: `status: "failed"`, `error_phase: "ig"`, campanha NAO criada

#### 6. Teste de falha (Ads)

- Configurar credenciais IG validas, Ads invalidas
- Agendar publicacao integrada
- Verificar: `status: "failed"`, `error_phase: "ads"`, post DO Instagram ja publicado

#### 7. Delecao (DELETE)

```bash
curl -X DELETE http://localhost:8088/api/v1/admin/integrated-publish/<ID> \
  -H "Authorization: Bearer <TOKEN>"
```

**Esperado para `status: "scheduled"`**: 200 OK com `{"message": "Integrated publish deleted"}`

**Esperado para `status: "publishing_ig"`**: 400 Bad Request com mensagem de erro

#### 8. Validacoes de entrada

| Teste | Esperado |
|-------|----------|
| Enviar sem `image_ids` | 400: "At least one image is required" |
| `media_type: "image"` com 2+ images | 400: "image type allows only one image" |
| `media_type: "carousel"` com 1 image | 400: "carousel requires at least 2 images" |
| `caption` com 2201+ caracteres | 400: "Caption must be 2200 characters or less" |
| `campaign.name` vazio | 400: "campaign.name is required" |
| `campaign.daily_budget: 0` | 400: "campaign.daily_budget must be > 0" |
| `campaign.duration_days: 0` | 400: "campaign.duration_days must be > 0" |
| `scheduled_at` invalido | 400: "scheduled_at must be a valid ISO 8601 date" |
| Image ID inexistente | 400: "Image not found: xxx" |
| IG nao configurado | 400: "Instagram not configured..." |
| Ads nao configurado | 400: "Meta Ads not configured..." |

### Checklist de integracao

- [ ] Collection `integrated_publishes` criada automaticamente pelo MongoDB
- [ ] Indices criados via `EnsureIndexes()`
- [ ] Funcao `IntegratedPublishes()` adicionada em `database/mongo.go`
- [ ] Handler file `integrated_publish.go` criado em `internal/handlers/`
- [ ] Model file `integrated_publish.go` criado em `internal/models/`
- [ ] Rotas registradas em `internal/router/router.go`
- [ ] Scheduler registrado em `cmd/api/main.go`
- [ ] Namespace `integratedPublish` adicionado em `src/services/api.js`
- [ ] Pagina `IntegratedPublishPage.jsx` criada e rota adicionada no React Router
- [ ] Testes manuais de criacao, listagem, processamento e delecao passando
