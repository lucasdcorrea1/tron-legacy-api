package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
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

	"github.com/tron-legacy/api/internal/crypto"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// facebookCredentials holds resolved Facebook Page API credentials.
type facebookCredentials struct {
	PageID   string
	Token    string // Page access token
	PageName string
	Source   string // "user" or "env"
	OrgID    primitive.ObjectID
}

// getFacebookCredentials resolves credentials from DB per-org config.
func getFacebookCredentials(ctx context.Context, userID, orgID primitive.ObjectID) (*facebookCredentials, error) {
	if orgID == primitive.NilObjectID || !crypto.Available() {
		return nil, nil
	}

	var cfg models.FacebookConfig
	err := database.FacebookConfigs().FindOne(ctx, bson.M{"org_id": orgID}).Decode(&cfg)
	if err == nil {
		token, err := crypto.Decrypt(cfg.PageAccessToken)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt token: %w", err)
		}
		return &facebookCredentials{
			PageID:   cfg.PageID,
			Token:    token,
			PageName: cfg.PageName,
			Source:   "user",
			OrgID:    cfg.OrgID,
		}, nil
	}
	if err != mongo.ErrNoDocuments {
		return nil, fmt.Errorf("db error: %w", err)
	}

	return nil, nil // not configured
}

// requireFacebookCreds is a helper that extracts user/org and resolves FB credentials.
func requireFacebookCreds(w http.ResponseWriter, r *http.Request) (primitive.ObjectID, *facebookCredentials, bool) {
	userID := middleware.GetUserID(r)
	orgID := middleware.GetOrgID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return primitive.NilObjectID, nil, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	creds, err := getFacebookCredentials(ctx, userID, orgID)
	if err != nil {
		slog.Error("facebook_creds_error", "error", err)
		http.Error(w, "Error getting credentials", http.StatusInternalServerError)
		return primitive.NilObjectID, nil, false
	}
	if creds == nil {
		http.Error(w, "Facebook not configured", http.StatusBadRequest)
		return primitive.NilObjectID, nil, false
	}

	return userID, creds, true
}

// GetFacebookConfig returns whether Facebook is configured
// @Summary Obter configuração do Facebook
// @Description Retorna se o Facebook está configurado para a organização atual
// @Tags facebook
// @Produce json
// @Security BearerAuth
// @Success 200 {object} models.FacebookConfigResponse
// @Failure 401 {string} string "Unauthorized"
// @Failure 500 {string} string "Error checking config"
// @Router /admin/facebook/config [get]
func GetFacebookConfig(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	creds, err := getFacebookCredentials(ctx, userID, orgID)
	if err != nil {
		slog.Error("get_facebook_config_error", "error", err)
		http.Error(w, "Error checking config", http.StatusInternalServerError)
		return
	}

	resp := models.FacebookConfigResponse{
		Configured: creds != nil,
		HasToken:   creds != nil,
	}
	if creds != nil {
		resp.PageID = maskAccountID(creds.PageID)
		resp.PageName = creds.PageName
		resp.Source = creds.Source
	}

	json.NewEncoder(w).Encode(resp)
}

// SaveFacebookConfig saves or updates per-org Facebook Page credentials
// @Summary Salvar configuração do Facebook
// @Description Salva ou atualiza credenciais da página do Facebook para a organização
// @Tags facebook
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body models.SaveFacebookConfigRequest true "Dados da configuração"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 500 {string} string "Error saving config"
// @Failure 503 {string} string "Encryption not configured"
// @Router /admin/facebook/config [put]
func SaveFacebookConfig(w http.ResponseWriter, r *http.Request) {
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

	var req models.SaveFacebookConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.PageID == "" || req.PageAccessToken == "" {
		http.Error(w, "page_id and page_access_token are required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Fetch page info from Meta API
	pageName := ""
	pageInfoURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s?fields=name&access_token=%s",
		url.QueryEscape(req.PageID), url.QueryEscape(req.PageAccessToken))
	pageResp, err := http.Get(pageInfoURL)
	if err == nil {
		defer pageResp.Body.Close()
		var pageInfo struct {
			Name string `json:"name"`
		}
		if json.NewDecoder(pageResp.Body).Decode(&pageInfo) == nil {
			pageName = pageInfo.Name
		}
	}

	// Encrypt token
	encToken, err := crypto.Encrypt(req.PageAccessToken)
	if err != nil {
		slog.Error("encrypt_token_error", "error", err)
		http.Error(w, "Error encrypting token", http.StatusInternalServerError)
		return
	}

	now := time.Now()
	filter := bson.M{"org_id": orgID}
	setFields := bson.M{
		"page_id":               req.PageID,
		"page_access_token_enc": encToken,
		"updated_at":            now,
	}
	if pageName != "" {
		setFields["page_name"] = pageName
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

	_, dbErr := database.FacebookConfigs().UpdateOne(ctx, filter, update, opts)
	if dbErr != nil {
		slog.Error("save_facebook_config_error", "error", dbErr)
		http.Error(w, "Error saving config", http.StatusInternalServerError)
		return
	}

	slog.Info("facebook_config_saved",
		"user_id", userID.Hex(),
		"page_id", maskAccountID(req.PageID),
		"page_name", pageName,
	)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":   "Config saved",
		"page_id":   maskAccountID(req.PageID),
		"page_name": pageName,
	})
}

// DeleteFacebookConfig removes per-org Facebook credentials
// @Summary Remover configuração do Facebook
// @Description Remove as credenciais do Facebook da organização
// @Tags facebook
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]string
// @Failure 400 {string} string "Organization context required"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "No config found"
// @Failure 500 {string} string "Error deleting config"
// @Router /admin/facebook/config [delete]
func DeleteFacebookConfig(w http.ResponseWriter, r *http.Request) {
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

	result, err := database.FacebookConfigs().DeleteOne(ctx, bson.M{"org_id": orgID})
	if err != nil {
		slog.Error("delete_facebook_config_error", "error", err)
		http.Error(w, "Error deleting config", http.StatusInternalServerError)
		return
	}

	if result.DeletedCount == 0 {
		http.Error(w, "No config found", http.StatusNotFound)
		return
	}

	slog.Info("facebook_config_deleted",
		"user_id", userID.Hex(),
		"org_id", orgID.Hex(),
	)

	json.NewEncoder(w).Encode(map[string]string{"message": "Config deleted"})
}

// TestFacebookConnection verifies credentials by fetching page info
// @Summary Testar conexão com Facebook
// @Description Verifica as credenciais buscando informações da página
// @Tags facebook
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {string} string "Facebook not configured"
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Failed to reach Facebook API"
// @Router /admin/facebook/test [get]
func TestFacebookConnection(w http.ResponseWriter, r *http.Request) {
	userID, creds, ok := requireFacebookCreds(w, r)
	if !ok {
		return
	}

	params := url.Values{}
	params.Set("fields", "id,name,fan_count,followers_count,category")
	params.Set("access_token", creds.Token)
	apiURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s?%s", creds.PageID, params.Encode())

	resp, err := http.Get(apiURL)
	if err != nil {
		http.Error(w, "Failed to reach Facebook API: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		http.Error(w, "Failed to parse Facebook response", http.StatusBadGateway)
		return
	}

	if errObj, ok := result["error"]; ok {
		slog.Error("facebook_test_failed", "error", errObj)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   errObj,
			"source":  creds.Source,
		})
		return
	}

	slog.Info("facebook_test_success",
		"user_id", userID.Hex(),
		"page_name", result["name"],
	)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":         true,
		"source":          creds.Source,
		"id":              result["id"],
		"name":            result["name"],
		"fan_count":       result["fan_count"],
		"followers_count": result["followers_count"],
		"category":        result["category"],
	})
}

// ListFacebookPages returns all Facebook Pages accessible via the stored token (from InstagramConfig)
// @Summary Listar páginas do Facebook
// @Description Retorna todas as páginas do Facebook acessíveis via token armazenado
// @Tags facebook
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {string} string "Instagram not configured"
// @Failure 401 {string} string "Unauthorized"
// @Failure 500 {string} string "Error decrypting token"
// @Failure 502 {string} string "Error fetching Facebook pages"
// @Router /admin/facebook/pages [get]
func ListFacebookPages(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	orgID := middleware.GetOrgID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get token from InstagramConfig (same OAuth flow)
	var igCfg models.InstagramConfig
	err := database.InstagramConfigs().FindOne(ctx, bson.M{"org_id": orgID}).Decode(&igCfg)
	if err != nil {
		http.Error(w, "Instagram not configured - connect via Meta OAuth first", http.StatusBadRequest)
		return
	}

	token, err := crypto.Decrypt(igCfg.AccessTokenEnc)
	if err != nil {
		http.Error(w, "Error decrypting token", http.StatusInternalServerError)
		return
	}

	pages, err := fetchFacebookPages(token)
	if err != nil {
		slog.Error("list_facebook_pages_error", "error", err)
		http.Error(w, "Error fetching Facebook pages", http.StatusBadGateway)
		return
	}

	if pages == nil {
		pages = []models.FacebookPage{}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"data": pages,
	})
}

// fetchFacebookPages fetches all Facebook Pages the user has access to with their page access tokens.
func fetchFacebookPages(userToken string) ([]models.FacebookPage, error) {
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

	var pages []models.FacebookPage
	for _, p := range result.Data {
		pages = append(pages, models.FacebookPage{
			PageID:      p.ID,
			PageName:    p.Name,
			AccessToken: p.AccessToken,
			Category:    p.Category,
		})
	}

	return pages, nil
}

// GetFacebookFeed fetches recent posts from the Facebook Page
// @Summary Obter feed do Facebook
// @Description Busca posts recentes da página do Facebook
// @Tags facebook
// @Produce json
// @Security BearerAuth
// @Param limit query string false "Número de posts (padrão 12)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {string} string "Facebook not configured"
// @Failure 401 {string} string "Unauthorized"
// @Failure 502 {string} string "Failed to reach Facebook API"
// @Router /admin/facebook/feed [get]
func GetFacebookFeed(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireFacebookCreds(w, r)
	if !ok {
		return
	}

	limit := r.URL.Query().Get("limit")
	if limit == "" {
		limit = "12"
	}

	feedParams := url.Values{}
	feedParams.Set("fields", "id,message,created_time,full_picture,permalink_url,shares,reactions.summary(true),comments.summary(true)")
	feedParams.Set("limit", limit)
	feedParams.Set("access_token", creds.Token)
	apiURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/posts?%s", creds.PageID, feedParams.Encode())

	resp, err := http.Get(apiURL)
	if err != nil {
		http.Error(w, "Failed to reach Facebook API: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		http.Error(w, "Failed to parse Facebook response", http.StatusBadGateway)
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

// CreateFacebookSchedule creates a new scheduled Facebook Page post
// @Summary Criar agendamento de post no Facebook
// @Description Cria um novo post agendado para a página do Facebook
// @Tags facebook
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body models.CreateFacebookScheduleRequest true "Dados do agendamento"
// @Success 201 {object} models.FacebookScheduleResponse
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 500 {string} string "Error creating schedule"
// @Router /admin/facebook/schedules [post]
func CreateFacebookSchedule(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	orgID := middleware.GetOrgID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req models.CreateFacebookScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate media_type
	validTypes := map[string]bool{"text": true, "image": true, "carousel": true, "link": true}
	if !validTypes[req.MediaType] {
		http.Error(w, "media_type must be 'text', 'image', 'carousel', or 'link'", http.StatusBadRequest)
		return
	}

	// Validate based on media_type
	if req.MediaType == "image" && len(req.ImageIDs) == 0 {
		http.Error(w, "At least one image is required for image type", http.StatusBadRequest)
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

	if req.MediaType == "link" && req.LinkURL == "" {
		http.Error(w, "link_url is required for link type", http.StatusBadRequest)
		return
	}

	if req.MediaType == "text" && req.Message == "" {
		http.Error(w, "message is required for text type", http.StatusBadRequest)
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
	schedule := models.FacebookSchedule{
		ID:          primitive.NewObjectID(),
		UserID:      userID,
		OrgID:       orgID,
		Message:     req.Message,
		MediaType:   req.MediaType,
		ImageIDs:    req.ImageIDs,
		LinkURL:     req.LinkURL,
		ScheduledAt: scheduledAt,
		Status:      "scheduled",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err = database.FacebookSchedules().InsertOne(ctx, schedule)
	if err != nil {
		http.Error(w, "Error creating schedule", http.StatusInternalServerError)
		return
	}

	slog.Info("facebook_schedule_created",
		"schedule_id", schedule.ID.Hex(),
		"user_id", userID.Hex(),
		"scheduled_at", scheduledAt.Format(time.RFC3339),
	)

	resp := buildFacebookScheduleResponse(schedule)
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// ListFacebookSchedules lists scheduled posts with pagination and filtering
// @Summary Listar agendamentos do Facebook
// @Description Lista posts agendados com paginação e filtro por status
// @Tags facebook
// @Produce json
// @Security BearerAuth
// @Param page query int false "Página (padrão 1)"
// @Param limit query int false "Itens por página (padrão 10, máx 50)"
// @Param status query string false "Filtrar por status"
// @Success 200 {object} models.FacebookScheduleListResponse
// @Failure 401 {string} string "Unauthorized"
// @Failure 500 {string} string "Error fetching schedules"
// @Router /admin/facebook/schedules [get]
func ListFacebookSchedules(w http.ResponseWriter, r *http.Request) {
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

	total, err := database.FacebookSchedules().CountDocuments(ctx, filter)
	if err != nil {
		http.Error(w, "Error counting schedules", http.StatusInternalServerError)
		return
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().
		SetSort(bson.D{{Key: "scheduled_at", Value: -1}}).
		SetSkip(skip).
		SetLimit(int64(limit))

	cursor, err := database.FacebookSchedules().Find(ctx, filter, opts)
	if err != nil {
		http.Error(w, "Error fetching schedules", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var schedules []models.FacebookSchedule
	if err := cursor.All(ctx, &schedules); err != nil {
		http.Error(w, "Error decoding schedules", http.StatusInternalServerError)
		return
	}

	responses := make([]models.FacebookScheduleResponse, len(schedules))
	for i, s := range schedules {
		responses[i] = buildFacebookScheduleResponse(s)
	}

	json.NewEncoder(w).Encode(models.FacebookScheduleListResponse{
		Schedules: responses,
		Total:     total,
		Page:      page,
		Limit:     limit,
	})
}

// GetFacebookSchedule returns a single schedule by ID
// @Summary Obter agendamento do Facebook
// @Description Retorna um agendamento específico pelo ID
// @Tags facebook
// @Produce json
// @Security BearerAuth
// @Param id path string true "Schedule ID"
// @Success 200 {object} models.FacebookScheduleResponse
// @Failure 400 {string} string "Invalid schedule ID"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Schedule not found"
// @Router /admin/facebook/schedules/{id} [get]
func GetFacebookSchedule(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	idStr := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid schedule ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var schedule models.FacebookSchedule
	err = database.FacebookSchedules().FindOne(ctx, bson.M{"_id": oid, "org_id": orgID}).Decode(&schedule)
	if err != nil {
		http.Error(w, "Schedule not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(buildFacebookScheduleResponse(schedule))
}

// UpdateFacebookSchedule updates a scheduled post (only if status is "scheduled" or "failed")
// @Summary Atualizar agendamento do Facebook
// @Description Atualiza um post agendado (somente se status for "scheduled" ou "failed")
// @Tags facebook
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Schedule ID"
// @Param body body models.UpdateFacebookScheduleRequest true "Dados para atualização"
// @Success 200 {object} models.FacebookScheduleResponse
// @Failure 400 {string} string "Dados inválidos"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Schedule not found"
// @Failure 500 {string} string "Error updating schedule"
// @Router /admin/facebook/schedules/{id} [put]
func UpdateFacebookSchedule(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	idStr := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid schedule ID", http.StatusBadRequest)
		return
	}

	var req models.UpdateFacebookScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var schedule models.FacebookSchedule
	err = database.FacebookSchedules().FindOne(ctx, bson.M{"_id": oid, "org_id": orgID}).Decode(&schedule)
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

	if req.Message != nil {
		setFields["message"] = *req.Message
	}

	if req.MediaType != nil {
		validTypes := map[string]bool{"text": true, "image": true, "carousel": true, "link": true}
		if !validTypes[*req.MediaType] {
			http.Error(w, "media_type must be 'text', 'image', 'carousel', or 'link'", http.StatusBadRequest)
			return
		}
		setFields["media_type"] = *req.MediaType
	}

	if req.ImageIDs != nil {
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

	if req.LinkURL != nil {
		setFields["link_url"] = *req.LinkURL
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

	_, err = database.FacebookSchedules().UpdateOne(ctx, bson.M{"_id": oid, "org_id": orgID}, update)
	if err != nil {
		http.Error(w, "Error updating schedule", http.StatusInternalServerError)
		return
	}

	var updated models.FacebookSchedule
	database.FacebookSchedules().FindOne(ctx, bson.M{"_id": oid, "org_id": orgID}).Decode(&updated)

	slog.Info("facebook_schedule_updated", "schedule_id", oid.Hex())

	json.NewEncoder(w).Encode(buildFacebookScheduleResponse(updated))
}

// DeleteFacebookSchedule deletes a scheduled post
// @Summary Deletar agendamento do Facebook
// @Description Remove um post agendado (não pode deletar posts em publicação)
// @Tags facebook
// @Produce json
// @Security BearerAuth
// @Param id path string true "Schedule ID"
// @Success 200 {object} map[string]string
// @Failure 400 {string} string "Cannot delete a post that is currently publishing"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Schedule not found"
// @Failure 500 {string} string "Error deleting schedule"
// @Router /admin/facebook/schedules/{id} [delete]
func DeleteFacebookSchedule(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	idStr := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid schedule ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var schedule models.FacebookSchedule
	err = database.FacebookSchedules().FindOne(ctx, bson.M{"_id": oid, "org_id": orgID}).Decode(&schedule)
	if err != nil {
		http.Error(w, "Schedule not found", http.StatusNotFound)
		return
	}

	if schedule.Status == "publishing" {
		http.Error(w, "Cannot delete a post that is currently publishing", http.StatusBadRequest)
		return
	}

	_, err = database.FacebookSchedules().DeleteOne(ctx, bson.M{"_id": oid, "org_id": orgID})
	if err != nil {
		http.Error(w, "Error deleting schedule", http.StatusInternalServerError)
		return
	}

	slog.Info("facebook_schedule_deleted", "schedule_id", oid.Hex())

	json.NewEncoder(w).Encode(map[string]string{"message": "Schedule deleted"})
}

// publishToFacebook publishes a scheduled post to Facebook Page via Graph API
func publishToFacebook(schedule models.FacebookSchedule) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	creds, err := getFacebookCredentials(ctx, schedule.UserID, schedule.OrgID)
	if err != nil {
		return "", fmt.Errorf("get credentials: %w", err)
	}
	if creds == nil {
		return "", fmt.Errorf("facebook not configured")
	}

	switch schedule.MediaType {
	case "text":
		return publishFacebookTextPost(creds.PageID, creds.Token, schedule.Message)
	case "link":
		return publishFacebookLinkPost(creds.PageID, creds.Token, schedule.Message, schedule.LinkURL)
	case "image":
		imageURL := getPublicImageURL(schedule.ImageIDs[0])
		return publishFacebookPhotoPost(creds.PageID, creds.Token, schedule.Message, imageURL)
	case "carousel":
		var imageURLs []string
		for _, imgID := range schedule.ImageIDs {
			imageURLs = append(imageURLs, getPublicImageURL(imgID))
		}
		return publishFacebookMultiPhotoPost(creds.PageID, creds.Token, schedule.Message, imageURLs)
	default:
		return "", fmt.Errorf("unsupported media type: %s", schedule.MediaType)
	}
}

// publishFacebookTextPost posts a text-only message to a Facebook Page
func publishFacebookTextPost(pageID, token, message string) (string, error) {
	apiURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/feed", pageID)

	formValues := url.Values{}
	formValues.Set("message", message)
	formValues.Set("access_token", token)

	resp, err := http.PostForm(apiURL, formValues)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		ID    string `json:"id"`
		Error *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Error != nil {
		return "", fmt.Errorf("facebook API error %d: %s", result.Error.Code, result.Error.Message)
	}

	return result.ID, nil
}

// publishFacebookLinkPost posts a link with optional message
func publishFacebookLinkPost(pageID, token, message, linkURL string) (string, error) {
	apiURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/feed", pageID)

	formValues := url.Values{}
	formValues.Set("link", linkURL)
	if message != "" {
		formValues.Set("message", message)
	}
	formValues.Set("access_token", token)

	resp, err := http.PostForm(apiURL, formValues)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		ID    string `json:"id"`
		Error *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Error != nil {
		return "", fmt.Errorf("facebook API error %d: %s", result.Error.Code, result.Error.Message)
	}

	return result.ID, nil
}

// publishFacebookPhotoPost posts a single photo with optional message
func publishFacebookPhotoPost(pageID, token, message, imageURL string) (string, error) {
	apiURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/photos", pageID)

	formValues := url.Values{}
	formValues.Set("url", imageURL)
	if message != "" {
		formValues.Set("message", message)
	}
	formValues.Set("access_token", token)

	resp, err := http.PostForm(apiURL, formValues)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		ID     string `json:"id"`
		PostID string `json:"post_id"`
		Error  *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Error != nil {
		return "", fmt.Errorf("facebook API error %d: %s", result.Error.Code, result.Error.Message)
	}

	// Return post_id if available, otherwise photo id
	if result.PostID != "" {
		return result.PostID, nil
	}
	return result.ID, nil
}

// publishFacebookMultiPhotoPost posts multiple photos as unpublished, then creates a multi-photo post
func publishFacebookMultiPhotoPost(pageID, token, message string, imageURLs []string) (string, error) {
	// Step 1: Upload each photo as unpublished
	var photoIDs []string
	for _, imageURL := range imageURLs {
		photoID, err := uploadUnpublishedPhoto(pageID, token, imageURL)
		if err != nil {
			return "", fmt.Errorf("upload photo failed: %w", err)
		}
		photoIDs = append(photoIDs, photoID)
	}

	// Step 2: Create multi-photo post
	apiURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/feed", pageID)

	// Build multipart form
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if message != "" {
		writer.WriteField("message", message)
	}

	for i, photoID := range photoIDs {
		writer.WriteField(fmt.Sprintf("attached_media[%d]", i), fmt.Sprintf(`{"media_fbid":"%s"}`, photoID))
	}

	writer.WriteField("access_token", token)
	writer.Close()

	req, err := http.NewRequest("POST", apiURL, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		ID    string `json:"id"`
		Error *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Error != nil {
		return "", fmt.Errorf("facebook API error %d: %s", result.Error.Code, result.Error.Message)
	}

	return result.ID, nil
}

// uploadUnpublishedPhoto uploads a photo as unpublished to be used in a multi-photo post
func uploadUnpublishedPhoto(pageID, token, imageURL string) (string, error) {
	apiURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/photos", pageID)

	formValues := url.Values{}
	formValues.Set("url", imageURL)
	formValues.Set("published", "false")
	formValues.Set("access_token", token)

	resp, err := http.PostForm(apiURL, formValues)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		ID    string `json:"id"`
		Error *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Error != nil {
		return "", fmt.Errorf("facebook API error %d: %s", result.Error.Code, result.Error.Message)
	}

	return result.ID, nil
}

// ProcessScheduledFacebookPosts checks for due posts and publishes them
func ProcessScheduledFacebookPosts() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	now := time.Now()
	filter := bson.M{
		"status":       "scheduled",
		"scheduled_at": bson.M{"$lte": now},
	}

	cursor, err := database.FacebookSchedules().Find(ctx, filter)
	if err != nil {
		slog.Error("facebook_scheduler_query_error", "error", err)
		return
	}
	defer cursor.Close(ctx)

	var schedules []models.FacebookSchedule
	if err := cursor.All(ctx, &schedules); err != nil {
		slog.Error("facebook_scheduler_decode_error", "error", err)
		return
	}

	for _, schedule := range schedules {
		// Set status to publishing
		database.FacebookSchedules().UpdateOne(ctx, bson.M{"_id": schedule.ID}, bson.M{
			"$set": bson.M{"status": "publishing", "updated_at": time.Now()},
		})

		postID, err := publishToFacebook(schedule)
		if err != nil {
			slog.Error("facebook_publish_failed",
				"schedule_id", schedule.ID.Hex(),
				"error", err,
			)
			database.FacebookSchedules().UpdateOne(ctx, bson.M{"_id": schedule.ID}, bson.M{
				"$set": bson.M{
					"status":        "failed",
					"error_message": err.Error(),
					"updated_at":    time.Now(),
				},
			})
			continue
		}

		database.FacebookSchedules().UpdateOne(ctx, bson.M{"_id": schedule.ID}, bson.M{
			"$set": bson.M{
				"status":      "published",
				"fb_post_id":  postID,
				"updated_at":  time.Now(),
			},
		})

		middleware.IncFacebookPublished()

		slog.Info("facebook_published",
			"schedule_id", schedule.ID.Hex(),
			"fb_post_id", postID,
		)
	}
}

// UploadFacebookImage uploads an image for Facebook posting (reuses Instagram upload logic)
// @Summary Upload de imagem para Facebook
// @Description Faz upload de uma imagem para uso em posts do Facebook (máx 10MB)
// @Tags facebook
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param image formData file true "Arquivo de imagem"
// @Success 200 {object} map[string]string
// @Failure 400 {string} string "No image provided"
// @Failure 401 {string} string "Unauthorized"
// @Failure 413 {string} string "Image too large"
// @Failure 500 {string} string "Error saving image"
// @Router /admin/facebook/upload [post]
func UploadFacebookImage(w http.ResponseWriter, r *http.Request) {
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
	if !strings.HasPrefix(detectedType, "image/") {
		http.Error(w, "Only image files are allowed", http.StatusBadRequest)
		return
	}

	// Facebook accepts larger images, so we don't resize as aggressively
	// Just store the original (already validated as image)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	imgDoc := models.BlogImage{
		ID:         primitive.NewObjectID(),
		UploaderID: userID,
		OrgID:      orgID,
		Data:       "", // Will be filled with base64
		Size:       len(imgData),
		CreatedAt:  time.Now(),
	}

	// Use the same base64 storage as Instagram for consistency
	imgDoc.Data = encodeBase64(imgData)

	_, err = database.Images().InsertOne(ctx, imgDoc)
	if err != nil {
		http.Error(w, "Error saving image", http.StatusInternalServerError)
		return
	}

	slog.Info("facebook_image_uploaded",
		"image_id", imgDoc.ID.Hex(),
		"user_id", userID.Hex(),
		"size", imgDoc.Size,
	)

	json.NewEncoder(w).Encode(map[string]string{
		"id":  imgDoc.ID.Hex(),
		"url": "/api/v1/blog/images/" + imgDoc.ID.Hex(),
	})
}

// encodeBase64 encodes image data to base64
func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// buildFacebookScheduleResponse creates a response with resolved image URLs
func buildFacebookScheduleResponse(s models.FacebookSchedule) models.FacebookScheduleResponse {
	imageURLs := make([]string, len(s.ImageIDs))
	for i, id := range s.ImageIDs {
		imageURLs[i] = "/api/v1/blog/images/" + id
	}
	return models.FacebookScheduleResponse{
		FacebookSchedule: s,
		ImageURLs:        imageURLs,
	}
}
