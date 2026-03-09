package handlers

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
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

// ListInstagramLeads returns a paginated, filterable list of leads.
// GET /api/v1/admin/instagram/leads?page=1&limit=20&search=&tag=&source=
func ListInstagramLeads(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}

	filter := bson.M{"org_id": orgID}

	if search := r.URL.Query().Get("search"); search != "" {
		filter["sender_username"] = bson.M{"$regex": search, "$options": "i"}
	}
	if tag := r.URL.Query().Get("tag"); tag != "" {
		filter["tags"] = tag
	}
	if source := r.URL.Query().Get("source"); source != "" {
		filter["sources"] = source
	}

	col := database.InstagramLeads()
	total, err := col.CountDocuments(ctx, filter)
	if err != nil {
		http.Error(w, `{"message":"Erro ao contar leads"}`, http.StatusInternalServerError)
		return
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().
		SetSort(bson.D{{Key: "last_interaction", Value: -1}}).
		SetSkip(skip).
		SetLimit(int64(limit))

	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		http.Error(w, `{"message":"Erro ao buscar leads"}`, http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var leads []models.InstagramLead
	if err := cursor.All(ctx, &leads); err != nil {
		http.Error(w, `{"message":"Erro ao decodificar leads"}`, http.StatusInternalServerError)
		return
	}
	if leads == nil {
		leads = []models.InstagramLead{}
	}

	json.NewEncoder(w).Encode(models.LeadListResponse{
		Leads: leads,
		Total: total,
		Page:  page,
		Limit: limit,
	})
}

// UpdateLeadTags updates the tags for a specific lead.
// PUT /api/v1/admin/instagram/leads/{id}/tags
func UpdateLeadTags(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	idStr := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, `{"message":"ID inválido"}`, http.StatusBadRequest)
		return
	}

	var req models.UpdateLeadTagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"JSON inválido"}`, http.StatusBadRequest)
		return
	}

	tags := req.Tags
	if tags == nil {
		tags = []string{}
	}

	result, err := database.InstagramLeads().UpdateOne(ctx, bson.M{"_id": oid, "org_id": orgID}, bson.M{
		"$set": bson.M{"tags": tags, "updated_at": time.Now()},
	})
	if err != nil {
		http.Error(w, `{"message":"Erro ao atualizar tags"}`, http.StatusInternalServerError)
		return
	}
	if result.MatchedCount == 0 {
		http.Error(w, `{"message":"Lead não encontrado"}`, http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"message": "Tags atualizadas", "tags": tags})
}

// ExportLeadsCSV exports all leads as a CSV file.
// GET /api/v1/admin/instagram/leads/export
func ExportLeadsCSV(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	cursor, err := database.InstagramLeads().Find(ctx, bson.M{"org_id": orgID}, options.Find().SetSort(bson.D{{Key: "last_interaction", Value: -1}}))
	if err != nil {
		http.Error(w, `{"message":"Erro ao buscar leads"}`, http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var leads []models.InstagramLead
	if err := cursor.All(ctx, &leads); err != nil {
		http.Error(w, `{"message":"Erro ao decodificar leads"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=instagram_leads_%s.csv", time.Now().Format("2006-01-02")))

	writer := csv.NewWriter(w)
	writer.Write([]string{"Username", "IG ID", "Interações", "Fontes", "Regras", "Tags", "Primeira Interação", "Última Interação"})

	for _, l := range leads {
		writer.Write([]string{
			l.SenderUsername,
			l.SenderIGID,
			strconv.Itoa(l.InteractionCount),
			joinStrings(l.Sources),
			joinStrings(l.RulesTriggered),
			joinStrings(l.Tags),
			l.FirstInteraction.Format("2006-01-02 15:04"),
			l.LastInteraction.Format("2006-01-02 15:04"),
		})
	}

	writer.Flush()
}

// GetLeadStats returns summary statistics for leads.
// GET /api/v1/admin/instagram/leads/stats
func GetLeadStats(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	col := database.InstagramLeads()
	orgFilter := bson.M{"org_id": orgID}

	total, err := col.CountDocuments(ctx, orgFilter)
	if err != nil {
		http.Error(w, `{"message":"Erro ao contar leads"}`, http.StatusInternalServerError)
		return
	}

	weekAgo := time.Now().AddDate(0, 0, -7)
	newThisWeek, err := col.CountDocuments(ctx, bson.M{
		"org_id":     orgID,
		"created_at": bson.M{"$gte": weekAgo},
	})
	if err != nil {
		http.Error(w, `{"message":"Erro ao contar novos leads"}`, http.StatusInternalServerError)
		return
	}

	// Count by source using aggregation
	bySource := map[string]int64{}
	pipeline := []bson.M{
		{"$match": orgFilter},
		{"$unwind": "$sources"},
		{"$group": bson.M{"_id": "$sources", "count": bson.M{"$sum": 1}}},
	}
	cursor, err := col.Aggregate(ctx, pipeline)
	if err == nil {
		defer cursor.Close(ctx)
		for cursor.Next(ctx) {
			var result struct {
				ID    string `bson:"_id"`
				Count int64  `bson:"count"`
			}
			if cursor.Decode(&result) == nil {
				bySource[result.ID] = result.Count
			}
		}
	}

	json.NewEncoder(w).Encode(models.LeadStatsResponse{
		Total:       total,
		NewThisWeek: newThisWeek,
		BySource:    bySource,
	})
}

// joinStrings joins a slice with commas for CSV export.
func joinStrings(s []string) string {
	result := ""
	for i, v := range s {
		if i > 0 {
			result += ", "
		}
		result += v
	}
	return result
}
