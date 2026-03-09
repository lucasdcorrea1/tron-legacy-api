# 12 - Monitoramento de Concorrencia

## 1. Visao Geral

O Monitoramento de Concorrencia e um dashboard que permite ao usuario acompanhar contas publicas do Instagram de concorrentes. A feature oferece:

- **Rastreamento de contas publicas**: adicionar/remover concorrentes pelo username do Instagram
- **Comparacao de metricas**: seguidores, taxa de engajamento, frequencia de postagem lado a lado com a propria conta
- **Tendencias historicas**: snapshots periodicos para visualizar crescimento/queda ao longo do tempo
- **Alertas inteligentes**: notificacoes quando concorrentes fazem movimentos significativos (pico de seguidores, post viral, queda de engajamento)

A coleta de dados utiliza a **Business Discovery API** do Meta Graph API v21.0, que permite consultar metricas publicas de qualquer conta profissional/business do Instagram usando as credenciais da propria conta do usuario.

---

## 2. Arquitetura

```
Usuario (Frontend)
    |
    |  HTTPS (REST API)
    v
[React 18 - CompetitorsPage.jsx]
    |
    |  GET/POST/DELETE /api/v1/admin/instagram/competitors/*
    v
[Go API - handlers/instagram_competitors.go]
    |
    |--- getInstagramCredentials(ctx, userID)  -->  [instagram_configs collection]
    |--- Business Discovery API (Meta Graph v21.0)
    |
    v
[MongoDB]
    |--- competitors           (contas rastreadas)
    |--- competitor_snapshots   (historico de metricas)
    |--- competitor_alerts      (configuracao de alertas)

[Background Job - competitorSnapshotScheduler()]
    |  Executa a cada 6 horas
    |--- Itera todos os competitors ativos
    |--- Chama Business Discovery API
    |--- Salva snapshot
    |--- Verifica alertas configurados
```

**Fluxo de dados:**

1. Usuario adiciona username de concorrente via frontend
2. Handler valida e busca dados iniciais via Business Discovery API
3. Dados sao salvos na collection `competitors`
4. Background job (6h) coleta snapshots automaticamente
5. Cada snapshot e armazenado em `competitor_snapshots`
6. Alertas sao verificados comparando snapshot atual vs anterior
7. Frontend exibe comparacao, tendencias e alertas

---

## 3. Models (Go)

Arquivo: `internal/models/competitor.go`

```go
package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Competitor represents a tracked competitor Instagram account
type Competitor struct {
	ID                primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID            primitive.ObjectID `json:"user_id" bson:"user_id"`
	Username          string             `json:"username" bson:"username"`
	Name              string             `json:"name" bson:"name"`
	ProfilePicURL     string             `json:"profile_pic_url" bson:"profile_pic_url"`
	FollowersCount    int64              `json:"followers_count" bson:"followers_count"`
	MediaCount        int64              `json:"media_count" bson:"media_count"`
	AvgEngagementRate float64            `json:"avg_engagement_rate" bson:"avg_engagement_rate"`
	LastSnapshotAt    time.Time          `json:"last_snapshot_at" bson:"last_snapshot_at"`
	Active            bool               `json:"active" bson:"active"`
	CreatedAt         time.Time          `json:"created_at" bson:"created_at"`
}

// CompetitorPost represents a single media item from a competitor
type CompetitorPost struct {
	ID            string `json:"id" bson:"id"`
	Caption       string `json:"caption" bson:"caption"`
	LikeCount     int64  `json:"like_count" bson:"like_count"`
	CommentsCount int64  `json:"comments_count" bson:"comments_count"`
	Timestamp     string `json:"timestamp" bson:"timestamp"`
	MediaType     string `json:"media_type" bson:"media_type"`
	MediaURL      string `json:"media_url" bson:"media_url"`
}

// CompetitorSnapshot represents a point-in-time capture of competitor metrics
type CompetitorSnapshot struct {
	ID             primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	CompetitorID   primitive.ObjectID `json:"competitor_id" bson:"competitor_id"`
	UserID         primitive.ObjectID `json:"user_id" bson:"user_id"`
	FollowersCount int64              `json:"followers_count" bson:"followers_count"`
	MediaCount     int64              `json:"media_count" bson:"media_count"`
	AvgLikes       float64            `json:"avg_likes" bson:"avg_likes"`
	AvgComments    float64            `json:"avg_comments" bson:"avg_comments"`
	EngagementRate float64            `json:"engagement_rate" bson:"engagement_rate"`
	TopPosts       []CompetitorPost   `json:"top_posts" bson:"top_posts"`
	SnapshotAt     time.Time          `json:"snapshot_at" bson:"snapshot_at"`
}

// CompetitorAlert represents an alert configuration for competitor monitoring
type CompetitorAlert struct {
	ID            primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID        primitive.ObjectID `json:"user_id" bson:"user_id"`
	CompetitorID  primitive.ObjectID `json:"competitor_id" bson:"competitor_id"`
	AlertType     string             `json:"alert_type" bson:"alert_type"` // "follower_spike", "engagement_drop", "new_post_viral"
	Threshold     float64            `json:"threshold" bson:"threshold"`
	Active        bool               `json:"active" bson:"active"`
	LastTriggered time.Time          `json:"last_triggered,omitempty" bson:"last_triggered,omitempty"`
	CreatedAt     time.Time          `json:"created_at" bson:"created_at"`
}

// AddCompetitorRequest is the request body for adding a competitor
type AddCompetitorRequest struct {
	Username string `json:"username"`
}

// CreateCompetitorAlertRequest is the request body for creating an alert
type CreateCompetitorAlertRequest struct {
	CompetitorID string  `json:"competitor_id"`
	AlertType    string  `json:"alert_type"`
	Threshold    float64 `json:"threshold"`
}

// CompetitorComparison holds side-by-side metrics for own account vs competitors
type CompetitorComparison struct {
	OwnAccount  CompetitorMetrics   `json:"own_account"`
	Competitors []CompetitorMetrics `json:"competitors"`
}

// CompetitorMetrics holds display metrics for comparison
type CompetitorMetrics struct {
	Username       string  `json:"username"`
	Name           string  `json:"name"`
	ProfilePicURL  string  `json:"profile_pic_url"`
	FollowersCount int64   `json:"followers_count"`
	MediaCount     int64   `json:"media_count"`
	EngagementRate float64 `json:"engagement_rate"`
	AvgLikes       float64 `json:"avg_likes"`
	AvgComments    float64 `json:"avg_comments"`
}

// CompetitorTrendPoint represents a single data point in a trend chart
type CompetitorTrendPoint struct {
	Date           string  `json:"date"`
	FollowersCount int64   `json:"followers_count"`
	EngagementRate float64 `json:"engagement_rate"`
	MediaCount     int64   `json:"media_count"`
}

// CompetitorTrendResponse is the response for trend data
type CompetitorTrendResponse struct {
	Competitor Competitor             `json:"competitor"`
	Trend      []CompetitorTrendPoint `json:"trend"`
}

// TriggeredAlert represents an alert that was triggered
type TriggeredAlert struct {
	CompetitorAlert `json:",inline"`
	CompetitorName  string  `json:"competitor_name"`
	Message         string  `json:"message"`
	CurrentValue    float64 `json:"current_value"`
	PreviousValue   float64 `json:"previous_value"`
}
```

**Tipos de alerta (`alert_type`):**

| Tipo | Descricao | Threshold exemplo |
|------|-----------|-------------------|
| `follower_spike` | Crescimento percentual de seguidores entre snapshots | `5.0` (5% de aumento) |
| `engagement_drop` | Queda percentual na taxa de engajamento | `20.0` (20% de queda) |
| `new_post_viral` | Post com engajamento X vezes acima da media | `3.0` (3x a media) |

---

## 4. Database

### Collections

Arquivo: `internal/database/mongo.go` (adicionar)

```go
func Competitors() *mongo.Collection {
	return DB.Collection("competitors")
}

func CompetitorSnapshots() *mongo.Collection {
	return DB.Collection("competitor_snapshots")
}

func CompetitorAlerts() *mongo.Collection {
	return DB.Collection("competitor_alerts")
}
```

### Indexes

Adicionar em `EnsureIndexes()`:

```go
// competitors: unique index on {user_id, username} — one entry per competitor per user
_, err = Competitors().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys:    bson.D{{Key: "user_id", Value: 1}, {Key: "username", Value: 1}},
	Options: options.Index().SetUnique(true),
})
if err != nil {
	return err
}

// competitors: index on {user_id, active} for listing active competitors
_, err = Competitors().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "active", Value: 1}},
})
if err != nil {
	return err
}

// competitor_snapshots: compound index on {competitor_id, snapshot_at} for trend queries
_, err = CompetitorSnapshots().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "competitor_id", Value: 1}, {Key: "snapshot_at", Value: -1}},
})
if err != nil {
	return err
}

// competitor_snapshots: index on {user_id, snapshot_at} for user-scoped queries
_, err = CompetitorSnapshots().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "snapshot_at", Value: -1}},
})
if err != nil {
	return err
}

// competitor_snapshots: TTL index — auto-delete snapshots after 180 days
_, err = CompetitorSnapshots().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys:    bson.D{{Key: "snapshot_at", Value: 1}},
	Options: options.Index().SetExpireAfterSeconds(180 * 24 * 3600),
})
if err != nil {
	return err
}

// competitor_alerts: index on {user_id, active}
_, err = CompetitorAlerts().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "active", Value: 1}},
})
if err != nil {
	return err
}

// competitor_alerts: index on {competitor_id, active} for alert checking
_, err = CompetitorAlerts().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "competitor_id", Value: 1}, {Key: "active", Value: 1}},
})
if err != nil {
	return err
}
```

---

## 5. Handlers (Go)

Arquivo: `internal/handlers/instagram_competitors.go`

### 5.1 AddCompetitor — `POST /api/v1/admin/instagram/competitors`

Adiciona uma conta para rastreamento. Valida que o username existe e e uma conta publica/business via Business Discovery API.

```go
func AddCompetitor(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req models.AddCompetitorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	username := strings.TrimPrefix(strings.TrimSpace(req.Username), "@")
	if username == "" {
		http.Error(w, "Username is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Check limit (max 10 competitors per user)
	count, _ := database.Competitors().CountDocuments(ctx, bson.M{"user_id": userID})
	if count >= 10 {
		http.Error(w, "Maximum of 10 competitors allowed", http.StatusBadRequest)
		return
	}

	// Check duplicate
	existingCount, _ := database.Competitors().CountDocuments(ctx, bson.M{
		"user_id":  userID,
		"username": username,
	})
	if existingCount > 0 {
		http.Error(w, "Competitor already tracked", http.StatusConflict)
		return
	}

	// Fetch competitor data via Business Discovery API
	creds, err := getInstagramCredentials(ctx, userID)
	if err != nil || creds == nil {
		http.Error(w, "Instagram not configured", http.StatusBadRequest)
		return
	}

	discovery, err := fetchBusinessDiscovery(creds.AccountID, creds.Token, username)
	if err != nil {
		slog.Error("business_discovery_failed", "username", username, "error", err)
		http.Error(w, "Could not find Instagram account: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Calculate initial engagement rate
	avgLikes, avgComments, engRate := calculateEngagementMetrics(discovery.Media, discovery.FollowersCount)

	now := time.Now()
	competitor := models.Competitor{
		ID:                primitive.NewObjectID(),
		UserID:            userID,
		Username:          username,
		Name:              discovery.Name,
		ProfilePicURL:     discovery.ProfilePicURL,
		FollowersCount:    discovery.FollowersCount,
		MediaCount:        discovery.MediaCount,
		AvgEngagementRate: engRate,
		LastSnapshotAt:    now,
		Active:            true,
		CreatedAt:         now,
	}

	_, err = database.Competitors().InsertOne(ctx, competitor)
	if err != nil {
		http.Error(w, "Error saving competitor", http.StatusInternalServerError)
		return
	}

	// Save initial snapshot
	snapshot := models.CompetitorSnapshot{
		ID:             primitive.NewObjectID(),
		CompetitorID:   competitor.ID,
		UserID:         userID,
		FollowersCount: discovery.FollowersCount,
		MediaCount:     discovery.MediaCount,
		AvgLikes:       avgLikes,
		AvgComments:    avgComments,
		EngagementRate: engRate,
		TopPosts:       discovery.TopPosts,
		SnapshotAt:     now,
	}
	database.CompetitorSnapshots().InsertOne(ctx, snapshot)

	slog.Info("competitor_added",
		"user_id", userID.Hex(),
		"username", username,
		"followers", discovery.FollowersCount,
	)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(competitor)
}
```

### 5.2 ListCompetitors — `GET /api/v1/admin/instagram/competitors`

Lista todos os concorrentes rastreados pelo usuario.

```go
func ListCompetitors(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{"user_id": userID, "active": true}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})

	cursor, err := database.Competitors().Find(ctx, filter, opts)
	if err != nil {
		http.Error(w, "Error fetching competitors", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var competitors []models.Competitor
	if err := cursor.All(ctx, &competitors); err != nil {
		http.Error(w, "Error decoding competitors", http.StatusInternalServerError)
		return
	}

	if competitors == nil {
		competitors = []models.Competitor{}
	}

	json.NewEncoder(w).Encode(competitors)
}
```

### 5.3 DeleteCompetitor — `DELETE /api/v1/admin/instagram/competitors/{id}`

Remove um concorrente (soft delete — marca como inativo).

```go
func DeleteCompetitor(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	idStr := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid competitor ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := database.Competitors().UpdateOne(ctx,
		bson.M{"_id": oid, "user_id": userID},
		bson.M{"$set": bson.M{"active": false}},
	)
	if err != nil {
		http.Error(w, "Error deleting competitor", http.StatusInternalServerError)
		return
	}
	if result.MatchedCount == 0 {
		http.Error(w, "Competitor not found", http.StatusNotFound)
		return
	}

	slog.Info("competitor_deleted", "competitor_id", idStr, "user_id", userID.Hex())
	json.NewEncoder(w).Encode(map[string]string{"message": "Competitor removed"})
}
```

### 5.4 RefreshCompetitor — `POST /api/v1/admin/instagram/competitors/{id}/refresh`

Forca a atualizacao imediata dos dados de um concorrente (fora do ciclo de 6h).

```go
func RefreshCompetitor(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	idStr := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid competitor ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var competitor models.Competitor
	err = database.Competitors().FindOne(ctx, bson.M{"_id": oid, "user_id": userID, "active": true}).Decode(&competitor)
	if err != nil {
		http.Error(w, "Competitor not found", http.StatusNotFound)
		return
	}

	creds, err := getInstagramCredentials(ctx, userID)
	if err != nil || creds == nil {
		http.Error(w, "Instagram not configured", http.StatusBadRequest)
		return
	}

	discovery, err := fetchBusinessDiscovery(creds.AccountID, creds.Token, competitor.Username)
	if err != nil {
		http.Error(w, "Failed to fetch competitor data: "+err.Error(), http.StatusBadGateway)
		return
	}

	avgLikes, avgComments, engRate := calculateEngagementMetrics(discovery.Media, discovery.FollowersCount)
	now := time.Now()

	// Update competitor record
	database.Competitors().UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set": bson.M{
			"name":                discovery.Name,
			"profile_pic_url":     discovery.ProfilePicURL,
			"followers_count":     discovery.FollowersCount,
			"media_count":         discovery.MediaCount,
			"avg_engagement_rate": engRate,
			"last_snapshot_at":    now,
		},
	})

	// Save new snapshot
	snapshot := models.CompetitorSnapshot{
		ID:             primitive.NewObjectID(),
		CompetitorID:   competitor.ID,
		UserID:         userID,
		FollowersCount: discovery.FollowersCount,
		MediaCount:     discovery.MediaCount,
		AvgLikes:       avgLikes,
		AvgComments:    avgComments,
		EngagementRate: engRate,
		TopPosts:       discovery.TopPosts,
		SnapshotAt:     now,
	}
	database.CompetitorSnapshots().InsertOne(ctx, snapshot)

	slog.Info("competitor_refreshed", "competitor_id", idStr, "username", competitor.Username)

	// Return updated competitor
	database.Competitors().FindOne(ctx, bson.M{"_id": oid}).Decode(&competitor)
	json.NewEncoder(w).Encode(competitor)
}
```

### 5.5 GetCompetitorTrend — `GET /api/v1/admin/instagram/competitors/{id}/trend?days=30`

Retorna historico de metricas para exibicao em grafico de tendencia.

```go
func GetCompetitorTrend(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	idStr := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid competitor ID", http.StatusBadRequest)
		return
	}

	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 1 || days > 180 {
		days = 30
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var competitor models.Competitor
	err = database.Competitors().FindOne(ctx, bson.M{"_id": oid, "user_id": userID}).Decode(&competitor)
	if err != nil {
		http.Error(w, "Competitor not found", http.StatusNotFound)
		return
	}

	since := time.Now().AddDate(0, 0, -days)
	filter := bson.M{
		"competitor_id": oid,
		"snapshot_at":   bson.M{"$gte": since},
	}
	opts := options.Find().SetSort(bson.D{{Key: "snapshot_at", Value: 1}})

	cursor, err := database.CompetitorSnapshots().Find(ctx, filter, opts)
	if err != nil {
		http.Error(w, "Error fetching trend data", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var snapshots []models.CompetitorSnapshot
	cursor.All(ctx, &snapshots)

	trend := make([]models.CompetitorTrendPoint, len(snapshots))
	for i, s := range snapshots {
		trend[i] = models.CompetitorTrendPoint{
			Date:           s.SnapshotAt.Format("2006-01-02"),
			FollowersCount: s.FollowersCount,
			EngagementRate: s.EngagementRate,
			MediaCount:     s.MediaCount,
		}
	}

	json.NewEncoder(w).Encode(models.CompetitorTrendResponse{
		Competitor: competitor,
		Trend:      trend,
	})
}
```

### 5.6 GetComparison — `GET /api/v1/admin/instagram/competitors/comparison`

Retorna metricas da propria conta vs todos os concorrentes ativos lado a lado.

```go
func GetCompetitorComparison(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	creds, err := getInstagramCredentials(ctx, userID)
	if err != nil || creds == nil {
		http.Error(w, "Instagram not configured", http.StatusBadRequest)
		return
	}

	// Fetch own account metrics
	ownFollowers := fetchFollowersCount(creds.AccountID, creds.Token)
	ownPosts := fetchMediaWithInsights(creds.AccountID, creds.Token, ownFollowers)

	var ownAvgLikes, ownAvgComments, ownEngRate float64
	if len(ownPosts) > 0 {
		var totalLikes, totalComments int64
		var engSum float64
		for _, p := range ownPosts {
			totalLikes += p.LikeCount
			totalComments += p.CommentsCount
			engSum += p.EngagementRate
		}
		ownAvgLikes = float64(totalLikes) / float64(len(ownPosts))
		ownAvgComments = float64(totalComments) / float64(len(ownPosts))
		ownEngRate = engSum / float64(len(ownPosts))
	}

	// Fetch own username
	ownUsername := fetchOwnUsername(creds.AccountID, creds.Token)

	ownMetrics := models.CompetitorMetrics{
		Username:       ownUsername,
		Name:           ownUsername,
		FollowersCount: ownFollowers,
		MediaCount:     int64(len(ownPosts)),
		EngagementRate: ownEngRate,
		AvgLikes:       ownAvgLikes,
		AvgComments:    ownAvgComments,
	}

	// Fetch competitor metrics
	cursor, err := database.Competitors().Find(ctx, bson.M{"user_id": userID, "active": true})
	if err != nil {
		http.Error(w, "Error fetching competitors", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var competitors []models.Competitor
	cursor.All(ctx, &competitors)

	competitorMetrics := make([]models.CompetitorMetrics, len(competitors))
	for i, c := range competitors {
		// Use latest snapshot for avg_likes and avg_comments
		var latest models.CompetitorSnapshot
		database.CompetitorSnapshots().FindOne(ctx,
			bson.M{"competitor_id": c.ID},
			options.FindOne().SetSort(bson.D{{Key: "snapshot_at", Value: -1}}),
		).Decode(&latest)

		competitorMetrics[i] = models.CompetitorMetrics{
			Username:       c.Username,
			Name:           c.Name,
			ProfilePicURL:  c.ProfilePicURL,
			FollowersCount: c.FollowersCount,
			MediaCount:     c.MediaCount,
			EngagementRate: c.AvgEngagementRate,
			AvgLikes:       latest.AvgLikes,
			AvgComments:    latest.AvgComments,
		}
	}

	json.NewEncoder(w).Encode(models.CompetitorComparison{
		OwnAccount:  ownMetrics,
		Competitors: competitorMetrics,
	})
}
```

### 5.7 Alert CRUD

```go
// ListCompetitorAlerts — GET /api/v1/admin/instagram/competitors/alerts
func ListCompetitorAlerts(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cursor, err := database.CompetitorAlerts().Find(ctx, bson.M{"user_id": userID, "active": true})
	if err != nil {
		http.Error(w, "Error fetching alerts", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var alerts []models.CompetitorAlert
	cursor.All(ctx, &alerts)
	if alerts == nil {
		alerts = []models.CompetitorAlert{}
	}

	json.NewEncoder(w).Encode(alerts)
}

// CreateCompetitorAlert — POST /api/v1/admin/instagram/competitors/alerts
func CreateCompetitorAlert(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)

	var req models.CreateCompetitorAlertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate alert type
	validTypes := map[string]bool{
		"follower_spike":  true,
		"engagement_drop": true,
		"new_post_viral":  true,
	}
	if !validTypes[req.AlertType] {
		http.Error(w, "Invalid alert_type. Must be: follower_spike, engagement_drop, new_post_viral", http.StatusBadRequest)
		return
	}

	if req.Threshold <= 0 {
		http.Error(w, "Threshold must be greater than 0", http.StatusBadRequest)
		return
	}

	competitorOID, err := primitive.ObjectIDFromHex(req.CompetitorID)
	if err != nil {
		http.Error(w, "Invalid competitor_id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Verify competitor exists and belongs to user
	count, _ := database.Competitors().CountDocuments(ctx, bson.M{
		"_id": competitorOID, "user_id": userID, "active": true,
	})
	if count == 0 {
		http.Error(w, "Competitor not found", http.StatusNotFound)
		return
	}

	now := time.Now()
	alert := models.CompetitorAlert{
		ID:           primitive.NewObjectID(),
		UserID:       userID,
		CompetitorID: competitorOID,
		AlertType:    req.AlertType,
		Threshold:    req.Threshold,
		Active:       true,
		CreatedAt:    now,
	}

	_, err = database.CompetitorAlerts().InsertOne(ctx, alert)
	if err != nil {
		http.Error(w, "Error creating alert", http.StatusInternalServerError)
		return
	}

	slog.Info("competitor_alert_created",
		"user_id", userID.Hex(),
		"competitor_id", req.CompetitorID,
		"type", req.AlertType,
	)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(alert)
}

// DeleteCompetitorAlert — DELETE /api/v1/admin/instagram/competitors/alerts/{id}
func DeleteCompetitorAlert(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	idStr := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid alert ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := database.CompetitorAlerts().DeleteOne(ctx, bson.M{"_id": oid, "user_id": userID})
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
```

### 5.8 Funcoes auxiliares: Business Discovery API e calculo de metricas

```go
// businessDiscoveryResult holds parsed data from the Business Discovery API
type businessDiscoveryResult struct {
	Username       string
	Name           string
	ProfilePicURL  string
	FollowersCount int64
	MediaCount     int64
	Media          []businessDiscoveryMedia
	TopPosts       []models.CompetitorPost
}

type businessDiscoveryMedia struct {
	ID            string `json:"id"`
	Caption       string `json:"caption"`
	LikeCount     int64  `json:"like_count"`
	CommentsCount int64  `json:"comments_count"`
	Timestamp     string `json:"timestamp"`
	MediaType     string `json:"media_type"`
	MediaURL      string `json:"media_url"`
}

// fetchBusinessDiscovery calls the Meta Business Discovery API
func fetchBusinessDiscovery(ownAccountID, token, targetUsername string) (*businessDiscoveryResult, error) {
	fields := "username,name,biography,followers_count,media_count,profile_picture_url,media.limit(12){id,caption,like_count,comments_count,timestamp,media_type,media_url}"
	apiURL := fmt.Sprintf(
		"https://graph.facebook.com/v21.0/%s?fields=business_discovery.fields(%s)&business_discovery=@%s&access_token=%s",
		ownAccountID, url.QueryEscape(fields), targetUsername, token,
	)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var result struct {
		BusinessDiscovery struct {
			Username       string `json:"username"`
			Name           string `json:"name"`
			ProfilePicURL  string `json:"profile_picture_url"`
			FollowersCount int64  `json:"followers_count"`
			MediaCount     int64  `json:"media_count"`
			Media          struct {
				Data []businessDiscoveryMedia `json:"data"`
			} `json:"media"`
		} `json:"business_discovery"`
		Error *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("API error %d: %s", result.Error.Code, result.Error.Message)
	}

	bd := result.BusinessDiscovery

	// Convert media to top posts
	topPosts := make([]models.CompetitorPost, len(bd.Media.Data))
	for i, m := range bd.Media.Data {
		topPosts[i] = models.CompetitorPost{
			ID:            m.ID,
			Caption:       m.Caption,
			LikeCount:     m.LikeCount,
			CommentsCount: m.CommentsCount,
			Timestamp:     m.Timestamp,
			MediaType:     m.MediaType,
			MediaURL:      m.MediaURL,
		}
	}

	// Sort by engagement (likes + comments) descending
	sort.Slice(topPosts, func(i, j int) bool {
		engI := topPosts[i].LikeCount + topPosts[i].CommentsCount
		engJ := topPosts[j].LikeCount + topPosts[j].CommentsCount
		return engI > engJ
	})

	return &businessDiscoveryResult{
		Username:       bd.Username,
		Name:           bd.Name,
		ProfilePicURL:  bd.ProfilePicURL,
		FollowersCount: bd.FollowersCount,
		MediaCount:     bd.MediaCount,
		Media:          bd.Media.Data,
		TopPosts:       topPosts,
	}, nil
}

// calculateEngagementMetrics computes avg likes, avg comments, and engagement rate
func calculateEngagementMetrics(media []businessDiscoveryMedia, followers int64) (avgLikes, avgComments, engRate float64) {
	if len(media) == 0 {
		return 0, 0, 0
	}

	var totalLikes, totalComments int64
	for _, m := range media {
		totalLikes += m.LikeCount
		totalComments += m.CommentsCount
	}

	avgLikes = float64(totalLikes) / float64(len(media))
	avgComments = float64(totalComments) / float64(len(media))

	if followers > 0 {
		engRate = (avgLikes + avgComments) / float64(followers) * 100
	}

	return avgLikes, avgComments, engRate
}

// fetchOwnUsername fetches the authenticated account's username
func fetchOwnUsername(accountID, token string) string {
	apiURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s?fields=username&access_token=%s", accountID, token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var result struct {
		Username string `json:"username"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Username
}
```

### 5.9 SnapshotAllCompetitors — Background Job Handler

```go
// SnapshotAllCompetitors is called by the background job every 6 hours.
// Iterates all active competitors across all users, fetches fresh data, and saves snapshots.
func SnapshotAllCompetitors() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Find all distinct user_ids that have active competitors
	userIDs, err := database.Competitors().Distinct(ctx, "user_id", bson.M{"active": true})
	if err != nil {
		slog.Error("competitor_snapshot_distinct_users_error", "error", err)
		return
	}

	for _, uid := range userIDs {
		userID, ok := uid.(primitive.ObjectID)
		if !ok {
			continue
		}

		creds, err := getInstagramCredentials(ctx, userID)
		if err != nil || creds == nil {
			slog.Warn("competitor_snapshot_no_creds", "user_id", userID.Hex())
			continue
		}

		// Fetch all active competitors for this user
		cursor, err := database.Competitors().Find(ctx, bson.M{"user_id": userID, "active": true})
		if err != nil {
			continue
		}

		var competitors []models.Competitor
		cursor.All(ctx, &competitors)
		cursor.Close(ctx)

		for _, comp := range competitors {
			// Rate limit: sleep 2 seconds between API calls
			time.Sleep(2 * time.Second)

			discovery, err := fetchBusinessDiscovery(creds.AccountID, creds.Token, comp.Username)
			if err != nil {
				slog.Error("competitor_snapshot_fetch_error",
					"username", comp.Username,
					"error", err,
				)
				continue
			}

			avgLikes, avgComments, engRate := calculateEngagementMetrics(discovery.Media, discovery.FollowersCount)
			now := time.Now()

			// Update competitor record
			database.Competitors().UpdateOne(ctx, bson.M{"_id": comp.ID}, bson.M{
				"$set": bson.M{
					"name":                discovery.Name,
					"profile_pic_url":     discovery.ProfilePicURL,
					"followers_count":     discovery.FollowersCount,
					"media_count":         discovery.MediaCount,
					"avg_engagement_rate": engRate,
					"last_snapshot_at":    now,
				},
			})

			// Save snapshot
			snapshot := models.CompetitorSnapshot{
				ID:             primitive.NewObjectID(),
				CompetitorID:   comp.ID,
				UserID:         userID,
				FollowersCount: discovery.FollowersCount,
				MediaCount:     discovery.MediaCount,
				AvgLikes:       avgLikes,
				AvgComments:    avgComments,
				EngagementRate: engRate,
				TopPosts:       discovery.TopPosts,
				SnapshotAt:     now,
			}
			database.CompetitorSnapshots().InsertOne(ctx, snapshot)

			// Check alerts for this competitor
			checkCompetitorAlerts(ctx, comp, snapshot)

			slog.Info("competitor_snapshot_saved",
				"username", comp.Username,
				"followers", discovery.FollowersCount,
				"engagement_rate", engRate,
			)
		}
	}
}

// checkCompetitorAlerts compares current snapshot against previous and triggers alerts
func checkCompetitorAlerts(ctx context.Context, comp models.Competitor, current models.CompetitorSnapshot) {
	// Get previous snapshot (second most recent)
	opts := options.FindOne().SetSort(bson.D{{Key: "snapshot_at", Value: -1}}).SetSkip(1)
	var previous models.CompetitorSnapshot
	err := database.CompetitorSnapshots().FindOne(ctx,
		bson.M{"competitor_id": comp.ID},
		opts,
	).Decode(&previous)
	if err != nil {
		return // No previous snapshot to compare
	}

	// Fetch active alerts for this competitor
	cursor, err := database.CompetitorAlerts().Find(ctx, bson.M{
		"competitor_id": comp.ID,
		"active":        true,
	})
	if err != nil {
		return
	}
	defer cursor.Close(ctx)

	var alerts []models.CompetitorAlert
	cursor.All(ctx, &alerts)

	for _, alert := range alerts {
		triggered := false

		switch alert.AlertType {
		case "follower_spike":
			if previous.FollowersCount > 0 {
				growthPct := float64(current.FollowersCount-previous.FollowersCount) / float64(previous.FollowersCount) * 100
				if growthPct >= alert.Threshold {
					triggered = true
					slog.Warn("competitor_alert_triggered",
						"type", "follower_spike",
						"username", comp.Username,
						"growth_pct", growthPct,
						"threshold", alert.Threshold,
					)
				}
			}

		case "engagement_drop":
			if previous.EngagementRate > 0 {
				dropPct := (previous.EngagementRate - current.EngagementRate) / previous.EngagementRate * 100
				if dropPct >= alert.Threshold {
					triggered = true
					slog.Warn("competitor_alert_triggered",
						"type", "engagement_drop",
						"username", comp.Username,
						"drop_pct", dropPct,
						"threshold", alert.Threshold,
					)
				}
			}

		case "new_post_viral":
			if len(current.TopPosts) > 0 && current.AvgLikes > 0 {
				topPost := current.TopPosts[0]
				topEng := float64(topPost.LikeCount + topPost.CommentsCount)
				avgEng := current.AvgLikes + current.AvgComments
				if avgEng > 0 && topEng/avgEng >= alert.Threshold {
					triggered = true
					slog.Warn("competitor_alert_triggered",
						"type", "new_post_viral",
						"username", comp.Username,
						"top_eng", topEng,
						"avg_eng", avgEng,
						"multiplier", topEng/avgEng,
					)
				}
			}
		}

		if triggered {
			database.CompetitorAlerts().UpdateOne(ctx,
				bson.M{"_id": alert.ID},
				bson.M{"$set": bson.M{"last_triggered": time.Now()}},
			)
		}
	}
}
```

---

## 6. Rotas API

Arquivo: `internal/router/router.go` (adicionar na secao de rotas Instagram admin)

```go
// Instagram competitor monitoring routes (superuser + admin)
mux.Handle("GET /api/v1/admin/instagram/competitors",
	middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListCompetitors))))
mux.Handle("POST /api/v1/admin/instagram/competitors",
	middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.AddCompetitor))))
mux.Handle("DELETE /api/v1/admin/instagram/competitors/{id}",
	middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.DeleteCompetitor))))
mux.Handle("POST /api/v1/admin/instagram/competitors/{id}/refresh",
	middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.RefreshCompetitor))))
mux.Handle("GET /api/v1/admin/instagram/competitors/{id}/trend",
	middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetCompetitorTrend))))
mux.Handle("GET /api/v1/admin/instagram/competitors/comparison",
	middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetCompetitorComparison))))

// Competitor alert routes
mux.Handle("GET /api/v1/admin/instagram/competitors/alerts",
	middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListCompetitorAlerts))))
mux.Handle("POST /api/v1/admin/instagram/competitors/alerts",
	middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.CreateCompetitorAlert))))
mux.Handle("DELETE /api/v1/admin/instagram/competitors/alerts/{id}",
	middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.DeleteCompetitorAlert))))
```

**Importante:** A rota `GET /competitors/comparison` deve ser registrada **antes** de `GET /competitors/{id}/trend` para evitar conflito de path matching no `ServeMux` do Go 1.22+. Da mesma forma, as rotas `/competitors/alerts` devem vir antes de `/competitors/{id}`.

### Resumo de endpoints

| Metodo | Endpoint | Descricao |
|--------|----------|-----------|
| `GET` | `/api/v1/admin/instagram/competitors` | Listar concorrentes ativos |
| `POST` | `/api/v1/admin/instagram/competitors` | Adicionar concorrente |
| `DELETE` | `/api/v1/admin/instagram/competitors/{id}` | Remover concorrente |
| `POST` | `/api/v1/admin/instagram/competitors/{id}/refresh` | Atualizar dados manualmente |
| `GET` | `/api/v1/admin/instagram/competitors/{id}/trend` | Tendencia historica (sparkline) |
| `GET` | `/api/v1/admin/instagram/competitors/comparison` | Comparacao propria conta vs concorrentes |
| `GET` | `/api/v1/admin/instagram/competitors/alerts` | Listar alertas |
| `POST` | `/api/v1/admin/instagram/competitors/alerts` | Criar alerta |
| `DELETE` | `/api/v1/admin/instagram/competitors/alerts/{id}` | Remover alerta |

---

## 7. Background Jobs

Arquivo: `cmd/api/main.go` (adicionar)

```go
// Start competitor snapshot scheduler
go competitorSnapshotScheduler()
```

Funcao do scheduler:

```go
// competitorSnapshotScheduler runs every 6 hours and snapshots all active competitors.
func competitorSnapshotScheduler() {
	// Wait for server to start
	time.Sleep(45 * time.Second)
	log.Println("Competitor snapshot scheduler started (6h interval)")

	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		handlers.SnapshotAllCompetitors()
	}
}
```

**Detalhes do job:**

- **Intervalo:** 6 horas (suficiente para acompanhar tendencias sem exceder rate limits)
- **Rate limiting:** 2 segundos entre chamadas para evitar throttling da API do Meta
- **Timeout:** 5 minutos total para processar todos os competitors de todos os usuarios
- **Execucao:** Itera todos os usuarios com competitors ativos, busca credenciais via `getInstagramCredentials()`, chama Business Discovery API, salva snapshot, verifica alertas
- **Tratamento de erro:** Log de erro por competitor individual sem interromper o processamento dos demais

---

## 8. Frontend

### 8.1 Pagina principal: `CompetitorsPage.jsx`

Localizar em: `tron-legacy-frontend/src/pages/admin/CompetitorsPage.jsx`

```jsx
import { useState, useEffect, useCallback } from 'react';

export default function CompetitorsPage() {
  const [competitors, setCompetitors] = useState([]);
  const [comparison, setComparison] = useState(null);
  const [alerts, setAlerts] = useState([]);
  const [selectedCompetitor, setSelectedCompetitor] = useState(null);
  const [trendData, setTrendData] = useState(null);
  const [showAddModal, setShowAddModal] = useState(false);
  const [showAlertModal, setShowAlertModal] = useState(false);
  const [loading, setLoading] = useState(true);
  const [newUsername, setNewUsername] = useState('');

  const fetchCompetitors = useCallback(async () => {
    const res = await fetch('/api/v1/admin/instagram/competitors', {
      headers: { Authorization: `Bearer ${token}` },
    });
    const data = await res.json();
    setCompetitors(data);
  }, []);

  const fetchComparison = useCallback(async () => {
    const res = await fetch('/api/v1/admin/instagram/competitors/comparison', {
      headers: { Authorization: `Bearer ${token}` },
    });
    const data = await res.json();
    setComparison(data);
  }, []);

  const fetchAlerts = useCallback(async () => {
    const res = await fetch('/api/v1/admin/instagram/competitors/alerts', {
      headers: { Authorization: `Bearer ${token}` },
    });
    const data = await res.json();
    setAlerts(data);
  }, []);

  useEffect(() => {
    Promise.all([fetchCompetitors(), fetchComparison(), fetchAlerts()])
      .finally(() => setLoading(false));
  }, []);

  const handleAddCompetitor = async () => {
    await fetch('/api/v1/admin/instagram/competitors', {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ username: newUsername }),
    });
    setNewUsername('');
    setShowAddModal(false);
    fetchCompetitors();
    fetchComparison();
  };

  const handleRefresh = async (id) => {
    await fetch(`/api/v1/admin/instagram/competitors/${id}/refresh`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${token}` },
    });
    fetchCompetitors();
    fetchComparison();
  };

  const handleDelete = async (id) => {
    if (!confirm('Remover este concorrente?')) return;
    await fetch(`/api/v1/admin/instagram/competitors/${id}`, {
      method: 'DELETE',
      headers: { Authorization: `Bearer ${token}` },
    });
    fetchCompetitors();
    fetchComparison();
  };

  const handleViewTrend = async (competitor) => {
    setSelectedCompetitor(competitor);
    const res = await fetch(
      `/api/v1/admin/instagram/competitors/${competitor.id}/trend?days=30`,
      { headers: { Authorization: `Bearer ${token}` } }
    );
    const data = await res.json();
    setTrendData(data);
  };

  // ... render
}
```

### 8.2 Componentes do dashboard

**ComparisonCards** — Cards lado a lado comparando a propria conta com cada concorrente:

```
+------------------+    +------------------+    +------------------+
| @minha_conta     |    | @concorrente_1   |    | @concorrente_2   |
| [foto]           |    | [foto]           |    | [foto]           |
| 12.5K seguidores |    | 8.3K seguidores  |    | 15.1K seguidores |
| 3.2% engajamento|    | 2.8% engajamento |    | 4.1% engajamento |
| 245 posts        |    | 312 posts        |    | 189 posts        |
| Avg: 400 likes   |    | Avg: 233 likes   |    | Avg: 619 likes   |
+------------------+    +------------------+    +------------------+
```

**TrendTable** — Tabela com sparklines para cada concorrente:

```
+------------------+----------+--------+------------+-------------------+--------+
| Concorrente      | Seguid.  | Posts  | Engaj. (%) | Tendencia (30d)   | Acoes  |
+------------------+----------+--------+------------+-------------------+--------+
| @concorrente_1   | 8.3K     | 312    | 2.8%       | [sparkline ^^^^]  | [R][X] |
| @concorrente_2   | 15.1K    | 189    | 4.1%       | [sparkline ____]  | [R][X] |
+------------------+----------+--------+------------+-------------------+--------+
  [R] = Refresh   [X] = Remover
```

**AlertConfigModal** — Modal para configurar alertas:

```
+---------------------------------------+
| Configurar Alerta                     |
|                                       |
| Concorrente: [dropdown]               |
| Tipo:        [dropdown]               |
|   - Pico de seguidores                |
|   - Queda de engajamento             |
|   - Post viral                        |
| Threshold:   [input numerico] %       |
|                                       |
| [Cancelar]  [Criar Alerta]            |
+---------------------------------------+
```

### 8.3 Rota no React Router

```jsx
// App.jsx ou routes config
<Route path="/admin/instagram/competitors" element={<CompetitorsPage />} />
```

---

## 9. APIs Externas

### 9.1 Business Discovery API

A **Business Discovery API** permite que uma conta business/creator do Instagram consulte informacoes publicas de outras contas business/creator.

**Requisitos:**
- A propria conta do usuario deve ser uma conta Business ou Creator
- A conta alvo deve ser publica e do tipo Business ou Creator
- Token de acesso com permissao `instagram_basic` e `pages_read_engagement`

**Endpoint principal:**

```
GET https://graph.facebook.com/v21.0/{own_ig_account_id}
  ?fields=business_discovery.fields(
    username,
    name,
    biography,
    followers_count,
    media_count,
    profile_picture_url,
    media.limit(12){
      id,
      caption,
      like_count,
      comments_count,
      timestamp,
      media_type,
      media_url
    }
  )
  &business_discovery=@{competitor_username}
  &access_token={token}
```

**Parametros:**

| Parametro | Tipo | Descricao |
|-----------|------|-----------|
| `own_ig_account_id` | string | ID da conta Instagram do usuario autenticado |
| `business_discovery` | string | Username do concorrente prefixado com `@` |
| `fields` | string | Campos solicitados via subquery `business_discovery.fields(...)` |
| `access_token` | string | Token de acesso do usuario |

**Campos disponiveis na subquery `business_discovery.fields()`:**

| Campo | Tipo | Descricao |
|-------|------|-----------|
| `username` | string | Username do concorrente |
| `name` | string | Nome de exibicao |
| `biography` | string | Bio do perfil |
| `followers_count` | int | Numero total de seguidores |
| `media_count` | int | Numero total de publicacoes |
| `profile_picture_url` | string | URL da foto de perfil |
| `media` | connection | Lista de publicacoes recentes |

**Campos disponiveis em `media{}`:**

| Campo | Tipo | Descricao |
|-------|------|-----------|
| `id` | string | ID da publicacao |
| `caption` | string | Legenda |
| `like_count` | int | Numero de curtidas |
| `comments_count` | int | Numero de comentarios |
| `timestamp` | ISO 8601 | Data/hora da publicacao |
| `media_type` | string | `IMAGE`, `VIDEO`, `CAROUSEL_ALBUM` |
| `media_url` | string | URL da midia |

**Exemplo de resposta:**

```json
{
  "business_discovery": {
    "username": "concorrente_exemplo",
    "name": "Concorrente Exemplo",
    "biography": "Perfil de exemplo",
    "followers_count": 15234,
    "media_count": 312,
    "profile_picture_url": "https://...",
    "media": {
      "data": [
        {
          "id": "17895695668004550",
          "caption": "Novo produto!",
          "like_count": 523,
          "comments_count": 45,
          "timestamp": "2025-03-15T14:30:00+0000",
          "media_type": "IMAGE",
          "media_url": "https://..."
        }
      ]
    },
    "id": "17841405309012345"
  },
  "id": "17841400123456789"
}
```

**Tratamento de erros:**

| Codigo | Significado | Acao |
|--------|------------|------|
| `100` | Invalid parameter | Username nao existe ou conta nao e business/creator |
| `10` | Permission denied | Token sem permissao `instagram_basic` |
| `4` | Rate limit | Aguardar e tentar novamente |
| `190` | Token expirado | Renovar token |

### 9.2 Rate Limits

- **Business Discovery API:** ~200 chamadas/hora por token (compartilhado com outras chamadas)
- **Estrategia do job:** 2 segundos de delay entre chamadas para manter margem segura
- **Limite de concorrentes:** Maximo 10 por usuario para controlar consumo de API

---

## 10. Codigo Reutilizado

A feature reutiliza componentes ja existentes no codebase:

### `getInstagramCredentials(ctx, userID)`
- **Arquivo:** `internal/handlers/instagram.go` (linhas 85-117)
- **Uso:** Obter `AccountID` e `Token` do usuario para autenticar chamadas a Graph API
- **Padrao:** Tenta DB primeiro (`instagram_configs` collection), depois fallback para env vars

### `fetchFollowersCount(accountID, token)`
- **Arquivo:** `internal/handlers/instagram_analytics.go` (linhas 240-254)
- **Uso:** Buscar `followers_count` da propria conta para calcular engagement rate na comparacao
- **Endpoint:** `GET /v21.0/{accountID}?fields=followers_count`

### `fetchMediaWithInsights(accountID, token, followers)`
- **Arquivo:** `internal/handlers/instagram_analytics.go` (linhas 256-301)
- **Uso:** Buscar posts recentes da propria conta com metricas para comparacao
- **Endpoint:** `GET /v21.0/{accountID}/media?fields=id,caption,...`

### `database.CollectionName()` pattern
- **Arquivo:** `internal/database/mongo.go`
- **Uso:** Acesso tipado as collections MongoDB
- **Padrao:** Funcao publica retornando `*mongo.Collection`

### `middleware.Auth()` + `middleware.RequireRole()`
- **Arquivo:** `internal/middleware/auth.go`, `internal/middleware/role.go`
- **Uso:** Proteger rotas com autenticacao JWT e verificacao de role
- **Padrao:** `middleware.Auth(middleware.RequireRole("superuser", "admin")(handler))`

### `r.PathValue("id")` pattern
- **Arquivo:** Multiplos handlers (Go 1.22+ ServeMux)
- **Uso:** Extrair parametros de rota como `{id}` do path

---

## 11. Fluxo Completo

### Jornada do usuario

1. **Acesso ao dashboard**
   - Usuario admin acessa `/admin/instagram/competitors`
   - Frontend carrega lista de concorrentes, comparacao e alertas em paralelo

2. **Adicionar concorrente**
   - Clica em "Adicionar Concorrente"
   - Digita `@username` no modal
   - Backend chama Business Discovery API para validar a conta
   - Se a conta e publica e business/creator, salva na collection `competitors`
   - Snapshot inicial e criado automaticamente
   - Card de comparacao aparece no dashboard

3. **Visualizar comparacao**
   - Dashboard exibe cards lado a lado: propria conta vs cada concorrente
   - Metricas: seguidores, engajamento, media de likes/comentarios, total de posts
   - Destaques visuais para indicar quem esta a frente em cada metrica

4. **Analisar tendencias**
   - Clica no icone de tendencia de um concorrente
   - Grafico de sparkline/linha mostra evolucao de seguidores e engajamento nos ultimos 30 dias
   - Dados baseados nos snapshots coletados a cada 6 horas

5. **Configurar alertas**
   - Clica em "Configurar Alertas"
   - Seleciona concorrente, tipo de alerta e threshold
   - Exemplo: "Avisar quando @concorrente_1 crescer mais de 5% em seguidores"
   - Alerta e verificado automaticamente a cada snapshot

6. **Receber alertas**
   - A cada 6 horas, o background job compara snapshots e verifica thresholds
   - Se um alerta e acionado, log e registrado e `last_triggered` e atualizado
   - (Extensao futura: notificacao in-app ou email)

7. **Atualizar manualmente**
   - Clica no botao de refresh em um concorrente especifico
   - Backend busca dados frescos e salva novo snapshot
   - Dashboard atualiza imediatamente

8. **Remover concorrente**
   - Clica no botao de remover
   - Confirma a acao
   - Competitor e marcado como `active: false` (soft delete)
   - Snapshots historicos sao mantidos (expiram via TTL de 180 dias)

---

## 12. Verificacao

### 12.1 Testes manuais — API

**Pre-requisito:** Instagram configurado com conta Business/Creator e token valido.

```bash
# 1. Adicionar concorrente
curl -X POST http://localhost:8088/api/v1/admin/instagram/competitors \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"username": "instagram"}'

# Esperado: 201 Created com dados do competitor

# 2. Listar concorrentes
curl http://localhost:8088/api/v1/admin/instagram/competitors \
  -H "Authorization: Bearer $TOKEN"

# Esperado: Array com o concorrente adicionado

# 3. Comparacao propria conta vs concorrentes
curl http://localhost:8088/api/v1/admin/instagram/competitors/comparison \
  -H "Authorization: Bearer $TOKEN"

# Esperado: { own_account: {...}, competitors: [{...}] }

# 4. Refresh manual
curl -X POST http://localhost:8088/api/v1/admin/instagram/competitors/{id}/refresh \
  -H "Authorization: Bearer $TOKEN"

# Esperado: 200 OK com dados atualizados

# 5. Tendencia (apos alguns snapshots)
curl http://localhost:8088/api/v1/admin/instagram/competitors/{id}/trend?days=7 \
  -H "Authorization: Bearer $TOKEN"

# Esperado: { competitor: {...}, trend: [{date, followers_count, ...}] }

# 6. Criar alerta
curl -X POST http://localhost:8088/api/v1/admin/instagram/competitors/alerts \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"competitor_id": "COMPETITOR_OID", "alert_type": "follower_spike", "threshold": 5.0}'

# Esperado: 201 Created com dados do alerta

# 7. Listar alertas
curl http://localhost:8088/api/v1/admin/instagram/competitors/alerts \
  -H "Authorization: Bearer $TOKEN"

# Esperado: Array de alertas ativos

# 8. Deletar alerta
curl -X DELETE http://localhost:8088/api/v1/admin/instagram/competitors/alerts/{id} \
  -H "Authorization: Bearer $TOKEN"

# Esperado: { "message": "Alert deleted" }

# 9. Remover concorrente
curl -X DELETE http://localhost:8088/api/v1/admin/instagram/competitors/{id} \
  -H "Authorization: Bearer $TOKEN"

# Esperado: { "message": "Competitor removed" }
```

### 12.2 Testes de validacao

| Cenario | Teste | Resultado esperado |
|---------|-------|--------------------|
| Username inexistente | POST com `{"username": "conta_que_nao_existe_xyz"}` | 400 + mensagem de erro da API |
| Conta pessoal (nao business) | POST com username de conta pessoal | 400 + erro `Invalid parameter` |
| Duplicata | POST com username ja adicionado | 409 Conflict |
| Limite excedido | Adicionar 11o concorrente | 400 + "Maximum of 10 competitors allowed" |
| Sem credenciais | POST sem Instagram configurado | 400 + "Instagram not configured" |
| Alert type invalido | POST alerta com `alert_type: "invalid"` | 400 + mensagem de validacao |
| Threshold zero | POST alerta com `threshold: 0` | 400 + "Threshold must be greater than 0" |
| Sem autenticacao | Qualquer endpoint sem token | 401 Unauthorized |
| Role insuficiente | Endpoint com role `user` | 403 Forbidden |

### 12.3 Verificacao do background job

1. Inicie o servidor e verifique o log:
   ```
   Competitor snapshot scheduler started (6h interval)
   ```

2. Para testar sem esperar 6h, altere temporariamente o intervalo para 1 minuto em `main.go`

3. Apos execucao do job, verifique no MongoDB:
   ```javascript
   // Verificar snapshots criados
   db.competitor_snapshots.find().sort({snapshot_at: -1}).limit(5)

   // Verificar se competitor foi atualizado
   db.competitors.find({active: true})

   // Verificar se alertas foram acionados
   db.competitor_alerts.find({last_triggered: {$ne: null}})
   ```

4. Verifique os logs do servidor para confirmar:
   ```
   competitor_snapshot_saved username=concorrente followers=15234 engagement_rate=3.45
   competitor_alert_triggered type=follower_spike username=concorrente growth_pct=6.2 threshold=5.0
   ```

### 12.4 Checklist de implementacao

- [ ] Criar `internal/models/competitor.go` com todos os structs
- [ ] Adicionar collection functions em `internal/database/mongo.go`
- [ ] Adicionar indexes em `EnsureIndexes()`
- [ ] Criar `internal/handlers/instagram_competitors.go` com todos os handlers
- [ ] Adicionar rotas em `internal/router/router.go`
- [ ] Adicionar `competitorSnapshotScheduler()` em `cmd/api/main.go`
- [ ] Criar `CompetitorsPage.jsx` no frontend
- [ ] Adicionar rota no React Router
- [ ] Testar todos os endpoints manualmente
- [ ] Verificar background job nos logs
- [ ] Verificar dados nas collections do MongoDB
