# 09 - Gestao de Criativos

## 1. Visao Geral

A **Gestao de Criativos** e um hub central para todos os ativos criativos utilizados em campanhas Meta Ads. A feature permite ao usuario:

- **Upload** de imagens, videos e carroseis diretamente na plataforma
- **Organizacao** por tags e categorias personalizadas
- **Rastreamento de performance** automatico vinculando criativos aos anuncios que os utilizam
- **Comparacao A/B** lado a lado entre criativos do mesmo grupo de teste
- **Reutilizacao** facilitada com contagem de uso e status (ativo/arquivado)

O objetivo e centralizar a gestao de ativos criativos, eliminar a necessidade de ferramentas externas e fornecer dados de performance agregados por criativo (nao por anuncio), permitindo decisoes mais rapidas sobre quais criativos performam melhor.

---

## 2. Arquitetura

```
Usuario (Frontend)
    |
    |-- Upload criativo (multipart/form-data)
    |-- CRUD criativos (JSON)
    |-- Consulta performance / A/B test
    |
    v
[React Frontend - CreativesPage.jsx]
    |
    v
[API REST - Go Handlers]
    |
    |-- creatives_handlers.go
    |       |-- ListCreatives (filtros: tag, category, type, status)
    |       |-- CreateCreative
    |       |-- UpdateCreative
    |       |-- DeleteCreative
    |       |-- UploadCreative (multipart -> base64 + thumbnail)
    |       |-- GetCreativePerformance
    |       |-- GetABTestComparison
    |       |-- ListCategories
    |       |-- ListTags
    |       |
    |       v
    |   [MongoDB - collection "creatives"]
    |       |-- Indexes: user_id, tags, category, ab_test_group
    |
    |-- SyncCreativePerformance() [Background Job - 2h]
    |       |-- Para cada criativo com meta_ad_ids
    |       |-- GET /{ad_id}/insights (Meta Graph API v21.0)
    |       |-- Agrega metricas e atualiza creative.performance
    |       v
    |   [Meta Graph API v21.0]
    |
    v
[Funcoes reutilizadas]
    |-- requireMetaAdsCreds()
    |-- metaGraphGet()
    |-- adAccountPath()
    |-- Processamento de imagem (resize, base64) de UploadInstagramImage
```

---

## 3. Models (Go)

**Arquivo:** `internal/models/creative.go`

```go
package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ── Creative ────────────────────────────────────────────────────────

type Creative struct {
	ID            primitive.ObjectID  `json:"id" bson:"_id,omitempty"`
	UserID        primitive.ObjectID  `json:"user_id" bson:"user_id"`
	Name          string              `json:"name" bson:"name"`
	Type          string              `json:"type" bson:"type"`                         // "image", "video", "carousel"
	FileData      string              `json:"file_data,omitempty" bson:"file_data"`     // base64 encoded
	ThumbnailData string              `json:"thumbnail_data,omitempty" bson:"thumbnail_data"` // base64 thumbnail
	Tags          []string            `json:"tags" bson:"tags"`
	Category      string              `json:"category" bson:"category"`
	ABTestGroup   string              `json:"ab_test_group,omitempty" bson:"ab_test_group"`
	MetaAdIDs     []string            `json:"meta_ad_ids" bson:"meta_ad_ids"`           // ads using this creative
	Performance   CreativePerformance `json:"performance" bson:"performance"`
	Status        string              `json:"status" bson:"status"`                     // "active", "archived"
	UsageCount    int                 `json:"usage_count" bson:"usage_count"`
	CreatedAt     time.Time           `json:"created_at" bson:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at" bson:"updated_at"`
}

type CreativePerformance struct {
	Impressions int64   `json:"impressions" bson:"impressions"`
	Clicks      int64   `json:"clicks" bson:"clicks"`
	CTR         float64 `json:"ctr" bson:"ctr"`
	CPC         float64 `json:"cpc" bson:"cpc"`
	Spend       float64 `json:"spend" bson:"spend"`
	Conversions int64   `json:"conversions" bson:"conversions"`
	LastSyncAt  *time.Time `json:"last_sync_at,omitempty" bson:"last_sync_at,omitempty"`
}

// ── Requests ────────────────────────────────────────────────────────

type CreateCreativeRequest struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`                          // "image", "video", "carousel"
	Tags        []string `json:"tags,omitempty"`
	Category    string   `json:"category,omitempty"`
	ABTestGroup string   `json:"ab_test_group,omitempty"`
	MetaAdIDs   []string `json:"meta_ad_ids,omitempty"`
}

type UpdateCreativeRequest struct {
	Name        *string   `json:"name,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`
	Category    *string   `json:"category,omitempty"`
	ABTestGroup *string   `json:"ab_test_group,omitempty"`
	MetaAdIDs   *[]string `json:"meta_ad_ids,omitempty"`
	Status      *string   `json:"status,omitempty"`
}
```

**Notas sobre o modelo:**

- `FileData` armazena o arquivo em base64, seguindo o mesmo padrao de `UploadInstagramImage` que salva `base64Img` no MongoDB
- `ThumbnailData` e uma versao reduzida (300px) para exibicao no grid do frontend
- `MetaAdIDs` e o array de IDs de anuncios Meta que usam este criativo -- e a chave para buscar performance via API
- `Performance` e um subdocumento agregado (soma de todos os anuncios vinculados), atualizado pelo background job
- `Status` controla visibilidade: `active` aparece nas listagens, `archived` fica oculto por padrao

---

## 4. Database

**Collection:** `creatives`

**Funcao de acesso (em `internal/database/mongo.go`):**

```go
func Creatives() *mongo.Collection {
	return DB.Collection("creatives")
}
```

**Indexes (adicionar em `EnsureIndexes()`):**

```go
// creatives: index on user_id for listing
_, err = Creatives().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}},
})
if err != nil {
	return err
}

// creatives: index on tags for tag-based filtering
_, err = Creatives().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "tags", Value: 1}},
})
if err != nil {
	return err
}

// creatives: index on category for category-based filtering
_, err = Creatives().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "category", Value: 1}},
})
if err != nil {
	return err
}

// creatives: index on ab_test_group for A/B test comparison queries
_, err = Creatives().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "ab_test_group", Value: 1}},
})
if err != nil {
	return err
}
```

**Justificativa dos indexes:**

| Index | Uso |
|-------|-----|
| `{user_id, created_at}` | Listagem paginada dos criativos do usuario, ordenada por data |
| `{tags}` | Filtro por tags (multikey index, suporta `$in` e `$all`) |
| `{category}` | Filtro por categoria |
| `{ab_test_group}` | Busca rapida de criativos do mesmo grupo A/B |

---

## 5. Handlers (Go)

**Arquivo:** `internal/handlers/creatives.go`

### 5.1 ListCreatives

Lista criativos do usuario com filtros opcionais via query params.

```go
package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"bytes"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/image/draw"
)

func ListCreatives(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{"user_id": userID}

	// Optional filters
	if tag := r.URL.Query().Get("tag"); tag != "" {
		filter["tags"] = tag
	}
	if category := r.URL.Query().Get("category"); category != "" {
		filter["category"] = category
	}
	if creativeType := r.URL.Query().Get("type"); creativeType != "" {
		filter["type"] = creativeType
	}
	if status := r.URL.Query().Get("status"); status != "" {
		filter["status"] = status
	} else {
		// Default: only active
		filter["status"] = "active"
	}
	if abGroup := r.URL.Query().Get("ab_test_group"); abGroup != "" {
		filter["ab_test_group"] = abGroup
	}

	// Projection: exclude file_data from list (too large), return thumbnail only
	projection := bson.M{"file_data": 0}

	opts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}}).
		SetProjection(projection)

	cursor, err := database.Creatives().Find(ctx, filter, opts)
	if err != nil {
		http.Error(w, "Error fetching creatives", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var creatives []models.Creative
	if err := cursor.All(ctx, &creatives); err != nil {
		http.Error(w, "Error decoding creatives", http.StatusInternalServerError)
		return
	}

	if creatives == nil {
		creatives = []models.Creative{}
	}

	json.NewEncoder(w).Encode(creatives)
}
```

### 5.2 CreateCreative

Cria um registro de criativo (metadados). O upload do arquivo e feito separadamente via `UploadCreative`.

```go
func CreateCreative(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req models.CreateCreativeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	validTypes := map[string]bool{"image": true, "video": true, "carousel": true}
	if !validTypes[req.Type] {
		http.Error(w, "type must be image, video, or carousel", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if req.Tags == nil {
		req.Tags = []string{}
	}
	if req.MetaAdIDs == nil {
		req.MetaAdIDs = []string{}
	}

	now := time.Now()
	creative := models.Creative{
		ID:          primitive.NewObjectID(),
		UserID:      userID,
		Name:        req.Name,
		Type:        req.Type,
		Tags:        req.Tags,
		Category:    req.Category,
		ABTestGroup: req.ABTestGroup,
		MetaAdIDs:   req.MetaAdIDs,
		Performance: models.CreativePerformance{},
		Status:      "active",
		UsageCount:  len(req.MetaAdIDs),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err := database.Creatives().InsertOne(ctx, creative)
	if err != nil {
		http.Error(w, "Error creating creative", http.StatusInternalServerError)
		return
	}

	slog.Info("creative_created", "id", creative.ID.Hex(), "user_id", userID.Hex(), "type", req.Type)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(creative)
}
```

### 5.3 UpdateCreative

```go
func UpdateCreative(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	creativeID := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(creativeID)
	if err != nil {
		http.Error(w, "Invalid creative ID", http.StatusBadRequest)
		return
	}

	var req models.UpdateCreativeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := bson.M{"$set": bson.M{"updated_at": time.Now()}}
	setFields := update["$set"].(bson.M)

	if req.Name != nil {
		setFields["name"] = *req.Name
	}
	if req.Tags != nil {
		setFields["tags"] = *req.Tags
	}
	if req.Category != nil {
		setFields["category"] = *req.Category
	}
	if req.ABTestGroup != nil {
		setFields["ab_test_group"] = *req.ABTestGroup
	}
	if req.MetaAdIDs != nil {
		setFields["meta_ad_ids"] = *req.MetaAdIDs
		setFields["usage_count"] = len(*req.MetaAdIDs)
	}
	if req.Status != nil {
		validStatuses := map[string]bool{"active": true, "archived": true}
		if !validStatuses[*req.Status] {
			http.Error(w, "status must be active or archived", http.StatusBadRequest)
			return
		}
		setFields["status"] = *req.Status
	}

	result, err := database.Creatives().UpdateOne(ctx, bson.M{"_id": oid, "user_id": userID}, update)
	if err != nil {
		http.Error(w, "Error updating creative", http.StatusInternalServerError)
		return
	}

	if result.MatchedCount == 0 {
		http.Error(w, "Creative not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"message": "Creative updated"})
}
```

### 5.4 DeleteCreative

```go
func DeleteCreative(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	creativeID := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(creativeID)
	if err != nil {
		http.Error(w, "Invalid creative ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := database.Creatives().DeleteOne(ctx, bson.M{"_id": oid, "user_id": userID})
	if err != nil {
		http.Error(w, "Error deleting creative", http.StatusInternalServerError)
		return
	}

	if result.DeletedCount == 0 {
		http.Error(w, "Creative not found", http.StatusNotFound)
		return
	}

	slog.Info("creative_deleted", "id", creativeID, "user_id", userID.Hex())
	json.NewEncoder(w).Encode(map[string]string{"message": "Creative deleted"})
}
```

### 5.5 UploadCreative

Recebe o arquivo via multipart, processa (resize + thumbnail), salva em base64 no documento do criativo. Reutiliza o padrao de `UploadInstagramImage` para processamento de imagem.

```go
func UploadCreative(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	creativeID := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(creativeID)
	if err != nil {
		http.Error(w, "Invalid creative ID", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 50<<20) // 50MB limit
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		http.Error(w, "File too large (max 50MB)", http.StatusRequestEntityTooLarge)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "No file provided. Use field name 'file'", http.StatusBadRequest)
		return
	}
	defer file.Close()

	fileData, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Failed to read file", http.StatusBadRequest)
		return
	}

	detectedType := http.DetectContentType(fileData)

	var fileBase64, thumbBase64 string

	switch {
	case detectedType == "image/jpeg" || detectedType == "image/png" || detectedType == "image/webp":
		// Image processing - reuses pattern from UploadInstagramImage
		img, _, err := image.Decode(bytes.NewReader(fileData))
		if err != nil {
			http.Error(w, "Invalid image format", http.StatusBadRequest)
			return
		}

		// Full size (max 1080px width)
		resized := resizeCreativeImage(img, 1080)
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 85}); err != nil {
			http.Error(w, "Failed to process image", http.StatusInternalServerError)
			return
		}
		fileBase64 = base64.StdEncoding.EncodeToString(buf.Bytes())

		// Thumbnail (300px width)
		thumb := resizeCreativeImage(img, 300)
		var thumbBuf bytes.Buffer
		if err := jpeg.Encode(&thumbBuf, thumb, &jpeg.Options{Quality: 70}); err != nil {
			http.Error(w, "Failed to create thumbnail", http.StatusInternalServerError)
			return
		}
		thumbBase64 = base64.StdEncoding.EncodeToString(thumbBuf.Bytes())

	case detectedType == "video/mp4" || detectedType == "video/quicktime":
		// Video: store raw base64, no thumbnail generation server-side
		fileBase64 = base64.StdEncoding.EncodeToString(fileData)
		thumbBase64 = "" // frontend generates video thumbnail via <video> element

	default:
		http.Error(w, "Unsupported file type. Allowed: JPEG, PNG, WebP, MP4", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := database.Creatives().UpdateOne(ctx,
		bson.M{"_id": oid, "user_id": userID},
		bson.M{"$set": bson.M{
			"file_data":      fileBase64,
			"thumbnail_data": thumbBase64,
			"updated_at":     time.Now(),
		}},
	)
	if err != nil {
		http.Error(w, "Error saving file", http.StatusInternalServerError)
		return
	}

	if result.MatchedCount == 0 {
		http.Error(w, "Creative not found", http.StatusNotFound)
		return
	}

	slog.Info("creative_file_uploaded", "id", creativeID, "user_id", userID.Hex(), "content_type", detectedType)

	json.NewEncoder(w).Encode(map[string]string{
		"message":      "File uploaded",
		"content_type": detectedType,
	})
}

// resizeCreativeImage redimensiona a imagem mantendo aspect ratio.
// Reutiliza a mesma logica de resizeInstagramImage.
func resizeCreativeImage(img image.Image, maxWidth int) image.Image {
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	if w <= maxWidth {
		return img
	}

	newW := maxWidth
	newH := int(float64(h) * float64(maxWidth) / float64(w))

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
	return dst
}
```

### 5.6 GetCreativePerformance

Retorna os dados de performance de um criativo especifico.

```go
func GetCreativePerformance(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	creativeID := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(creativeID)
	if err != nil {
		http.Error(w, "Invalid creative ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var creative models.Creative
	err = database.Creatives().FindOne(ctx, bson.M{"_id": oid, "user_id": userID}).Decode(&creative)
	if err != nil {
		http.Error(w, "Creative not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":          creative.ID,
		"name":        creative.Name,
		"type":        creative.Type,
		"meta_ad_ids": creative.MetaAdIDs,
		"performance": creative.Performance,
		"usage_count": creative.UsageCount,
	})
}
```

### 5.7 GetABTestComparison

Retorna todos os criativos de um grupo A/B com suas metricas para comparacao lado a lado.

```go
func GetABTestComparison(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	group := r.PathValue("group")
	if group == "" {
		http.Error(w, "A/B test group is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cursor, err := database.Creatives().Find(ctx,
		bson.M{"user_id": userID, "ab_test_group": group},
		options.Find().
			SetSort(bson.D{{Key: "performance.ctr", Value: -1}}).
			SetProjection(bson.M{"file_data": 0}), // exclude heavy data
	)
	if err != nil {
		http.Error(w, "Error fetching A/B test data", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var creatives []models.Creative
	if err := cursor.All(ctx, &creatives); err != nil {
		http.Error(w, "Error decoding A/B test data", http.StatusInternalServerError)
		return
	}

	if creatives == nil {
		creatives = []models.Creative{}
	}

	// Calculate winner based on CTR
	var winner *models.Creative
	for i := range creatives {
		if winner == nil || creatives[i].Performance.CTR > winner.Performance.CTR {
			winner = &creatives[i]
		}
	}

	var winnerID *primitive.ObjectID
	if winner != nil && winner.Performance.Impressions > 0 {
		winnerID = &winner.ID
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"group":     group,
		"creatives": creatives,
		"winner_id": winnerID,
		"count":     len(creatives),
	})
}
```

### 5.8 ListCategories e ListTags

Retorna valores unicos de categorias e tags para popular filtros no frontend.

```go
func ListCreativeCategories(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	categories, err := database.Creatives().Distinct(ctx, "category", bson.M{
		"user_id":  userID,
		"category": bson.M{"$ne": ""},
	})
	if err != nil {
		http.Error(w, "Error fetching categories", http.StatusInternalServerError)
		return
	}

	if categories == nil {
		categories = []interface{}{}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"categories": categories})
}

func ListCreativeTags(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tags, err := database.Creatives().Distinct(ctx, "tags", bson.M{
		"user_id": userID,
	})
	if err != nil {
		http.Error(w, "Error fetching tags", http.StatusInternalServerError)
		return
	}

	if tags == nil {
		tags = []interface{}{}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"tags": tags})
}
```

### 5.9 SyncCreativePerformance

Funcao chamada pelo background job a cada 2 horas. Para cada criativo com `meta_ad_ids`, busca insights da Meta Graph API e agrega as metricas.

```go
// SyncCreativePerformance is called by the background job goroutine every 2 hours.
// It fetches performance data from Meta Ads API for each creative's associated ads.
func SyncCreativePerformance() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Find all creatives that have linked Meta ad IDs
	cursor, err := database.Creatives().Find(ctx, bson.M{
		"meta_ad_ids": bson.M{"$exists": true, "$ne": []string{}},
		"status":      "active",
	})
	if err != nil {
		slog.Error("sync_creative_performance_query_error", "error", err)
		return
	}
	defer cursor.Close(ctx)

	var creatives []models.Creative
	if err := cursor.All(ctx, &creatives); err != nil {
		slog.Error("sync_creative_performance_decode_error", "error", err)
		return
	}

	slog.Info("sync_creative_performance_start", "count", len(creatives))

	for _, creative := range creatives {
		creds, err := getMetaAdsCredentials(ctx, creative.UserID)
		if err != nil || creds == nil {
			continue
		}

		var totalImpressions, totalClicks, totalConversions int64
		var totalSpend float64

		for _, adID := range creative.MetaAdIDs {
			params := url.Values{}
			params.Set("fields", "impressions,clicks,spend,ctr,cpc,actions")

			// Last 30 days
			now := time.Now()
			params.Set("time_range", fmt.Sprintf(`{"since":"%s","until":"%s"}`,
				now.AddDate(0, 0, -30).Format("2006-01-02"),
				now.Format("2006-01-02"),
			))

			result, err := metaGraphGet("/"+adID+"/insights", creds.Token, params)
			if err != nil {
				slog.Warn("sync_creative_ad_insights_error",
					"creative_id", creative.ID.Hex(),
					"ad_id", adID,
					"error", err,
				)
				continue
			}

			data, ok := result["data"].([]interface{})
			if !ok || len(data) == 0 {
				continue
			}

			row, ok := data[0].(map[string]interface{})
			if !ok {
				continue
			}

			// Parse metrics
			if v, ok := row["impressions"].(string); ok {
				var n int64
				fmt.Sscanf(v, "%d", &n)
				totalImpressions += n
			}
			if v, ok := row["clicks"].(string); ok {
				var n int64
				fmt.Sscanf(v, "%d", &n)
				totalClicks += n
			}
			if v, ok := row["spend"].(string); ok {
				var n float64
				fmt.Sscanf(v, "%f", &n)
				totalSpend += n
			}

			// Parse conversions from actions array
			if actions, ok := row["actions"].([]interface{}); ok {
				for _, a := range actions {
					action, ok := a.(map[string]interface{})
					if !ok {
						continue
					}
					actionType, _ := action["action_type"].(string)
					if actionType == "offsite_conversion" || actionType == "lead" || actionType == "purchase" {
						if v, ok := action["value"].(string); ok {
							var n int64
							fmt.Sscanf(v, "%d", &n)
							totalConversions += n
						}
					}
				}
			}
		}

		// Calculate derived metrics
		var ctr, cpc float64
		if totalImpressions > 0 {
			ctr = float64(totalClicks) / float64(totalImpressions) * 100
		}
		if totalClicks > 0 {
			cpc = totalSpend / float64(totalClicks)
		}

		now := time.Now()
		perf := models.CreativePerformance{
			Impressions: totalImpressions,
			Clicks:      totalClicks,
			CTR:         ctr,
			CPC:         cpc,
			Spend:       totalSpend,
			Conversions: totalConversions,
			LastSyncAt:  &now,
		}

		database.Creatives().UpdateOne(ctx,
			bson.M{"_id": creative.ID},
			bson.M{"$set": bson.M{
				"performance": perf,
				"updated_at":  now,
			}},
		)

		slog.Info("sync_creative_performance_updated",
			"creative_id", creative.ID.Hex(),
			"impressions", totalImpressions,
			"clicks", totalClicks,
			"spend", totalSpend,
		)
	}

	slog.Info("sync_creative_performance_done")
}
```

---

## 6. Rotas API

**Arquivo:** `internal/router/router.go`

Adicionar no bloco de rotas protegidas (superuser + admin):

```go
// Creative management routes
mux.Handle("GET /api/v1/admin/creatives", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListCreatives))))
mux.Handle("POST /api/v1/admin/creatives", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.CreateCreative))))
mux.Handle("PUT /api/v1/admin/creatives/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.UpdateCreative))))
mux.Handle("DELETE /api/v1/admin/creatives/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.DeleteCreative))))
mux.Handle("POST /api/v1/admin/creatives/{id}/upload", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.UploadCreative))))
mux.Handle("GET /api/v1/admin/creatives/{id}/performance", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetCreativePerformance))))
mux.Handle("GET /api/v1/admin/creatives/ab-test/{group}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetABTestComparison))))
mux.Handle("GET /api/v1/admin/creatives/categories", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListCreativeCategories))))
mux.Handle("GET /api/v1/admin/creatives/tags", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListCreativeTags))))
```

**Tabela de referencia:**

| Metodo | Endpoint | Handler | Descricao |
|--------|----------|---------|-----------|
| `GET` | `/api/v1/admin/creatives` | `ListCreatives` | Lista criativos (com filtros por query param) |
| `POST` | `/api/v1/admin/creatives` | `CreateCreative` | Cria um novo criativo (metadados) |
| `PUT` | `/api/v1/admin/creatives/{id}` | `UpdateCreative` | Atualiza metadados do criativo |
| `DELETE` | `/api/v1/admin/creatives/{id}` | `DeleteCreative` | Remove criativo |
| `POST` | `/api/v1/admin/creatives/{id}/upload` | `UploadCreative` | Upload do arquivo (imagem/video) |
| `GET` | `/api/v1/admin/creatives/{id}/performance` | `GetCreativePerformance` | Metricas de performance do criativo |
| `GET` | `/api/v1/admin/creatives/ab-test/{group}` | `GetABTestComparison` | Comparacao A/B do grupo |
| `GET` | `/api/v1/admin/creatives/categories` | `ListCreativeCategories` | Lista categorias existentes |
| `GET` | `/api/v1/admin/creatives/tags` | `ListCreativeTags` | Lista tags existentes |

**Query params para `GET /api/v1/admin/creatives`:**

| Param | Tipo | Descricao |
|-------|------|-----------|
| `tag` | string | Filtra por tag |
| `category` | string | Filtra por categoria |
| `type` | string | `image`, `video`, `carousel` |
| `status` | string | `active` (padrao), `archived` |
| `ab_test_group` | string | Filtra por grupo A/B |

---

## 7. Background Jobs

**Arquivo:** `cmd/api/main.go`

Adicionar a goroutine do sync de performance dos criativos, seguindo o padrao existente de `metaAdsBudgetChecker`:

```go
// Start creative performance sync
go creativePerformanceSync()
```

Funcao no mesmo arquivo:

```go
// creativePerformanceSync runs every 2 hours and syncs creative performance data from Meta Ads API.
func creativePerformanceSync() {
	// Wait for server to start
	time.Sleep(45 * time.Second)
	log.Println("Creative performance sync started (2h interval)")

	ticker := time.NewTicker(2 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		handlers.SyncCreativePerformance()
	}
}
```

**Configuracao do job:**

| Parametro | Valor | Justificativa |
|-----------|-------|---------------|
| Delay inicial | 45s | Evita conflito com outros jobs no startup (keepAlive=10s, instagramScheduler=15s, budgetChecker=30s) |
| Intervalo | 2h | Balanco entre dados atualizados e limites de rate da Meta API |
| Timeout | 120s | Permite processar muitos criativos sem travar |

**Rate limiting da Meta API:** A Meta Graph API tem limite de ~200 chamadas/hora por token. O job busca 1 chamada por anuncio vinculado. Se um usuario tem 50 criativos com 2 anuncios cada = 100 chamadas. Com intervalo de 2h, fica bem dentro do limite.

---

## 8. Frontend

**Arquivo:** `tron-legacy-frontend/src/pages/CreativesPage.jsx`

### 8.1 Estrutura da Pagina

A pagina e dividida em 4 views principais, controladas por estado:

```jsx
import React, { useState, useEffect, useCallback, useRef } from 'react';
import { useAuth } from '../context/AuthContext';

const API_URL = import.meta.env.VITE_API_URL;

export default function CreativesPage() {
  const { token } = useAuth();
  const [view, setView] = useState('grid'); // 'grid' | 'detail' | 'upload' | 'abtest'
  const [creatives, setCreatives] = useState([]);
  const [selectedCreative, setSelectedCreative] = useState(null);
  const [filters, setFilters] = useState({ tag: '', category: '', type: '', status: 'active' });
  const [categories, setCategories] = useState([]);
  const [tags, setTags] = useState([]);
  const [abTestGroup, setAbTestGroup] = useState('');
  const [abTestData, setAbTestData] = useState(null);
  const [showUploadModal, setShowUploadModal] = useState(false);
  const [loading, setLoading] = useState(false);

  const headers = { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' };

  // ── Fetch creatives ──────────────────────────────────────────────
  const fetchCreatives = useCallback(async () => {
    setLoading(true);
    const params = new URLSearchParams();
    Object.entries(filters).forEach(([k, v]) => { if (v) params.set(k, v); });

    const res = await fetch(`${API_URL}/api/v1/admin/creatives?${params}`, { headers });
    if (res.ok) setCreatives(await res.json());
    setLoading(false);
  }, [filters, token]);

  useEffect(() => { fetchCreatives(); }, [fetchCreatives]);

  // ── Fetch categories & tags (for filter dropdowns) ───────────────
  useEffect(() => {
    Promise.all([
      fetch(`${API_URL}/api/v1/admin/creatives/categories`, { headers }).then(r => r.json()),
      fetch(`${API_URL}/api/v1/admin/creatives/tags`, { headers }).then(r => r.json()),
    ]).then(([catData, tagData]) => {
      setCategories(catData.categories || []);
      setTags(tagData.tags || []);
    });
  }, [token]);

  // ── Grid View (main) ────────────────────────────────────────────
  // - Cards com thumbnail, nome, tipo, tags, status badge
  // - Barra de filtros (tag, category, type dropdowns)
  // - Botao "Novo Criativo" abre upload modal
  // - Click no card abre detail view

  // ── Upload Modal ────────────────────────────────────────────────
  // - Drag-and-drop zone (onDragOver, onDrop)
  // - Input file com accept="image/*,video/mp4"
  // - Form: name, type (auto-detected), tags (comma-separated), category, ab_test_group
  // - Fluxo: POST /creatives -> POST /creatives/{id}/upload

  // ── Detail View ─────────────────────────────────────────────────
  // - Imagem/video full size
  // - Metricas de performance (chart)
  // - Lista de anuncios vinculados (meta_ad_ids)
  // - Edicao de tags, categoria, grupo A/B
  // - Botao arquivar/ativar

  // ── A/B Test Comparison View ────────────────────────────────────
  // - Input para selecionar grupo
  // - Cards lado a lado com metricas
  // - Destaque do "winner" baseado em CTR
  // - Metricas comparadas: Impressions, Clicks, CTR, CPC, Spend, Conversions

  return (/* ... */);
}
```

### 8.2 Componente: Upload Modal com Drag-and-Drop

```jsx
function UploadModal({ show, onClose, onCreated, headers }) {
  const [dragActive, setDragActive] = useState(false);
  const [file, setFile] = useState(null);
  const [preview, setPreview] = useState(null);
  const [form, setForm] = useState({ name: '', type: 'image', tags: '', category: '', ab_test_group: '' });
  const [uploading, setUploading] = useState(false);
  const fileInputRef = useRef(null);

  const handleDrag = (e) => {
    e.preventDefault();
    e.stopPropagation();
    setDragActive(e.type === 'dragenter' || e.type === 'dragover');
  };

  const handleDrop = (e) => {
    e.preventDefault();
    setDragActive(false);
    if (e.dataTransfer.files?.[0]) processFile(e.dataTransfer.files[0]);
  };

  const processFile = (f) => {
    setFile(f);
    setForm(prev => ({
      ...prev,
      name: prev.name || f.name.replace(/\.[^.]+$/, ''),
      type: f.type.startsWith('video/') ? 'video' : 'image',
    }));
    if (f.type.startsWith('image/')) {
      const reader = new FileReader();
      reader.onload = (e) => setPreview(e.target.result);
      reader.readAsDataURL(f);
    }
  };

  const handleSubmit = async () => {
    if (!file || !form.name) return;
    setUploading(true);

    // Step 1: Create creative record
    const tags = form.tags.split(',').map(t => t.trim()).filter(Boolean);
    const createRes = await fetch(`${API_URL}/api/v1/admin/creatives`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ ...form, tags }),
    });

    if (!createRes.ok) { setUploading(false); return; }
    const creative = await createRes.json();

    // Step 2: Upload file
    const formData = new FormData();
    formData.append('file', file);
    await fetch(`${API_URL}/api/v1/admin/creatives/${creative.id}/upload`, {
      method: 'POST',
      headers: { Authorization: headers.Authorization },
      body: formData,
    });

    setUploading(false);
    onCreated();
    onClose();
  };

  if (!show) return null;

  return (
    <div className="modal-overlay">
      <div className="modal-content">
        <h2>Novo Criativo</h2>

        <div
          className={`drop-zone ${dragActive ? 'active' : ''}`}
          onDragEnter={handleDrag}
          onDragOver={handleDrag}
          onDragLeave={handleDrag}
          onDrop={handleDrop}
          onClick={() => fileInputRef.current?.click()}
        >
          {preview ? (
            <img src={preview} alt="Preview" style={{ maxHeight: 200 }} />
          ) : (
            <p>Arraste um arquivo ou clique para selecionar</p>
          )}
          <input ref={fileInputRef} type="file" accept="image/*,video/mp4" hidden
            onChange={(e) => e.target.files?.[0] && processFile(e.target.files[0])} />
        </div>

        <input placeholder="Nome" value={form.name}
          onChange={(e) => setForm(p => ({ ...p, name: e.target.value }))} />
        <input placeholder="Tags (separadas por virgula)" value={form.tags}
          onChange={(e) => setForm(p => ({ ...p, tags: e.target.value }))} />
        <input placeholder="Categoria" value={form.category}
          onChange={(e) => setForm(p => ({ ...p, category: e.target.value }))} />
        <input placeholder="Grupo A/B (opcional)" value={form.ab_test_group}
          onChange={(e) => setForm(p => ({ ...p, ab_test_group: e.target.value }))} />

        <button onClick={handleSubmit} disabled={uploading}>
          {uploading ? 'Enviando...' : 'Criar Criativo'}
        </button>
        <button onClick={onClose}>Cancelar</button>
      </div>
    </div>
  );
}
```

### 8.3 Componente: A/B Test Comparison View

```jsx
function ABTestComparison({ group, headers }) {
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!group) return;
    setLoading(true);
    fetch(`${API_URL}/api/v1/admin/creatives/ab-test/${encodeURIComponent(group)}`, { headers })
      .then(r => r.json())
      .then(d => setData(d))
      .finally(() => setLoading(false));
  }, [group]);

  if (!data || loading) return <p>Carregando...</p>;

  return (
    <div>
      <h3>Teste A/B: {data.group} ({data.count} criativos)</h3>
      <div className="ab-test-grid">
        {data.creatives.map(c => (
          <div key={c.id} className={`ab-card ${data.winner_id === c.id ? 'winner' : ''}`}>
            {c.thumbnail_data && (
              <img src={`data:image/jpeg;base64,${c.thumbnail_data}`} alt={c.name} />
            )}
            <h4>{c.name}</h4>
            <div className="metrics">
              <div><strong>Impressoes:</strong> {c.performance.impressions.toLocaleString()}</div>
              <div><strong>Cliques:</strong> {c.performance.clicks.toLocaleString()}</div>
              <div><strong>CTR:</strong> {c.performance.ctr.toFixed(2)}%</div>
              <div><strong>CPC:</strong> R$ {c.performance.cpc.toFixed(2)}</div>
              <div><strong>Gasto:</strong> R$ {c.performance.spend.toFixed(2)}</div>
              <div><strong>Conversoes:</strong> {c.performance.conversions}</div>
            </div>
            {data.winner_id === c.id && <span className="badge-winner">Vencedor</span>}
          </div>
        ))}
      </div>
    </div>
  );
}
```

---

## 9. APIs Externas

### Meta Graph API v21.0 - Insights por Anuncio

Utilizada pelo background job `SyncCreativePerformance` para buscar metricas de cada anuncio vinculado a um criativo.

**Endpoint:**

```
GET https://graph.facebook.com/v21.0/{ad_id}/insights
```

**Parametros:**

| Parametro | Valor | Descricao |
|-----------|-------|-----------|
| `access_token` | Token do usuario | Injetado por `metaGraphGet` |
| `fields` | `impressions,clicks,spend,ctr,cpc,actions` | Metricas solicitadas |
| `time_range` | `{"since":"YYYY-MM-DD","until":"YYYY-MM-DD"}` | Ultimos 30 dias |

**Resposta (exemplo):**

```json
{
  "data": [
    {
      "impressions": "15420",
      "clicks": "312",
      "spend": "45.67",
      "ctr": "2.024",
      "cpc": "0.146",
      "actions": [
        { "action_type": "link_click", "value": "298" },
        { "action_type": "offsite_conversion", "value": "12" },
        { "action_type": "lead", "value": "5" }
      ],
      "date_start": "2026-02-04",
      "date_stop": "2026-03-06"
    }
  ]
}
```

**Tratamento:**
- `impressions`, `clicks`, `spend` sao somados entre todos os `meta_ad_ids` do criativo
- `ctr` e `cpc` sao recalculados a partir dos totais (nao somados da API)
- `conversions` = soma dos `actions` com `action_type` in (`offsite_conversion`, `lead`, `purchase`)
- Erros em anuncios individuais sao logados e pulados (nao interrompem o sync de outros anuncios)

---

## 10. Codigo Reutilizado

### 10.1 `metaGraphGet` (de `meta_ads.go`)

Usado por `SyncCreativePerformance` para buscar insights de cada anuncio. Ja trata injecao de `access_token`, parse de resposta e deteccao de erros da Meta API.

```go
// Uso no sync:
result, err := metaGraphGet("/"+adID+"/insights", creds.Token, params)
```

### 10.2 `requireMetaAdsCreds` (de `meta_ads.go`)

Nao e usado diretamente nos handlers de criativos porque os handlers usam `middleware.GetUserID()` (criativos sao dados locais). Porem, `getMetaAdsCredentials` (funcao interna) e usada no background job para obter o token do usuario.

### 10.3 `adAccountPath` (de `meta_ads.go`)

Nao utilizado diretamente nos handlers de criativos (os insights sao buscados por `ad_id`, nao por `ad_account_id`). Disponivel se necessario para futuras expansoes.

### 10.4 Upload e processamento de imagem (de `instagram.go`)

O handler `UploadCreative` reutiliza o mesmo padrao de:
- `UploadInstagramImage`: multipart parsing, `http.MaxBytesReader`, `http.DetectContentType`, `image.Decode`, resize com `draw.CatmullRom.Scale`, encode JPEG com qualidade 85, armazenamento base64
- A funcao `resizeCreativeImage` e identica a `resizeInstagramImage` (mesma logica de manter aspect ratio)

### 10.5 Padrao de handlers (de todo o projeto)

Todos os handlers seguem o padrao:
- `middleware.GetUserID(r)` para obter usuario autenticado
- `primitive.ObjectIDFromHex()` para validar IDs
- `context.WithTimeout(context.Background(), 5*time.Second)` para operacoes de banco
- `bson.M{"$set": bson.M{...}}` para updates parciais
- `result.MatchedCount == 0` / `result.DeletedCount == 0` para verificar existencia
- Resposta JSON com `json.NewEncoder(w).Encode(...)`

---

## 11. Fluxo Completo

### 11.1 Upload de Novo Criativo

```
1. Usuario acessa /admin/criativos no frontend
2. Clica em "Novo Criativo"
3. Modal de upload abre com zona de drag-and-drop
4. Usuario arrasta imagem para a zona (ou clica para selecionar)
5. Preview da imagem aparece no modal
6. Usuario preenche: nome, tags, categoria, grupo A/B (opcional)
7. Clica "Criar Criativo"
8. Frontend envia POST /api/v1/admin/creatives (metadados)
9. Backend cria documento na collection "creatives" com status "active"
10. Frontend recebe o ID do criativo criado
11. Frontend envia POST /api/v1/admin/creatives/{id}/upload (arquivo)
12. Backend processa a imagem:
    a. Valida tipo (JPEG, PNG, WebP, MP4)
    b. Redimensiona para max 1080px (imagem)
    c. Gera thumbnail de 300px
    d. Converte ambos para base64
    e. Atualiza o documento com file_data e thumbnail_data
13. Modal fecha, grid recarrega mostrando o novo criativo
```

### 11.2 Vinculacao com Anuncios e Sync de Performance

```
1. Usuario edita um criativo existente
2. Adiciona IDs de anuncios Meta no campo "meta_ad_ids"
   (Ex: ["23851234567890123", "23851234567890456"])
3. Frontend envia PUT /api/v1/admin/creatives/{id}
4. Backend atualiza meta_ad_ids e usage_count
5. A cada 2 horas, background job SyncCreativePerformance executa:
   a. Busca todos os criativos com meta_ad_ids nao vazio
   b. Para cada criativo, obtem credenciais Meta do usuario
   c. Para cada ad_id, faz GET /{ad_id}/insights
   d. Soma impressions, clicks, spend, conversions de todos os ads
   e. Calcula CTR e CPC agregados
   f. Atualiza creative.performance no MongoDB
6. Usuario acessa GET /api/v1/admin/creatives/{id}/performance
7. Frontend exibe as metricas atualizadas em graficos
```

### 11.3 Comparacao A/B

```
1. Ao criar criativos, usuario atribui o mesmo "ab_test_group" a 2+ criativos
   (Ex: grupo "landing-page-header-v2")
2. Cada criativo do grupo usa anuncios diferentes com o mesmo targeting/budget
3. Background job sincroniza performance de cada criativo individualmente
4. Usuario acessa a view "Teste A/B" no frontend
5. Seleciona o grupo "landing-page-header-v2"
6. Frontend faz GET /api/v1/admin/creatives/ab-test/landing-page-header-v2
7. Backend retorna todos os criativos do grupo, ordenados por CTR
8. Frontend exibe cards lado a lado com metricas comparativas
9. Criativo com maior CTR (e impressoes > 0) e destacado como "Vencedor"
```

---

## 12. Verificacao

### 12.1 Testes Manuais — Backend

**Pre-requisito:** Servidor rodando, usuario autenticado com role `superuser` ou `admin`.

```bash
# 1. Criar criativo
curl -X POST http://localhost:8088/api/v1/admin/creatives \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Banner Promo Verao",
    "type": "image",
    "tags": ["verao", "promocao", "banner"],
    "category": "banners",
    "ab_test_group": "promo-verao-2026"
  }'
# Esperado: 201 Created, retorna objeto com ID

# 2. Upload do arquivo
curl -X POST http://localhost:8088/api/v1/admin/creatives/{id}/upload \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@banner-verao.jpg"
# Esperado: 200 OK, {"message": "File uploaded", "content_type": "image/jpeg"}

# 3. Listar criativos
curl http://localhost:8088/api/v1/admin/creatives \
  -H "Authorization: Bearer $TOKEN"
# Esperado: Array com criativos (thumbnail_data presente, file_data ausente)

# 4. Listar com filtros
curl "http://localhost:8088/api/v1/admin/creatives?tag=verao&type=image" \
  -H "Authorization: Bearer $TOKEN"
# Esperado: Apenas criativos com tag "verao" e tipo "image"

# 5. Atualizar criativo (vincular anuncios)
curl -X PUT http://localhost:8088/api/v1/admin/creatives/{id} \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"meta_ad_ids": ["23851234567890123"]}'
# Esperado: 200 OK, {"message": "Creative updated"}

# 6. Buscar performance
curl http://localhost:8088/api/v1/admin/creatives/{id}/performance \
  -H "Authorization: Bearer $TOKEN"
# Esperado: Objeto com performance (zerado ate o primeiro sync)

# 7. Criar segundo criativo do mesmo grupo A/B
curl -X POST http://localhost:8088/api/v1/admin/creatives \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Banner Promo Verao V2",
    "type": "image",
    "tags": ["verao", "promocao"],
    "category": "banners",
    "ab_test_group": "promo-verao-2026"
  }'

# 8. Comparacao A/B
curl http://localhost:8088/api/v1/admin/creatives/ab-test/promo-verao-2026 \
  -H "Authorization: Bearer $TOKEN"
# Esperado: {"group": "promo-verao-2026", "creatives": [...], "winner_id": null, "count": 2}

# 9. Listar categorias
curl http://localhost:8088/api/v1/admin/creatives/categories \
  -H "Authorization: Bearer $TOKEN"
# Esperado: {"categories": ["banners"]}

# 10. Listar tags
curl http://localhost:8088/api/v1/admin/creatives/tags \
  -H "Authorization: Bearer $TOKEN"
# Esperado: {"tags": ["verao", "promocao", "banner"]}

# 11. Arquivar criativo
curl -X PUT http://localhost:8088/api/v1/admin/creatives/{id} \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"status": "archived"}'
# Esperado: 200 OK

# 12. Verificar que arquivado nao aparece na listagem padrao
curl http://localhost:8088/api/v1/admin/creatives \
  -H "Authorization: Bearer $TOKEN"
# Esperado: Array sem o criativo arquivado

# 13. Listar arquivados explicitamente
curl "http://localhost:8088/api/v1/admin/creatives?status=archived" \
  -H "Authorization: Bearer $TOKEN"
# Esperado: Array com o criativo arquivado

# 14. Deletar criativo
curl -X DELETE http://localhost:8088/api/v1/admin/creatives/{id} \
  -H "Authorization: Bearer $TOKEN"
# Esperado: 200 OK, {"message": "Creative deleted"}
```

### 12.2 Testes do Background Job

```bash
# Verificar no log do servidor apos 2h (ou reduzir intervalo para teste):
# Esperado:
#   "Creative performance sync started (2h interval)"
#   "sync_creative_performance_start" count=N
#   "sync_creative_performance_updated" creative_id=... impressions=... clicks=... spend=...
#   "sync_creative_performance_done"

# Para testar manualmente, chamar a funcao diretamente em um test file:
# handlers.SyncCreativePerformance()
```

### 12.3 Verificacao do MongoDB

```javascript
// Conectar no MongoDB shell

// Verificar collection e indexes
db.creatives.getIndexes()
// Esperado: indexes em user_id, tags, category, ab_test_group

// Verificar documento
db.creatives.findOne({ name: "Banner Promo Verao" })
// Esperado: documento completo com file_data, thumbnail_data, performance, etc.

// Verificar que file_data foi populado apos upload
db.creatives.findOne({ name: "Banner Promo Verao" }, { file_data: { $exists: true } })

// Verificar performance apos sync
db.creatives.findOne({ "performance.last_sync_at": { $exists: true } })
```

### 12.4 Testes do Frontend

1. **Grid View:** Acessar `/admin/criativos`, verificar que os criativos aparecem como cards com thumbnail
2. **Filtros:** Selecionar filtros por tag, categoria e tipo, verificar que a listagem atualiza
3. **Upload:** Clicar "Novo Criativo", arrastar imagem para a zona, preencher campos e submeter
4. **Detail View:** Clicar em um card, verificar que exibe imagem full size e metricas
5. **A/B Test:** Acessar aba "Teste A/B", selecionar grupo, verificar cards lado a lado com metricas
6. **Arquivar:** No detail view, clicar "Arquivar", verificar que some do grid

### 12.5 Checklist de Seguranca

- [ ] Todos os endpoints exigem autenticacao (`middleware.Auth`)
- [ ] Todos os endpoints exigem role `superuser` ou `admin` (`middleware.RequireRole`)
- [ ] Filtro `user_id` em todas as queries (isolamento multi-tenant)
- [ ] `http.MaxBytesReader` com limite de 50MB no upload
- [ ] Validacao de `Content-Type` do arquivo (JPEG, PNG, WebP, MP4 apenas)
- [ ] IDs validados com `primitive.ObjectIDFromHex` antes de uso
- [ ] `file_data` excluido da listagem (projection) para evitar payload enorme
- [ ] Background job usa `context.WithTimeout` para evitar execucao infinita
