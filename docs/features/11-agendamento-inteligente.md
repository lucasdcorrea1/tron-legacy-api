# Feature 11 - Agendamento Inteligente

## 1. Visao Geral

O Agendamento Inteligente analisa dados historicos de engajamento do Instagram para identificar os melhores horarios e dias para publicacao de conteudo. A feature oferece:

- **Heatmap de Engajamento**: Matriz visual 7x24 (dias da semana x horas do dia) mostrando a taxa media de engajamento por slot de tempo, baseada no historico real de posts da conta.
- **Sugestoes Inteligentes**: Algoritmo que cruza os dados do heatmap com as preferencias do usuario (dias preferenciais, horarios bloqueados) para sugerir os N melhores slots de publicacao.
- **Fila de Prioridade**: Sistema de fila onde posts sao organizados por prioridade (high/medium/low) e publicados automaticamente no horario sugerido, delegando a publicacao ao scheduler existente (`ProcessScheduledInstagramPosts`).

O objetivo e maximizar o alcance e engajamento organico sem que o usuario precise analisar metricas manualmente.

---

## 2. Arquitetura

```
Fluxo de Dados:

[Instagram Graph API v21.0]
        |
        | GET /{ig_account}/media?fields=id,like_count,comments_count,timestamp
        v
[fetchMediaWithInsights()]  <-- reutilizado de instagram_analytics.go
        |
        v
[buildEngagementHeatmap()]  --> Matriz 7x24 com media de engagement por (dia, hora)
        |
        v
[SuggestTimes()]  --> Filtra por PreferredDays / BlockedHours --> Top N slots
        |
        v
[ScheduleQueue]   --> Item na fila com SuggestedAt + Priority
        |
        v
[ProcessSmartScheduleQueue()]  (background job, 6h)
        |
        | Quando SuggestedAt <= now, cria InstagramSchedule
        v
[ProcessScheduledInstagramPosts()]  (background job existente, 1min)
        |
        | Publica via Graph API
        v
[Instagram]
```

O fluxo reutiliza ao maximo a infraestrutura existente. O job de 6h apenas converte itens da fila inteligente em `InstagramSchedule` convencionais. A publicacao real continua sendo feita pelo scheduler de 1 minuto ja implementado.

---

## 3. Models (Go)

Arquivo: `internal/models/smart_schedule.go`

```go
package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// SmartScheduleConfig stores per-user intelligent scheduling preferences.
type SmartScheduleConfig struct {
	ID                  primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID              primitive.ObjectID `json:"user_id" bson:"user_id"`
	Enabled             bool               `json:"enabled" bson:"enabled"`
	AnalysisWindowDays  int                `json:"analysis_window_days" bson:"analysis_window_days"`   // how many days of history to analyze (default: 90)
	MinPostsForAnalysis int                `json:"min_posts_for_analysis" bson:"min_posts_for_analysis"` // minimum posts needed (default: 10)
	PreferredDays       []int              `json:"preferred_days" bson:"preferred_days"`                 // 0=Sunday..6=Saturday; empty = all days
	BlockedHours        []int              `json:"blocked_hours" bson:"blocked_hours"`                   // hours to never suggest (0-23)
	CreatedAt           time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at" bson:"updated_at"`
}

// EngagementHeatmapData is a computed 7x24 matrix with average engagement per day/hour.
// Rows = days of week (0=Sunday..6=Saturday), Columns = hours (0-23).
type EngagementHeatmapData struct {
	Matrix    [7][24]float64      `json:"matrix"`     // avg engagement rate per slot
	PostCount [7][24]int          `json:"post_count"` // number of posts per slot
	TotalPosts int                `json:"total_posts"`
	Period     string             `json:"period"`      // e.g. "last 90 days"
	GeneratedAt time.Time         `json:"generated_at"`
}

// ScheduleQueueItem represents a post in the smart scheduling priority queue.
type ScheduleQueueItem struct {
	ID          primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID      primitive.ObjectID `json:"user_id" bson:"user_id"`
	Caption     string             `json:"caption" bson:"caption"`
	MediaType   string             `json:"media_type" bson:"media_type"`     // "image" or "carousel"
	ImageIDs    []string           `json:"image_ids" bson:"image_ids"`
	Priority    string             `json:"priority" bson:"priority"`         // "high", "medium", "low"
	SuggestedAt time.Time          `json:"suggested_at" bson:"suggested_at"` // optimal time chosen
	Status      string             `json:"status" bson:"status"`             // "queued", "scheduled", "published", "failed"
	ScheduleID  primitive.ObjectID `json:"schedule_id,omitempty" bson:"schedule_id,omitempty"` // linked InstagramSchedule after promotion
	CreatedAt   time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at" bson:"updated_at"`
}

// CreateScheduleQueueRequest is the request body for adding an item to the queue.
type CreateScheduleQueueRequest struct {
	Caption     string   `json:"caption"`
	MediaType   string   `json:"media_type"`
	ImageIDs    []string `json:"image_ids"`
	Priority    string   `json:"priority"`
	SuggestedAt string   `json:"suggested_at"` // ISO 8601; optional — if empty, auto-pick next best slot
}

// UpdateScheduleQueueRequest is the request body for updating a queue item.
type UpdateScheduleQueueRequest struct {
	Caption     *string  `json:"caption,omitempty"`
	MediaType   *string  `json:"media_type,omitempty"`
	ImageIDs    []string `json:"image_ids,omitempty"`
	Priority    *string  `json:"priority,omitempty"`
	SuggestedAt *string  `json:"suggested_at,omitempty"`
}

// SuggestTimesRequest is the request body for suggesting optimal times.
type SuggestTimesRequest struct {
	Count int `json:"count"` // how many slots to return (default: 5, max: 20)
}

// SuggestedSlot represents a single suggested posting time.
type SuggestedSlot struct {
	DayOfWeek     int       `json:"day_of_week"`     // 0=Sunday..6=Saturday
	DayName       string    `json:"day_name"`         // "Domingo", "Segunda", etc.
	Hour          int       `json:"hour"`             // 0-23
	AvgEngagement float64   `json:"avg_engagement"`   // historical avg engagement rate
	PostCount     int       `json:"post_count"`       // how many historical posts in this slot
	NextOccurrence time.Time `json:"next_occurrence"` // next calendar datetime for this slot
}

// ScheduleQueueListResponse is the paginated response for listing queue items.
type ScheduleQueueListResponse struct {
	Items []ScheduleQueueItem `json:"items"`
	Total int64               `json:"total"`
	Page  int                 `json:"page"`
	Limit int                 `json:"limit"`
}
```

---

## 4. Database

### Collections

Arquivo: `internal/database/mongo.go` - adicionar:

```go
func SmartScheduleConfigs() *mongo.Collection {
	return DB.Collection("smart_schedule_configs")
}

func ScheduleQueue() *mongo.Collection {
	return DB.Collection("schedule_queue")
}
```

### Indexes

Adicionar em `EnsureIndexes()`:

```go
// smart_schedule_configs: unique index on user_id (one config per user)
_, err = SmartScheduleConfigs().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys:    bson.D{{Key: "user_id", Value: 1}},
	Options: options.Index().SetUnique(true),
})
if err != nil {
	return err
}

// schedule_queue: compound index on {user_id, status, suggested_at} for queue processing
_, err = ScheduleQueue().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{
		{Key: "user_id", Value: 1},
		{Key: "status", Value: 1},
		{Key: "suggested_at", Value: 1},
	},
})
if err != nil {
	return err
}

// schedule_queue: index on {status, suggested_at} for background job query
_, err = ScheduleQueue().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{
		{Key: "status", Value: 1},
		{Key: "suggested_at", Value: 1},
	},
})
if err != nil {
	return err
}

// schedule_queue: index on {user_id, priority} for priority-sorted listing
_, err = ScheduleQueue().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{
		{Key: "user_id", Value: 1},
		{Key: "priority", Value: 1},
		{Key: "created_at", Value: -1},
	},
})
if err != nil {
	return err
}
```

---

## 5. Handlers (Go)

Arquivo: `internal/handlers/smart_schedule.go`

### 5.1 GetSmartScheduleConfig

```go
// GetSmartScheduleConfig returns the user's smart scheduling configuration.
// GET /api/v1/admin/smart-schedule/config
func GetSmartScheduleConfig(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	userID := middleware.GetUserID(r)

	var cfg models.SmartScheduleConfig
	err := database.SmartScheduleConfigs().FindOne(ctx, bson.M{"user_id": userID}).Decode(&cfg)
	if err == mongo.ErrNoDocuments {
		// Return default config
		cfg = models.SmartScheduleConfig{
			UserID:              userID,
			Enabled:             false,
			AnalysisWindowDays:  90,
			MinPostsForAnalysis: 10,
			PreferredDays:       []int{},
			BlockedHours:        []int{},
		}
	} else if err != nil {
		http.Error(w, `{"message":"Erro ao buscar configuracao"}`, http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(cfg)
}
```

### 5.2 UpdateSmartScheduleConfig

```go
// UpdateSmartScheduleConfig creates or updates the user's smart scheduling config.
// PUT /api/v1/admin/smart-schedule/config
func UpdateSmartScheduleConfig(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	userID := middleware.GetUserID(r)

	var req models.SmartScheduleConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"Corpo da requisicao invalido"}`, http.StatusBadRequest)
		return
	}

	// Validate AnalysisWindowDays
	if req.AnalysisWindowDays < 7 || req.AnalysisWindowDays > 365 {
		req.AnalysisWindowDays = 90
	}
	if req.MinPostsForAnalysis < 1 {
		req.MinPostsForAnalysis = 10
	}

	// Validate PreferredDays (0-6)
	for _, d := range req.PreferredDays {
		if d < 0 || d > 6 {
			http.Error(w, `{"message":"preferred_days deve conter valores de 0 (Domingo) a 6 (Sabado)"}`, http.StatusBadRequest)
			return
		}
	}

	// Validate BlockedHours (0-23)
	for _, h := range req.BlockedHours {
		if h < 0 || h > 23 {
			http.Error(w, `{"message":"blocked_hours deve conter valores de 0 a 23"}`, http.StatusBadRequest)
			return
		}
	}

	now := time.Now()
	filter := bson.M{"user_id": userID}
	update := bson.M{
		"$set": bson.M{
			"enabled":                req.Enabled,
			"analysis_window_days":   req.AnalysisWindowDays,
			"min_posts_for_analysis": req.MinPostsForAnalysis,
			"preferred_days":         req.PreferredDays,
			"blocked_hours":          req.BlockedHours,
			"updated_at":            now,
		},
		"$setOnInsert": bson.M{
			"user_id":    userID,
			"created_at": now,
		},
	}
	opts := options.Update().SetUpsert(true)

	_, err := database.SmartScheduleConfigs().UpdateOne(ctx, filter, update, opts)
	if err != nil {
		http.Error(w, `{"message":"Erro ao salvar configuracao"}`, http.StatusInternalServerError)
		return
	}

	slog.Info("smart_schedule_config_updated", "user_id", userID.Hex(), "enabled", req.Enabled)

	var updated models.SmartScheduleConfig
	database.SmartScheduleConfigs().FindOne(ctx, filter).Decode(&updated)
	json.NewEncoder(w).Encode(updated)
}
```

### 5.3 GetEngagementHeatmap

```go
// GetEngagementHeatmap fetches IG media and builds a 7x24 engagement matrix.
// GET /api/v1/admin/smart-schedule/heatmap
func GetEngagementHeatmap(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	userID := middleware.GetUserID(r)
	creds, err := getInstagramCredentials(ctx, userID)
	if err != nil || creds == nil {
		http.Error(w, `{"message":"Credenciais Instagram nao configuradas"}`, http.StatusBadRequest)
		return
	}

	// Load user config for analysis window
	var cfg models.SmartScheduleConfig
	err = database.SmartScheduleConfigs().FindOne(ctx, bson.M{"user_id": userID}).Decode(&cfg)
	if err != nil {
		cfg.AnalysisWindowDays = 90
		cfg.MinPostsForAnalysis = 10
	}

	followersCount := fetchFollowersCount(creds.AccountID, creds.Token)
	posts := fetchMediaWithInsights(creds.AccountID, creds.Token, followersCount)

	if len(posts) < cfg.MinPostsForAnalysis {
		http.Error(w, fmt.Sprintf(
			`{"message":"Posts insuficientes para analise. Necessario: %d, encontrado: %d"}`,
			cfg.MinPostsForAnalysis, len(posts),
		), http.StatusUnprocessableEntity)
		return
	}

	heatmap := buildEngagementHeatmap(posts, cfg.AnalysisWindowDays)
	json.NewEncoder(w).Encode(heatmap)
}

// buildEngagementHeatmap computes a 7x24 engagement matrix from post data.
func buildEngagementHeatmap(posts []models.PostEngagement, windowDays int) models.EngagementHeatmapData {
	type slotAcc struct {
		totalEng float64
		count    int
	}

	var matrix [7][24]*slotAcc
	for d := 0; d < 7; d++ {
		for h := 0; h < 24; h++ {
			matrix[d][h] = &slotAcc{}
		}
	}

	cutoff := time.Now().AddDate(0, 0, -windowDays)
	filteredCount := 0

	for _, p := range posts {
		t, err := time.Parse(time.RFC3339, p.Timestamp)
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			continue
		}
		filteredCount++
		day := int(t.Weekday()) // 0=Sunday..6=Saturday
		hour := t.Hour()
		matrix[day][hour].totalEng += p.EngagementRate
		matrix[day][hour].count++
	}

	var result models.EngagementHeatmapData
	for d := 0; d < 7; d++ {
		for h := 0; h < 24; h++ {
			acc := matrix[d][h]
			if acc.count > 0 {
				result.Matrix[d][h] = acc.totalEng / float64(acc.count)
			}
			result.PostCount[d][h] = acc.count
		}
	}

	result.TotalPosts = filteredCount
	result.Period = fmt.Sprintf("last %d days", windowDays)
	result.GeneratedAt = time.Now()
	return result
}
```

### 5.4 SuggestTimes

```go
// SuggestTimes returns the top N optimal posting slots based on heatmap + user preferences.
// POST /api/v1/admin/smart-schedule/suggest
func SuggestTimes(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	userID := middleware.GetUserID(r)

	var req models.SuggestTimesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Count < 1 {
		req.Count = 5
	}
	if req.Count > 20 {
		req.Count = 20
	}

	creds, err := getInstagramCredentials(ctx, userID)
	if err != nil || creds == nil {
		http.Error(w, `{"message":"Credenciais Instagram nao configuradas"}`, http.StatusBadRequest)
		return
	}

	// Load config
	var cfg models.SmartScheduleConfig
	err = database.SmartScheduleConfigs().FindOne(ctx, bson.M{"user_id": userID}).Decode(&cfg)
	if err != nil {
		cfg.AnalysisWindowDays = 90
		cfg.MinPostsForAnalysis = 10
	}

	followersCount := fetchFollowersCount(creds.AccountID, creds.Token)
	posts := fetchMediaWithInsights(creds.AccountID, creds.Token, followersCount)

	heatmap := buildEngagementHeatmap(posts, cfg.AnalysisWindowDays)

	// Build candidate slots (filtering by preferred days and blocked hours)
	preferredSet := map[int]bool{}
	for _, d := range cfg.PreferredDays {
		preferredSet[d] = true
	}
	blockedSet := map[int]bool{}
	for _, h := range cfg.BlockedHours {
		blockedSet[h] = true
	}

	dayNames := []string{"Domingo", "Segunda", "Terca", "Quarta", "Quinta", "Sexta", "Sabado"}

	type candidate struct {
		day  int
		hour int
		eng  float64
		cnt  int
	}

	var candidates []candidate
	for d := 0; d < 7; d++ {
		// Skip days not in preferred list (if list is non-empty)
		if len(preferredSet) > 0 && !preferredSet[d] {
			continue
		}
		for h := 0; h < 24; h++ {
			if blockedSet[h] {
				continue
			}
			if heatmap.PostCount[d][h] > 0 {
				candidates = append(candidates, candidate{
					day: d, hour: h,
					eng: heatmap.Matrix[d][h],
					cnt: heatmap.PostCount[d][h],
				})
			}
		}
	}

	// Sort by avg engagement descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].eng > candidates[j].eng
	})

	if len(candidates) > req.Count {
		candidates = candidates[:req.Count]
	}

	// Compute next occurrence for each slot
	now := time.Now()
	var suggestions []models.SuggestedSlot
	for _, c := range candidates {
		next := nextOccurrence(now, c.day, c.hour)
		suggestions = append(suggestions, models.SuggestedSlot{
			DayOfWeek:      c.day,
			DayName:        dayNames[c.day],
			Hour:           c.hour,
			AvgEngagement:  c.eng,
			PostCount:      c.cnt,
			NextOccurrence: next,
		})
	}

	if suggestions == nil {
		suggestions = []models.SuggestedSlot{}
	}

	json.NewEncoder(w).Encode(suggestions)
}

// nextOccurrence returns the next calendar datetime for a given day-of-week and hour.
func nextOccurrence(from time.Time, dayOfWeek, hour int) time.Time {
	// Start from next full hour
	candidate := time.Date(from.Year(), from.Month(), from.Day(), hour, 0, 0, 0, from.Location())

	// Find the next matching day of week
	daysAhead := (dayOfWeek - int(candidate.Weekday()) + 7) % 7
	if daysAhead == 0 && candidate.Before(from) {
		daysAhead = 7
	}
	candidate = candidate.AddDate(0, 0, daysAhead)

	return candidate
}
```

### 5.5 Queue CRUD

```go
// AddToScheduleQueue adds a new item to the smart scheduling queue.
// POST /api/v1/admin/smart-schedule/queue
func AddToScheduleQueue(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	userID := middleware.GetUserID(r)

	var req models.CreateScheduleQueueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"Corpo da requisicao invalido"}`, http.StatusBadRequest)
		return
	}

	// Validate
	if len(req.ImageIDs) == 0 {
		http.Error(w, `{"message":"Pelo menos uma imagem e necessaria"}`, http.StatusBadRequest)
		return
	}
	if req.MediaType != "image" && req.MediaType != "carousel" {
		http.Error(w, `{"message":"media_type deve ser 'image' ou 'carousel'"}`, http.StatusBadRequest)
		return
	}
	if req.MediaType == "image" && len(req.ImageIDs) > 1 {
		http.Error(w, `{"message":"Tipo 'image' permite apenas uma imagem; use 'carousel' para multiplas"}`, http.StatusBadRequest)
		return
	}
	if req.MediaType == "carousel" && len(req.ImageIDs) < 2 {
		http.Error(w, `{"message":"Carousel requer pelo menos 2 imagens"}`, http.StatusBadRequest)
		return
	}
	if len(req.Caption) > 2200 {
		http.Error(w, `{"message":"Caption deve ter no maximo 2200 caracteres"}`, http.StatusBadRequest)
		return
	}

	priority := req.Priority
	if priority != "high" && priority != "medium" && priority != "low" {
		priority = "medium"
	}

	// Parse SuggestedAt or auto-pick
	var suggestedAt time.Time
	if req.SuggestedAt != "" {
		var err error
		suggestedAt, err = time.Parse(time.RFC3339, req.SuggestedAt)
		if err != nil {
			http.Error(w, `{"message":"suggested_at deve ser um ISO 8601 valido"}`, http.StatusBadRequest)
			return
		}
	} else {
		// Auto-pick: use next best slot from heatmap
		suggestedAt = autoPickNextSlot(ctx, userID)
		if suggestedAt.IsZero() {
			http.Error(w, `{"message":"Nao foi possivel sugerir horario. Configure e gere o heatmap primeiro."}`, http.StatusUnprocessableEntity)
			return
		}
	}

	// Verify images exist
	for _, imgID := range req.ImageIDs {
		oid, err := primitive.ObjectIDFromHex(imgID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"message":"Image ID invalido: %s"}`, imgID), http.StatusBadRequest)
			return
		}
		count, _ := database.Images().CountDocuments(ctx, bson.M{"_id": oid})
		if count == 0 {
			http.Error(w, fmt.Sprintf(`{"message":"Imagem nao encontrada: %s"}`, imgID), http.StatusBadRequest)
			return
		}
	}

	now := time.Now()
	item := models.ScheduleQueueItem{
		ID:          primitive.NewObjectID(),
		UserID:      userID,
		Caption:     req.Caption,
		MediaType:   req.MediaType,
		ImageIDs:    req.ImageIDs,
		Priority:    priority,
		SuggestedAt: suggestedAt,
		Status:      "queued",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err := database.ScheduleQueue().InsertOne(ctx, item)
	if err != nil {
		http.Error(w, `{"message":"Erro ao adicionar a fila"}`, http.StatusInternalServerError)
		return
	}

	slog.Info("smart_schedule_queue_add",
		"queue_id", item.ID.Hex(),
		"user_id", userID.Hex(),
		"priority", priority,
		"suggested_at", suggestedAt.Format(time.RFC3339),
	)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(item)
}

// autoPickNextSlot picks the next best posting slot based on heatmap data.
func autoPickNextSlot(ctx context.Context, userID primitive.ObjectID) time.Time {
	creds, err := getInstagramCredentials(ctx, userID)
	if err != nil || creds == nil {
		return time.Time{}
	}

	var cfg models.SmartScheduleConfig
	err = database.SmartScheduleConfigs().FindOne(ctx, bson.M{"user_id": userID}).Decode(&cfg)
	if err != nil {
		cfg.AnalysisWindowDays = 90
	}

	followersCount := fetchFollowersCount(creds.AccountID, creds.Token)
	posts := fetchMediaWithInsights(creds.AccountID, creds.Token, followersCount)
	if len(posts) == 0 {
		return time.Time{}
	}

	heatmap := buildEngagementHeatmap(posts, cfg.AnalysisWindowDays)

	// Find best slot respecting preferences
	preferredSet := map[int]bool{}
	for _, d := range cfg.PreferredDays {
		preferredSet[d] = true
	}
	blockedSet := map[int]bool{}
	for _, h := range cfg.BlockedHours {
		blockedSet[h] = true
	}

	type slot struct {
		day, hour int
		eng       float64
	}
	var best *slot
	for d := 0; d < 7; d++ {
		if len(preferredSet) > 0 && !preferredSet[d] {
			continue
		}
		for h := 0; h < 24; h++ {
			if blockedSet[h] {
				continue
			}
			if heatmap.PostCount[d][h] > 0 {
				if best == nil || heatmap.Matrix[d][h] > best.eng {
					best = &slot{day: d, hour: h, eng: heatmap.Matrix[d][h]}
				}
			}
		}
	}

	if best == nil {
		return time.Time{}
	}

	return nextOccurrence(time.Now(), best.day, best.hour)
}

// ListScheduleQueue lists queue items with pagination and filtering.
// GET /api/v1/admin/smart-schedule/queue
func ListScheduleQueue(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	userID := middleware.GetUserID(r)

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
	if priority := r.URL.Query().Get("priority"); priority != "" {
		filter["priority"] = priority
	}

	total, _ := database.ScheduleQueue().CountDocuments(ctx, filter)

	skip := int64((page - 1) * limit)
	opts := options.Find().
		SetSort(bson.D{
			{Key: "priority_order", Value: 1}, // custom sort: high=1, medium=2, low=3
			{Key: "suggested_at", Value: 1},
		}).
		SetSkip(skip).
		SetLimit(int64(limit))

	// Use aggregation for priority sorting
	pipeline := []bson.M{
		{"$match": filter},
		{"$addFields": bson.M{
			"priority_order": bson.M{
				"$switch": bson.M{
					"branches": []bson.M{
						{"case": bson.M{"$eq": []string{"$priority", "high"}}, "then": 1},
						{"case": bson.M{"$eq": []string{"$priority", "medium"}}, "then": 2},
						{"case": bson.M{"$eq": []string{"$priority", "low"}}, "then": 3},
					},
					"default": 4,
				},
			},
		}},
		{"$sort": bson.D{{Key: "priority_order", Value: 1}, {Key: "suggested_at", Value: 1}}},
		{"$skip": skip},
		{"$limit": int64(limit)},
	}

	cursor, err := database.ScheduleQueue().Aggregate(ctx, pipeline)
	if err != nil {
		http.Error(w, `{"message":"Erro ao buscar fila"}`, http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var items []models.ScheduleQueueItem
	cursor.All(ctx, &items)
	if items == nil {
		items = []models.ScheduleQueueItem{}
	}

	json.NewEncoder(w).Encode(models.ScheduleQueueListResponse{
		Items: items,
		Total: total,
		Page:  page,
		Limit: limit,
	})

	_ = opts // opts replaced by aggregation pipeline
}

// UpdateScheduleQueueItem updates a queued item (only if status is "queued").
// PUT /api/v1/admin/smart-schedule/queue/{id}
func UpdateScheduleQueueItem(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	idStr := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, `{"message":"ID invalido"}`, http.StatusBadRequest)
		return
	}

	var item models.ScheduleQueueItem
	err = database.ScheduleQueue().FindOne(ctx, bson.M{"_id": oid}).Decode(&item)
	if err != nil {
		http.Error(w, `{"message":"Item nao encontrado"}`, http.StatusNotFound)
		return
	}

	if item.Status != "queued" {
		http.Error(w, `{"message":"Apenas itens com status 'queued' podem ser editados"}`, http.StatusBadRequest)
		return
	}

	var req models.UpdateScheduleQueueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"Corpo da requisicao invalido"}`, http.StatusBadRequest)
		return
	}

	setFields := bson.M{"updated_at": time.Now()}

	if req.Caption != nil {
		if len(*req.Caption) > 2200 {
			http.Error(w, `{"message":"Caption deve ter no maximo 2200 caracteres"}`, http.StatusBadRequest)
			return
		}
		setFields["caption"] = *req.Caption
	}
	if req.MediaType != nil {
		if *req.MediaType != "image" && *req.MediaType != "carousel" {
			http.Error(w, `{"message":"media_type deve ser 'image' ou 'carousel'"}`, http.StatusBadRequest)
			return
		}
		setFields["media_type"] = *req.MediaType
	}
	if req.ImageIDs != nil {
		if len(req.ImageIDs) == 0 {
			http.Error(w, `{"message":"Pelo menos uma imagem e necessaria"}`, http.StatusBadRequest)
			return
		}
		setFields["image_ids"] = req.ImageIDs
	}
	if req.Priority != nil {
		if *req.Priority != "high" && *req.Priority != "medium" && *req.Priority != "low" {
			http.Error(w, `{"message":"priority deve ser 'high', 'medium' ou 'low'"}`, http.StatusBadRequest)
			return
		}
		setFields["priority"] = *req.Priority
	}
	if req.SuggestedAt != nil {
		t, err := time.Parse(time.RFC3339, *req.SuggestedAt)
		if err != nil {
			http.Error(w, `{"message":"suggested_at deve ser ISO 8601 valido"}`, http.StatusBadRequest)
			return
		}
		setFields["suggested_at"] = t
	}

	_, err = database.ScheduleQueue().UpdateOne(ctx, bson.M{"_id": oid}, bson.M{"$set": setFields})
	if err != nil {
		http.Error(w, `{"message":"Erro ao atualizar item"}`, http.StatusInternalServerError)
		return
	}

	var updated models.ScheduleQueueItem
	database.ScheduleQueue().FindOne(ctx, bson.M{"_id": oid}).Decode(&updated)

	slog.Info("smart_schedule_queue_update", "queue_id", oid.Hex())
	json.NewEncoder(w).Encode(updated)
}

// DeleteScheduleQueueItem removes a queued item.
// DELETE /api/v1/admin/smart-schedule/queue/{id}
func DeleteScheduleQueueItem(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	idStr := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, `{"message":"ID invalido"}`, http.StatusBadRequest)
		return
	}

	var item models.ScheduleQueueItem
	err = database.ScheduleQueue().FindOne(ctx, bson.M{"_id": oid}).Decode(&item)
	if err != nil {
		http.Error(w, `{"message":"Item nao encontrado"}`, http.StatusNotFound)
		return
	}

	if item.Status == "scheduled" || item.Status == "published" {
		http.Error(w, `{"message":"Nao e possivel remover itens ja agendados ou publicados"}`, http.StatusBadRequest)
		return
	}

	_, err = database.ScheduleQueue().DeleteOne(ctx, bson.M{"_id": oid})
	if err != nil {
		http.Error(w, `{"message":"Erro ao remover item"}`, http.StatusInternalServerError)
		return
	}

	slog.Info("smart_schedule_queue_delete", "queue_id", oid.Hex())
	json.NewEncoder(w).Encode(map[string]string{"message": "Item removido da fila"})
}
```

### 5.6 ProcessSmartScheduleQueue (Background Job)

```go
// ProcessSmartScheduleQueue checks for queued items whose suggested time has arrived
// and promotes them to InstagramSchedule for the existing 1-min scheduler to publish.
func ProcessSmartScheduleQueue() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	now := time.Now()

	// Find all "queued" items where suggested_at <= now
	filter := bson.M{
		"status":       "queued",
		"suggested_at": bson.M{"$lte": now},
	}

	// Sort by priority: high first, then medium, then low
	pipeline := []bson.M{
		{"$match": filter},
		{"$addFields": bson.M{
			"priority_order": bson.M{
				"$switch": bson.M{
					"branches": []bson.M{
						{"case": bson.M{"$eq": []string{"$priority", "high"}}, "then": 1},
						{"case": bson.M{"$eq": []string{"$priority", "medium"}}, "then": 2},
						{"case": bson.M{"$eq": []string{"$priority", "low"}}, "then": 3},
					},
					"default": 4,
				},
			},
		}},
		{"$sort": bson.D{{Key: "priority_order", Value: 1}, {Key: "suggested_at", Value: 1}}},
	}

	cursor, err := database.ScheduleQueue().Aggregate(ctx, pipeline)
	if err != nil {
		slog.Error("smart_schedule_queue_query_error", "error", err)
		return
	}
	defer cursor.Close(ctx)

	var items []models.ScheduleQueueItem
	if err := cursor.All(ctx, &items); err != nil {
		slog.Error("smart_schedule_queue_decode_error", "error", err)
		return
	}

	for _, item := range items {
		// Create an InstagramSchedule entry (the existing 1-min scheduler will pick it up)
		schedule := models.InstagramSchedule{
			ID:          primitive.NewObjectID(),
			UserID:      item.UserID,
			Caption:     item.Caption,
			MediaType:   item.MediaType,
			ImageIDs:    item.ImageIDs,
			ScheduledAt: now, // publish immediately (the 1-min ticker will pick it up)
			Status:      "scheduled",
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		_, err := database.InstagramSchedules().InsertOne(ctx, schedule)
		if err != nil {
			slog.Error("smart_schedule_promote_error",
				"queue_id", item.ID.Hex(),
				"error", err,
			)
			database.ScheduleQueue().UpdateOne(ctx, bson.M{"_id": item.ID}, bson.M{
				"$set": bson.M{"status": "failed", "updated_at": time.Now()},
			})
			continue
		}

		// Update queue item status to "scheduled" and link the schedule ID
		database.ScheduleQueue().UpdateOne(ctx, bson.M{"_id": item.ID}, bson.M{
			"$set": bson.M{
				"status":      "scheduled",
				"schedule_id": schedule.ID,
				"updated_at":  time.Now(),
			},
		})

		slog.Info("smart_schedule_promoted",
			"queue_id", item.ID.Hex(),
			"schedule_id", schedule.ID.Hex(),
			"user_id", item.UserID.Hex(),
			"priority", item.Priority,
		)
	}

	if len(items) > 0 {
		slog.Info("smart_schedule_queue_processed", "promoted_count", len(items))
	}
}
```

---

## 6. Rotas API

Arquivo: `internal/router/router.go` - adicionar na secao de rotas protegidas (admin/superuser):

```go
// Smart Schedule routes (superuser + admin)
mux.Handle("GET /api/v1/admin/smart-schedule/config",
	middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetSmartScheduleConfig))))
mux.Handle("PUT /api/v1/admin/smart-schedule/config",
	middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.UpdateSmartScheduleConfig))))
mux.Handle("GET /api/v1/admin/smart-schedule/heatmap",
	middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetEngagementHeatmap))))
mux.Handle("POST /api/v1/admin/smart-schedule/suggest",
	middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.SuggestTimes))))
mux.Handle("GET /api/v1/admin/smart-schedule/queue",
	middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListScheduleQueue))))
mux.Handle("POST /api/v1/admin/smart-schedule/queue",
	middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.AddToScheduleQueue))))
mux.Handle("PUT /api/v1/admin/smart-schedule/queue/{id}",
	middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.UpdateScheduleQueueItem))))
mux.Handle("DELETE /api/v1/admin/smart-schedule/queue/{id}",
	middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.DeleteScheduleQueueItem))))
```

### Resumo de Endpoints

| Metodo   | Rota                                     | Descricao                                      |
|----------|------------------------------------------|-------------------------------------------------|
| `GET`    | `/api/v1/admin/smart-schedule/config`    | Retorna configuracao do agendamento inteligente |
| `PUT`    | `/api/v1/admin/smart-schedule/config`    | Cria/atualiza configuracao                      |
| `GET`    | `/api/v1/admin/smart-schedule/heatmap`   | Gera heatmap de engajamento 7x24                |
| `POST`   | `/api/v1/admin/smart-schedule/suggest`   | Sugere top N horarios otimos                    |
| `GET`    | `/api/v1/admin/smart-schedule/queue`     | Lista fila de agendamento (paginada)            |
| `POST`   | `/api/v1/admin/smart-schedule/queue`     | Adiciona item a fila                            |
| `PUT`    | `/api/v1/admin/smart-schedule/queue/{id}`| Atualiza item na fila                           |
| `DELETE` | `/api/v1/admin/smart-schedule/queue/{id}`| Remove item da fila                             |

---

## 7. Background Jobs

Arquivo: `cmd/api/main.go` - adicionar novo goroutine:

```go
// Start Smart Schedule queue processor
go smartScheduleProcessor()
```

Funcao:

```go
// smartScheduleProcessor runs every 6 hours and promotes ready queue items to InstagramSchedule.
func smartScheduleProcessor() {
	// Wait for server to start
	time.Sleep(45 * time.Second)
	log.Println("Smart Schedule queue processor started (6h interval)")

	// Run once immediately after startup
	handlers.ProcessSmartScheduleQueue()

	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		handlers.ProcessSmartScheduleQueue()
	}
}
```

### Cadeia de Jobs

1. **`smartScheduleProcessor`** (a cada 6h): Busca itens da `schedule_queue` com `status=queued` e `suggested_at <= now`. Para cada item encontrado, cria um `InstagramSchedule` com `status=scheduled` e `scheduled_at=now`. Atualiza o item na fila para `status=scheduled`.

2. **`instagramScheduler`** (a cada 1min, ja existente): Busca `InstagramSchedule` com `status=scheduled` e `scheduled_at <= now`. Publica via Graph API. Atualiza para `published` ou `failed`.

Essa separacao garante que o agendamento inteligente nao duplica logica de publicacao. O job de 6h apenas "promove" itens da fila, e o scheduler de 1 minuto (ja testado e em producao) cuida da publicacao real.

---

## 8. Frontend

Arquivo: `tron-legacy-frontend/src/pages/SmartSchedulingPage.jsx`

### 8.1 Estrutura da Pagina

```jsx
import React, { useState, useEffect, useCallback } from 'react';
import { useAuth } from '../context/AuthContext';

const DAYS = ['Dom', 'Seg', 'Ter', 'Qua', 'Qui', 'Sex', 'Sab'];
const HOURS = Array.from({ length: 24 }, (_, i) => i);

export default function SmartSchedulingPage() {
  const { token } = useAuth();
  const [config, setConfig] = useState(null);
  const [heatmap, setHeatmap] = useState(null);
  const [suggestions, setSuggestions] = useState([]);
  const [queue, setQueue] = useState([]);
  const [loading, setLoading] = useState(true);

  // ... fetch functions, state management

  return (
    <div className="smart-scheduling-page">
      <h1>Agendamento Inteligente</h1>

      {/* Config Section */}
      <ConfigPanel config={config} onSave={saveConfig} />

      {/* Heatmap Section */}
      <EngagementHeatmap data={heatmap} onRefresh={fetchHeatmap} />

      {/* Suggestions Section */}
      <SuggestionCards suggestions={suggestions} onAddToQueue={addToQueue} />

      {/* Queue Section */}
      <ScheduleQueue items={queue} onUpdate={updateItem} onDelete={deleteItem} />
    </div>
  );
}
```

### 8.2 Componente Heatmap (CSS Grid 7x24)

```jsx
function EngagementHeatmap({ data, onRefresh }) {
  if (!data) return <p>Carregando heatmap...</p>;

  const maxEng = Math.max(...data.matrix.flat(), 0.01);

  // Color scale: transparent (0) -> amarelo (low) -> laranja (mid) -> vermelho (high)
  const getColor = (value) => {
    if (value === 0) return 'rgba(255,255,255,0.05)';
    const intensity = value / maxEng;
    const r = Math.round(255);
    const g = Math.round(255 * (1 - intensity * 0.8));
    const b = Math.round(80 * (1 - intensity));
    return `rgba(${r},${g},${b},${0.3 + intensity * 0.7})`;
  };

  return (
    <section className="heatmap-section">
      <h2>Heatmap de Engajamento</h2>
      <p className="heatmap-period">{data.period} - {data.total_posts} posts analisados</p>

      <div className="heatmap-grid">
        {/* Header row: hours */}
        <div className="heatmap-corner" />
        {HOURS.map(h => (
          <div key={h} className="heatmap-hour-label">{String(h).padStart(2, '0')}</div>
        ))}

        {/* Data rows */}
        {DAYS.map((day, d) => (
          <React.Fragment key={d}>
            <div className="heatmap-day-label">{day}</div>
            {HOURS.map(h => (
              <div
                key={`${d}-${h}`}
                className="heatmap-cell"
                style={{ backgroundColor: getColor(data.matrix[d][h]) }}
                title={`${day} ${h}h: ${data.matrix[d][h].toFixed(2)}% eng (${data.post_count[d][h]} posts)`}
              />
            ))}
          </React.Fragment>
        ))}
      </div>

      <button onClick={onRefresh} className="btn-refresh-heatmap">
        Atualizar Heatmap
      </button>
    </section>
  );
}
```

### 8.3 CSS do Heatmap

```css
.heatmap-grid {
  display: grid;
  grid-template-columns: 48px repeat(24, 1fr);
  gap: 2px;
  max-width: 900px;
  margin: 1rem auto;
}

.heatmap-cell {
  aspect-ratio: 1;
  border-radius: 3px;
  cursor: pointer;
  transition: transform 0.15s ease;
  min-width: 20px;
  min-height: 20px;
}

.heatmap-cell:hover {
  transform: scale(1.3);
  z-index: 2;
  outline: 2px solid #00d4ff;
}

.heatmap-day-label,
.heatmap-hour-label {
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 0.7rem;
  color: #aaa;
  font-weight: 600;
}

.heatmap-corner {
  /* empty top-left corner */
}
```

### 8.4 Suggestion Cards

```jsx
function SuggestionCards({ suggestions, onAddToQueue }) {
  if (!suggestions.length) return null;

  return (
    <section className="suggestions-section">
      <h2>Horarios Recomendados</h2>
      <div className="suggestion-cards">
        {suggestions.map((slot, i) => (
          <div key={i} className="suggestion-card">
            <div className="suggestion-rank">#{i + 1}</div>
            <div className="suggestion-info">
              <strong>{slot.day_name} as {String(slot.hour).padStart(2, '0')}:00</strong>
              <span className="suggestion-eng">
                {slot.avg_engagement.toFixed(2)}% engajamento medio
              </span>
              <span className="suggestion-next">
                Proximo: {new Date(slot.next_occurrence).toLocaleDateString('pt-BR')}
              </span>
            </div>
            <button
              className="btn-add-queue"
              onClick={() => onAddToQueue(slot)}
            >
              Agendar
            </button>
          </div>
        ))}
      </div>
    </section>
  );
}
```

### 8.5 Fila com Drag-and-Drop

A fila utiliza a API nativa HTML5 Drag and Drop para reordenacao visual. A prioridade do item e atualizada via `PUT /smart-schedule/queue/{id}` ao soltar.

```jsx
function ScheduleQueue({ items, onUpdate, onDelete }) {
  const [dragIndex, setDragIndex] = useState(null);

  const handleDragStart = (index) => setDragIndex(index);

  const handleDrop = (targetIndex) => {
    if (dragIndex === null || dragIndex === targetIndex) return;

    // Map position to priority: top third = high, middle = medium, bottom = low
    const total = items.length;
    let newPriority;
    if (targetIndex < total / 3) newPriority = 'high';
    else if (targetIndex < (total * 2) / 3) newPriority = 'medium';
    else newPriority = 'low';

    onUpdate(items[dragIndex].id, { priority: newPriority });
    setDragIndex(null);
  };

  const priorityBadge = (p) => {
    const colors = { high: '#ff4444', medium: '#ffaa00', low: '#44aa44' };
    const labels = { high: 'Alta', medium: 'Media', low: 'Baixa' };
    return (
      <span className="priority-badge" style={{ backgroundColor: colors[p] }}>
        {labels[p]}
      </span>
    );
  };

  return (
    <section className="queue-section">
      <h2>Fila de Publicacao</h2>
      {items.length === 0 ? (
        <p className="queue-empty">Nenhum item na fila. Use as sugestoes acima para agendar posts.</p>
      ) : (
        <div className="queue-list">
          {items.map((item, i) => (
            <div
              key={item.id}
              className={`queue-item ${item.status}`}
              draggable={item.status === 'queued'}
              onDragStart={() => handleDragStart(i)}
              onDragOver={(e) => e.preventDefault()}
              onDrop={() => handleDrop(i)}
            >
              <div className="queue-item-drag">&#x2630;</div>
              <div className="queue-item-info">
                <p className="queue-caption">{item.caption?.substring(0, 80) || '(sem legenda)'}...</p>
                <div className="queue-meta">
                  {priorityBadge(item.priority)}
                  <span>{new Date(item.suggested_at).toLocaleString('pt-BR')}</span>
                  <span className="queue-status">{item.status}</span>
                </div>
              </div>
              {item.status === 'queued' && (
                <button className="btn-delete-queue" onClick={() => onDelete(item.id)}>
                  Remover
                </button>
              )}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
```

### 8.6 Rota no React Router

Arquivo: `tron-legacy-frontend/src/App.jsx` - adicionar:

```jsx
import SmartSchedulingPage from './pages/SmartSchedulingPage';

// Dentro de <Routes>:
<Route path="/admin/agendamento-inteligente" element={<SmartSchedulingPage />} />
```

---

## 9. APIs Externas

### Instagram Graph API v21.0

A feature utiliza exclusivamente o endpoint de midia ja consumido pelo sistema:

```
GET https://graph.facebook.com/v21.0/{ig_account}/media
  ?fields=id,caption,media_url,media_type,like_count,comments_count,timestamp
  &limit=25
  &access_token={token}
```

**Campos utilizados para o heatmap:**
- `timestamp` (RFC3339): Determina dia da semana e hora da publicacao
- `like_count` + `comments_count`: Calculam a taxa de engajamento (`(likes + comments) / followers * 100`)

**Paginacao:** O endpoint retorna ate 25 posts por pagina. Para janelas de analise maiores, sera necessario paginar usando o cursor `paging.next` retornado pela API. A implementacao atual em `fetchMediaWithInsights()` busca 25 posts; para o heatmap, recomenda-se expandir para buscar ate `limit=100` ou paginar ate cobrir a janela de dias configurada.

**Rate Limits:** A API do Instagram impoe limite de 200 chamadas/hora por usuario. O heatmap e gerado sob demanda (nao em background), entao o impacto e minimo.

---

## 10. Codigo Reutilizado

A feature aproveita componentes ja implementados e testados:

| Componente                          | Arquivo original                  | Uso no Agendamento Inteligente                                      |
|-------------------------------------|-----------------------------------|----------------------------------------------------------------------|
| `fetchMediaWithInsights()`          | `instagram_analytics.go:256`      | Busca posts com metricas de engajamento para construir o heatmap     |
| `computeBestHours()`                | `instagram_analytics.go:303`      | Referencia de algoritmo; o heatmap expande para 7x24 (dia + hora)   |
| `fetchFollowersCount()`            | `instagram_analytics.go:240`      | Obtem total de seguidores para calcular taxa de engajamento          |
| `getInstagramCredentials()`         | `instagram.go:85`                 | Resolve credenciais do usuario (DB ou env fallback)                  |
| `InstagramSchedule` model           | `models/instagram.go:10`          | O job de 6h cria instancias deste model para delegar publicacao      |
| `ProcessScheduledInstagramPosts()`  | `instagram.go:1057`               | Scheduler de 1 min existente que faz a publicacao real via Graph API |
| `publishToInstagram()`             | `instagram.go:889`                | Funcao de publicacao (single image + carousel) ja implementada       |
| `database.InstagramSchedules()`    | `database/mongo.go:79`            | Collection de agendamentos ja indexada                               |
| `database.Images()`               | `database/mongo.go:59`            | Validacao de imagens existentes ao adicionar item na fila            |
| `middleware.Auth` + `RequireRole`  | `middleware/auth.go`, `role.go`    | Protecao das rotas (superuser + admin)                               |

Nenhuma funcao existente precisa ser modificada. Todo o codigo novo e aditivo.

---

## 11. Fluxo Completo

### Jornada do Usuario

1. **Configuracao Inicial**
   - Usuario acessa `/admin/agendamento-inteligente`
   - Configura a janela de analise (ex: 90 dias), dias preferenciais (ex: Seg-Sex), horarios bloqueados (ex: 0h-6h)
   - Ativa o agendamento inteligente (`enabled: true`)

2. **Analise do Heatmap**
   - Usuario clica em "Gerar Heatmap"
   - Frontend chama `GET /smart-schedule/heatmap`
   - Backend busca posts via `fetchMediaWithInsights()`, constroi matriz 7x24
   - Heatmap renderizado com cores: celulas mais quentes = maior engajamento medio

3. **Receber Sugestoes**
   - Usuario clica em "Sugerir Horarios"
   - Frontend chama `POST /smart-schedule/suggest` com `count: 5`
   - Backend filtra slots pelo heatmap, respeitando `PreferredDays` e `BlockedHours`
   - Retorna top 5 slots com proximo horario disponivel no calendario

4. **Adicionar a Fila**
   - Usuario seleciona um horario sugerido e clica "Agendar"
   - Preenche caption, seleciona imagens, define prioridade
   - Frontend chama `POST /smart-schedule/queue`
   - Item entra na fila com `status: queued` e `suggested_at` definido

5. **Gerenciar Fila**
   - Usuario visualiza fila ordenada por prioridade
   - Pode arrastar itens para reordenar (altera prioridade via drag-and-drop)
   - Pode editar caption, horario, prioridade de itens com `status: queued`
   - Pode remover itens da fila

6. **Publicacao Automatica**
   - Background job de 6h (`ProcessSmartScheduleQueue`) verifica itens prontos
   - Para cada item com `suggested_at <= now` e `status: queued`:
     - Cria um `InstagramSchedule` com `scheduled_at: now`
     - Atualiza item na fila para `status: scheduled`
   - O scheduler de 1 min (`ProcessScheduledInstagramPosts`) detecta o novo `InstagramSchedule`
   - Publica via Instagram Graph API
   - Atualiza para `status: published` ou `status: failed`

7. **Acompanhamento**
   - Usuario ve o status atualizado na fila (queued -> scheduled -> published/failed)
   - Pode re-agendar itens com falha

---

## 12. Verificacao

### 12.1 Testes Manuais - Backend

**Pre-requisito:** Instagram configurado com credenciais validas e pelo menos 10 posts publicados.

1. **Config CRUD**
   ```bash
   # GET config (deve retornar defaults)
   curl -H "Authorization: Bearer $TOKEN" \
     http://localhost:8088/api/v1/admin/smart-schedule/config

   # PUT config
   curl -X PUT -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" \
     -d '{
       "enabled": true,
       "analysis_window_days": 90,
       "min_posts_for_analysis": 5,
       "preferred_days": [1, 2, 3, 4, 5],
       "blocked_hours": [0, 1, 2, 3, 4, 5, 6]
     }' \
     http://localhost:8088/api/v1/admin/smart-schedule/config
   ```

2. **Heatmap**
   ```bash
   # Deve retornar matriz 7x24 com valores de engagement
   curl -H "Authorization: Bearer $TOKEN" \
     http://localhost:8088/api/v1/admin/smart-schedule/heatmap
   ```
   **Verificar:** `matrix` deve ter pelo menos algumas celulas com valores > 0. `total_posts` deve corresponder ao numero de posts dentro da janela de analise.

3. **Sugestoes**
   ```bash
   curl -X POST -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"count": 5}' \
     http://localhost:8088/api/v1/admin/smart-schedule/suggest
   ```
   **Verificar:** Retorna array de slots ordenados por `avg_engagement` decrescente. Nenhum slot deve ter hora em `blocked_hours`. Dias devem estar em `preferred_days` (se configurado).

4. **Queue CRUD**
   ```bash
   # Adicionar item
   curl -X POST -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" \
     -d '{
       "caption": "Post de teste do agendamento inteligente",
       "media_type": "image",
       "image_ids": ["<IMAGE_ID>"],
       "priority": "high",
       "suggested_at": "2026-03-10T14:00:00Z"
     }' \
     http://localhost:8088/api/v1/admin/smart-schedule/queue

   # Listar fila
   curl -H "Authorization: Bearer $TOKEN" \
     "http://localhost:8088/api/v1/admin/smart-schedule/queue?page=1&limit=10"

   # Atualizar prioridade
   curl -X PUT -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"priority": "low"}' \
     http://localhost:8088/api/v1/admin/smart-schedule/queue/<ITEM_ID>

   # Deletar
   curl -X DELETE -H "Authorization: Bearer $TOKEN" \
     http://localhost:8088/api/v1/admin/smart-schedule/queue/<ITEM_ID>
   ```

5. **Background Job**
   ```bash
   # Para testar, adicione um item com suggested_at no passado:
   curl -X POST -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" \
     -d '{
       "caption": "Teste de promocao automatica",
       "media_type": "image",
       "image_ids": ["<IMAGE_ID>"],
       "priority": "high",
       "suggested_at": "2020-01-01T00:00:00Z"
     }' \
     http://localhost:8088/api/v1/admin/smart-schedule/queue
   ```
   Aguardar o job de 6h executar (ou chamar `ProcessSmartScheduleQueue()` manualmente em um teste).
   **Verificar:** Item na fila muda para `status: scheduled`. Um novo documento aparece em `instagram_schedules` com `status: scheduled`. Apos o scheduler de 1 min executar, o post e publicado no Instagram.

### 12.2 Testes Manuais - Frontend

1. Acessar `/admin/agendamento-inteligente`
2. Configurar preferencias e salvar
3. Clicar em "Gerar Heatmap" - verificar que o grid 7x24 aparece com cores
4. Clicar em "Sugerir Horarios" - verificar cards com horarios recomendados
5. Clicar "Agendar" em uma sugestao - preencher dados e confirmar
6. Verificar item na fila com prioridade correta
7. Arrastar item para reordenar - verificar que prioridade atualiza
8. Deletar item da fila

### 12.3 Checklist de Validacao

- [ ] `GET /smart-schedule/config` retorna defaults quando nao configurado
- [ ] `PUT /smart-schedule/config` valida dias (0-6) e horas (0-23)
- [ ] `GET /smart-schedule/heatmap` retorna 422 quando ha poucos posts
- [ ] `GET /smart-schedule/heatmap` retorna matriz 7x24 valida com posts suficientes
- [ ] `POST /smart-schedule/suggest` respeita `preferred_days` e `blocked_hours`
- [ ] `POST /smart-schedule/suggest` ordena por engajamento decrescente
- [ ] `POST /smart-schedule/queue` valida imagens, media_type, caption
- [ ] `POST /smart-schedule/queue` auto-seleciona horario quando `suggested_at` vazio
- [ ] `PUT /smart-schedule/queue/{id}` so edita itens com `status: queued`
- [ ] `DELETE /smart-schedule/queue/{id}` nao permite deletar `scheduled` ou `published`
- [ ] `ProcessSmartScheduleQueue()` promove itens prontos para `InstagramSchedule`
- [ ] `ProcessScheduledInstagramPosts()` publica os posts promovidos normalmente
- [ ] Rotas protegidas retornam 401 sem token e 403 sem role adequada
- [ ] Heatmap no frontend renderiza corretamente com tooltip por celula
- [ ] Drag-and-drop na fila atualiza prioridade via API
