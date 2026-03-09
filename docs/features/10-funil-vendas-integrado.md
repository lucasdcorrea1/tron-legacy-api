# 10 - Funil de Vendas Integrado

## 1. Visao Geral

O Funil de Vendas Integrado rastreia a jornada completa do cliente: desde o engajamento no Instagram (comentarios, DMs) ate a conversao final (clique em anuncio, visita a landing page, acao manual). A funcionalidade consolida dados de multiplas fontes ja existentes no sistema — `instagram_leads`, `auto_reply_logs` e Meta Ads Insights — em uma colecao unificada de eventos (`funnel_events`), permitindo visualizar o funil em estagios claros com taxas de conversao entre cada etapa.

Alem do funil, a feature inclui um gerenciador de templates UTM que permite criar, salvar e gerar URLs rastreadas para atribuicao precisa de campanhas.

**Fontes de dados:**
- Collection `instagram_leads` — contatos capturados via DM/comentarios do Instagram
- Collection `auto_reply_logs` — logs de respostas automaticas enviadas
- Meta Ads Insights API — cliques em anuncios, impressoes, conversoes
- Entrada manual — eventos registrados pelo usuario via API

**Estagios do funil:**
1. **Awareness** — usuario viu/interagiu pela primeira vez (comentario, DM)
2. **Interest** — usuario recebeu resposta automatica / continuou interagindo
3. **Consideration** — usuario clicou em anuncio / visitou landing page
4. **Conversion** — acao de conversao registrada (compra, formulario, manual)

---

## 2. Arquitetura

```
+---------------------+     +---------------------+     +-------------------------+
|  instagram_leads    |     |  auto_reply_logs    |     |  Meta Ads Insights API  |
|  (MongoDB)          |     |  (MongoDB)          |     |  (Graph API v21.0)      |
+----------+----------+     +----------+----------+     +------------+------------+
           |                           |                              |
           v                           v                              v
+--------------------------------------------------------------------------+
|                     SyncFunnelData() — Background Job (1h)               |
|                                                                          |
|  1. Busca novos leads desde ultimo sync -> cria evento "awareness"       |
|  2. Busca logs de auto-reply enviados   -> cria evento "interest"        |
|  3. Busca cliques em anuncios via API   -> cria evento "consideration"   |
|  4. Eventos "conversion" sao criados manualmente via API                 |
+--------------------------------------------------------------------------+
           |
           v
+---------------------+
|   funnel_events     |
|   (MongoDB)         |
|                     |
|  - user_id          |
|  - contact_id       |
|  - stage            |
|  - source           |
|  - metadata         |
|  - created_at       |
+---------------------+
           |
           v
+---------------------+     +---------------------+
|  GET /funnel/summary|     |  GET /funnel/events  |
|  (aggregation)      |     |  (listagem)          |
+---------------------+     +---------------------+
           |
           v
+---------------------+
|  Frontend           |
|  FunnelPage.jsx     |
|  - Funil visual     |
|  - Cards por stage  |
|  - Timeline contato |
|  - UTM Builder      |
+---------------------+
```

Separadamente, a gestao de UTMs utiliza a collection `utm_templates` para salvar templates reutilizaveis e uma rota para gerar URLs completas a partir deles.

---

## 3. Models (Go)

### `internal/models/funnel.go`

```go
package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ── Funnel Events ───────────────────────────────────────────────────

// FunnelEvent represents a single event in the sales funnel.
type FunnelEvent struct {
	ID        primitive.ObjectID     `json:"id" bson:"_id,omitempty"`
	UserID    primitive.ObjectID     `json:"user_id" bson:"user_id"`
	ContactID string                 `json:"contact_id" bson:"contact_id"` // sender_ig_id or external ID
	Stage     string                 `json:"stage" bson:"stage"`           // "awareness", "interest", "consideration", "conversion"
	Source    string                 `json:"source" bson:"source"`         // "instagram_dm", "instagram_comment", "ad_click", "landing_page", "manual"
	Metadata  map[string]interface{} `json:"metadata,omitempty" bson:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at" bson:"created_at"`
}

// ── UTM Templates ───────────────────────────────────────────────────

// UTMTemplate is a saved UTM parameter set for reuse.
type UTMTemplate struct {
	ID        primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID    primitive.ObjectID `json:"user_id" bson:"user_id"`
	Name      string             `json:"name" bson:"name"`
	Source    string             `json:"source" bson:"source"`     // e.g. "instagram", "facebook"
	Medium    string             `json:"medium" bson:"medium"`     // e.g. "social", "cpc"
	Campaign  string             `json:"campaign" bson:"campaign"` // e.g. "lancamento_produto"
	Content   string             `json:"content,omitempty" bson:"content,omitempty"`
	Term      string             `json:"term,omitempty" bson:"term,omitempty"`
	CreatedAt time.Time          `json:"created_at" bson:"created_at"`
}

// ── Funnel Summary (computed, not stored) ───────────────────────────

// FunnelStage represents a single stage in the funnel summary.
type FunnelStage struct {
	Name           string  `json:"name"`
	Count          int64   `json:"count"`
	ConversionRate float64 `json:"conversion_rate"` // percentage to next stage
	AvgTimeInStage float64 `json:"avg_time_in_stage_hours"` // average hours spent before moving to next stage
}

// FunnelSummary is the aggregated funnel view.
type FunnelSummary struct {
	Stages         []FunnelStage `json:"stages"`
	TotalContacts  int64         `json:"total_contacts"`
	OverallConvRate float64      `json:"overall_conversion_rate"` // awareness -> conversion
	Period         string        `json:"period"`                  // e.g. "30d"
}

// ── Request/Response types ──────────────────────────────────────────

type CreateFunnelEventRequest struct {
	ContactID string                 `json:"contact_id"`
	Stage     string                 `json:"stage"`
	Source    string                 `json:"source"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

type CreateUTMTemplateRequest struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Medium   string `json:"medium"`
	Campaign string `json:"campaign"`
	Content  string `json:"content,omitempty"`
	Term     string `json:"term,omitempty"`
}

type GenerateUTMRequest struct {
	BaseURL    string `json:"base_url"`
	TemplateID string `json:"template_id,omitempty"` // if provided, uses template values
	Source     string `json:"source,omitempty"`       // override or manual
	Medium     string `json:"medium,omitempty"`
	Campaign   string `json:"campaign,omitempty"`
	Content    string `json:"content,omitempty"`
	Term       string `json:"term,omitempty"`
}

type GenerateUTMResponse struct {
	URL string `json:"url"`
}

type FunnelEventListResponse struct {
	Events []FunnelEvent `json:"events"`
	Total  int64         `json:"total"`
	Page   int           `json:"page"`
	Limit  int           `json:"limit"`
}

type ContactJourneyResponse struct {
	ContactID string        `json:"contact_id"`
	Events    []FunnelEvent `json:"events"`
	CurrentStage string     `json:"current_stage"`
}
```

---

## 4. Database

### Collections

Adicionar em `internal/database/mongo.go`:

```go
func FunnelEvents() *mongo.Collection {
	return DB.Collection("funnel_events")
}

func UTMTemplates() *mongo.Collection {
	return DB.Collection("utm_templates")
}
```

### Indexes

Adicionar em `EnsureIndexes()` dentro de `internal/database/mongo.go`:

```go
// funnel_events: compound index on {user_id, stage, created_at} for funnel queries
_, err = FunnelEvents().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "stage", Value: 1}, {Key: "created_at", Value: -1}},
})
if err != nil {
	return err
}

// funnel_events: index on {user_id, contact_id} for contact journey lookup
_, err = FunnelEvents().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "contact_id", Value: 1}},
})
if err != nil {
	return err
}

// funnel_events: index on {contact_id, source, created_at} for dedup in sync
_, err = FunnelEvents().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "contact_id", Value: 1}, {Key: "source", Value: 1}, {Key: "created_at", Value: 1}},
})
if err != nil {
	return err
}

// utm_templates: index on user_id for listing
_, err = UTMTemplates().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}},
})
if err != nil {
	return err
}
```

### Estrutura dos documentos

**funnel_events:**
```json
{
  "_id": ObjectId("..."),
  "user_id": ObjectId("..."),
  "contact_id": "17841400123456",
  "stage": "awareness",
  "source": "instagram_dm",
  "metadata": {
    "rule_name": "Promo Verao",
    "trigger_text": "quero saber mais"
  },
  "created_at": ISODate("2026-03-01T14:30:00Z")
}
```

**utm_templates:**
```json
{
  "_id": ObjectId("..."),
  "user_id": ObjectId("..."),
  "name": "Campanha Instagram Stories",
  "source": "instagram",
  "medium": "social",
  "campaign": "stories_marco_2026",
  "content": "cta_swipe_up",
  "term": "",
  "created_at": ISODate("2026-03-01T10:00:00Z")
}
```

---

## 5. Handlers (Go)

### `internal/handlers/funnel.go`

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
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ══════════════════════════════════════════════════════════════════════
// FUNNEL SUMMARY
// ══════════════════════════════════════════════════════════════════════

// GetFunnelSummary returns aggregated funnel data with counts per stage
// and conversion rates between stages.
// GET /api/v1/admin/funnel/summary?days=30
func GetFunnelSummary(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 1 || days > 365 {
		days = 30
	}

	since := time.Now().AddDate(0, 0, -days)

	stageNames := []string{"awareness", "interest", "consideration", "conversion"}
	stages := make([]models.FunnelStage, len(stageNames))

	// Count events per stage
	for i, stage := range stageNames {
		count, _ := database.FunnelEvents().CountDocuments(ctx, bson.M{
			"user_id":    userID,
			"stage":      stage,
			"created_at": bson.M{"$gte": since},
		})
		stages[i] = models.FunnelStage{
			Name:  stage,
			Count: count,
		}
	}

	// Calculate conversion rates between consecutive stages
	for i := 0; i < len(stages)-1; i++ {
		if stages[i].Count > 0 {
			stages[i].ConversionRate = float64(stages[i+1].Count) / float64(stages[i].Count) * 100
		}
	}

	// Calculate average time in stage using aggregation pipeline
	for i := 0; i < len(stages)-1; i++ {
		stages[i].AvgTimeInStage = computeAvgTimeInStage(ctx, userID, stageNames[i], stageNames[i+1], since)
	}

	// Count unique contacts
	pipeline := []bson.M{
		{"$match": bson.M{"user_id": userID, "created_at": bson.M{"$gte": since}}},
		{"$group": bson.M{"_id": "$contact_id"}},
		{"$count": "total"},
	}
	cursor, err := database.FunnelEvents().Aggregate(ctx, pipeline)
	if err != nil {
		slog.Error("funnel_summary_aggregate_error", "error", err)
	}
	var totalResult []struct {
		Total int64 `bson:"total"`
	}
	cursor.All(ctx, &totalResult)
	cursor.Close(ctx)

	var totalContacts int64
	if len(totalResult) > 0 {
		totalContacts = totalResult[0].Total
	}

	var overallRate float64
	if stages[0].Count > 0 {
		overallRate = float64(stages[len(stages)-1].Count) / float64(stages[0].Count) * 100
	}

	json.NewEncoder(w).Encode(models.FunnelSummary{
		Stages:          stages,
		TotalContacts:   totalContacts,
		OverallConvRate: overallRate,
		Period:          fmt.Sprintf("%dd", days),
	})
}

// computeAvgTimeInStage calculates the average time (in hours) contacts spend
// in stageFrom before progressing to stageTo.
func computeAvgTimeInStage(ctx context.Context, userID primitive.ObjectID, stageFrom, stageTo string, since time.Time) float64 {
	// Find contacts that have both stages
	pipeline := []bson.M{
		{"$match": bson.M{
			"user_id":    userID,
			"stage":      bson.M{"$in": []string{stageFrom, stageTo}},
			"created_at": bson.M{"$gte": since},
		}},
		{"$sort": bson.M{"created_at": 1}},
		{"$group": bson.M{
			"_id": "$contact_id",
			"stages": bson.M{"$push": bson.M{
				"stage":      "$stage",
				"created_at": "$created_at",
			}},
		}},
	}

	cursor, err := database.FunnelEvents().Aggregate(ctx, pipeline)
	if err != nil {
		return 0
	}
	defer cursor.Close(ctx)

	type stageEntry struct {
		Stage     string    `bson:"stage"`
		CreatedAt time.Time `bson:"created_at"`
	}
	type contactStages struct {
		ContactID string       `bson:"_id"`
		Stages    []stageEntry `bson:"stages"`
	}

	var totalHours float64
	var count int

	for cursor.Next(ctx) {
		var cs contactStages
		if err := cursor.Decode(&cs); err != nil {
			continue
		}

		var fromTime, toTime time.Time
		foundFrom, foundTo := false, false

		for _, s := range cs.Stages {
			if s.Stage == stageFrom && !foundFrom {
				fromTime = s.CreatedAt
				foundFrom = true
			}
			if s.Stage == stageTo && foundFrom && !foundTo {
				toTime = s.CreatedAt
				foundTo = true
			}
		}

		if foundFrom && foundTo {
			diff := toTime.Sub(fromTime).Hours()
			if diff >= 0 {
				totalHours += diff
				count++
			}
		}
	}

	if count > 0 {
		return totalHours / float64(count)
	}
	return 0
}

// ══════════════════════════════════════════════════════════════════════
// FUNNEL EVENTS
// ══════════════════════════════════════════════════════════════════════

// ListFunnelEvents returns paginated funnel events with optional filters.
// GET /api/v1/admin/funnel/events?stage=awareness&source=instagram_dm&page=1&limit=50
func ListFunnelEvents(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	filter := bson.M{"user_id": userID}

	if stage := r.URL.Query().Get("stage"); stage != "" {
		filter["stage"] = stage
	}
	if source := r.URL.Query().Get("source"); source != "" {
		filter["source"] = source
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 50
	}
	skip := int64((page - 1) * limit)

	total, _ := database.FunnelEvents().CountDocuments(ctx, filter)

	opts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}}).
		SetSkip(skip).
		SetLimit(int64(limit))

	cursor, err := database.FunnelEvents().Find(ctx, filter, opts)
	if err != nil {
		http.Error(w, "Error fetching events", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var events []models.FunnelEvent
	if err := cursor.All(ctx, &events); err != nil {
		http.Error(w, "Error decoding events", http.StatusInternalServerError)
		return
	}

	if events == nil {
		events = []models.FunnelEvent{}
	}

	json.NewEncoder(w).Encode(models.FunnelEventListResponse{
		Events: events,
		Total:  total,
		Page:   page,
		Limit:  limit,
	})
}

// CreateFunnelEvent allows manual creation of a funnel event.
// POST /api/v1/admin/funnel/events
func CreateFunnelEvent(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req models.CreateFunnelEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	validStages := map[string]bool{"awareness": true, "interest": true, "consideration": true, "conversion": true}
	if !validStages[req.Stage] {
		http.Error(w, "Invalid stage. Must be: awareness, interest, consideration, conversion", http.StatusBadRequest)
		return
	}

	validSources := map[string]bool{"instagram_dm": true, "instagram_comment": true, "ad_click": true, "landing_page": true, "manual": true}
	if !validSources[req.Source] {
		http.Error(w, "Invalid source. Must be: instagram_dm, instagram_comment, ad_click, landing_page, manual", http.StatusBadRequest)
		return
	}

	if req.ContactID == "" {
		http.Error(w, "contact_id is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	event := models.FunnelEvent{
		ID:        primitive.NewObjectID(),
		UserID:    userID,
		ContactID: req.ContactID,
		Stage:     req.Stage,
		Source:    req.Source,
		Metadata:  req.Metadata,
		CreatedAt: time.Now(),
	}

	_, err := database.FunnelEvents().InsertOne(ctx, event)
	if err != nil {
		http.Error(w, "Error creating event", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(event)
}

// ══════════════════════════════════════════════════════════════════════
// CONTACT JOURNEY
// ══════════════════════════════════════════════════════════════════════

// GetContactJourney returns all funnel events for a specific contact, ordered chronologically.
// GET /api/v1/admin/funnel/contacts/{id}/journey
func GetContactJourney(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	contactID := r.PathValue("id")
	if contactID == "" {
		http.Error(w, "Contact ID required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	cursor, err := database.FunnelEvents().Find(ctx,
		bson.M{"user_id": userID, "contact_id": contactID},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}),
	)
	if err != nil {
		http.Error(w, "Error fetching journey", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var events []models.FunnelEvent
	if err := cursor.All(ctx, &events); err != nil {
		http.Error(w, "Error decoding events", http.StatusInternalServerError)
		return
	}

	if events == nil {
		events = []models.FunnelEvent{}
	}

	// Determine current stage (latest event's stage)
	currentStage := ""
	if len(events) > 0 {
		currentStage = events[len(events)-1].Stage
	}

	json.NewEncoder(w).Encode(models.ContactJourneyResponse{
		ContactID:    contactID,
		Events:       events,
		CurrentStage: currentStage,
	})
}

// ══════════════════════════════════════════════════════════════════════
// UTM TEMPLATES
// ══════════════════════════════════════════════════════════════════════

// ListUTMTemplates returns all UTM templates for the authenticated user.
// GET /api/v1/admin/funnel/utm-templates
func ListUTMTemplates(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cursor, err := database.UTMTemplates().Find(ctx,
		bson.M{"user_id": userID},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}),
	)
	if err != nil {
		http.Error(w, "Error fetching UTM templates", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var templates []models.UTMTemplate
	if err := cursor.All(ctx, &templates); err != nil {
		http.Error(w, "Error decoding templates", http.StatusInternalServerError)
		return
	}

	if templates == nil {
		templates = []models.UTMTemplate{}
	}

	json.NewEncoder(w).Encode(templates)
}

// CreateUTMTemplate creates a new UTM template.
// POST /api/v1/admin/funnel/utm-templates
func CreateUTMTemplate(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req models.CreateUTMTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" || req.Source == "" || req.Medium == "" || req.Campaign == "" {
		http.Error(w, "name, source, medium and campaign are required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	tpl := models.UTMTemplate{
		ID:        primitive.NewObjectID(),
		UserID:    userID,
		Name:      req.Name,
		Source:    req.Source,
		Medium:    req.Medium,
		Campaign:  req.Campaign,
		Content:   req.Content,
		Term:      req.Term,
		CreatedAt: time.Now(),
	}

	_, err := database.UTMTemplates().InsertOne(ctx, tpl)
	if err != nil {
		http.Error(w, "Error creating UTM template", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(tpl)
}

// DeleteUTMTemplate removes a UTM template.
// DELETE /api/v1/admin/funnel/utm-templates/{id}
func DeleteUTMTemplate(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	tplID := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(tplID)
	if err != nil {
		http.Error(w, "Invalid template ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	result, err := database.UTMTemplates().DeleteOne(ctx, bson.M{"_id": oid, "user_id": userID})
	if err != nil {
		http.Error(w, "Error deleting template", http.StatusInternalServerError)
		return
	}

	if result.DeletedCount == 0 {
		http.Error(w, "Template not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"message": "UTM template deleted"})
}

// GenerateUTMUrl builds a full URL with UTM parameters.
// POST /api/v1/admin/funnel/utm-templates/generate
func GenerateUTMUrl(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req models.GenerateUTMRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.BaseURL == "" {
		http.Error(w, "base_url is required", http.StatusBadRequest)
		return
	}

	utmSource := req.Source
	utmMedium := req.Medium
	utmCampaign := req.Campaign
	utmContent := req.Content
	utmTerm := req.Term

	// If template_id is provided, load values from template (manual overrides take precedence)
	if req.TemplateID != "" {
		oid, err := primitive.ObjectIDFromHex(req.TemplateID)
		if err == nil {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()

			var tpl models.UTMTemplate
			err := database.UTMTemplates().FindOne(ctx, bson.M{"_id": oid, "user_id": userID}).Decode(&tpl)
			if err == nil {
				if utmSource == "" {
					utmSource = tpl.Source
				}
				if utmMedium == "" {
					utmMedium = tpl.Medium
				}
				if utmCampaign == "" {
					utmCampaign = tpl.Campaign
				}
				if utmContent == "" {
					utmContent = tpl.Content
				}
				if utmTerm == "" {
					utmTerm = tpl.Term
				}
			}
		}
	}

	if utmSource == "" || utmMedium == "" || utmCampaign == "" {
		http.Error(w, "source, medium and campaign are required (via template or direct params)", http.StatusBadRequest)
		return
	}

	// Build URL
	parsedURL, err := url.Parse(req.BaseURL)
	if err != nil {
		http.Error(w, "Invalid base_url", http.StatusBadRequest)
		return
	}

	params := parsedURL.Query()
	params.Set("utm_source", utmSource)
	params.Set("utm_medium", utmMedium)
	params.Set("utm_campaign", utmCampaign)
	if utmContent != "" {
		params.Set("utm_content", utmContent)
	}
	if utmTerm != "" {
		params.Set("utm_term", utmTerm)
	}
	parsedURL.RawQuery = params.Encode()

	json.NewEncoder(w).Encode(models.GenerateUTMResponse{
		URL: parsedURL.String(),
	})
}

// ══════════════════════════════════════════════════════════════════════
// SYNC (Background Job)
// ══════════════════════════════════════════════════════════════════════

// SyncFunnelData is called by a background goroutine every 1 hour.
// It reads data from instagram_leads, auto_reply_logs, and Meta Ads
// insights, and creates funnel_events for new entries.
func SyncFunnelData() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	slog.Info("funnel_sync_started")

	// Get all users that have Instagram or Meta Ads configured
	userIDs := getActiveUserIDs(ctx)

	for _, userID := range userIDs {
		syncLeadsToFunnel(ctx, userID)
		syncAutoReplyToFunnel(ctx, userID)
		syncAdClicksToFunnel(ctx, userID)
	}

	slog.Info("funnel_sync_completed", "users_processed", len(userIDs))
}

// getActiveUserIDs returns user IDs that have Instagram configs.
func getActiveUserIDs(ctx context.Context) []primitive.ObjectID {
	cursor, err := database.InstagramConfigs().Find(ctx, bson.M{}, options.Find().SetProjection(bson.M{"user_id": 1}))
	if err != nil {
		return nil
	}
	defer cursor.Close(ctx)

	var configs []struct {
		UserID primitive.ObjectID `bson:"user_id"`
	}
	cursor.All(ctx, &configs)

	ids := make([]primitive.ObjectID, len(configs))
	for i, c := range configs {
		ids[i] = c.UserID
	}
	return ids
}

// syncLeadsToFunnel creates "awareness" events from instagram_leads that
// don't yet have a corresponding funnel event.
func syncLeadsToFunnel(ctx context.Context, userID primitive.ObjectID) {
	// Find leads that were created/updated in the last 2 hours (overlap window)
	since := time.Now().Add(-2 * time.Hour)

	cursor, err := database.InstagramLeads().Find(ctx, bson.M{
		"created_at": bson.M{"$gte": since},
	})
	if err != nil {
		slog.Error("funnel_sync_leads_error", "error", err, "user_id", userID.Hex())
		return
	}
	defer cursor.Close(ctx)

	var leads []models.InstagramLead
	cursor.All(ctx, &leads)

	for _, lead := range leads {
		// Check if awareness event already exists for this contact
		count, _ := database.FunnelEvents().CountDocuments(ctx, bson.M{
			"user_id":    userID,
			"contact_id": lead.SenderIGID,
			"stage":      "awareness",
		})
		if count > 0 {
			continue
		}

		// Determine source from lead data
		source := "instagram_dm"
		if len(lead.Sources) > 0 {
			if lead.Sources[0] == "comment" {
				source = "instagram_comment"
			}
		}

		event := models.FunnelEvent{
			ID:        primitive.NewObjectID(),
			UserID:    userID,
			ContactID: lead.SenderIGID,
			Stage:     "awareness",
			Source:    source,
			Metadata: map[string]interface{}{
				"username":          lead.SenderUsername,
				"interaction_count": lead.InteractionCount,
				"synced":            true,
			},
			CreatedAt: lead.FirstInteraction,
		}

		database.FunnelEvents().InsertOne(ctx, event)
	}
}

// syncAutoReplyToFunnel creates "interest" events from auto_reply_logs
// (status=sent) that don't yet have a corresponding funnel event.
func syncAutoReplyToFunnel(ctx context.Context, userID primitive.ObjectID) {
	since := time.Now().Add(-2 * time.Hour)

	cursor, err := database.AutoReplyLogs().Find(ctx, bson.M{
		"status":     "sent",
		"created_at": bson.M{"$gte": since},
	})
	if err != nil {
		slog.Error("funnel_sync_autoreply_error", "error", err, "user_id", userID.Hex())
		return
	}
	defer cursor.Close(ctx)

	var logs []models.AutoReplyLog
	cursor.All(ctx, &logs)

	for _, lg := range logs {
		// Check if interest event already exists for this contact + timestamp
		count, _ := database.FunnelEvents().CountDocuments(ctx, bson.M{
			"user_id":    userID,
			"contact_id": lg.SenderIGID,
			"stage":      "interest",
			"source":     "instagram_dm",
			"created_at": lg.CreatedAt,
		})
		if count > 0 {
			continue
		}

		event := models.FunnelEvent{
			ID:        primitive.NewObjectID(),
			UserID:    userID,
			ContactID: lg.SenderIGID,
			Stage:     "interest",
			Source:    "instagram_dm",
			Metadata: map[string]interface{}{
				"rule_name":    lg.RuleName,
				"trigger_text": lg.TriggerText,
				"trigger_type": lg.TriggerType,
				"synced":       true,
			},
			CreatedAt: lg.CreatedAt,
		}

		database.FunnelEvents().InsertOne(ctx, event)
	}
}

// syncAdClicksToFunnel fetches ad click data from Meta Ads Insights API
// and creates "consideration" events.
func syncAdClicksToFunnel(ctx context.Context, userID primitive.ObjectID) {
	creds, err := getMetaAdsCredentials(ctx, userID)
	if err != nil || creds == nil {
		return // user has no Meta Ads configured
	}

	// Fetch ad-level insights for the last 2 hours window
	now := time.Now()
	today := now.Format("2006-01-02")

	params := url.Values{}
	params.Set("fields", "ad_id,ad_name,campaign_name,clicks,actions")
	params.Set("level", "ad")
	params.Set("time_range", fmt.Sprintf(`{"since":"%s","until":"%s"}`, today, today))

	result, err := metaGraphGet(adAccountPath(creds.AdAccountID)+"/insights", creds.Token, params)
	if err != nil {
		slog.Error("funnel_sync_ads_error", "error", err, "user_id", userID.Hex())
		return
	}

	data, ok := result["data"].([]interface{})
	if !ok {
		return
	}

	for _, item := range data {
		row, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		adID, _ := row["ad_id"].(string)
		adName, _ := row["ad_name"].(string)
		campaignName, _ := row["campaign_name"].(string)
		clicksStr, _ := row["clicks"].(string)
		clicks, _ := strconv.ParseInt(clicksStr, 10, 64)

		if clicks == 0 || adID == "" {
			continue
		}

		// Use ad_id as a pseudo contact_id for ad clicks (grouped by ad)
		contactID := "ad_click_" + adID + "_" + today

		// Check if already synced today
		count, _ := database.FunnelEvents().CountDocuments(ctx, bson.M{
			"user_id":    userID,
			"contact_id": contactID,
			"stage":      "consideration",
		})
		if count > 0 {
			continue
		}

		event := models.FunnelEvent{
			ID:        primitive.NewObjectID(),
			UserID:    userID,
			ContactID: contactID,
			Stage:     "consideration",
			Source:    "ad_click",
			Metadata: map[string]interface{}{
				"ad_id":         adID,
				"ad_name":       adName,
				"campaign_name": campaignName,
				"clicks":        clicks,
				"date":          today,
				"synced":        true,
			},
			CreatedAt: now,
		}

		database.FunnelEvents().InsertOne(ctx, event)
	}
}
```

**Nota sobre `strings`:** O import `strings` sera utilizado se houver extensoes futuras. Se o linter reclamar, remover do import list.

---

## 6. Rotas API

Adicionar em `internal/router/router.go`, no bloco de rotas protegidas (admin):

```go
// Funnel routes
mux.Handle("GET /api/v1/admin/funnel/summary", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetFunnelSummary))))
mux.Handle("GET /api/v1/admin/funnel/events", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListFunnelEvents))))
mux.Handle("POST /api/v1/admin/funnel/events", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.CreateFunnelEvent))))
mux.Handle("GET /api/v1/admin/funnel/contacts/{id}/journey", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetContactJourney))))

// UTM template routes
mux.Handle("GET /api/v1/admin/funnel/utm-templates", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListUTMTemplates))))
mux.Handle("POST /api/v1/admin/funnel/utm-templates", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.CreateUTMTemplate))))
mux.Handle("DELETE /api/v1/admin/funnel/utm-templates/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.DeleteUTMTemplate))))
mux.Handle("POST /api/v1/admin/funnel/utm-templates/generate", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GenerateUTMUrl))))
```

### Resumo dos endpoints

| Metodo | Rota | Descricao |
|--------|------|-----------|
| `GET` | `/api/v1/admin/funnel/summary?days=30` | Retorna resumo do funil com contagens por estagio e taxas de conversao |
| `GET` | `/api/v1/admin/funnel/events?stage=&source=&page=1&limit=50` | Lista eventos do funil com filtros e paginacao |
| `POST` | `/api/v1/admin/funnel/events` | Cria evento manualmente (ex: conversao) |
| `GET` | `/api/v1/admin/funnel/contacts/{id}/journey` | Retorna timeline completa de um contato |
| `GET` | `/api/v1/admin/funnel/utm-templates` | Lista templates UTM salvos |
| `POST` | `/api/v1/admin/funnel/utm-templates` | Cria novo template UTM |
| `DELETE` | `/api/v1/admin/funnel/utm-templates/{id}` | Remove template UTM |
| `POST` | `/api/v1/admin/funnel/utm-templates/generate` | Gera URL completa com parametros UTM |

---

## 7. Background Jobs

Adicionar em `cmd/api/main.go`:

```go
// Start Funnel data sync
go funnelSyncer()
```

E a funcao:

```go
// funnelSyncer runs every 1 hour and syncs data from instagram_leads,
// auto_reply_logs, and Meta Ads into funnel_events.
func funnelSyncer() {
	// Wait for server to start
	time.Sleep(45 * time.Second)
	log.Println("Funnel syncer started (1h interval)")

	// Run once immediately
	handlers.SyncFunnelData()

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		handlers.SyncFunnelData()
	}
}
```

### Logica de sincronizacao

O job `SyncFunnelData()` executa a cada 1 hora e realiza tres operacoes:

1. **Leads -> Awareness:** Busca documentos em `instagram_leads` criados nas ultimas 2h. Para cada lead sem evento "awareness" correspondente, cria um `FunnelEvent` com stage "awareness" e source baseado no campo `sources` do lead ("comment" -> "instagram_comment", default -> "instagram_dm"). O `created_at` do evento usa `first_interaction` do lead.

2. **Auto-reply logs -> Interest:** Busca documentos em `auto_reply_logs` com `status=sent` nas ultimas 2h. Para cada log sem evento "interest" correspondente (mesmo contact_id + timestamp), cria um `FunnelEvent` com stage "interest". Metadata inclui `rule_name` e `trigger_text`.

3. **Meta Ads clicks -> Consideration:** Chama `metaGraphGet()` no endpoint `/insights` da conta de anuncios com `level=ad` e `fields=ad_id,ad_name,campaign_name,clicks,actions`. Para cada anuncio com cliques > 0, cria um evento "consideration" agrupado por dia + ad_id. Metadata inclui numero de cliques, nome do anuncio e campanha.

**Deduplicacao:** Cada etapa verifica se o evento ja existe antes de inserir, usando combinacao de `user_id`, `contact_id`, `stage` e `source`/`created_at`.

**Janela de overlap:** As queries usam uma janela de 2h para garantir que nenhum dado seja perdido entre execucoes de 1h.

---

## 8. Frontend

### `FunnelPage.jsx`

Localizado em `tron-legacy-frontend/src/pages/admin/FunnelPage.jsx`:

```jsx
import React, { useState, useEffect } from 'react';
import { useAuth } from '../../context/AuthContext';

const STAGES = ['awareness', 'interest', 'consideration', 'conversion'];
const STAGE_LABELS = {
  awareness: 'Descoberta',
  interest: 'Interesse',
  consideration: 'Consideracao',
  conversion: 'Conversao',
};
const STAGE_COLORS = {
  awareness: '#6366f1',
  interest: '#8b5cf6',
  consideration: '#a855f7',
  conversion: '#22c55e',
};

export default function FunnelPage() {
  const { token } = useAuth();
  const [summary, setSummary] = useState(null);
  const [events, setEvents] = useState([]);
  const [utmTemplates, setUtmTemplates] = useState([]);
  const [selectedContact, setSelectedContact] = useState(null);
  const [contactJourney, setContactJourney] = useState(null);
  const [days, setDays] = useState(30);
  const [activeTab, setActiveTab] = useState('funnel'); // 'funnel' | 'events' | 'utm'
  const [loading, setLoading] = useState(true);

  // UTM form state
  const [utmForm, setUtmForm] = useState({
    name: '', source: '', medium: '', campaign: '', content: '', term: '',
  });
  const [generateForm, setGenerateForm] = useState({
    base_url: '', template_id: '',
  });
  const [generatedUrl, setGeneratedUrl] = useState('');

  const API = import.meta.env.VITE_API_URL;
  const headers = {
    'Content-Type': 'application/json',
    Authorization: `Bearer ${token}`,
  };

  useEffect(() => {
    fetchSummary();
    fetchEvents();
    fetchUTMTemplates();
  }, [days]);

  async function fetchSummary() {
    setLoading(true);
    try {
      const res = await fetch(`${API}/admin/funnel/summary?days=${days}`, { headers });
      if (res.ok) setSummary(await res.json());
    } catch (err) {
      console.error('Error fetching funnel summary:', err);
    } finally {
      setLoading(false);
    }
  }

  async function fetchEvents() {
    try {
      const res = await fetch(`${API}/admin/funnel/events?limit=50`, { headers });
      if (res.ok) {
        const data = await res.json();
        setEvents(data.events || []);
      }
    } catch (err) {
      console.error('Error fetching events:', err);
    }
  }

  async function fetchUTMTemplates() {
    try {
      const res = await fetch(`${API}/admin/funnel/utm-templates`, { headers });
      if (res.ok) setUtmTemplates(await res.json());
    } catch (err) {
      console.error('Error fetching UTM templates:', err);
    }
  }

  async function fetchContactJourney(contactId) {
    try {
      const res = await fetch(`${API}/admin/funnel/contacts/${contactId}/journey`, { headers });
      if (res.ok) {
        setContactJourney(await res.json());
        setSelectedContact(contactId);
      }
    } catch (err) {
      console.error('Error fetching journey:', err);
    }
  }

  async function createUTMTemplate(e) {
    e.preventDefault();
    try {
      const res = await fetch(`${API}/admin/funnel/utm-templates`, {
        method: 'POST', headers, body: JSON.stringify(utmForm),
      });
      if (res.ok) {
        setUtmForm({ name: '', source: '', medium: '', campaign: '', content: '', term: '' });
        fetchUTMTemplates();
      }
    } catch (err) {
      console.error('Error creating UTM template:', err);
    }
  }

  async function deleteUTMTemplate(id) {
    try {
      await fetch(`${API}/admin/funnel/utm-templates/${id}`, { method: 'DELETE', headers });
      fetchUTMTemplates();
    } catch (err) {
      console.error('Error deleting UTM template:', err);
    }
  }

  async function generateUTM(e) {
    e.preventDefault();
    try {
      const res = await fetch(`${API}/admin/funnel/utm-templates/generate`, {
        method: 'POST', headers, body: JSON.stringify(generateForm),
      });
      if (res.ok) {
        const data = await res.json();
        setGeneratedUrl(data.url);
      }
    } catch (err) {
      console.error('Error generating UTM URL:', err);
    }
  }

  // ── Funnel Visualization ──────────────────────────────────────────
  function renderFunnel() {
    if (!summary) return null;

    const maxCount = Math.max(...summary.stages.map((s) => s.count), 1);

    return (
      <div className="funnel-container">
        <div className="funnel-header">
          <h2>Funil de Vendas</h2>
          <select value={days} onChange={(e) => setDays(Number(e.target.value))}>
            <option value={7}>7 dias</option>
            <option value={30}>30 dias</option>
            <option value={90}>90 dias</option>
            <option value={180}>180 dias</option>
          </select>
        </div>

        <div className="funnel-visual">
          {summary.stages.map((stage, i) => {
            const widthPercent = Math.max((stage.count / maxCount) * 100, 20);
            return (
              <div key={stage.name} className="funnel-stage">
                <div
                  className="funnel-trapezoid"
                  style={{
                    width: `${widthPercent}%`,
                    backgroundColor: STAGE_COLORS[stage.name],
                  }}
                >
                  <span className="funnel-stage-label">
                    {STAGE_LABELS[stage.name]}
                  </span>
                  <span className="funnel-stage-count">{stage.count}</span>
                </div>
                {i < summary.stages.length - 1 && (
                  <div className="funnel-conversion-arrow">
                    {stage.conversion_rate.toFixed(1)}%
                  </div>
                )}
              </div>
            );
          })}
        </div>

        <div className="funnel-stats-grid">
          {summary.stages.map((stage) => (
            <div key={stage.name} className="funnel-stat-card">
              <div
                className="funnel-stat-indicator"
                style={{ backgroundColor: STAGE_COLORS[stage.name] }}
              />
              <h4>{STAGE_LABELS[stage.name]}</h4>
              <p className="funnel-stat-count">{stage.count}</p>
              {stage.conversion_rate > 0 && (
                <p className="funnel-stat-rate">
                  Taxa: {stage.conversion_rate.toFixed(1)}%
                </p>
              )}
              {stage.avg_time_in_stage_hours > 0 && (
                <p className="funnel-stat-time">
                  Tempo medio: {stage.avg_time_in_stage_hours.toFixed(1)}h
                </p>
              )}
            </div>
          ))}
        </div>

        <div className="funnel-overall">
          <p>
            <strong>Contatos unicos:</strong> {summary.total_contacts} |{' '}
            <strong>Conversao geral:</strong> {summary.overall_conversion_rate.toFixed(1)}% |{' '}
            <strong>Periodo:</strong> {summary.period}
          </p>
        </div>
      </div>
    );
  }

  // ── Contact Timeline ──────────────────────────────────────────────
  function renderTimeline() {
    if (!contactJourney) return null;

    return (
      <div className="journey-modal">
        <div className="journey-modal-content">
          <h3>Jornada: {contactJourney.contact_id}</h3>
          <p>Estagio atual: <strong>{STAGE_LABELS[contactJourney.current_stage]}</strong></p>
          <div className="journey-timeline">
            {contactJourney.events.map((event) => (
              <div key={event.id} className="timeline-item">
                <div
                  className="timeline-dot"
                  style={{ backgroundColor: STAGE_COLORS[event.stage] }}
                />
                <div className="timeline-content">
                  <p className="timeline-stage">{STAGE_LABELS[event.stage]}</p>
                  <p className="timeline-source">{event.source}</p>
                  <p className="timeline-date">
                    {new Date(event.created_at).toLocaleString('pt-BR')}
                  </p>
                </div>
              </div>
            ))}
          </div>
          <button onClick={() => { setSelectedContact(null); setContactJourney(null); }}>
            Fechar
          </button>
        </div>
      </div>
    );
  }

  // ── Main Render ───────────────────────────────────────────────────
  return (
    <div className="funnel-page">
      <div className="funnel-tabs">
        <button
          className={activeTab === 'funnel' ? 'active' : ''}
          onClick={() => setActiveTab('funnel')}
        >
          Funil
        </button>
        <button
          className={activeTab === 'events' ? 'active' : ''}
          onClick={() => setActiveTab('events')}
        >
          Eventos
        </button>
        <button
          className={activeTab === 'utm' ? 'active' : ''}
          onClick={() => setActiveTab('utm')}
        >
          UTM Builder
        </button>
      </div>

      {loading && <p>Carregando...</p>}

      {activeTab === 'funnel' && renderFunnel()}

      {activeTab === 'events' && (
        <div className="events-list">
          <h2>Eventos do Funil</h2>
          <table>
            <thead>
              <tr>
                <th>Contato</th>
                <th>Estagio</th>
                <th>Origem</th>
                <th>Data</th>
                <th>Acoes</th>
              </tr>
            </thead>
            <tbody>
              {events.map((ev) => (
                <tr key={ev.id}>
                  <td>{ev.contact_id}</td>
                  <td>
                    <span
                      className="stage-badge"
                      style={{ backgroundColor: STAGE_COLORS[ev.stage] }}
                    >
                      {STAGE_LABELS[ev.stage]}
                    </span>
                  </td>
                  <td>{ev.source}</td>
                  <td>{new Date(ev.created_at).toLocaleString('pt-BR')}</td>
                  <td>
                    <button onClick={() => fetchContactJourney(ev.contact_id)}>
                      Ver jornada
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {activeTab === 'utm' && (
        <div className="utm-section">
          <h2>Templates UTM</h2>

          <form onSubmit={createUTMTemplate} className="utm-form">
            <input placeholder="Nome" value={utmForm.name}
              onChange={(e) => setUtmForm({ ...utmForm, name: e.target.value })} required />
            <input placeholder="Source (ex: instagram)" value={utmForm.source}
              onChange={(e) => setUtmForm({ ...utmForm, source: e.target.value })} required />
            <input placeholder="Medium (ex: social)" value={utmForm.medium}
              onChange={(e) => setUtmForm({ ...utmForm, medium: e.target.value })} required />
            <input placeholder="Campaign" value={utmForm.campaign}
              onChange={(e) => setUtmForm({ ...utmForm, campaign: e.target.value })} required />
            <input placeholder="Content (opcional)" value={utmForm.content}
              onChange={(e) => setUtmForm({ ...utmForm, content: e.target.value })} />
            <input placeholder="Term (opcional)" value={utmForm.term}
              onChange={(e) => setUtmForm({ ...utmForm, term: e.target.value })} />
            <button type="submit">Salvar Template</button>
          </form>

          <div className="utm-templates-list">
            {utmTemplates.map((tpl) => (
              <div key={tpl.id} className="utm-template-card">
                <h4>{tpl.name}</h4>
                <p>{tpl.source} / {tpl.medium} / {tpl.campaign}</p>
                <button onClick={() => deleteUTMTemplate(tpl.id)}>Excluir</button>
                <button onClick={() => setGenerateForm({ ...generateForm, template_id: tpl.id })}>
                  Usar para gerar URL
                </button>
              </div>
            ))}
          </div>

          <h3>Gerar URL com UTM</h3>
          <form onSubmit={generateUTM} className="utm-generate-form">
            <input placeholder="URL base (ex: https://seusite.com/pagina)" value={generateForm.base_url}
              onChange={(e) => setGenerateForm({ ...generateForm, base_url: e.target.value })} required />
            {generateForm.template_id && (
              <p>Template selecionado: {utmTemplates.find((t) => t.id === generateForm.template_id)?.name}</p>
            )}
            <button type="submit">Gerar URL</button>
          </form>

          {generatedUrl && (
            <div className="generated-url">
              <p>URL gerada:</p>
              <code>{generatedUrl}</code>
              <button onClick={() => navigator.clipboard.writeText(generatedUrl)}>
                Copiar
              </button>
            </div>
          )}
        </div>
      )}

      {selectedContact && renderTimeline()}
    </div>
  );
}
```

### CSS principal (estilos do funil)

Adicionar em `tron-legacy-frontend/src/pages/admin/FunnelPage.css`:

```css
.funnel-page {
  padding: 2rem;
  max-width: 1200px;
  margin: 0 auto;
}

.funnel-tabs {
  display: flex;
  gap: 0.5rem;
  margin-bottom: 2rem;
  border-bottom: 2px solid #e5e7eb;
  padding-bottom: 0.5rem;
}
.funnel-tabs button {
  padding: 0.5rem 1.5rem;
  border: none;
  background: transparent;
  cursor: pointer;
  font-size: 1rem;
  color: #6b7280;
  border-bottom: 2px solid transparent;
  transition: all 0.2s;
}
.funnel-tabs button.active {
  color: #6366f1;
  border-bottom-color: #6366f1;
}

/* Funil visual — estagios em trapezio */
.funnel-visual {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.25rem;
  margin: 2rem 0;
}
.funnel-stage {
  display: flex;
  flex-direction: column;
  align-items: center;
  width: 100%;
}
.funnel-trapezoid {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 1rem 1.5rem;
  color: #fff;
  font-weight: 600;
  border-radius: 4px;
  min-height: 50px;
  transition: width 0.5s ease;
  clip-path: polygon(2% 0%, 98% 0%, 100% 100%, 0% 100%);
}
.funnel-conversion-arrow {
  padding: 0.25rem 0;
  font-size: 0.85rem;
  color: #6b7280;
}
.funnel-stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 1rem;
  margin: 2rem 0;
}
.funnel-stat-card {
  border: 1px solid #e5e7eb;
  border-radius: 8px;
  padding: 1rem;
  position: relative;
}
.funnel-stat-indicator {
  width: 4px;
  height: 100%;
  position: absolute;
  left: 0;
  top: 0;
  border-radius: 8px 0 0 8px;
}
.funnel-stat-count {
  font-size: 1.5rem;
  font-weight: 700;
}
.funnel-stat-rate,
.funnel-stat-time {
  font-size: 0.85rem;
  color: #6b7280;
}

/* Timeline do contato */
.journey-modal {
  position: fixed;
  top: 0; left: 0; right: 0; bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}
.journey-modal-content {
  background: #fff;
  border-radius: 12px;
  padding: 2rem;
  max-width: 500px;
  width: 90%;
  max-height: 80vh;
  overflow-y: auto;
}
.journey-timeline {
  position: relative;
  padding-left: 2rem;
  margin: 1rem 0;
}
.timeline-item {
  position: relative;
  padding-bottom: 1.5rem;
  border-left: 2px solid #e5e7eb;
  padding-left: 1.5rem;
}
.timeline-dot {
  width: 12px;
  height: 12px;
  border-radius: 50%;
  position: absolute;
  left: -7px;
  top: 4px;
}
.timeline-stage {
  font-weight: 600;
}
.timeline-source {
  font-size: 0.85rem;
  color: #6b7280;
}
.timeline-date {
  font-size: 0.8rem;
  color: #9ca3af;
}

/* Stage badge */
.stage-badge {
  display: inline-block;
  padding: 0.2rem 0.6rem;
  border-radius: 12px;
  color: #fff;
  font-size: 0.8rem;
  font-weight: 500;
}
```

### Rota no frontend

Em `tron-legacy-frontend/src/App.jsx`, adicionar rota admin:

```jsx
<Route path="/admin/funil" element={<FunnelPage />} />
```

---

## 9. APIs Externas

### Meta Ads Insights API (Graph API v21.0)

Utilizada na funcao `syncAdClicksToFunnel()` para buscar dados de cliques em anuncios.

**Endpoint:**
```
GET https://graph.facebook.com/v21.0/act_{ad_account_id}/insights
  ?fields=ad_id,ad_name,campaign_name,clicks,actions
  &level=ad
  &time_range={"since":"2026-03-06","until":"2026-03-06"}
  &access_token={token}
```

**Resposta esperada:**
```json
{
  "data": [
    {
      "ad_id": "12345678",
      "ad_name": "Promo Verao - Stories",
      "campaign_name": "Campanha Verao 2026",
      "clicks": "42",
      "actions": [
        { "action_type": "link_click", "value": "38" },
        { "action_type": "landing_page_view", "value": "30" }
      ]
    }
  ]
}
```

**Permissoes necessarias:** `ads_read` (ja necessaria para o modulo Meta Ads existente).

### Dados do Instagram (colecoes locais)

- **`instagram_leads`**: Campos usados: `sender_ig_id`, `sender_username`, `sources`, `first_interaction`, `interaction_count`, `created_at`. Acessado via `database.InstagramLeads()`.

- **`auto_reply_logs`**: Campos usados: `sender_ig_id`, `sender_username`, `rule_name`, `trigger_text`, `trigger_type`, `status`, `created_at`. Acessado via `database.AutoReplyLogs()`.

---

## 10. Codigo Reutilizado

| Funcao/Recurso | Arquivo original | Uso no funil |
|---|---|---|
| `database.InstagramLeads()` | `internal/database/mongo.go` | Busca leads para gerar eventos "awareness" |
| `database.AutoReplyLogs()` | `internal/database/mongo.go` | Busca logs de auto-reply para gerar eventos "interest" |
| `metaGraphGet(endpoint, token, params)` | `internal/handlers/meta_ads.go` | Busca insights de cliques em anuncios |
| `adAccountPath(adAccountID)` | `internal/handlers/meta_ads.go` | Formata path da conta de anuncios com prefixo `act_` |
| `getMetaAdsCredentials(ctx, userID)` | `internal/handlers/meta_ads.go` | Obtem credenciais Meta Ads do usuario |
| `requireMetaAdsCreds(w, r)` | `internal/handlers/meta_ads.go` | Padrao reutilizado (nao chamado diretamente, pois o sync roda em background) |
| `middleware.Auth(...)` | `internal/middleware/auth.go` | Protege rotas do funil |
| `middleware.RequireRole(...)` | `internal/middleware/role.go` | Restringe acesso a superuser/admin |
| `middleware.GetUserID(r)` | `internal/middleware/auth.go` | Extrai user ID do request autenticado |
| `models.InstagramLead` | `internal/models/lead.go` | Struct do lead para decode no sync |
| `models.AutoReplyLog` | `internal/models/autoreply.go` | Struct do log para decode no sync |

---

## 11. Fluxo Completo

### Cenario: Jornada de um lead do Instagram ate conversao

```
1. AWARENESS
   - Um usuario comenta "quero saber mais" em um post do Instagram
   - O webhook do Instagram (`POST /api/v1/webhooks/instagram`) recebe o evento
   - O sistema de auto-reply processa e cria um registro em `auto_reply_logs`
   - O sistema de leads cria/atualiza um registro em `instagram_leads`
   - Na proxima execucao do SyncFunnelData() (a cada 1h):
     - syncLeadsToFunnel() encontra o novo lead
     - Cria um FunnelEvent: stage="awareness", source="instagram_comment"

2. INTEREST
   - O auto-reply envia uma DM automatica com informacoes do produto
   - O log com status="sent" e registrado em `auto_reply_logs`
   - Na proxima execucao do SyncFunnelData():
     - syncAutoReplyToFunnel() encontra o novo log
     - Cria um FunnelEvent: stage="interest", source="instagram_dm"

3. CONSIDERATION
   - O usuario ve um anuncio da campanha no Instagram e clica
   - O clique e registrado pelo Meta Ads
   - Na proxima execucao do SyncFunnelData():
     - syncAdClicksToFunnel() busca insights da API
     - Cria um FunnelEvent: stage="consideration", source="ad_click"

4. CONVERSION
   - O usuario completa uma compra na landing page
   - O admin registra manualmente via POST /api/v1/admin/funnel/events:
     {
       "contact_id": "17841400123456",
       "stage": "conversion",
       "source": "manual",
       "metadata": { "order_id": "ORD-2026-001", "value": 299.90 }
     }

5. VISUALIZACAO
   - O admin acessa /admin/funil no frontend
   - Ve o funil com 4 estagios e taxas de conversao entre eles
   - Clica em "Ver jornada" para ver a timeline completa do contato
   - Os 4 eventos aparecem em ordem cronologica com cores distintas
```

### Cenario: Criando uma URL rastreada com UTM

```
1. O admin acessa a aba "UTM Builder" na pagina do funil
2. Cria um template UTM:
   - Nome: "Stories Instagram Marco"
   - Source: "instagram"
   - Medium: "social"
   - Campaign: "stories_marco_2026"
   - Content: "swipe_up"
3. Seleciona o template e insere a URL base: https://meusite.com/produto
4. Clica em "Gerar URL"
5. Recebe: https://meusite.com/produto?utm_source=instagram&utm_medium=social&utm_campaign=stories_marco_2026&utm_content=swipe_up
6. Copia a URL e usa no link do anuncio ou stories
```

---

## 12. Verificacao

### Testes manuais

**Pre-requisitos:**
- Servidor rodando com MongoDB conectado
- Pelo menos um usuario admin autenticado
- Credenciais Instagram e/ou Meta Ads configuradas

**1. Verificar colecoes e indexes:**
```bash
# Conectar ao MongoDB e verificar colecoes criadas
mongosh --eval "db.funnel_events.getIndexes()"
mongosh --eval "db.utm_templates.getIndexes()"
```

**2. Testar criacao manual de evento:**
```bash
curl -X POST http://localhost:8088/api/v1/admin/funnel/events \
  -H "Authorization: Bearer {token}" \
  -H "Content-Type: application/json" \
  -d '{
    "contact_id": "test_user_123",
    "stage": "awareness",
    "source": "instagram_dm",
    "metadata": {"test": true}
  }'
# Esperado: 201 Created com o evento criado
```

**3. Testar summary do funil:**
```bash
curl http://localhost:8088/api/v1/admin/funnel/summary?days=30 \
  -H "Authorization: Bearer {token}"
# Esperado: JSON com stages[], total_contacts, overall_conversion_rate
```

**4. Testar listagem de eventos:**
```bash
curl "http://localhost:8088/api/v1/admin/funnel/events?stage=awareness&page=1&limit=10" \
  -H "Authorization: Bearer {token}"
# Esperado: JSON com events[], total, page, limit
```

**5. Testar jornada do contato:**
```bash
curl http://localhost:8088/api/v1/admin/funnel/contacts/test_user_123/journey \
  -H "Authorization: Bearer {token}"
# Esperado: JSON com contact_id, events[], current_stage
```

**6. Testar CRUD de UTM templates:**
```bash
# Criar
curl -X POST http://localhost:8088/api/v1/admin/funnel/utm-templates \
  -H "Authorization: Bearer {token}" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Test Template",
    "source": "instagram",
    "medium": "social",
    "campaign": "test_campaign"
  }'
# Esperado: 201 Created

# Listar
curl http://localhost:8088/api/v1/admin/funnel/utm-templates \
  -H "Authorization: Bearer {token}"
# Esperado: array de templates

# Deletar
curl -X DELETE http://localhost:8088/api/v1/admin/funnel/utm-templates/{id} \
  -H "Authorization: Bearer {token}"
# Esperado: {"message": "UTM template deleted"}
```

**7. Testar geracao de URL com UTM:**
```bash
curl -X POST http://localhost:8088/api/v1/admin/funnel/utm-templates/generate \
  -H "Authorization: Bearer {token}" \
  -H "Content-Type: application/json" \
  -d '{
    "base_url": "https://meusite.com/produto",
    "source": "instagram",
    "medium": "social",
    "campaign": "teste"
  }'
# Esperado: {"url": "https://meusite.com/produto?utm_campaign=teste&utm_medium=social&utm_source=instagram"}
```

**8. Testar sync do background job:**
```bash
# Inserir um lead de teste
mongosh --eval 'db.instagram_leads.insertOne({
  sender_ig_id: "sync_test_001",
  sender_username: "usuario_teste",
  sources: ["comment"],
  first_interaction: new Date(),
  last_interaction: new Date(),
  interaction_count: 1,
  tags: [],
  created_at: new Date(),
  updated_at: new Date()
})'

# Aguardar 1h ou chamar SyncFunnelData() manualmente via test
# Verificar se evento foi criado
mongosh --eval 'db.funnel_events.find({contact_id: "sync_test_001"})'
# Esperado: documento com stage="awareness", source="instagram_comment"
```

**9. Verificar protecao de rotas:**
```bash
# Sem token
curl http://localhost:8088/api/v1/admin/funnel/summary
# Esperado: 401 Unauthorized

# Com token de usuario sem role admin
curl http://localhost:8088/api/v1/admin/funnel/summary \
  -H "Authorization: Bearer {user_token}"
# Esperado: 403 Forbidden
```

**10. Verificar validacao de entrada:**
```bash
# Stage invalido
curl -X POST http://localhost:8088/api/v1/admin/funnel/events \
  -H "Authorization: Bearer {token}" \
  -H "Content-Type: application/json" \
  -d '{"contact_id": "x", "stage": "invalid", "source": "manual"}'
# Esperado: 400 Bad Request

# Source invalido
curl -X POST http://localhost:8088/api/v1/admin/funnel/events \
  -H "Authorization: Bearer {token}" \
  -H "Content-Type: application/json" \
  -d '{"contact_id": "x", "stage": "awareness", "source": "invalid"}'
# Esperado: 400 Bad Request

# UTM sem campos obrigatorios
curl -X POST http://localhost:8088/api/v1/admin/funnel/utm-templates \
  -H "Authorization: Bearer {token}" \
  -H "Content-Type: application/json" \
  -d '{"name": "test"}'
# Esperado: 400 Bad Request
```

### Checklist de implementacao

- [ ] Criar `internal/models/funnel.go` com structs
- [ ] Adicionar `FunnelEvents()` e `UTMTemplates()` em `internal/database/mongo.go`
- [ ] Adicionar indexes em `EnsureIndexes()`
- [ ] Criar `internal/handlers/funnel.go` com todos os handlers
- [ ] Adicionar rotas em `internal/router/router.go`
- [ ] Adicionar `go funnelSyncer()` em `cmd/api/main.go`
- [ ] Criar `FunnelPage.jsx` e `FunnelPage.css` no frontend
- [ ] Adicionar rota `/admin/funil` no React Router
- [ ] Compilar: `go build ./cmd/api`
- [ ] Testar todos os endpoints via curl
- [ ] Verificar sync com dados reais de instagram_leads
- [ ] Verificar funil visual no frontend
