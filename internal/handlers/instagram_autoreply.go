package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// CreateAutoReplyRule creates a new auto-reply rule.
// @Summary Criar regra de auto-resposta
// @Description Cria uma nova regra de auto-resposta para Instagram
// @Tags instagram-autoreply
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body models.CreateAutoReplyRuleRequest true "Dados da regra"
// @Success 201 {object} models.AutoReplyRule
// @Failure 400 {string} string "Invalid request"
// @Failure 401 {string} string "Unauthorized"
// @Failure 500 {string} string "Erro ao criar regra"
// @Router /admin/instagram/autoreply/rules [post]
func CreateAutoReplyRule(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	orgID := middleware.GetOrgID(r)

	var req models.CreateAutoReplyRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"Invalid request body"}`, http.StatusBadRequest)
		return
	}

	// Validate
	if req.Name == "" {
		http.Error(w, `{"message":"Nome é obrigatório"}`, http.StatusBadRequest)
		return
	}
	if req.TriggerType != "comment" && req.TriggerType != "dm" && req.TriggerType != "both" {
		http.Error(w, `{"message":"Tipo de trigger inválido (comment, dm, both)"}`, http.StatusBadRequest)
		return
	}
	if len(req.Keywords) == 0 {
		http.Error(w, `{"message":"Pelo menos uma keyword é obrigatória"}`, http.StatusBadRequest)
		return
	}
	if req.ResponseMessage == "" {
		http.Error(w, `{"message":"Mensagem de resposta é obrigatória"}`, http.StatusBadRequest)
		return
	}

	now := time.Now()
	rule := models.AutoReplyRule{
		UserID:          userID,
		OrgID:           orgID,
		Name:            req.Name,
		TriggerType:     req.TriggerType,
		Keywords:        req.Keywords,
		ResponseMessage: req.ResponseMessage,
		CommentReply:    req.CommentReply,
		Active:          true,
		PostIDs:         req.PostIDs,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := database.AutoReplyRules().InsertOne(ctx, rule)
	if err != nil {
		slog.Error("create_autoreply_rule_error", "error", err)
		http.Error(w, `{"message":"Erro ao criar regra"}`, http.StatusInternalServerError)
		return
	}

	rule.ID = result.InsertedID.(primitive.ObjectID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(rule)
}

// ListAutoReplyRules lists all rules for the current org.
// @Summary Listar regras de auto-resposta
// @Description Lista todas as regras de auto-resposta da organização
// @Tags instagram-autoreply
// @Produce json
// @Security BearerAuth
// @Success 200 {object} models.AutoReplyRuleListResponse
// @Failure 401 {string} string "Unauthorized"
// @Failure 500 {string} string "Erro ao listar regras"
// @Router /admin/instagram/autoreply/rules [get]
func ListAutoReplyRules(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{"org_id": orgID}

	total, err := database.AutoReplyRules().CountDocuments(ctx, filter)
	if err != nil {
		slog.Error("list_autoreply_rules_count", "error", err)
		http.Error(w, `{"message":"Erro ao listar regras"}`, http.StatusInternalServerError)
		return
	}

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	cursor, err := database.AutoReplyRules().Find(ctx, filter, opts)
	if err != nil {
		slog.Error("list_autoreply_rules_find", "error", err)
		http.Error(w, `{"message":"Erro ao listar regras"}`, http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var rules []models.AutoReplyRule
	if err := cursor.All(ctx, &rules); err != nil {
		slog.Error("list_autoreply_rules_decode", "error", err)
		http.Error(w, `{"message":"Erro ao listar regras"}`, http.StatusInternalServerError)
		return
	}

	if rules == nil {
		rules = []models.AutoReplyRule{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.AutoReplyRuleListResponse{
		Rules: rules,
		Total: total,
	})
}

// UpdateAutoReplyRule updates an existing rule.
// @Summary Atualizar regra de auto-resposta
// @Description Atualiza uma regra de auto-resposta existente
// @Tags instagram-autoreply
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "ID da regra"
// @Param body body models.UpdateAutoReplyRuleRequest true "Dados para atualizar"
// @Success 200 {object} models.AutoReplyRule
// @Failure 400 {string} string "Invalid request"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Regra não encontrada"
// @Failure 500 {string} string "Erro ao atualizar regra"
// @Router /admin/instagram/autoreply/rules/{id} [put]
func UpdateAutoReplyRule(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	ruleID, err := primitive.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		http.Error(w, `{"message":"ID inválido"}`, http.StatusBadRequest)
		return
	}

	var req models.UpdateAutoReplyRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"Invalid request body"}`, http.StatusBadRequest)
		return
	}

	update := bson.M{"updated_at": time.Now()}
	if req.Name != nil {
		update["name"] = *req.Name
	}
	if req.TriggerType != nil {
		if *req.TriggerType != "comment" && *req.TriggerType != "dm" && *req.TriggerType != "both" {
			http.Error(w, `{"message":"Tipo de trigger inválido"}`, http.StatusBadRequest)
			return
		}
		update["trigger_type"] = *req.TriggerType
	}
	if req.Keywords != nil {
		update["keywords"] = req.Keywords
	}
	if req.ResponseMessage != nil {
		update["response_message"] = *req.ResponseMessage
	}
	if req.CommentReply != nil {
		update["comment_reply"] = *req.CommentReply
	}
	if req.PostIDs != nil {
		update["post_ids"] = req.PostIDs
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{"_id": ruleID, "org_id": orgID}
	result, err := database.AutoReplyRules().UpdateOne(ctx, filter, bson.M{"$set": update})
	if err != nil {
		slog.Error("update_autoreply_rule_error", "error", err)
		http.Error(w, `{"message":"Erro ao atualizar regra"}`, http.StatusInternalServerError)
		return
	}

	if result.MatchedCount == 0 {
		http.Error(w, `{"message":"Regra não encontrada"}`, http.StatusNotFound)
		return
	}

	// Return updated rule
	var updated models.AutoReplyRule
	database.AutoReplyRules().FindOne(ctx, bson.M{"_id": ruleID}).Decode(&updated)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated)
}

// ToggleAutoReplyRule toggles the active state of a rule.
// @Summary Ativar/desativar regra de auto-resposta
// @Description Alterna o estado ativo/inativo de uma regra
// @Tags instagram-autoreply
// @Produce json
// @Security BearerAuth
// @Param id path string true "ID da regra"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {string} string "ID inválido"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Regra não encontrada"
// @Failure 500 {string} string "Erro ao alterar regra"
// @Router /admin/instagram/autoreply/rules/{id} [patch]
func ToggleAutoReplyRule(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	ruleID, err := primitive.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		http.Error(w, `{"message":"ID inválido"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Find current state
	var rule models.AutoReplyRule
	err = database.AutoReplyRules().FindOne(ctx, bson.M{"_id": ruleID, "org_id": orgID}).Decode(&rule)
	if err != nil {
		http.Error(w, `{"message":"Regra não encontrada"}`, http.StatusNotFound)
		return
	}

	// Toggle
	newActive := !rule.Active
	_, err = database.AutoReplyRules().UpdateOne(ctx,
		bson.M{"_id": ruleID},
		bson.M{"$set": bson.M{"active": newActive, "updated_at": time.Now()}},
	)
	if err != nil {
		slog.Error("toggle_autoreply_rule_error", "error", err)
		http.Error(w, `{"message":"Erro ao alterar regra"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     ruleID,
		"active": newActive,
	})
}

// DeleteAutoReplyRule deletes a rule.
// @Summary Remover regra de auto-resposta
// @Description Remove uma regra de auto-resposta
// @Tags instagram-autoreply
// @Produce json
// @Security BearerAuth
// @Param id path string true "ID da regra"
// @Success 200 {object} map[string]string
// @Failure 400 {string} string "ID inválido"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Regra não encontrada"
// @Failure 500 {string} string "Erro ao deletar regra"
// @Router /admin/instagram/autoreply/rules/{id} [delete]
func DeleteAutoReplyRule(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	ruleID, err := primitive.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		http.Error(w, `{"message":"ID inválido"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := database.AutoReplyRules().DeleteOne(ctx, bson.M{"_id": ruleID, "org_id": orgID})
	if err != nil {
		slog.Error("delete_autoreply_rule_error", "error", err)
		http.Error(w, `{"message":"Erro ao deletar regra"}`, http.StatusInternalServerError)
		return
	}

	if result.DeletedCount == 0 {
		http.Error(w, `{"message":"Regra não encontrada"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Regra removida"})
}

// ListAutoReplyLogs lists auto-reply logs with pagination and optional filters.
// @Summary Listar logs de auto-resposta
// @Description Lista logs de auto-resposta com paginação e filtros opcionais
// @Tags instagram-autoreply
// @Produce json
// @Security BearerAuth
// @Param page query int false "Página (padrão 1)"
// @Param limit query int false "Itens por página (padrão 20, máx 100)"
// @Param status query string false "Filtrar por status"
// @Param rule_id query string false "Filtrar por ID da regra"
// @Param trigger_type query string false "Filtrar por tipo de trigger"
// @Success 200 {object} models.AutoReplyLogListResponse
// @Failure 401 {string} string "Unauthorized"
// @Failure 500 {string} string "Erro ao listar logs"
// @Router /admin/instagram/autoreply/logs [get]
func ListAutoReplyLogs(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{"org_id": orgID}

	// Optional filters
	if status := r.URL.Query().Get("status"); status != "" {
		filter["status"] = status
	}
	if ruleID := r.URL.Query().Get("rule_id"); ruleID != "" {
		if oid, err := primitive.ObjectIDFromHex(ruleID); err == nil {
			filter["rule_id"] = oid
		}
	}
	if triggerType := r.URL.Query().Get("trigger_type"); triggerType != "" {
		filter["trigger_type"] = triggerType
	}

	total, err := database.AutoReplyLogs().CountDocuments(ctx, filter)
	if err != nil {
		slog.Error("list_autoreply_logs_count", "error", err)
		http.Error(w, `{"message":"Erro ao listar logs"}`, http.StatusInternalServerError)
		return
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}}).
		SetSkip(skip).
		SetLimit(int64(limit))

	cursor, err := database.AutoReplyLogs().Find(ctx, filter, opts)
	if err != nil {
		slog.Error("list_autoreply_logs_find", "error", err)
		http.Error(w, `{"message":"Erro ao listar logs"}`, http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var logs []models.AutoReplyLog
	if err := cursor.All(ctx, &logs); err != nil {
		slog.Error("list_autoreply_logs_decode", "error", err)
		http.Error(w, `{"message":"Erro ao listar logs"}`, http.StatusInternalServerError)
		return
	}

	if logs == nil {
		logs = []models.AutoReplyLog{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.AutoReplyLogListResponse{
		Logs:  logs,
		Total: total,
		Page:  page,
		Limit: limit,
	})
}
