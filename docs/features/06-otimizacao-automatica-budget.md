# 06 - Otimizacao Automatica de Budget

## 1. Visao Geral

A funcionalidade de **Otimizacao Automatica de Budget** analisa as metricas de performance das campanhas Meta Ads ativas e redistribui o orcamento automaticamente entre elas, movendo verba das campanhas com pior desempenho para as com melhor desempenho.

**Proposta de valor:** Maximizar o ROI das campanhas de forma automatica, eliminando a necessidade de ajustes manuais constantes. O sistema opera em ciclos de 30 minutos, avaliando metricas como CTR, CPC, ROAS e CPM para tomar decisoes baseadas em dados.

**Principais capacidades:**
- Configuracao por usuario (metrica de otimizacao, limites min/max, campanhas protegidas)
- Modo dry-run para simular redistribuicoes sem aplicar
- Logs completos de cada execucao com historico de mudancas
- Background job automatico a cada 30 minutos
- Respeito a limites minimos e maximos de budget por campanha

---

## 2. Arquitetura

```
Fluxo de Dados:

[Ticker 30min em main.go]
        |
        v
[ProcessBudgetOptimizations()]
        |
        v
[MongoDB: budget_optimizer_configs] --> busca configs com enabled=true
        |
        v
[Para cada config ativa:]
        |
        +---> [Meta Graph API: GET /act_{id}/campaigns]
        |           busca campanhas ACTIVE com daily_budget
        |
        +---> [Meta Graph API: GET /{campaign_id}/insights]
        |           busca metricas dos ultimos 7 dias por campanha
        |
        +---> [Algoritmo de Ranking]
        |           ordena por metrica escolhida (CTR, CPC, ROAS, CPM)
        |           top 25% = aumento | bottom 25% = reducao
        |           filtra campanhas protegidas
        |           aplica limites min/max
        |
        +---> [Meta Graph API: POST /{campaign_id}]
        |           atualiza daily_budget de cada campanha
        |
        +---> [MongoDB: budget_optimization_logs]
                    salva log com todas as mudancas

[Frontend]
        |
        +---> [GET /config]       --> exibe configuracao atual
        +---> [PUT /config]       --> atualiza configuracao
        +---> [PATCH /config]     --> liga/desliga otimizador
        +---> [POST /dry-run]     --> simula redistribuicao
        +---> [POST /apply]       --> aplica redistribuicao manualmente
        +---> [GET /logs]         --> lista historico de execucoes
```

---

## 3. Models (Go)

Arquivo: `internal/models/budget_optimizer.go`

```go
package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ── Budget Optimizer Config ─────────────────────────────────────────

// BudgetOptimizerConfig stores per-user optimization preferences
type BudgetOptimizerConfig struct {
	ID                   primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID               primitive.ObjectID `json:"user_id" bson:"user_id"`
	Enabled              bool               `json:"enabled" bson:"enabled"`
	Metric               string             `json:"metric" bson:"metric"`                               // "CTR", "CPC", "ROAS", "CPM"
	MinBudget            int64              `json:"min_budget" bson:"min_budget"`                        // minimum daily budget in cents
	MaxBudget            int64              `json:"max_budget" bson:"max_budget"`                        // maximum daily budget in cents
	ReallocationPercent  float64            `json:"reallocation_percent" bson:"reallocation_percent"`    // e.g. 0.20 = 20%
	ProtectedCampaigns   []string           `json:"protected_campaigns" bson:"protected_campaigns"`     // Meta campaign IDs to skip
	Frequency            string             `json:"frequency" bson:"frequency"`                          // "30m", "1h", "6h", "24h"
	CreatedAt            time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt            time.Time          `json:"updated_at" bson:"updated_at"`
}

// ── Budget Optimization Log ─────────────────────────────────────────

// BudgetOptimizationLog records each optimization execution
type BudgetOptimizationLog struct {
	ID                primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID            primitive.ObjectID `json:"user_id" bson:"user_id"`
	ExecutedAt        time.Time          `json:"executed_at" bson:"executed_at"`
	Metric            string             `json:"metric" bson:"metric"`
	TotalBudgetBefore int64              `json:"total_budget_before" bson:"total_budget_before"` // sum in cents
	TotalBudgetAfter  int64              `json:"total_budget_after" bson:"total_budget_after"`   // sum in cents
	Changes           []BudgetChange     `json:"changes" bson:"changes"`
	DryRun            bool               `json:"dry_run" bson:"dry_run"`
	Status            string             `json:"status" bson:"status"` // "success", "partial", "error"
	ErrorMessage      string             `json:"error_message,omitempty" bson:"error_message,omitempty"`
	CreatedAt         time.Time          `json:"created_at" bson:"created_at"`
}

// BudgetChange represents a single campaign budget adjustment
type BudgetChange struct {
	CampaignID   string  `json:"campaign_id" bson:"campaign_id"`
	CampaignName string  `json:"campaign_name" bson:"campaign_name"`
	BudgetBefore int64   `json:"budget_before" bson:"budget_before"` // in cents
	BudgetAfter  int64   `json:"budget_after" bson:"budget_after"`   // in cents
	MetricValue  float64 `json:"metric_value" bson:"metric_value"`
	Rank         int     `json:"rank" bson:"rank"`                   // 1 = best performer
	Action       string  `json:"action" bson:"action"`               // "increase", "decrease", "unchanged"
}

// ── Request types ───────────────────────────────────────────────────

type UpdateBudgetOptimizerConfigRequest struct {
	Metric              *string   `json:"metric,omitempty"`
	MinBudget           *int64    `json:"min_budget,omitempty"`
	MaxBudget           *int64    `json:"max_budget,omitempty"`
	ReallocationPercent *float64  `json:"reallocation_percent,omitempty"`
	ProtectedCampaigns  *[]string `json:"protected_campaigns,omitempty"`
	Frequency           *string   `json:"frequency,omitempty"`
}

type ToggleBudgetOptimizerRequest struct {
	Enabled bool `json:"enabled"`
}
```

---

## 4. Database

### Collections

Arquivo: `internal/database/mongo.go` -- adicionar:

```go
func BudgetOptimizerConfigs() *mongo.Collection {
	return DB.Collection("budget_optimizer_configs")
}

func BudgetOptimizationLogs() *mongo.Collection {
	return DB.Collection("budget_optimization_logs")
}
```

### Indexes

Adicionar em `EnsureIndexes()`:

```go
// budget_optimizer_configs: one config per user
database.BudgetOptimizerConfigs().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys:    bson.D{{Key: "user_id", Value: 1}},
	Options: options.Index().SetUnique(true),
})

// budget_optimization_logs: query by user, sorted by date
database.BudgetOptimizationLogs().Indexes().CreateMany(ctx, []mongo.IndexModel{
	{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "executed_at", Value: -1}}},
	{Keys: bson.D{{Key: "executed_at", Value: -1}}},
})
```

### Estrutura dos Documentos

**budget_optimizer_configs:**
```json
{
  "_id": ObjectId,
  "user_id": ObjectId,
  "enabled": true,
  "metric": "CTR",
  "min_budget": 500,
  "max_budget": 50000,
  "reallocation_percent": 0.20,
  "protected_campaigns": ["123456789"],
  "frequency": "30m",
  "created_at": ISODate,
  "updated_at": ISODate
}
```

**budget_optimization_logs:**
```json
{
  "_id": ObjectId,
  "user_id": ObjectId,
  "executed_at": ISODate,
  "metric": "CTR",
  "total_budget_before": 150000,
  "total_budget_after": 150000,
  "changes": [
    {
      "campaign_id": "123456",
      "campaign_name": "Campanha Verao",
      "budget_before": 5000,
      "budget_after": 6000,
      "metric_value": 3.45,
      "rank": 1,
      "action": "increase"
    }
  ],
  "dry_run": false,
  "status": "success",
  "created_at": ISODate
}
```

---

## 5. Handlers (Go)

Arquivo: `internal/handlers/budget_optimizer.go`

### GetBudgetOptimizerConfig

Retorna a configuracao atual do otimizador para o usuario autenticado. Se nao existir, retorna um objeto padrao com `enabled: false`.

```go
func GetBudgetOptimizerConfig(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var cfg models.BudgetOptimizerConfig
	err := database.BudgetOptimizerConfigs().FindOne(ctx, bson.M{"user_id": userID}).Decode(&cfg)
	if err == mongo.ErrNoDocuments {
		// Return default config
		json.NewEncoder(w).Encode(models.BudgetOptimizerConfig{
			UserID:              userID,
			Enabled:             false,
			Metric:              "CTR",
			MinBudget:           500,    // R$5.00
			MaxBudget:           100000, // R$1000.00
			ReallocationPercent: 0.15,
			ProtectedCampaigns:  []string{},
			Frequency:           "30m",
		})
		return
	}
	if err != nil {
		http.Error(w, "Error fetching config", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(cfg)
}
```

### UpdateBudgetOptimizerConfig

Atualiza a configuracao de otimizacao. Usa upsert para criar se nao existir.

```go
func UpdateBudgetOptimizerConfig(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req models.UpdateBudgetOptimizerConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate metric
	if req.Metric != nil {
		validMetrics := map[string]bool{"CTR": true, "CPC": true, "ROAS": true, "CPM": true}
		if !validMetrics[*req.Metric] {
			http.Error(w, "Invalid metric. Valid: CTR, CPC, ROAS, CPM", http.StatusBadRequest)
			return
		}
	}

	// Validate reallocation percent
	if req.ReallocationPercent != nil {
		if *req.ReallocationPercent <= 0 || *req.ReallocationPercent > 0.50 {
			http.Error(w, "reallocation_percent must be between 0.01 and 0.50", http.StatusBadRequest)
			return
		}
	}

	// Validate frequency
	if req.Frequency != nil {
		validFreqs := map[string]bool{"30m": true, "1h": true, "6h": true, "24h": true}
		if !validFreqs[*req.Frequency] {
			http.Error(w, "Invalid frequency. Valid: 30m, 1h, 6h, 24h", http.StatusBadRequest)
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now()
	update := bson.M{"$set": bson.M{"updated_at": now}}
	setFields := update["$set"].(bson.M)

	if req.Metric != nil {
		setFields["metric"] = *req.Metric
	}
	if req.MinBudget != nil {
		setFields["min_budget"] = *req.MinBudget
	}
	if req.MaxBudget != nil {
		setFields["max_budget"] = *req.MaxBudget
	}
	if req.ReallocationPercent != nil {
		setFields["reallocation_percent"] = *req.ReallocationPercent
	}
	if req.ProtectedCampaigns != nil {
		setFields["protected_campaigns"] = *req.ProtectedCampaigns
	}
	if req.Frequency != nil {
		setFields["frequency"] = *req.Frequency
	}

	// Upsert: create with defaults if not exists
	update["$setOnInsert"] = bson.M{
		"user_id":    userID,
		"enabled":    false,
		"created_at": now,
	}

	opts := options.Update().SetUpsert(true)
	_, err := database.BudgetOptimizerConfigs().UpdateOne(ctx, bson.M{"user_id": userID}, update, opts)
	if err != nil {
		http.Error(w, "Error updating config", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"message": "Config updated"})
}
```

### ToggleBudgetOptimizer

Liga ou desliga o otimizador. Endpoint separado (PATCH) para seguranca.

```go
func ToggleBudgetOptimizer(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req models.ToggleBudgetOptimizerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now()
	opts := options.Update().SetUpsert(true)
	_, err := database.BudgetOptimizerConfigs().UpdateOne(ctx,
		bson.M{"user_id": userID},
		bson.M{
			"$set": bson.M{"enabled": req.Enabled, "updated_at": now},
			"$setOnInsert": bson.M{
				"user_id":              userID,
				"metric":               "CTR",
				"min_budget":           500,
				"max_budget":           100000,
				"reallocation_percent": 0.15,
				"protected_campaigns":  []string{},
				"frequency":            "30m",
				"created_at":           now,
			},
		},
		opts,
	)
	if err != nil {
		http.Error(w, "Error toggling optimizer", http.StatusInternalServerError)
		return
	}

	status := "disabled"
	if req.Enabled {
		status = "enabled"
	}

	slog.Info("budget_optimizer_toggled", "user_id", userID.Hex(), "enabled", req.Enabled)
	json.NewEncoder(w).Encode(map[string]string{"message": "Optimizer " + status})
}
```

### DryRunBudgetOptimization

Executa o algoritmo de otimizacao sem aplicar as mudancas. Retorna a simulacao completa.

```go
func DryRunBudgetOptimization(w http.ResponseWriter, r *http.Request) {
	userID, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var cfg models.BudgetOptimizerConfig
	err := database.BudgetOptimizerConfigs().FindOne(ctx, bson.M{"user_id": userID}).Decode(&cfg)
	if err != nil {
		http.Error(w, "Optimizer not configured. Save config first.", http.StatusBadRequest)
		return
	}

	logEntry, err := executeBudgetOptimization(ctx, userID, creds, &cfg, true)
	if err != nil {
		slog.Error("budget_optimizer_dry_run_error", "error", err, "user_id", userID.Hex())
		http.Error(w, "Error running simulation: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(logEntry)
}
```

### ApplyBudgetOptimization

Executa o algoritmo de otimizacao e aplica as mudancas via Meta API.

```go
func ApplyBudgetOptimization(w http.ResponseWriter, r *http.Request) {
	userID, creds, ok := requireMetaAdsCreds(w, r)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var cfg models.BudgetOptimizerConfig
	err := database.BudgetOptimizerConfigs().FindOne(ctx, bson.M{"user_id": userID}).Decode(&cfg)
	if err != nil {
		http.Error(w, "Optimizer not configured. Save config first.", http.StatusBadRequest)
		return
	}

	logEntry, err := executeBudgetOptimization(ctx, userID, creds, &cfg, false)
	if err != nil {
		slog.Error("budget_optimizer_apply_error", "error", err, "user_id", userID.Hex())
		http.Error(w, "Error applying optimization: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Save log
	database.BudgetOptimizationLogs().InsertOne(ctx, logEntry)

	slog.Info("budget_optimizer_applied",
		"user_id", userID.Hex(),
		"changes", len(logEntry.Changes),
		"status", logEntry.Status,
	)

	json.NewEncoder(w).Encode(logEntry)
}
```

### ListBudgetOptimizationLogs

Lista o historico de execucoes de otimizacao do usuario, ordenado por data decrescente.

```go
func ListBudgetOptimizationLogs(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	limit := int64(50)
	cursor, err := database.BudgetOptimizationLogs().Find(ctx,
		bson.M{"user_id": userID},
		options.Find().SetSort(bson.D{{Key: "executed_at", Value: -1}}).SetLimit(limit),
	)
	if err != nil {
		http.Error(w, "Error fetching logs", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var logs []models.BudgetOptimizationLog
	if err := cursor.All(ctx, &logs); err != nil {
		http.Error(w, "Error decoding logs", http.StatusInternalServerError)
		return
	}

	if logs == nil {
		logs = []models.BudgetOptimizationLog{}
	}

	json.NewEncoder(w).Encode(logs)
}
```

### executeBudgetOptimization (funcao interna)

Logica central do algoritmo. Compartilhada entre dry-run, apply manual e background job.

```go
// executeBudgetOptimization runs the core optimization algorithm.
// If dryRun is true, no changes are applied to Meta API.
func executeBudgetOptimization(
	ctx context.Context,
	userID primitive.ObjectID,
	creds *metaAdsCredentials,
	cfg *models.BudgetOptimizerConfig,
	dryRun bool,
) (*models.BudgetOptimizationLog, error) {

	// 1. Fetch all ACTIVE campaigns with daily_budget
	campaignParams := url.Values{}
	campaignParams.Set("fields", "id,name,daily_budget,status")
	campaignParams.Set("effective_status", `["ACTIVE"]`)
	campaignParams.Set("limit", "200")

	campaignResult, err := metaGraphGet(adAccountPath(creds.AdAccountID)+"/campaigns", creds.Token, campaignParams)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch campaigns: %w", err)
	}

	campaignsRaw, ok := campaignResult["data"].([]interface{})
	if !ok || len(campaignsRaw) == 0 {
		return nil, fmt.Errorf("no active campaigns found")
	}

	// 2. Parse campaigns and filter those with daily_budget
	type campaignData struct {
		ID          string
		Name        string
		DailyBudget int64
	}

	var campaigns []campaignData
	protectedSet := make(map[string]bool)
	for _, pid := range cfg.ProtectedCampaigns {
		protectedSet[pid] = true
	}

	for _, c := range campaignsRaw {
		cm, ok := c.(map[string]interface{})
		if !ok {
			continue
		}

		id, _ := cm["id"].(string)
		name, _ := cm["name"].(string)
		budgetStr, _ := cm["daily_budget"].(string)

		if id == "" || budgetStr == "" {
			continue
		}

		// Skip protected campaigns
		if protectedSet[id] {
			continue
		}

		var budget int64
		fmt.Sscanf(budgetStr, "%d", &budget)
		if budget <= 0 {
			continue
		}

		campaigns = append(campaigns, campaignData{ID: id, Name: name, DailyBudget: budget})
	}

	if len(campaigns) < 2 {
		return nil, fmt.Errorf("need at least 2 non-protected active campaigns with daily_budget")
	}

	// 3. Fetch insights for each campaign (last 7 days)
	now := time.Now()
	timeRange := fmt.Sprintf(`{"since":"%s","until":"%s"}`,
		now.AddDate(0, 0, -7).Format("2006-01-02"),
		now.Format("2006-01-02"),
	)

	type campaignInsight struct {
		campaignData
		MetricValue float64
	}

	var insights []campaignInsight

	for _, camp := range campaigns {
		insightParams := url.Values{}
		insightParams.Set("fields", "ctr,cpc,cpm,spend,actions")
		insightParams.Set("time_range", timeRange)

		result, err := metaGraphGet("/"+camp.ID+"/insights", creds.Token, insightParams)
		if err != nil {
			slog.Warn("budget_optimizer_insight_error", "campaign_id", camp.ID, "error", err)
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

		var metricValue float64
		switch cfg.Metric {
		case "CTR":
			valStr, _ := row["ctr"].(string)
			fmt.Sscanf(valStr, "%f", &metricValue)
		case "CPC":
			valStr, _ := row["cpc"].(string)
			fmt.Sscanf(valStr, "%f", &metricValue)
		case "CPM":
			valStr, _ := row["cpm"].(string)
			fmt.Sscanf(valStr, "%f", &metricValue)
		case "ROAS":
			// ROAS = revenue / spend
			spendStr, _ := row["spend"].(string)
			var spend float64
			fmt.Sscanf(spendStr, "%f", &spend)

			var revenue float64
			if actions, ok := row["actions"].([]interface{}); ok {
				for _, a := range actions {
					action, ok := a.(map[string]interface{})
					if !ok {
						continue
					}
					actionType, _ := action["action_type"].(string)
					if actionType == "offsite_conversion.fb_pixel_purchase" ||
						actionType == "purchase" {
						valStr, _ := action["value"].(string)
						fmt.Sscanf(valStr, "%f", &revenue)
					}
				}
			}
			if spend > 0 {
				metricValue = revenue / spend
			}
		}

		insights = append(insights, campaignInsight{
			campaignData: camp,
			MetricValue:  metricValue,
		})
	}

	if len(insights) < 2 {
		return nil, fmt.Errorf("insufficient insight data: got %d campaigns with metrics", len(insights))
	}

	// 4. Rank campaigns by metric
	// For CTR and ROAS: higher is better
	// For CPC and CPM: lower is better (invert sort)
	sort.Slice(insights, func(i, j int) bool {
		switch cfg.Metric {
		case "CPC", "CPM":
			// Lower is better, but handle zero values
			if insights[i].MetricValue == 0 {
				return false
			}
			if insights[j].MetricValue == 0 {
				return true
			}
			return insights[i].MetricValue < insights[j].MetricValue
		default: // CTR, ROAS
			return insights[i].MetricValue > insights[j].MetricValue
		}
	})

	// 5. Calculate budget changes
	total := len(insights)
	topCount := max(1, total/4)    // top 25%
	bottomCount := max(1, total/4) // bottom 25%

	var totalBudgetBefore int64
	var totalBudgetAfter int64
	var changes []models.BudgetChange

	// Calculate total budget being moved from bottom performers
	var budgetPool int64
	for i := total - bottomCount; i < total; i++ {
		reduction := int64(float64(insights[i].DailyBudget) * cfg.ReallocationPercent)
		budgetPool += reduction
	}

	// Distribute pool evenly among top performers
	increasePerCampaign := budgetPool / int64(topCount)

	for i, insight := range insights {
		rank := i + 1
		budgetBefore := insight.DailyBudget
		budgetAfter := budgetBefore
		action := "unchanged"

		if i < topCount {
			// Top performer: increase budget
			budgetAfter = budgetBefore + increasePerCampaign
			action = "increase"
		} else if i >= total-bottomCount {
			// Bottom performer: decrease budget
			reduction := int64(float64(budgetBefore) * cfg.ReallocationPercent)
			budgetAfter = budgetBefore - reduction
			action = "decrease"
		}

		// Enforce min/max limits
		if budgetAfter < cfg.MinBudget {
			budgetAfter = cfg.MinBudget
		}
		if budgetAfter > cfg.MaxBudget {
			budgetAfter = cfg.MaxBudget
		}

		totalBudgetBefore += budgetBefore
		totalBudgetAfter += budgetAfter

		changes = append(changes, models.BudgetChange{
			CampaignID:   insight.ID,
			CampaignName: insight.Name,
			BudgetBefore: budgetBefore,
			BudgetAfter:  budgetAfter,
			MetricValue:  insight.MetricValue,
			Rank:         rank,
			Action:       action,
		})
	}

	// 6. Apply changes via Meta API (if not dry run)
	status := "success"
	var errorMsg string

	if !dryRun {
		var applyErrors []string
		for _, change := range changes {
			if change.Action == "unchanged" || change.BudgetBefore == change.BudgetAfter {
				continue
			}

			params := url.Values{}
			params.Set("daily_budget", fmt.Sprintf("%d", change.BudgetAfter))

			_, err := metaGraphPost("/"+change.CampaignID, creds.Token, params)
			if err != nil {
				applyErrors = append(applyErrors, fmt.Sprintf("%s: %v", change.CampaignID, err))
				slog.Error("budget_optimizer_apply_campaign_error",
					"campaign_id", change.CampaignID,
					"error", err,
				)
			}
		}

		if len(applyErrors) > 0 {
			if len(applyErrors) == len(changes) {
				status = "error"
			} else {
				status = "partial"
			}
			errorMsg = strings.Join(applyErrors, "; ")
		}
	}

	// 7. Build log entry
	logEntry := &models.BudgetOptimizationLog{
		ID:                primitive.NewObjectID(),
		UserID:            userID,
		ExecutedAt:        time.Now(),
		Metric:            cfg.Metric,
		TotalBudgetBefore: totalBudgetBefore,
		TotalBudgetAfter:  totalBudgetAfter,
		Changes:           changes,
		DryRun:            dryRun,
		Status:            status,
		ErrorMessage:      errorMsg,
		CreatedAt:         time.Now(),
	}

	return logEntry, nil
}
```

### ProcessBudgetOptimizations (Background Job)

Funcao chamada pelo ticker no `main.go`. Percorre todos os configs ativos e executa a otimizacao.

```go
// ProcessBudgetOptimizations is called by the background ticker.
// It iterates all enabled configs and runs optimization for each user.
func ProcessBudgetOptimizations() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cursor, err := database.BudgetOptimizerConfigs().Find(ctx, bson.M{"enabled": true})
	if err != nil {
		slog.Error("budget_optimizer_query_error", "error", err)
		return
	}
	defer cursor.Close(ctx)

	var configs []models.BudgetOptimizerConfig
	if err := cursor.All(ctx, &configs); err != nil {
		slog.Error("budget_optimizer_decode_error", "error", err)
		return
	}

	for _, cfg := range configs {
		// Check frequency: skip if last execution was too recent
		var lastLog models.BudgetOptimizationLog
		err := database.BudgetOptimizationLogs().FindOne(ctx,
			bson.M{"user_id": cfg.UserID, "dry_run": false},
			options.FindOne().SetSort(bson.D{{Key: "executed_at", Value: -1}}),
		).Decode(&lastLog)

		if err == nil {
			var interval time.Duration
			switch cfg.Frequency {
			case "1h":
				interval = 1 * time.Hour
			case "6h":
				interval = 6 * time.Hour
			case "24h":
				interval = 24 * time.Hour
			default: // "30m"
				interval = 30 * time.Minute
			}

			if time.Since(lastLog.ExecutedAt) < interval {
				continue
			}
		}

		// Get credentials for this user
		creds, err := getMetaAdsCredentials(ctx, cfg.UserID)
		if err != nil || creds == nil {
			slog.Warn("budget_optimizer_no_creds", "user_id", cfg.UserID.Hex())
			continue
		}

		logEntry, err := executeBudgetOptimization(ctx, cfg.UserID, creds, &cfg, false)
		if err != nil {
			slog.Error("budget_optimizer_execution_error",
				"user_id", cfg.UserID.Hex(),
				"error", err,
			)
			continue
		}

		// Save log
		database.BudgetOptimizationLogs().InsertOne(ctx, logEntry)

		slog.Info("budget_optimizer_executed",
			"user_id", cfg.UserID.Hex(),
			"changes", len(logEntry.Changes),
			"status", logEntry.Status,
			"metric", cfg.Metric,
		)
	}
}
```

### Imports necessarios no handler

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)
```

---

## 6. Rotas API

Arquivo: `internal/router/router.go` -- adicionar na secao Meta Ads:

```go
// Budget Optimizer
mux.Handle("GET /api/v1/admin/meta-ads/budget-optimizer/config", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetBudgetOptimizerConfig))))
mux.Handle("PUT /api/v1/admin/meta-ads/budget-optimizer/config", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.UpdateBudgetOptimizerConfig))))
mux.Handle("PATCH /api/v1/admin/meta-ads/budget-optimizer/config", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ToggleBudgetOptimizer))))
mux.Handle("POST /api/v1/admin/meta-ads/budget-optimizer/dry-run", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.DryRunBudgetOptimization))))
mux.Handle("POST /api/v1/admin/meta-ads/budget-optimizer/apply", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ApplyBudgetOptimization))))
mux.Handle("GET /api/v1/admin/meta-ads/budget-optimizer/logs", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListBudgetOptimizationLogs))))
```

### Tabela de Endpoints

| Metodo | Endpoint | Descricao |
|--------|----------|-----------|
| `GET` | `/api/v1/admin/meta-ads/budget-optimizer/config` | Retorna configuracao atual |
| `PUT` | `/api/v1/admin/meta-ads/budget-optimizer/config` | Atualiza configuracao |
| `PATCH` | `/api/v1/admin/meta-ads/budget-optimizer/config` | Liga/desliga otimizador |
| `POST` | `/api/v1/admin/meta-ads/budget-optimizer/dry-run` | Simula redistribuicao |
| `POST` | `/api/v1/admin/meta-ads/budget-optimizer/apply` | Aplica redistribuicao |
| `GET` | `/api/v1/admin/meta-ads/budget-optimizer/logs` | Lista historico |

---

## 7. Background Jobs

### Registro no main.go

Arquivo: `cmd/api/main.go` -- adicionar:

```go
// Start Budget Optimizer
go budgetOptimizerScheduler()
```

```go
// budgetOptimizerScheduler runs every 30 minutes and processes budget optimizations.
func budgetOptimizerScheduler() {
	// Wait for server to start
	time.Sleep(45 * time.Second)
	log.Println("Budget optimizer started (30 min interval)")

	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		handlers.ProcessBudgetOptimizations()
	}
}
```

### Algoritmo de Redistribuicao

O algoritmo segue estes passos a cada execucao:

1. **Buscar campanhas ativas:** `GET /act_{id}/campaigns?effective_status=["ACTIVE"]&fields=id,name,daily_budget`
   - Filtra apenas campanhas com `daily_budget` definido
   - Remove campanhas presentes na lista `protected_campaigns`

2. **Buscar metricas:** `GET /{campaign_id}/insights?time_range={"since":"7 dias atras","until":"hoje"}`
   - Para cada campanha, busca CTR, CPC, CPM, spend e actions
   - Calcula ROAS quando a metrica selecionada for ROAS (revenue / spend)

3. **Ranking:** Ordena campanhas pela metrica selecionada
   - CTR e ROAS: maior valor = melhor posicao
   - CPC e CPM: menor valor = melhor posicao

4. **Redistribuicao:**
   - **Top 25%** (melhores): recebem aumento de budget
   - **Bottom 25%** (piores): sofrem reducao de `reallocation_percent` do seu budget
   - **Middle 50%**: permanecem inalteradas
   - O total reduzido do bottom e distribuido igualmente entre o top

5. **Limites:** Todos os novos budgets sao limitados a `[min_budget, max_budget]`

6. **Aplicacao:** `POST /{campaign_id}?daily_budget={novo_valor}` para cada campanha alterada

7. **Log:** Salva registro completo com antes/depois de cada campanha

### Controle de Frequencia

O background job roda a cada 30 minutos, mas a frequencia real de execucao por usuario e controlada pelo campo `frequency` na config:

- `30m`: executa a cada 30 minutos (maximo)
- `1h`: executa a cada hora
- `6h`: executa a cada 6 horas
- `24h`: executa uma vez por dia

O sistema verifica o `executed_at` do ultimo log nao-dry-run para decidir se deve executar novamente.

---

## 8. Frontend

### BudgetOptimizerPage.jsx

Arquivo: `src/pages/admin/BudgetOptimizerPage.jsx`

```jsx
import { useState, useEffect, useCallback } from 'react';
import { useAuth } from '../../context/AuthContext';

const API_BASE = import.meta.env.VITE_API_URL || '';

const METRICS = [
  { value: 'CTR', label: 'CTR (Click-Through Rate)', description: 'Maior CTR = melhor desempenho' },
  { value: 'CPC', label: 'CPC (Cost Per Click)', description: 'Menor CPC = melhor desempenho' },
  { value: 'ROAS', label: 'ROAS (Return on Ad Spend)', description: 'Maior ROAS = melhor desempenho' },
  { value: 'CPM', label: 'CPM (Cost Per Mille)', description: 'Menor CPM = melhor desempenho' },
];

const FREQUENCIES = [
  { value: '30m', label: 'A cada 30 minutos' },
  { value: '1h', label: 'A cada hora' },
  { value: '6h', label: 'A cada 6 horas' },
  { value: '24h', label: 'Uma vez por dia' },
];

export default function BudgetOptimizerPage() {
  const { token } = useAuth();
  const [config, setConfig] = useState(null);
  const [logs, setLogs] = useState([]);
  const [dryRunResult, setDryRunResult] = useState(null);
  const [loading, setLoading] = useState(true);
  const [simulating, setSimulating] = useState(false);
  const [applying, setApplying] = useState(false);
  const [activeTab, setActiveTab] = useState('config'); // 'config' | 'simulation' | 'logs'

  const headers = { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' };

  // Fetch config
  const fetchConfig = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE}/api/v1/admin/meta-ads/budget-optimizer/config`, { headers });
      const data = await res.json();
      setConfig(data);
    } catch (err) {
      console.error('Error fetching config:', err);
    }
  }, [token]);

  // Fetch logs
  const fetchLogs = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE}/api/v1/admin/meta-ads/budget-optimizer/logs`, { headers });
      const data = await res.json();
      setLogs(data);
    } catch (err) {
      console.error('Error fetching logs:', err);
    }
  }, [token]);

  useEffect(() => {
    Promise.all([fetchConfig(), fetchLogs()]).finally(() => setLoading(false));
  }, [fetchConfig, fetchLogs]);

  // Toggle optimizer
  const handleToggle = async () => {
    try {
      await fetch(`${API_BASE}/api/v1/admin/meta-ads/budget-optimizer/config`, {
        method: 'PATCH',
        headers,
        body: JSON.stringify({ enabled: !config.enabled }),
      });
      setConfig(prev => ({ ...prev, enabled: !prev.enabled }));
    } catch (err) {
      console.error('Error toggling optimizer:', err);
    }
  };

  // Update config field
  const handleConfigUpdate = async (field, value) => {
    try {
      await fetch(`${API_BASE}/api/v1/admin/meta-ads/budget-optimizer/config`, {
        method: 'PUT',
        headers,
        body: JSON.stringify({ [field]: value }),
      });
      setConfig(prev => ({ ...prev, [field]: value }));
    } catch (err) {
      console.error('Error updating config:', err);
    }
  };

  // Dry run
  const handleDryRun = async () => {
    setSimulating(true);
    try {
      const res = await fetch(`${API_BASE}/api/v1/admin/meta-ads/budget-optimizer/dry-run`, {
        method: 'POST',
        headers,
      });
      const data = await res.json();
      setDryRunResult(data);
      setActiveTab('simulation');
    } catch (err) {
      console.error('Error running simulation:', err);
    } finally {
      setSimulating(false);
    }
  };

  // Apply
  const handleApply = async () => {
    if (!window.confirm('Tem certeza que deseja aplicar a redistribuicao de budget?')) return;
    setApplying(true);
    try {
      const res = await fetch(`${API_BASE}/api/v1/admin/meta-ads/budget-optimizer/apply`, {
        method: 'POST',
        headers,
      });
      const data = await res.json();
      setDryRunResult(data);
      fetchLogs();
    } catch (err) {
      console.error('Error applying optimization:', err);
    } finally {
      setApplying(false);
    }
  };

  // Format cents to currency
  const formatBudget = (cents) => `R$ ${(cents / 100).toFixed(2)}`;

  if (loading) return <div className="p-6">Carregando...</div>;

  return (
    <div className="p-6 max-w-6xl mx-auto">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">Otimizacao Automatica de Budget</h1>
        <label className="flex items-center gap-3 cursor-pointer">
          <span className="text-sm">{config?.enabled ? 'Ativo' : 'Inativo'}</span>
          <input
            type="checkbox"
            checked={config?.enabled || false}
            onChange={handleToggle}
            className="toggle"
          />
        </label>
      </div>

      {/* Tabs */}
      <div className="flex gap-4 mb-6 border-b">
        {['config', 'simulation', 'logs'].map(tab => (
          <button
            key={tab}
            onClick={() => setActiveTab(tab)}
            className={`pb-2 px-1 ${activeTab === tab ? 'border-b-2 border-blue-500 font-medium' : 'text-gray-500'}`}
          >
            {tab === 'config' ? 'Configuracao' : tab === 'simulation' ? 'Simulacao' : 'Historico'}
          </button>
        ))}
      </div>

      {/* Config Tab */}
      {activeTab === 'config' && config && (
        <div className="space-y-6">
          {/* Metric selector */}
          <div>
            <label className="block text-sm font-medium mb-2">Metrica de Otimizacao</label>
            <select
              value={config.metric}
              onChange={e => handleConfigUpdate('metric', e.target.value)}
              className="w-full p-2 border rounded"
            >
              {METRICS.map(m => (
                <option key={m.value} value={m.value}>{m.label}</option>
              ))}
            </select>
            <p className="text-sm text-gray-500 mt-1">
              {METRICS.find(m => m.value === config.metric)?.description}
            </p>
          </div>

          {/* Min/Max Budget */}
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium mb-2">Budget Minimo (centavos)</label>
              <input
                type="number"
                value={config.min_budget}
                onChange={e => handleConfigUpdate('min_budget', parseInt(e.target.value))}
                className="w-full p-2 border rounded"
              />
              <p className="text-sm text-gray-500 mt-1">{formatBudget(config.min_budget)}</p>
            </div>
            <div>
              <label className="block text-sm font-medium mb-2">Budget Maximo (centavos)</label>
              <input
                type="number"
                value={config.max_budget}
                onChange={e => handleConfigUpdate('max_budget', parseInt(e.target.value))}
                className="w-full p-2 border rounded"
              />
              <p className="text-sm text-gray-500 mt-1">{formatBudget(config.max_budget)}</p>
            </div>
          </div>

          {/* Reallocation Percent */}
          <div>
            <label className="block text-sm font-medium mb-2">
              Percentual de Realocacao: {(config.reallocation_percent * 100).toFixed(0)}%
            </label>
            <input
              type="range"
              min="1"
              max="50"
              value={config.reallocation_percent * 100}
              onChange={e => handleConfigUpdate('reallocation_percent', parseInt(e.target.value) / 100)}
              className="w-full"
            />
            <p className="text-sm text-gray-500 mt-1">
              Percentual do budget removido das campanhas de baixo desempenho
            </p>
          </div>

          {/* Frequency */}
          <div>
            <label className="block text-sm font-medium mb-2">Frequencia de Execucao</label>
            <select
              value={config.frequency}
              onChange={e => handleConfigUpdate('frequency', e.target.value)}
              className="w-full p-2 border rounded"
            >
              {FREQUENCIES.map(f => (
                <option key={f.value} value={f.value}>{f.label}</option>
              ))}
            </select>
          </div>

          {/* Actions */}
          <div className="flex gap-4 pt-4">
            <button
              onClick={handleDryRun}
              disabled={simulating}
              className="px-4 py-2 bg-gray-600 text-white rounded hover:bg-gray-700 disabled:opacity-50"
            >
              {simulating ? 'Simulando...' : 'Simular Redistribuicao'}
            </button>
            <button
              onClick={handleApply}
              disabled={applying}
              className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
            >
              {applying ? 'Aplicando...' : 'Aplicar Agora'}
            </button>
          </div>
        </div>
      )}

      {/* Simulation Tab */}
      {activeTab === 'simulation' && (
        <div>
          {!dryRunResult ? (
            <p className="text-gray-500">
              Nenhuma simulacao executada. Clique em "Simular Redistribuicao" na aba de configuracao.
            </p>
          ) : (
            <div>
              <div className="flex gap-6 mb-4 text-sm">
                <span>Status: <strong>{dryRunResult.status}</strong></span>
                <span>Metrica: <strong>{dryRunResult.metric}</strong></span>
                <span>Budget Total: {formatBudget(dryRunResult.total_budget_before)}</span>
                <span>Dry Run: <strong>{dryRunResult.dry_run ? 'Sim' : 'Nao'}</strong></span>
              </div>

              <table className="w-full border-collapse border text-sm">
                <thead>
                  <tr className="bg-gray-100">
                    <th className="border p-2 text-left">Rank</th>
                    <th className="border p-2 text-left">Campanha</th>
                    <th className="border p-2 text-right">{dryRunResult.metric}</th>
                    <th className="border p-2 text-right">Budget Antes</th>
                    <th className="border p-2 text-right">Budget Depois</th>
                    <th className="border p-2 text-right">Diferenca</th>
                    <th className="border p-2 text-center">Acao</th>
                  </tr>
                </thead>
                <tbody>
                  {dryRunResult.changes?.map((change, i) => {
                    const diff = change.budget_after - change.budget_before;
                    return (
                      <tr key={i} className={
                        change.action === 'increase' ? 'bg-green-50' :
                        change.action === 'decrease' ? 'bg-red-50' : ''
                      }>
                        <td className="border p-2">#{change.rank}</td>
                        <td className="border p-2">{change.campaign_name}</td>
                        <td className="border p-2 text-right">{change.metric_value.toFixed(4)}</td>
                        <td className="border p-2 text-right">{formatBudget(change.budget_before)}</td>
                        <td className="border p-2 text-right">{formatBudget(change.budget_after)}</td>
                        <td className="border p-2 text-right" style={{ color: diff > 0 ? 'green' : diff < 0 ? 'red' : 'inherit' }}>
                          {diff > 0 ? '+' : ''}{formatBudget(diff)}
                        </td>
                        <td className="border p-2 text-center">
                          <span className={`px-2 py-1 rounded text-xs ${
                            change.action === 'increase' ? 'bg-green-100 text-green-800' :
                            change.action === 'decrease' ? 'bg-red-100 text-red-800' :
                            'bg-gray-100 text-gray-800'
                          }`}>
                            {change.action === 'increase' ? 'Aumento' :
                             change.action === 'decrease' ? 'Reducao' : 'Sem alteracao'}
                          </span>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {/* Logs Tab */}
      {activeTab === 'logs' && (
        <div className="space-y-4">
          {logs.length === 0 ? (
            <p className="text-gray-500">Nenhuma execucao registrada ainda.</p>
          ) : (
            logs.map(log => (
              <details key={log.id} className="border rounded p-4">
                <summary className="cursor-pointer flex items-center justify-between">
                  <div className="flex gap-4 items-center">
                    <span className={`px-2 py-1 rounded text-xs ${
                      log.status === 'success' ? 'bg-green-100 text-green-800' :
                      log.status === 'partial' ? 'bg-yellow-100 text-yellow-800' :
                      'bg-red-100 text-red-800'
                    }`}>
                      {log.status}
                    </span>
                    <span className="text-sm">{new Date(log.executed_at).toLocaleString('pt-BR')}</span>
                    <span className="text-sm text-gray-500">{log.metric}</span>
                    {log.dry_run && <span className="text-xs bg-blue-100 text-blue-800 px-2 py-1 rounded">Dry Run</span>}
                  </div>
                  <span className="text-sm">{log.changes?.length || 0} campanhas</span>
                </summary>
                <div className="mt-4">
                  <div className="text-sm mb-2">
                    Budget Total: {formatBudget(log.total_budget_before)} → {formatBudget(log.total_budget_after)}
                  </div>
                  {log.error_message && (
                    <div className="text-sm text-red-600 mb-2">Erros: {log.error_message}</div>
                  )}
                  <table className="w-full border-collapse border text-xs mt-2">
                    <thead>
                      <tr className="bg-gray-50">
                        <th className="border p-1 text-left">Campanha</th>
                        <th className="border p-1 text-right">Antes</th>
                        <th className="border p-1 text-right">Depois</th>
                        <th className="border p-1 text-center">Acao</th>
                      </tr>
                    </thead>
                    <tbody>
                      {log.changes?.map((c, i) => (
                        <tr key={i}>
                          <td className="border p-1">{c.campaign_name}</td>
                          <td className="border p-1 text-right">{formatBudget(c.budget_before)}</td>
                          <td className="border p-1 text-right">{formatBudget(c.budget_after)}</td>
                          <td className="border p-1 text-center">{c.action}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </details>
            ))
          )}
        </div>
      )}
    </div>
  );
}
```

---

## 9. APIs Externas (Meta Graph API v21.0)

### Listar campanhas ativas

```
GET https://graph.facebook.com/v21.0/act_{ad_account_id}/campaigns
  ?fields=id,name,daily_budget,status
  &effective_status=["ACTIVE"]
  &limit=200
  &access_token={token}
```

**Resposta:**
```json
{
  "data": [
    {
      "id": "123456789",
      "name": "Campanha Verao 2025",
      "daily_budget": "5000",
      "status": "ACTIVE"
    }
  ]
}
```

### Buscar insights de campanha

```
GET https://graph.facebook.com/v21.0/{campaign_id}/insights
  ?fields=ctr,cpc,cpm,spend,actions
  &time_range={"since":"2025-01-01","until":"2025-01-07"}
  &access_token={token}
```

**Resposta:**
```json
{
  "data": [
    {
      "ctr": "2.345678",
      "cpc": "0.45",
      "cpm": "10.56",
      "spend": "150.00",
      "actions": [
        {"action_type": "link_click", "value": "334"},
        {"action_type": "purchase", "value": "12"}
      ]
    }
  ]
}
```

### Atualizar budget de campanha

```
POST https://graph.facebook.com/v21.0/{campaign_id}
  ?daily_budget=6000
  &access_token={token}
```

**Resposta:**
```json
{
  "success": true
}
```

### Notas importantes

- `daily_budget` e sempre em **centavos** (e.g., 5000 = R$50.00)
- Campanhas com `lifetime_budget` nao sao compatíveis com alteracao de `daily_budget`
- O campo `effective_status` filtra pelo status real (inclui status herdado de ad account)
- Rate limits da Meta API: ~200 chamadas por hora por ad account. O sistema respeita isso naturalmente pelo numero limitado de campanhas

---

## 10. Codigo Reutilizado

A implementacao reutiliza as seguintes funcoes e padroes ja existentes no projeto:

### Do arquivo `internal/handlers/meta_ads.go`

| Funcao/Padrao | Uso nesta feature |
|---------------|-------------------|
| `requireMetaAdsCreds(w, r)` | Extrair userID e credenciais Meta nos handlers DryRun e Apply |
| `getMetaAdsCredentials(ctx, userID)` | Buscar credenciais no background job (ProcessBudgetOptimizations) |
| `metaGraphGet(endpoint, token, params)` | Buscar campanhas e insights da Meta API |
| `metaGraphPost(endpoint, token, params)` | Atualizar daily_budget das campanhas |
| `adAccountPath(adAccountID)` | Construir path `/act_{id}` para endpoints de conta |
| `metaAdsCredentials` struct | Tipo de retorno das credenciais |

### Do arquivo `internal/database/mongo.go`

| Padrao | Uso |
|--------|-----|
| `database.CollectionName()` | Acesso a collections `budget_optimizer_configs` e `budget_optimization_logs` |

### Do arquivo `internal/middleware/`

| Padrao | Uso |
|--------|-----|
| `middleware.Auth()` | Autenticacao JWT em todas as rotas |
| `middleware.RequireRole("superuser", "admin")` | Restricao de acesso a admins |
| `middleware.GetUserID(r)` | Extrair userID do contexto JWT |

### Do arquivo `cmd/api/main.go`

| Padrao | Uso |
|--------|-----|
| `go funcScheduler()` + `time.NewTicker` | Background job a cada 30 minutos |

---

## 11. Fluxo Completo

### Jornada do usuario

1. **Configuracao inicial:**
   - Usuario acessa `/admin/meta-ads/budget-optimizer`
   - Seleciona a metrica de otimizacao (ex: CTR)
   - Define budget minimo (R$5.00) e maximo (R$500.00)
   - Ajusta percentual de realocacao (ex: 20%)
   - Opcionalmente, adiciona campanhas protegidas
   - Escolhe frequencia de execucao

2. **Simulacao (Dry Run):**
   - Clica em "Simular Redistribuicao"
   - O sistema busca campanhas ativas e suas metricas dos ultimos 7 dias
   - Exibe tabela com ranking, budgets atuais e propostos
   - Nenhuma mudanca e aplicada

3. **Aplicacao manual:**
   - Apos revisar a simulacao, clica em "Aplicar Agora"
   - Confirma no dialog
   - O sistema aplica as mudancas via Meta API
   - Log e salvo com status da execucao

4. **Ativacao automatica:**
   - Liga o toggle "Ativo"
   - O background job passa a executar na frequencia configurada
   - Logs aparecem automaticamente no historico

5. **Monitoramento:**
   - Aba "Historico" mostra todas as execucoes
   - Cada entrada pode ser expandida para ver detalhes por campanha
   - Status indica se a execucao foi completa ou parcial

### Diagrama de sequencia textual

```
Usuario         Frontend          Backend API          Meta Graph API     MongoDB
  |                |                   |                     |                |
  |-- Configura -->|                   |                     |                |
  |                |-- PUT /config --->|                     |                |
  |                |                   |-- upsert ---------> |                |
  |                |<-- 200 OK --------|                     |                |
  |                |                   |                     |                |
  |-- Simular ---->|                   |                     |                |
  |                |-- POST /dry-run ->|                     |                |
  |                |                   |-- GET /campaigns -->|                |
  |                |                   |<-- campaigns -------|                |
  |                |                   |-- GET /insights --->|                |
  |                |                   |<-- metrics ---------|                |
  |                |                   |-- ranking + calc    |                |
  |                |<-- simulation ----|                     |                |
  |                |                   |                     |                |
  |-- Aplicar ---->|                   |                     |                |
  |                |-- POST /apply --->|                     |                |
  |                |                   |-- GET /campaigns -->|                |
  |                |                   |-- GET /insights --->|                |
  |                |                   |-- ranking + calc    |                |
  |                |                   |-- POST /campaign -->| (update budget)|
  |                |                   |-- save log -------->|                |
  |                |<-- result --------|                     |                |
  |                |                   |                     |                |
  |  [Ticker 30m]  |                   |                     |                |
  |                |                   |-- Find enabled ---->|                |
  |                |                   |<-- configs ---------|                |
  |                |                   |-- (same flow) ----->|                |
  |                |                   |-- save log -------->|                |
```

---

## 12. Verificacao

### Testes manuais

#### 1. Configuracao

```bash
# Obter config padrao (deve retornar defaults com enabled=false)
curl -X GET http://localhost:8080/api/v1/admin/meta-ads/budget-optimizer/config \
  -H "Authorization: Bearer $TOKEN"

# Atualizar config
curl -X PUT http://localhost:8080/api/v1/admin/meta-ads/budget-optimizer/config \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "metric": "CTR",
    "min_budget": 500,
    "max_budget": 50000,
    "reallocation_percent": 0.20,
    "frequency": "30m"
  }'

# Ligar otimizador
curl -X PATCH http://localhost:8080/api/v1/admin/meta-ads/budget-optimizer/config \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"enabled": true}'
```

#### 2. Simulacao

```bash
# Dry run - deve retornar simulacao sem aplicar mudancas
curl -X POST http://localhost:8080/api/v1/admin/meta-ads/budget-optimizer/dry-run \
  -H "Authorization: Bearer $TOKEN"

# Verificar que dry_run=true no resultado
# Verificar que total_budget_before == total_budget_after (budget total preservado)
# Verificar que campanhas com rank baixo tem action="increase"
# Verificar que campanhas com rank alto tem action="decrease"
```

#### 3. Aplicacao

```bash
# Aplicar otimizacao
curl -X POST http://localhost:8080/api/v1/admin/meta-ads/budget-optimizer/apply \
  -H "Authorization: Bearer $TOKEN"

# Verificar que dry_run=false no resultado
# Verificar status="success" ou "partial"
# Verificar no Meta Ads Manager que os budgets foram alterados
```

#### 4. Logs

```bash
# Listar historico
curl -X GET http://localhost:8080/api/v1/admin/meta-ads/budget-optimizer/logs \
  -H "Authorization: Bearer $TOKEN"

# Deve conter a execucao anterior com todos os detalhes
```

#### 5. Validacoes

```bash
# Metrica invalida (deve retornar 400)
curl -X PUT http://localhost:8080/api/v1/admin/meta-ads/budget-optimizer/config \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"metric": "INVALID"}'

# Percentual fora do range (deve retornar 400)
curl -X PUT http://localhost:8080/api/v1/admin/meta-ads/budget-optimizer/config \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"reallocation_percent": 0.75}'

# Frequencia invalida (deve retornar 400)
curl -X PUT http://localhost:8080/api/v1/admin/meta-ads/budget-optimizer/config \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"frequency": "5m"}'
```

### Checklist de verificacao

- [ ] `GET /config` retorna defaults quando nao ha config salva
- [ ] `PUT /config` valida metric, reallocation_percent e frequency
- [ ] `PATCH /config` alterna enabled sem alterar outras configuracoes
- [ ] `POST /dry-run` retorna simulacao com `dry_run: true` e nao altera budgets na Meta
- [ ] `POST /apply` altera budgets na Meta API e salva log com `dry_run: false`
- [ ] `GET /logs` retorna historico ordenado por data decrescente
- [ ] Budget total se mantem proximo ao valor original (diferenca apenas por arredondamento)
- [ ] Campanhas protegidas nao sao incluidas na redistribuicao
- [ ] Limites min/max sao respeitados em todos os casos
- [ ] Background job respeita a frequencia configurada por usuario
- [ ] Erros parciais (algumas campanhas falham) registram status "partial"
- [ ] Frontend exibe simulacao com cores (verde para aumento, vermelho para reducao)
- [ ] Roles "superuser" e "admin" tem acesso; outros roles sao bloqueados

### Verificacao do Background Job

```bash
# No log do servidor, apos ativar o otimizador, deve aparecer:
# "Budget optimizer started (30 min interval)"
# E a cada 30 minutos (respeitando frequencia do user):
# "budget_optimizer_executed" com detalhes

# Verificar no MongoDB:
# db.budget_optimization_logs.find({user_id: ObjectId("...")}).sort({executed_at: -1}).limit(5)
```

### Verificacao no MongoDB

```javascript
// Verificar config
db.budget_optimizer_configs.findOne({user_id: ObjectId("USER_ID")})

// Verificar logs
db.budget_optimization_logs.find({user_id: ObjectId("USER_ID")}).sort({executed_at: -1}).limit(10)

// Verificar indexes
db.budget_optimizer_configs.getIndexes()
db.budget_optimization_logs.getIndexes()
```
