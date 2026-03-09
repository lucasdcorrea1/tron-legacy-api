# 08 - Relatorios Automatizados

## 1. Visao Geral

O modulo de Relatorios Automatizados permite que usuarios admin/superuser configurem a geracao periodica de relatorios contendo metricas do Instagram (engajamento, posts, melhores horarios) e do Meta Ads (campanhas, gastos, impressoes, CTR). Os relatorios sao gerados em formato PDF ou CSV e enviados automaticamente por email atraves da API do Resend.

Funcionalidades principais:

- **Agendamento flexivel**: frequencias diaria, semanal ou mensal
- **Secoes configuraveis**: o usuario escolhe quais blocos de dados incluir (ig_engagement, ig_posts, ads_campaigns, ads_insights)
- **Geracao sob demanda**: alem do agendamento, o usuario pode gerar um relatorio imediatamente
- **Envio por email**: PDF/CSV anexado via Resend API
- **Historico**: todos os relatorios gerados ficam armazenados por 90 dias com possibilidade de download

---

## 2. Arquitetura

```
Usuario (Frontend)
    |
    |  POST /reports/schedules    (cria agendamento)
    |  POST /reports/generate     (gera sob demanda)
    |  GET  /reports              (lista historico)
    |  GET  /reports/{id}/download (baixa arquivo)
    v
[ API Go - Handlers ]
    |
    |--- getInstagramCredentials() --> IG Graph API v21.0
    |       |-- fetchFollowersCount()
    |       |-- fetchMediaWithInsights()
    |       |-- computeBestHours()
    |
    |--- getMetaAdsCredentials() --> Meta Ads API v21.0
    |       |-- metaGraphGet(adAccountPath + "/campaigns")
    |       |-- metaGraphGet(adAccountPath + "/insights")
    |
    |--- gofpdf (PDF) / encoding/csv (CSV)
    |
    |--- Resend API (envio do email com anexo)
    |
    v
[ MongoDB ]
    |-- report_schedules   (agendamentos)
    |-- generated_reports  (relatorios gerados, TTL 90 dias)
```

Fluxo do background job (a cada 30 minutos):

```
main.go -> go reportScheduleChecker()
    |
    time.NewTicker(30 * time.Minute)
    |
    Para cada schedule com NextRunAt <= now && Active == true:
        1. Busca credenciais IG + Meta Ads do usuario
        2. Coleta dados das APIs externas
        3. Monta ReportSummary
        4. Gera arquivo PDF ou CSV
        5. Salva GeneratedReport no MongoDB
        6. Envia email com anexo via Resend
        7. Atualiza NextRunAt e LastRunAt do schedule
```

---

## 3. Models (Go)

Arquivo: `internal/models/report.go`

```go
package models

import (
    "time"

    "go.mongodb.org/mongo-driver/bson/primitive"
)

// ── Report Schedule ─────────────────────────────────────────────────

// ReportSchedule defines a recurring report generation configuration.
type ReportSchedule struct {
    ID         primitive.ObjectID `json:"id" bson:"_id,omitempty"`
    UserID     primitive.ObjectID `json:"user_id" bson:"user_id"`
    Name       string             `json:"name" bson:"name"`
    Frequency  string             `json:"frequency" bson:"frequency"`   // "daily", "weekly", "monthly"
    Format     string             `json:"format" bson:"format"`         // "pdf", "csv"
    Recipients []string           `json:"recipients" bson:"recipients"` // email addresses
    Sections   []string           `json:"sections" bson:"sections"`     // "ig_engagement", "ig_posts", "ads_campaigns", "ads_insights"
    DateRange  int                `json:"date_range" bson:"date_range"` // days to look back (7, 14, 30, 90)
    Active     bool               `json:"active" bson:"active"`
    NextRunAt  time.Time          `json:"next_run_at" bson:"next_run_at"`
    LastRunAt  *time.Time         `json:"last_run_at,omitempty" bson:"last_run_at,omitempty"`
    CreatedAt  time.Time          `json:"created_at" bson:"created_at"`
    UpdatedAt  time.Time          `json:"updated_at" bson:"updated_at"`
}

// CreateReportScheduleRequest is the payload for creating a new schedule.
type CreateReportScheduleRequest struct {
    Name       string   `json:"name"`
    Frequency  string   `json:"frequency"`
    Format     string   `json:"format"`
    Recipients []string `json:"recipients"`
    Sections   []string `json:"sections"`
    DateRange  int      `json:"date_range"`
}

// UpdateReportScheduleRequest is the payload for updating a schedule.
type UpdateReportScheduleRequest struct {
    Name       *string   `json:"name,omitempty"`
    Frequency  *string   `json:"frequency,omitempty"`
    Format     *string   `json:"format,omitempty"`
    Recipients *[]string `json:"recipients,omitempty"`
    Sections   *[]string `json:"sections,omitempty"`
    DateRange  *int      `json:"date_range,omitempty"`
    Active     *bool     `json:"active,omitempty"`
}

// ── Generated Report ────────────────────────────────────────────────

// GeneratedReport stores a generated report file and its metadata.
type GeneratedReport struct {
    ID          primitive.ObjectID `json:"id" bson:"_id,omitempty"`
    UserID      primitive.ObjectID `json:"user_id" bson:"user_id"`
    ScheduleID  primitive.ObjectID `json:"schedule_id,omitempty" bson:"schedule_id,omitempty"` // empty for on-demand
    Format      string             `json:"format" bson:"format"`                               // "pdf", "csv"
    FileName    string             `json:"file_name" bson:"file_name"`
    FileData    []byte             `json:"-" bson:"file_data"`                                 // never sent in JSON list
    FileSize    int64              `json:"file_size" bson:"file_size"`
    Period      ReportPeriod       `json:"period" bson:"period"`
    Sections    []string           `json:"sections" bson:"sections"`
    GeneratedAt time.Time          `json:"generated_at" bson:"generated_at"`
    SentTo      []string           `json:"sent_to" bson:"sent_to"`
    Status      string             `json:"status" bson:"status"` // "generated", "sent", "failed"
}

// ReportPeriod defines the date range of the report data.
type ReportPeriod struct {
    Start string `json:"start" bson:"start"` // "2025-01-01"
    End   string `json:"end" bson:"end"`     // "2025-01-31"
}

// ── Report Summary (in-memory, used during generation) ──────────────

// ReportSummary aggregates all metrics for report generation.
type ReportSummary struct {
    // Instagram
    TotalPosts        int                `json:"total_posts"`
    AvgEngagement     float64            `json:"avg_engagement"`
    FollowersCount    int64              `json:"followers_count"`
    TopPosts          []PostEngagement   `json:"top_posts"`
    BestPostingHours  []PostingHourStat  `json:"best_posting_hours"`

    // Meta Ads
    TotalSpend        float64            `json:"total_spend"`
    TotalImpressions  int64              `json:"total_impressions"`
    TotalClicks       int64              `json:"total_clicks"`
    TotalReach        int64              `json:"total_reach"`
    AvgCTR            float64            `json:"avg_ctr"`
    AvgCPC            float64            `json:"avg_cpc"`
    AvgCPM            float64            `json:"avg_cpm"`
    TopCampaign       string             `json:"top_campaign"`
    Campaigns         []CampaignSummary  `json:"campaigns"`
}

// CampaignSummary holds summary data for a single Meta Ads campaign.
type CampaignSummary struct {
    ID          string  `json:"id"`
    Name        string  `json:"name"`
    Status      string  `json:"status"`
    Spend       float64 `json:"spend"`
    Impressions int64   `json:"impressions"`
    Clicks      int64   `json:"clicks"`
    CTR         float64 `json:"ctr"`
    CPC         float64 `json:"cpc"`
}

// GenerateReportRequest is the payload for on-demand report generation.
type GenerateReportRequest struct {
    Format     string   `json:"format"`      // "pdf", "csv"
    Sections   []string `json:"sections"`
    DateRange  int      `json:"date_range"`  // days
    Recipients []string `json:"recipients"`  // optional, send email if provided
}
```

---

## 4. Database

### 4.1 Collections

Adicionar em `internal/database/mongo.go`:

```go
func ReportSchedules() *mongo.Collection {
    return DB.Collection("report_schedules")
}

func GeneratedReports() *mongo.Collection {
    return DB.Collection("generated_reports")
}
```

### 4.2 Indexes

Adicionar em `EnsureIndexes()` dentro de `internal/database/mongo.go`:

```go
// report_schedules: index on {user_id, active} for scheduled job queries
_, err = ReportSchedules().Indexes().CreateOne(ctx, mongo.IndexModel{
    Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "active", Value: 1}},
})
if err != nil {
    return err
}

// report_schedules: index on {active, next_run_at} for background job
_, err = ReportSchedules().Indexes().CreateOne(ctx, mongo.IndexModel{
    Keys: bson.D{{Key: "active", Value: 1}, {Key: "next_run_at", Value: 1}},
})
if err != nil {
    return err
}

// generated_reports: index on {user_id, generated_at} for listing
_, err = GeneratedReports().Indexes().CreateOne(ctx, mongo.IndexModel{
    Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "generated_at", Value: -1}},
})
if err != nil {
    return err
}

// generated_reports: TTL index — auto-delete after 90 days
_, err = GeneratedReports().Indexes().CreateOne(ctx, mongo.IndexModel{
    Keys:    bson.D{{Key: "generated_at", Value: 1}},
    Options: options.Index().SetExpireAfterSeconds(90 * 24 * 3600),
})
if err != nil {
    return err
}
```

### 4.3 Estrutura dos documentos

**report_schedules**:
```json
{
  "_id": ObjectId("..."),
  "user_id": ObjectId("..."),
  "name": "Relatorio Semanal IG + Ads",
  "frequency": "weekly",
  "format": "pdf",
  "recipients": ["cliente@email.com", "gerente@email.com"],
  "sections": ["ig_engagement", "ig_posts", "ads_campaigns", "ads_insights"],
  "date_range": 7,
  "active": true,
  "next_run_at": ISODate("2025-06-02T08:00:00Z"),
  "last_run_at": ISODate("2025-05-26T08:00:12Z"),
  "created_at": ISODate("2025-05-20T10:00:00Z"),
  "updated_at": ISODate("2025-05-26T08:00:12Z")
}
```

**generated_reports**:
```json
{
  "_id": ObjectId("..."),
  "user_id": ObjectId("..."),
  "schedule_id": ObjectId("..."),
  "format": "pdf",
  "file_name": "relatorio-semanal-2025-05-26.pdf",
  "file_data": BinData(0, "..."),
  "file_size": 45230,
  "period": { "start": "2025-05-19", "end": "2025-05-26" },
  "sections": ["ig_engagement", "ig_posts", "ads_campaigns", "ads_insights"],
  "generated_at": ISODate("2025-05-26T08:00:12Z"),
  "sent_to": ["cliente@email.com"],
  "status": "sent"
}
```

---

## 5. Handlers (Go)

Arquivo: `internal/handlers/reports.go`

### 5.1 CRUD de Agendamentos

```go
package handlers

import (
    "bytes"
    "context"
    "encoding/base64"
    "encoding/csv"
    "encoding/json"
    "fmt"
    "io"
    "log/slog"
    "net/http"
    "net/url"
    "sort"
    "time"

    "github.com/jung-kurt/gofpdf"
    "github.com/tron-legacy/api/internal/config"
    "github.com/tron-legacy/api/internal/database"
    "github.com/tron-legacy/api/internal/middleware"
    "github.com/tron-legacy/api/internal/models"
    "go.mongodb.org/mongo-driver/bson"
    "go.mongodb.org/mongo-driver/bson/primitive"
    "go.mongodb.org/mongo-driver/mongo/options"
)

// ══════════════════════════════════════════════════════════════════════
// SCHEDULE CRUD
// ══════════════════════════════════════════════════════════════════════

// ListReportSchedules returns all schedules for the authenticated user.
// GET /api/v1/admin/reports/schedules
func ListReportSchedules(w http.ResponseWriter, r *http.Request) {
    userID := middleware.GetUserID(r)
    if userID == primitive.NilObjectID {
        http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
        return
    }

    ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
    defer cancel()

    cursor, err := database.ReportSchedules().Find(ctx,
        bson.M{"user_id": userID},
        options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}),
    )
    if err != nil {
        http.Error(w, `{"message":"Erro ao buscar agendamentos"}`, http.StatusInternalServerError)
        return
    }
    defer cursor.Close(ctx)

    var schedules []models.ReportSchedule
    if err := cursor.All(ctx, &schedules); err != nil {
        http.Error(w, `{"message":"Erro ao decodificar agendamentos"}`, http.StatusInternalServerError)
        return
    }

    if schedules == nil {
        schedules = []models.ReportSchedule{}
    }

    json.NewEncoder(w).Encode(map[string]interface{}{
        "schedules": schedules,
        "total":     len(schedules),
    })
}

// CreateReportSchedule creates a new report schedule.
// POST /api/v1/admin/reports/schedules
func CreateReportSchedule(w http.ResponseWriter, r *http.Request) {
    userID := middleware.GetUserID(r)
    if userID == primitive.NilObjectID {
        http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
        return
    }

    var req models.CreateReportScheduleRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, `{"message":"Dados invalidos"}`, http.StatusBadRequest)
        return
    }

    // Validation
    if req.Name == "" {
        http.Error(w, `{"message":"Nome e obrigatorio"}`, http.StatusBadRequest)
        return
    }
    if req.Frequency != "daily" && req.Frequency != "weekly" && req.Frequency != "monthly" {
        http.Error(w, `{"message":"Frequencia deve ser daily, weekly ou monthly"}`, http.StatusBadRequest)
        return
    }
    if req.Format != "pdf" && req.Format != "csv" {
        http.Error(w, `{"message":"Formato deve ser pdf ou csv"}`, http.StatusBadRequest)
        return
    }
    if len(req.Recipients) == 0 {
        http.Error(w, `{"message":"Pelo menos um destinatario e obrigatorio"}`, http.StatusBadRequest)
        return
    }
    if len(req.Sections) == 0 {
        http.Error(w, `{"message":"Pelo menos uma secao e obrigatoria"}`, http.StatusBadRequest)
        return
    }

    validSections := map[string]bool{
        "ig_engagement": true, "ig_posts": true,
        "ads_campaigns": true, "ads_insights": true,
    }
    for _, s := range req.Sections {
        if !validSections[s] {
            http.Error(w, fmt.Sprintf(`{"message":"Secao invalida: %s"}`, s), http.StatusBadRequest)
            return
        }
    }

    if req.DateRange <= 0 {
        req.DateRange = 30
    }

    ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
    defer cancel()

    now := time.Now()
    schedule := models.ReportSchedule{
        ID:         primitive.NewObjectID(),
        UserID:     userID,
        Name:       req.Name,
        Frequency:  req.Frequency,
        Format:     req.Format,
        Recipients: req.Recipients,
        Sections:   req.Sections,
        DateRange:  req.DateRange,
        Active:     true,
        NextRunAt:  calculateNextRun(req.Frequency, now),
        CreatedAt:  now,
        UpdatedAt:  now,
    }

    _, err := database.ReportSchedules().InsertOne(ctx, schedule)
    if err != nil {
        http.Error(w, `{"message":"Erro ao criar agendamento"}`, http.StatusInternalServerError)
        return
    }

    slog.Info("reports: schedule created",
        "schedule_id", schedule.ID.Hex(),
        "user_id", userID.Hex(),
        "frequency", req.Frequency,
    )

    w.WriteHeader(http.StatusCreated)
    json.NewEncoder(w).Encode(schedule)
}

// GetReportSchedule returns a single schedule.
// GET /api/v1/admin/reports/schedules/{id}
func GetReportSchedule(w http.ResponseWriter, r *http.Request) {
    userID := middleware.GetUserID(r)
    if userID == primitive.NilObjectID {
        http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
        return
    }

    scheduleID := r.PathValue("id")
    oid, err := primitive.ObjectIDFromHex(scheduleID)
    if err != nil {
        http.Error(w, `{"message":"ID invalido"}`, http.StatusBadRequest)
        return
    }

    ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
    defer cancel()

    var schedule models.ReportSchedule
    err = database.ReportSchedules().FindOne(ctx, bson.M{
        "_id": oid, "user_id": userID,
    }).Decode(&schedule)
    if err != nil {
        http.Error(w, `{"message":"Agendamento nao encontrado"}`, http.StatusNotFound)
        return
    }

    json.NewEncoder(w).Encode(schedule)
}

// UpdateReportSchedule updates an existing schedule.
// PUT /api/v1/admin/reports/schedules/{id}
func UpdateReportSchedule(w http.ResponseWriter, r *http.Request) {
    userID := middleware.GetUserID(r)
    if userID == primitive.NilObjectID {
        http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
        return
    }

    scheduleID := r.PathValue("id")
    oid, err := primitive.ObjectIDFromHex(scheduleID)
    if err != nil {
        http.Error(w, `{"message":"ID invalido"}`, http.StatusBadRequest)
        return
    }

    var req models.UpdateReportScheduleRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, `{"message":"Dados invalidos"}`, http.StatusBadRequest)
        return
    }

    ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
    defer cancel()

    update := bson.M{"$set": bson.M{"updated_at": time.Now()}}
    setFields := update["$set"].(bson.M)

    if req.Name != nil {
        setFields["name"] = *req.Name
    }
    if req.Frequency != nil {
        setFields["frequency"] = *req.Frequency
        setFields["next_run_at"] = calculateNextRun(*req.Frequency, time.Now())
    }
    if req.Format != nil {
        setFields["format"] = *req.Format
    }
    if req.Recipients != nil {
        setFields["recipients"] = *req.Recipients
    }
    if req.Sections != nil {
        setFields["sections"] = *req.Sections
    }
    if req.DateRange != nil {
        setFields["date_range"] = *req.DateRange
    }
    if req.Active != nil {
        setFields["active"] = *req.Active
    }

    result, err := database.ReportSchedules().UpdateOne(ctx,
        bson.M{"_id": oid, "user_id": userID}, update,
    )
    if err != nil {
        http.Error(w, `{"message":"Erro ao atualizar agendamento"}`, http.StatusInternalServerError)
        return
    }
    if result.MatchedCount == 0 {
        http.Error(w, `{"message":"Agendamento nao encontrado"}`, http.StatusNotFound)
        return
    }

    json.NewEncoder(w).Encode(map[string]string{"message": "Agendamento atualizado"})
}

// ToggleReportSchedule toggles the active state of a schedule.
// PATCH /api/v1/admin/reports/schedules/{id}
func ToggleReportSchedule(w http.ResponseWriter, r *http.Request) {
    userID := middleware.GetUserID(r)
    if userID == primitive.NilObjectID {
        http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
        return
    }

    scheduleID := r.PathValue("id")
    oid, err := primitive.ObjectIDFromHex(scheduleID)
    if err != nil {
        http.Error(w, `{"message":"ID invalido"}`, http.StatusBadRequest)
        return
    }

    var req struct {
        Active bool `json:"active"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, `{"message":"Dados invalidos"}`, http.StatusBadRequest)
        return
    }

    ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
    defer cancel()

    update := bson.M{"$set": bson.M{"active": req.Active, "updated_at": time.Now()}}
    result, err := database.ReportSchedules().UpdateOne(ctx,
        bson.M{"_id": oid, "user_id": userID}, update,
    )
    if err != nil {
        http.Error(w, `{"message":"Erro ao atualizar agendamento"}`, http.StatusInternalServerError)
        return
    }
    if result.MatchedCount == 0 {
        http.Error(w, `{"message":"Agendamento nao encontrado"}`, http.StatusNotFound)
        return
    }

    json.NewEncoder(w).Encode(map[string]string{
        "message": "Status atualizado",
        "active":  fmt.Sprintf("%v", req.Active),
    })
}

// DeleteReportSchedule deletes a schedule.
// DELETE /api/v1/admin/reports/schedules/{id}
func DeleteReportSchedule(w http.ResponseWriter, r *http.Request) {
    userID := middleware.GetUserID(r)
    if userID == primitive.NilObjectID {
        http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
        return
    }

    scheduleID := r.PathValue("id")
    oid, err := primitive.ObjectIDFromHex(scheduleID)
    if err != nil {
        http.Error(w, `{"message":"ID invalido"}`, http.StatusBadRequest)
        return
    }

    ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
    defer cancel()

    result, err := database.ReportSchedules().DeleteOne(ctx, bson.M{"_id": oid, "user_id": userID})
    if err != nil {
        http.Error(w, `{"message":"Erro ao deletar agendamento"}`, http.StatusInternalServerError)
        return
    }
    if result.DeletedCount == 0 {
        http.Error(w, `{"message":"Agendamento nao encontrado"}`, http.StatusNotFound)
        return
    }

    json.NewEncoder(w).Encode(map[string]string{"message": "Agendamento removido"})
}
```

### 5.2 Geracao sob demanda

```go
// ══════════════════════════════════════════════════════════════════════
// ON-DEMAND GENERATION
// ══════════════════════════════════════════════════════════════════════

// GenerateReport generates a report on demand.
// POST /api/v1/admin/reports/generate
func GenerateReport(w http.ResponseWriter, r *http.Request) {
    userID := middleware.GetUserID(r)
    if userID == primitive.NilObjectID {
        http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
        return
    }

    var req models.GenerateReportRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, `{"message":"Dados invalidos"}`, http.StatusBadRequest)
        return
    }

    if req.Format != "pdf" && req.Format != "csv" {
        http.Error(w, `{"message":"Formato deve ser pdf ou csv"}`, http.StatusBadRequest)
        return
    }
    if len(req.Sections) == 0 {
        http.Error(w, `{"message":"Pelo menos uma secao e obrigatoria"}`, http.StatusBadRequest)
        return
    }
    if req.DateRange <= 0 {
        req.DateRange = 30
    }

    ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
    defer cancel()

    now := time.Now()
    period := models.ReportPeriod{
        Start: now.AddDate(0, 0, -req.DateRange).Format("2006-01-02"),
        End:   now.Format("2006-01-02"),
    }

    // Gather data
    summary, err := gatherReportData(ctx, userID, req.Sections, period)
    if err != nil {
        slog.Error("reports: gather data failed", "error", err, "user_id", userID.Hex())
        http.Error(w, `{"message":"Erro ao coletar dados para o relatorio"}`, http.StatusInternalServerError)
        return
    }

    // Generate file
    var fileData []byte
    var fileName string

    if req.Format == "pdf" {
        fileData, err = generatePDF(summary, req.Sections, period)
        fileName = fmt.Sprintf("relatorio-%s.pdf", now.Format("2006-01-02-150405"))
    } else {
        fileData, err = generateCSV(summary, req.Sections, period)
        fileName = fmt.Sprintf("relatorio-%s.csv", now.Format("2006-01-02-150405"))
    }
    if err != nil {
        slog.Error("reports: file generation failed", "error", err)
        http.Error(w, `{"message":"Erro ao gerar arquivo"}`, http.StatusInternalServerError)
        return
    }

    // Save to DB
    report := models.GeneratedReport{
        ID:          primitive.NewObjectID(),
        UserID:      userID,
        Format:      req.Format,
        FileName:    fileName,
        FileData:    fileData,
        FileSize:    int64(len(fileData)),
        Period:      period,
        Sections:    req.Sections,
        GeneratedAt: now,
        SentTo:      []string{},
        Status:      "generated",
    }

    // Send email if recipients provided
    if len(req.Recipients) > 0 {
        err := sendReportEmail(req.Recipients, fileName, fileData, req.Format, period)
        if err != nil {
            slog.Error("reports: email send failed", "error", err)
            report.Status = "failed"
        } else {
            report.SentTo = req.Recipients
            report.Status = "sent"
        }
    }

    database.GeneratedReports().InsertOne(ctx, report)

    slog.Info("reports: on-demand report generated",
        "report_id", report.ID.Hex(),
        "format", req.Format,
        "status", report.Status,
    )

    w.WriteHeader(http.StatusCreated)
    json.NewEncoder(w).Encode(map[string]interface{}{
        "report_id": report.ID.Hex(),
        "file_name": fileName,
        "file_size": report.FileSize,
        "status":    report.Status,
        "message":   "Relatorio gerado com sucesso",
    })
}
```

### 5.3 Listagem e Download

```go
// ══════════════════════════════════════════════════════════════════════
// LIST & DOWNLOAD
// ══════════════════════════════════════════════════════════════════════

// ListGeneratedReports returns the report history for the user.
// GET /api/v1/admin/reports
func ListGeneratedReports(w http.ResponseWriter, r *http.Request) {
    userID := middleware.GetUserID(r)
    if userID == primitive.NilObjectID {
        http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
        return
    }

    ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
    defer cancel()

    // Exclude file_data from listing (it can be very large)
    opts := options.Find().
        SetSort(bson.D{{Key: "generated_at", Value: -1}}).
        SetProjection(bson.M{"file_data": 0}).
        SetLimit(50)

    cursor, err := database.GeneratedReports().Find(ctx, bson.M{"user_id": userID}, opts)
    if err != nil {
        http.Error(w, `{"message":"Erro ao buscar relatorios"}`, http.StatusInternalServerError)
        return
    }
    defer cursor.Close(ctx)

    var reports []models.GeneratedReport
    if err := cursor.All(ctx, &reports); err != nil {
        http.Error(w, `{"message":"Erro ao decodificar relatorios"}`, http.StatusInternalServerError)
        return
    }

    if reports == nil {
        reports = []models.GeneratedReport{}
    }

    json.NewEncoder(w).Encode(map[string]interface{}{
        "reports": reports,
        "total":   len(reports),
    })
}

// DownloadReport serves the generated report file.
// GET /api/v1/admin/reports/{id}/download
func DownloadReport(w http.ResponseWriter, r *http.Request) {
    userID := middleware.GetUserID(r)
    if userID == primitive.NilObjectID {
        http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
        return
    }

    reportID := r.PathValue("id")
    oid, err := primitive.ObjectIDFromHex(reportID)
    if err != nil {
        http.Error(w, `{"message":"ID invalido"}`, http.StatusBadRequest)
        return
    }

    ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
    defer cancel()

    var report models.GeneratedReport
    err = database.GeneratedReports().FindOne(ctx, bson.M{
        "_id": oid, "user_id": userID,
    }).Decode(&report)
    if err != nil {
        http.Error(w, `{"message":"Relatorio nao encontrado"}`, http.StatusNotFound)
        return
    }

    contentType := "application/pdf"
    if report.Format == "csv" {
        contentType = "text/csv"
    }

    w.Header().Set("Content-Type", contentType)
    w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, report.FileName))
    w.Header().Set("Content-Length", fmt.Sprintf("%d", len(report.FileData)))
    w.Write(report.FileData)
}
```

### 5.4 Funcoes auxiliares de coleta de dados

```go
// ══════════════════════════════════════════════════════════════════════
// DATA GATHERING
// ══════════════════════════════════════════════════════════════════════

// gatherReportData collects data from IG and Meta Ads APIs based on selected sections.
func gatherReportData(ctx context.Context, userID primitive.ObjectID, sections []string, period models.ReportPeriod) (*models.ReportSummary, error) {
    summary := &models.ReportSummary{}
    sectionSet := map[string]bool{}
    for _, s := range sections {
        sectionSet[s] = true
    }

    // ── Instagram data ──────────────────────────────────────────
    if sectionSet["ig_engagement"] || sectionSet["ig_posts"] {
        igCreds, err := getInstagramCredentials(ctx, userID)
        if err == nil && igCreds != nil {
            followers := fetchFollowersCount(igCreds.AccountID, igCreds.Token)
            posts := fetchMediaWithInsights(igCreds.AccountID, igCreds.Token, followers)

            summary.FollowersCount = followers
            summary.TotalPosts = len(posts)

            if len(posts) > 0 {
                var engSum float64
                for _, p := range posts {
                    engSum += p.EngagementRate
                }
                summary.AvgEngagement = engSum / float64(len(posts))
            }

            summary.BestPostingHours = computeBestHours(posts)

            // Top 5 posts by engagement
            sort.Slice(posts, func(i, j int) bool {
                return posts[i].EngagementRate > posts[j].EngagementRate
            })
            if len(posts) > 5 {
                summary.TopPosts = posts[:5]
            } else {
                summary.TopPosts = posts
            }
        }
    }

    // ── Meta Ads data ───────────────────────────────────────────
    if sectionSet["ads_campaigns"] || sectionSet["ads_insights"] {
        adsCreds, err := getMetaAdsCredentials(ctx, userID)
        if err == nil && adsCreds != nil {
            // Account-level insights
            params := url.Values{}
            params.Set("fields", "impressions,reach,clicks,spend,ctr,cpc,cpm")
            params.Set("time_range", fmt.Sprintf(`{"since":"%s","until":"%s"}`, period.Start, period.End))

            result, err := metaGraphGet(adAccountPath(adsCreds.AdAccountID)+"/insights", adsCreds.Token, params)
            if err == nil {
                if data, ok := result["data"].([]interface{}); ok && len(data) > 0 {
                    if row, ok := data[0].(map[string]interface{}); ok {
                        summary.TotalSpend = parseFloat(row["spend"])
                        summary.TotalImpressions = parseInt(row["impressions"])
                        summary.TotalClicks = parseInt(row["clicks"])
                        summary.TotalReach = parseInt(row["reach"])
                        summary.AvgCTR = parseFloat(row["ctr"])
                        summary.AvgCPC = parseFloat(row["cpc"])
                        summary.AvgCPM = parseFloat(row["cpm"])
                    }
                }
            }

            // Campaign-level insights
            if sectionSet["ads_campaigns"] {
                campParams := url.Values{}
                campParams.Set("fields", "campaign_name,impressions,clicks,spend,ctr,cpc")
                campParams.Set("level", "campaign")
                campParams.Set("time_range", fmt.Sprintf(`{"since":"%s","until":"%s"}`, period.Start, period.End))

                campResult, err := metaGraphGet(adAccountPath(adsCreds.AdAccountID)+"/insights", adsCreds.Token, campParams)
                if err == nil {
                    if data, ok := campResult["data"].([]interface{}); ok {
                        var maxSpend float64
                        for _, item := range data {
                            row, ok := item.(map[string]interface{})
                            if !ok {
                                continue
                            }
                            cs := models.CampaignSummary{
                                Name:        fmt.Sprintf("%v", row["campaign_name"]),
                                Spend:       parseFloat(row["spend"]),
                                Impressions: parseInt(row["impressions"]),
                                Clicks:      parseInt(row["clicks"]),
                                CTR:         parseFloat(row["ctr"]),
                                CPC:         parseFloat(row["cpc"]),
                            }
                            summary.Campaigns = append(summary.Campaigns, cs)
                            if cs.Spend > maxSpend {
                                maxSpend = cs.Spend
                                summary.TopCampaign = cs.Name
                            }
                        }
                    }
                }
            }
        }
    }

    return summary, nil
}

// parseFloat safely extracts a float64 from a map value (string or number).
func parseFloat(v interface{}) float64 {
    switch val := v.(type) {
    case float64:
        return val
    case string:
        var f float64
        fmt.Sscanf(val, "%f", &f)
        return f
    default:
        return 0
    }
}

// parseInt safely extracts an int64 from a map value (string or number).
func parseInt(v interface{}) int64 {
    switch val := v.(type) {
    case float64:
        return int64(val)
    case string:
        var i int64
        fmt.Sscanf(val, "%d", &i)
        return i
    default:
        return 0
    }
}
```

### 5.5 Geracao de PDF

```go
// ══════════════════════════════════════════════════════════════════════
// PDF GENERATION
// ══════════════════════════════════════════════════════════════════════

// generatePDF creates a PDF report using gofpdf.
func generatePDF(summary *models.ReportSummary, sections []string, period models.ReportPeriod) ([]byte, error) {
    pdf := gofpdf.New("P", "mm", "A4", "")
    pdf.SetAutoPageBreak(true, 15)
    pdf.AddPage()

    // Title
    pdf.SetFont("Arial", "B", 20)
    pdf.Cell(0, 12, "Relatorio de Performance")
    pdf.Ln(10)

    pdf.SetFont("Arial", "", 11)
    pdf.SetTextColor(100, 100, 100)
    pdf.Cell(0, 8, fmt.Sprintf("Periodo: %s a %s", period.Start, period.End))
    pdf.Ln(6)
    pdf.Cell(0, 8, fmt.Sprintf("Gerado em: %s", time.Now().Format("02/01/2006 15:04")))
    pdf.Ln(12)

    sectionSet := map[string]bool{}
    for _, s := range sections {
        sectionSet[s] = true
    }

    // ── Instagram Engagement Section ────────────────────────────
    if sectionSet["ig_engagement"] {
        pdf.SetTextColor(0, 0, 0)
        pdf.SetFont("Arial", "B", 14)
        pdf.Cell(0, 10, "Instagram - Engajamento")
        pdf.Ln(8)

        pdf.SetFont("Arial", "", 11)
        pdf.Cell(95, 8, fmt.Sprintf("Seguidores: %d", summary.FollowersCount))
        pdf.Cell(95, 8, fmt.Sprintf("Total de Posts: %d", summary.TotalPosts))
        pdf.Ln(6)
        pdf.Cell(95, 8, fmt.Sprintf("Engajamento Medio: %.2f%%", summary.AvgEngagement))
        pdf.Ln(10)

        if len(summary.BestPostingHours) > 0 {
            pdf.SetFont("Arial", "B", 11)
            pdf.Cell(0, 8, "Melhores Horarios para Postar:")
            pdf.Ln(6)
            pdf.SetFont("Arial", "", 10)
            limit := len(summary.BestPostingHours)
            if limit > 5 {
                limit = 5
            }
            for _, h := range summary.BestPostingHours[:limit] {
                pdf.Cell(0, 6, fmt.Sprintf("  %02d:00 - Engajamento medio: %.2f%% (%d posts)",
                    h.Hour, h.AvgEngagement, h.PostCount))
                pdf.Ln(5)
            }
            pdf.Ln(6)
        }
    }

    // ── Instagram Posts Section ──────────────────────────────────
    if sectionSet["ig_posts"] && len(summary.TopPosts) > 0 {
        pdf.SetFont("Arial", "B", 14)
        pdf.Cell(0, 10, "Instagram - Top Posts")
        pdf.Ln(8)

        // Table header
        pdf.SetFont("Arial", "B", 9)
        pdf.SetFillColor(240, 240, 240)
        pdf.CellFormat(15, 7, "#", "1", 0, "C", true, 0, "")
        pdf.CellFormat(75, 7, "Caption", "1", 0, "C", true, 0, "")
        pdf.CellFormat(25, 7, "Likes", "1", 0, "C", true, 0, "")
        pdf.CellFormat(30, 7, "Comentarios", "1", 0, "C", true, 0, "")
        pdf.CellFormat(30, 7, "Engajamento", "1", 0, "C", true, 0, "")
        pdf.Ln(-1)

        pdf.SetFont("Arial", "", 9)
        for i, p := range summary.TopPosts {
            caption := p.Caption
            if len(caption) > 40 {
                caption = caption[:40] + "..."
            }
            pdf.CellFormat(15, 7, fmt.Sprintf("%d", i+1), "1", 0, "C", false, 0, "")
            pdf.CellFormat(75, 7, caption, "1", 0, "L", false, 0, "")
            pdf.CellFormat(25, 7, fmt.Sprintf("%d", p.LikeCount), "1", 0, "C", false, 0, "")
            pdf.CellFormat(30, 7, fmt.Sprintf("%d", p.CommentsCount), "1", 0, "C", false, 0, "")
            pdf.CellFormat(30, 7, fmt.Sprintf("%.2f%%", p.EngagementRate), "1", 0, "C", false, 0, "")
            pdf.Ln(-1)
        }
        pdf.Ln(8)
    }

    // ── Meta Ads Insights Section ───────────────────────────────
    if sectionSet["ads_insights"] {
        pdf.SetFont("Arial", "B", 14)
        pdf.Cell(0, 10, "Meta Ads - Visao Geral")
        pdf.Ln(8)

        pdf.SetFont("Arial", "", 11)
        pdf.Cell(95, 8, fmt.Sprintf("Investimento Total: R$ %.2f", summary.TotalSpend))
        pdf.Cell(95, 8, fmt.Sprintf("Impressoes: %d", summary.TotalImpressions))
        pdf.Ln(6)
        pdf.Cell(95, 8, fmt.Sprintf("Cliques: %d", summary.TotalClicks))
        pdf.Cell(95, 8, fmt.Sprintf("Alcance: %d", summary.TotalReach))
        pdf.Ln(6)
        pdf.Cell(95, 8, fmt.Sprintf("CTR Medio: %.2f%%", summary.AvgCTR))
        pdf.Cell(95, 8, fmt.Sprintf("CPC Medio: R$ %.2f", summary.AvgCPC))
        pdf.Ln(6)
        pdf.Cell(95, 8, fmt.Sprintf("CPM Medio: R$ %.2f", summary.AvgCPM))
        if summary.TopCampaign != "" {
            pdf.Cell(95, 8, fmt.Sprintf("Top Campanha: %s", summary.TopCampaign))
        }
        pdf.Ln(10)
    }

    // ── Meta Ads Campaigns Section ──────────────────────────────
    if sectionSet["ads_campaigns"] && len(summary.Campaigns) > 0 {
        pdf.SetFont("Arial", "B", 14)
        pdf.Cell(0, 10, "Meta Ads - Campanhas")
        pdf.Ln(8)

        // Table header
        pdf.SetFont("Arial", "B", 9)
        pdf.SetFillColor(240, 240, 240)
        pdf.CellFormat(60, 7, "Campanha", "1", 0, "C", true, 0, "")
        pdf.CellFormat(25, 7, "Gasto", "1", 0, "C", true, 0, "")
        pdf.CellFormat(30, 7, "Impressoes", "1", 0, "C", true, 0, "")
        pdf.CellFormat(25, 7, "Cliques", "1", 0, "C", true, 0, "")
        pdf.CellFormat(20, 7, "CTR", "1", 0, "C", true, 0, "")
        pdf.CellFormat(20, 7, "CPC", "1", 0, "C", true, 0, "")
        pdf.Ln(-1)

        pdf.SetFont("Arial", "", 9)
        for _, c := range summary.Campaigns {
            name := c.Name
            if len(name) > 30 {
                name = name[:30] + "..."
            }
            pdf.CellFormat(60, 7, name, "1", 0, "L", false, 0, "")
            pdf.CellFormat(25, 7, fmt.Sprintf("%.2f", c.Spend), "1", 0, "C", false, 0, "")
            pdf.CellFormat(30, 7, fmt.Sprintf("%d", c.Impressions), "1", 0, "C", false, 0, "")
            pdf.CellFormat(25, 7, fmt.Sprintf("%d", c.Clicks), "1", 0, "C", false, 0, "")
            pdf.CellFormat(20, 7, fmt.Sprintf("%.2f%%", c.CTR), "1", 0, "C", false, 0, "")
            pdf.CellFormat(20, 7, fmt.Sprintf("%.2f", c.CPC), "1", 0, "C", false, 0, "")
            pdf.Ln(-1)
        }
    }

    var buf bytes.Buffer
    if err := pdf.Output(&buf); err != nil {
        return nil, fmt.Errorf("pdf output error: %w", err)
    }
    return buf.Bytes(), nil
}
```

### 5.6 Geracao de CSV

```go
// ══════════════════════════════════════════════════════════════════════
// CSV GENERATION
// ══════════════════════════════════════════════════════════════════════

// generateCSV creates a CSV report.
func generateCSV(summary *models.ReportSummary, sections []string, period models.ReportPeriod) ([]byte, error) {
    var buf bytes.Buffer
    w := csv.NewWriter(&buf)

    sectionSet := map[string]bool{}
    for _, s := range sections {
        sectionSet[s] = true
    }

    // Header row
    w.Write([]string{"Relatorio de Performance", period.Start, period.End})
    w.Write([]string{})

    // ── Instagram Engagement ────────────────────────────────────
    if sectionSet["ig_engagement"] {
        w.Write([]string{"== Instagram - Engajamento =="})
        w.Write([]string{"Metrica", "Valor"})
        w.Write([]string{"Seguidores", fmt.Sprintf("%d", summary.FollowersCount)})
        w.Write([]string{"Total de Posts", fmt.Sprintf("%d", summary.TotalPosts)})
        w.Write([]string{"Engajamento Medio (%)", fmt.Sprintf("%.2f", summary.AvgEngagement)})
        w.Write([]string{})

        if len(summary.BestPostingHours) > 0 {
            w.Write([]string{"Melhores Horarios"})
            w.Write([]string{"Horario", "Engajamento Medio (%)", "Qtd Posts"})
            limit := len(summary.BestPostingHours)
            if limit > 5 {
                limit = 5
            }
            for _, h := range summary.BestPostingHours[:limit] {
                w.Write([]string{
                    fmt.Sprintf("%02d:00", h.Hour),
                    fmt.Sprintf("%.2f", h.AvgEngagement),
                    fmt.Sprintf("%d", h.PostCount),
                })
            }
            w.Write([]string{})
        }
    }

    // ── Instagram Posts ─────────────────────────────────────────
    if sectionSet["ig_posts"] && len(summary.TopPosts) > 0 {
        w.Write([]string{"== Instagram - Top Posts =="})
        w.Write([]string{"#", "Caption", "Likes", "Comentarios", "Engajamento (%)"})
        for i, p := range summary.TopPosts {
            caption := p.Caption
            if len(caption) > 80 {
                caption = caption[:80] + "..."
            }
            w.Write([]string{
                fmt.Sprintf("%d", i+1),
                caption,
                fmt.Sprintf("%d", p.LikeCount),
                fmt.Sprintf("%d", p.CommentsCount),
                fmt.Sprintf("%.2f", p.EngagementRate),
            })
        }
        w.Write([]string{})
    }

    // ── Meta Ads Insights ───────────────────────────────────────
    if sectionSet["ads_insights"] {
        w.Write([]string{"== Meta Ads - Visao Geral =="})
        w.Write([]string{"Metrica", "Valor"})
        w.Write([]string{"Investimento Total (R$)", fmt.Sprintf("%.2f", summary.TotalSpend)})
        w.Write([]string{"Impressoes", fmt.Sprintf("%d", summary.TotalImpressions)})
        w.Write([]string{"Cliques", fmt.Sprintf("%d", summary.TotalClicks)})
        w.Write([]string{"Alcance", fmt.Sprintf("%d", summary.TotalReach)})
        w.Write([]string{"CTR Medio (%)", fmt.Sprintf("%.2f", summary.AvgCTR)})
        w.Write([]string{"CPC Medio (R$)", fmt.Sprintf("%.2f", summary.AvgCPC)})
        w.Write([]string{"CPM Medio (R$)", fmt.Sprintf("%.2f", summary.AvgCPM)})
        if summary.TopCampaign != "" {
            w.Write([]string{"Top Campanha", summary.TopCampaign})
        }
        w.Write([]string{})
    }

    // ── Meta Ads Campaigns ──────────────────────────────────────
    if sectionSet["ads_campaigns"] && len(summary.Campaigns) > 0 {
        w.Write([]string{"== Meta Ads - Campanhas =="})
        w.Write([]string{"Campanha", "Gasto (R$)", "Impressoes", "Cliques", "CTR (%)", "CPC (R$)"})
        for _, c := range summary.Campaigns {
            w.Write([]string{
                c.Name,
                fmt.Sprintf("%.2f", c.Spend),
                fmt.Sprintf("%d", c.Impressions),
                fmt.Sprintf("%d", c.Clicks),
                fmt.Sprintf("%.2f", c.CTR),
                fmt.Sprintf("%.2f", c.CPC),
            })
        }
    }

    w.Flush()
    if err := w.Error(); err != nil {
        return nil, fmt.Errorf("csv write error: %w", err)
    }
    return buf.Bytes(), nil
}
```

### 5.7 Envio de Email via Resend

```go
// ══════════════════════════════════════════════════════════════════════
// EMAIL SENDING (Resend API)
// ══════════════════════════════════════════════════════════════════════

// sendReportEmail sends the report file as an email attachment using the Resend API.
// Follows the same pattern as email_marketing.go and newsletter_broadcast.go.
func sendReportEmail(recipients []string, fileName string, fileData []byte, format string, period models.ReportPeriod) error {
    cfg := config.Get()
    if cfg.ResendAPIKey == "" {
        return fmt.Errorf("RESEND_API_KEY not configured")
    }

    // Resend API accepts attachments as base64 in the /emails endpoint
    contentType := "application/pdf"
    if format == "csv" {
        contentType = "text/csv"
    }

    attachment := map[string]interface{}{
        "filename":     fileName,
        "content":      base64.StdEncoding.EncodeToString(fileData),
        "content_type": contentType,
    }

    subject := fmt.Sprintf("Relatorio de Performance - %s a %s", period.Start, period.End)

    emailBody := map[string]interface{}{
        "from":        cfg.FromEmail,
        "to":          recipients,
        "subject":     subject,
        "html":        buildReportEmailHTML(period),
        "attachments": []interface{}{attachment},
    }

    bodyBytes, err := json.Marshal(emailBody)
    if err != nil {
        return fmt.Errorf("marshal error: %w", err)
    }

    client := &http.Client{Timeout: 30 * time.Second}
    req, err := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewReader(bodyBytes))
    if err != nil {
        return fmt.Errorf("request error: %w", err)
    }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+cfg.ResendAPIKey)

    resp, err := client.Do(req)
    if err != nil {
        return fmt.Errorf("http error: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
        respBody, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("resend API error: status=%d body=%s", resp.StatusCode, string(respBody))
    }

    slog.Info("reports: email sent", "to", recipients, "file", fileName)
    return nil
}

// buildReportEmailHTML generates the HTML body for the report email.
func buildReportEmailHTML(period models.ReportPeriod) string {
    return fmt.Sprintf(`<!DOCTYPE html>
<html lang="pt-BR">
<head><meta charset="UTF-8"></head>
<body style="margin:0;padding:0;background-color:#09090b;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">
  <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="background-color:#09090b;padding:40px 20px;">
    <tr>
      <td align="center">
        <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="max-width:520px;background:linear-gradient(145deg,rgba(255,255,255,0.03),rgba(255,255,255,0.01));border:1px solid rgba(255,255,255,0.06);border-radius:16px;overflow:hidden;">
          <tr>
            <td style="padding:32px 40px 0;text-align:center;">
              <h1 style="margin:0;font-size:28px;font-weight:700;color:#a855f7;">Whodo</h1>
              <p style="margin:8px 0 0;font-size:13px;color:#a1a1aa;">Relatorio Automatizado</p>
            </td>
          </tr>
          <tr>
            <td style="padding:20px 40px;">
              <div style="height:1px;background:linear-gradient(90deg,transparent,rgba(168,85,247,0.3),transparent);"></div>
            </td>
          </tr>
          <tr>
            <td style="padding:0 40px 32px;">
              <h2 style="margin:0 0 12px;font-size:20px;font-weight:700;color:#fafafa;">Seu relatorio esta pronto!</h2>
              <p style="margin:0 0 8px;font-size:14px;color:#d4d4d8;line-height:1.6;">
                Periodo: <strong>%s</strong> a <strong>%s</strong>
              </p>
              <p style="margin:0;font-size:14px;color:#d4d4d8;line-height:1.6;">
                O relatorio com as metricas do seu Instagram e Meta Ads esta anexado a este email.
              </p>
            </td>
          </tr>
          <tr>
            <td style="padding:0 40px 32px;">
              <div style="height:1px;background:linear-gradient(90deg,transparent,rgba(255,255,255,0.05),transparent);margin-bottom:20px;"></div>
              <p style="margin:0;font-size:12px;color:#71717a;text-align:center;line-height:1.5;">
                Este email foi enviado automaticamente pelo Whodo.<br>
                &copy; %d Whodo Group LTDA
              </p>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`, period.Start, period.End, time.Now().Year())
}
```

### 5.8 Calculo do proximo horario de execucao

```go
// ══════════════════════════════════════════════════════════════════════
// SCHEDULE HELPERS
// ══════════════════════════════════════════════════════════════════════

// calculateNextRun computes the next execution time based on frequency.
func calculateNextRun(frequency string, from time.Time) time.Time {
    // Reports run at 08:00 UTC
    base := time.Date(from.Year(), from.Month(), from.Day(), 8, 0, 0, 0, time.UTC)

    switch frequency {
    case "daily":
        next := base.AddDate(0, 0, 1)
        return next
    case "weekly":
        // Next Monday at 08:00
        daysUntilMonday := (8 - int(from.Weekday())) % 7
        if daysUntilMonday == 0 {
            daysUntilMonday = 7
        }
        return base.AddDate(0, 0, daysUntilMonday)
    case "monthly":
        // First day of next month at 08:00
        return time.Date(from.Year(), from.Month()+1, 1, 8, 0, 0, 0, time.UTC)
    default:
        return base.AddDate(0, 0, 1)
    }
}

// ProcessScheduledReports checks for due schedules and generates reports.
// Called by the background job every 30 minutes.
func ProcessScheduledReports() {
    ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
    defer cancel()

    now := time.Now()
    filter := bson.M{
        "active":      true,
        "next_run_at": bson.M{"$lte": now},
    }

    cursor, err := database.ReportSchedules().Find(ctx, filter)
    if err != nil {
        slog.Error("reports: failed to query schedules", "error", err)
        return
    }
    defer cursor.Close(ctx)

    var schedules []models.ReportSchedule
    if err := cursor.All(ctx, &schedules); err != nil {
        slog.Error("reports: failed to decode schedules", "error", err)
        return
    }

    for _, schedule := range schedules {
        slog.Info("reports: processing schedule",
            "schedule_id", schedule.ID.Hex(),
            "name", schedule.Name,
            "frequency", schedule.Frequency,
        )

        period := models.ReportPeriod{
            Start: now.AddDate(0, 0, -schedule.DateRange).Format("2006-01-02"),
            End:   now.Format("2006-01-02"),
        }

        // Gather data
        summary, err := gatherReportData(ctx, schedule.UserID, schedule.Sections, period)
        if err != nil {
            slog.Error("reports: gather data failed for schedule",
                "schedule_id", schedule.ID.Hex(), "error", err)
            continue
        }

        // Generate file
        var fileData []byte
        var fileName string

        if schedule.Format == "pdf" {
            fileData, err = generatePDF(summary, schedule.Sections, period)
            fileName = fmt.Sprintf("relatorio-%s-%s.pdf", schedule.Name, now.Format("2006-01-02"))
        } else {
            fileData, err = generateCSV(summary, schedule.Sections, period)
            fileName = fmt.Sprintf("relatorio-%s-%s.csv", schedule.Name, now.Format("2006-01-02"))
        }
        if err != nil {
            slog.Error("reports: file generation failed",
                "schedule_id", schedule.ID.Hex(), "error", err)
            continue
        }

        // Save report
        report := models.GeneratedReport{
            ID:          primitive.NewObjectID(),
            UserID:      schedule.UserID,
            ScheduleID:  schedule.ID,
            Format:      schedule.Format,
            FileName:    fileName,
            FileData:    fileData,
            FileSize:    int64(len(fileData)),
            Period:      period,
            Sections:    schedule.Sections,
            GeneratedAt: now,
            SentTo:      []string{},
            Status:      "generated",
        }

        // Send email
        if len(schedule.Recipients) > 0 {
            err := sendReportEmail(schedule.Recipients, fileName, fileData, schedule.Format, period)
            if err != nil {
                slog.Error("reports: email failed",
                    "schedule_id", schedule.ID.Hex(), "error", err)
                report.Status = "failed"
            } else {
                report.SentTo = schedule.Recipients
                report.Status = "sent"
            }
        }

        database.GeneratedReports().InsertOne(ctx, report)

        // Update schedule: next_run_at and last_run_at
        database.ReportSchedules().UpdateOne(ctx,
            bson.M{"_id": schedule.ID},
            bson.M{"$set": bson.M{
                "next_run_at": calculateNextRun(schedule.Frequency, now),
                "last_run_at": now,
                "updated_at":  now,
            }},
        )

        slog.Info("reports: schedule processed",
            "schedule_id", schedule.ID.Hex(),
            "report_id", report.ID.Hex(),
            "status", report.Status,
        )
    }
}
```

---

## 6. Rotas API

Adicionar em `internal/router/router.go`, no bloco de rotas protegidas (admin/superuser):

```go
// Report schedules
mux.Handle("GET /api/v1/admin/reports/schedules", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListReportSchedules))))
mux.Handle("POST /api/v1/admin/reports/schedules", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.CreateReportSchedule))))
mux.Handle("GET /api/v1/admin/reports/schedules/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetReportSchedule))))
mux.Handle("PUT /api/v1/admin/reports/schedules/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.UpdateReportSchedule))))
mux.Handle("PATCH /api/v1/admin/reports/schedules/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ToggleReportSchedule))))
mux.Handle("DELETE /api/v1/admin/reports/schedules/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.DeleteReportSchedule))))

// Report generation & history
mux.Handle("POST /api/v1/admin/reports/generate", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GenerateReport))))
mux.Handle("GET /api/v1/admin/reports", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListGeneratedReports))))
mux.Handle("GET /api/v1/admin/reports/{id}/download", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.DownloadReport))))
```

### Tabela resumo

| Metodo | Rota | Descricao |
|--------|------|-----------|
| `GET` | `/api/v1/admin/reports/schedules` | Lista todos os agendamentos |
| `POST` | `/api/v1/admin/reports/schedules` | Cria um novo agendamento |
| `GET` | `/api/v1/admin/reports/schedules/{id}` | Retorna um agendamento |
| `PUT` | `/api/v1/admin/reports/schedules/{id}` | Atualiza um agendamento |
| `PATCH` | `/api/v1/admin/reports/schedules/{id}` | Ativa/desativa um agendamento |
| `DELETE` | `/api/v1/admin/reports/schedules/{id}` | Remove um agendamento |
| `POST` | `/api/v1/admin/reports/generate` | Gera relatorio sob demanda |
| `GET` | `/api/v1/admin/reports` | Lista historico de relatorios |
| `GET` | `/api/v1/admin/reports/{id}/download` | Faz download do arquivo |

---

## 7. Background Jobs

Adicionar em `cmd/api/main.go`:

### 7.1 Inicializacao do goroutine

```go
// No main(), apos o start do metaAdsBudgetChecker:
go reportScheduleChecker()
```

### 7.2 Funcao do goroutine

```go
// reportScheduleChecker runs every 30 minutes and processes due report schedules.
func reportScheduleChecker() {
    // Wait for server to start
    time.Sleep(45 * time.Second)
    log.Println("Report schedule checker started (30 min interval)")

    ticker := time.NewTicker(30 * time.Minute)
    defer ticker.Stop()

    for range ticker.C {
        handlers.ProcessScheduledReports()
    }
}
```

### 7.3 Fluxo do background job

A cada 30 minutos, o job:

1. Busca na collection `report_schedules` todos os documentos com `active: true` e `next_run_at <= now`
2. Para cada agendamento encontrado:
   - Busca credenciais Instagram via `getInstagramCredentials(ctx, userID)`
   - Busca credenciais Meta Ads via `getMetaAdsCredentials(ctx, userID)`
   - Coleta dados do Instagram Graph API (followers, posts, engagement)
   - Coleta dados do Meta Ads Insights API (spend, impressions, clicks, campanhas)
   - Monta o `ReportSummary` com todas as metricas
   - Gera o arquivo PDF (via `gofpdf`) ou CSV (via `encoding/csv`)
   - Salva o `GeneratedReport` no MongoDB
   - Envia o email com anexo via Resend API (`POST https://api.resend.com/emails`)
   - Atualiza `next_run_at` e `last_run_at` no agendamento

---

## 8. Frontend

### 8.1 Pagina principal: `ReportsPage.jsx`

```jsx
import { useState, useEffect } from 'react';
import { Helmet } from 'react-helmet-async';

const TABS = [
  { id: 'generate', label: 'Gerar Relatorio' },
  { id: 'schedules', label: 'Agendamentos' },
  { id: 'history', label: 'Historico' },
];

const SECTION_OPTIONS = [
  { value: 'ig_engagement', label: 'Instagram - Engajamento' },
  { value: 'ig_posts', label: 'Instagram - Posts' },
  { value: 'ads_campaigns', label: 'Meta Ads - Campanhas' },
  { value: 'ads_insights', label: 'Meta Ads - Insights' },
];

export default function ReportsPage() {
  const [activeTab, setActiveTab] = useState('generate');

  return (
    <>
      <Helmet>
        <title>Relatorios | Whodo</title>
      </Helmet>

      <div className="reports-page">
        <h1>Relatorios Automatizados</h1>

        {/* Tabs */}
        <div className="tabs">
          {TABS.map(tab => (
            <button
              key={tab.id}
              className={activeTab === tab.id ? 'active' : ''}
              onClick={() => setActiveTab(tab.id)}
            >
              {tab.label}
            </button>
          ))}
        </div>

        {activeTab === 'generate' && <GenerateTab />}
        {activeTab === 'schedules' && <SchedulesTab />}
        {activeTab === 'history' && <HistoryTab />}
      </div>
    </>
  );
}
```

### 8.2 Aba "Gerar Relatorio" (sob demanda)

```jsx
function GenerateTab() {
  const [format, setFormat] = useState('pdf');
  const [sections, setSections] = useState(['ig_engagement', 'ads_insights']);
  const [dateRange, setDateRange] = useState(30);
  const [recipients, setRecipients] = useState('');
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState(null);

  const handleGenerate = async () => {
    setLoading(true);
    try {
      const res = await fetch('/api/v1/admin/reports/generate', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${localStorage.getItem('token')}`,
        },
        body: JSON.stringify({
          format,
          sections,
          date_range: dateRange,
          recipients: recipients ? recipients.split(',').map(e => e.trim()) : [],
        }),
      });
      const data = await res.json();
      setResult(data);
    } catch (err) {
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="generate-tab">
      <div className="form-group">
        <label>Formato</label>
        <select value={format} onChange={e => setFormat(e.target.value)}>
          <option value="pdf">PDF</option>
          <option value="csv">CSV</option>
        </select>
      </div>

      <div className="form-group">
        <label>Secoes</label>
        {SECTION_OPTIONS.map(opt => (
          <label key={opt.value} className="checkbox-label">
            <input
              type="checkbox"
              checked={sections.includes(opt.value)}
              onChange={e => {
                if (e.target.checked) {
                  setSections([...sections, opt.value]);
                } else {
                  setSections(sections.filter(s => s !== opt.value));
                }
              }}
            />
            {opt.label}
          </label>
        ))}
      </div>

      <div className="form-group">
        <label>Periodo (dias)</label>
        <select value={dateRange} onChange={e => setDateRange(Number(e.target.value))}>
          <option value={7}>Ultimos 7 dias</option>
          <option value={14}>Ultimos 14 dias</option>
          <option value={30}>Ultimos 30 dias</option>
          <option value={90}>Ultimos 90 dias</option>
        </select>
      </div>

      <div className="form-group">
        <label>Destinatarios (opcional, separados por virgula)</label>
        <input
          type="text"
          value={recipients}
          onChange={e => setRecipients(e.target.value)}
          placeholder="email1@exemplo.com, email2@exemplo.com"
        />
      </div>

      <button onClick={handleGenerate} disabled={loading || sections.length === 0}>
        {loading ? 'Gerando...' : 'Gerar Relatorio'}
      </button>

      {result && (
        <div className="result-message">
          <p>Relatorio gerado: {result.file_name}</p>
          <p>Status: {result.status}</p>
          {result.report_id && (
            <a href={`/api/v1/admin/reports/${result.report_id}/download`}>
              Baixar Relatorio
            </a>
          )}
        </div>
      )}
    </div>
  );
}
```

### 8.3 Aba "Agendamentos" (CRUD)

```jsx
function SchedulesTab() {
  const [schedules, setSchedules] = useState([]);
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState({
    name: '', frequency: 'weekly', format: 'pdf',
    recipients: '', sections: ['ig_engagement', 'ads_insights'],
    date_range: 30,
  });

  useEffect(() => { fetchSchedules(); }, []);

  const fetchSchedules = async () => {
    const res = await fetch('/api/v1/admin/reports/schedules', {
      headers: { 'Authorization': `Bearer ${localStorage.getItem('token')}` },
    });
    const data = await res.json();
    setSchedules(data.schedules || []);
  };

  const handleCreate = async () => {
    await fetch('/api/v1/admin/reports/schedules', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${localStorage.getItem('token')}`,
      },
      body: JSON.stringify({
        ...form,
        recipients: form.recipients.split(',').map(e => e.trim()),
      }),
    });
    setShowForm(false);
    fetchSchedules();
  };

  const handleToggle = async (id, active) => {
    await fetch(`/api/v1/admin/reports/schedules/${id}`, {
      method: 'PATCH',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${localStorage.getItem('token')}`,
      },
      body: JSON.stringify({ active: !active }),
    });
    fetchSchedules();
  };

  const handleDelete = async (id) => {
    if (!confirm('Remover este agendamento?')) return;
    await fetch(`/api/v1/admin/reports/schedules/${id}`, {
      method: 'DELETE',
      headers: { 'Authorization': `Bearer ${localStorage.getItem('token')}` },
    });
    fetchSchedules();
  };

  return (
    <div className="schedules-tab">
      <button onClick={() => setShowForm(!showForm)}>
        {showForm ? 'Cancelar' : 'Novo Agendamento'}
      </button>

      {showForm && (
        <div className="schedule-form">
          {/* Name, Frequency, Format, Recipients, Sections, DateRange fields */}
          {/* Same pattern as GenerateTab form */}
          <button onClick={handleCreate}>Criar Agendamento</button>
        </div>
      )}

      <table>
        <thead>
          <tr>
            <th>Nome</th>
            <th>Frequencia</th>
            <th>Formato</th>
            <th>Proxima Execucao</th>
            <th>Status</th>
            <th>Acoes</th>
          </tr>
        </thead>
        <tbody>
          {schedules.map(s => (
            <tr key={s.id}>
              <td>{s.name}</td>
              <td>{s.frequency}</td>
              <td>{s.format.toUpperCase()}</td>
              <td>{new Date(s.next_run_at).toLocaleString('pt-BR')}</td>
              <td>
                <span className={s.active ? 'badge-active' : 'badge-inactive'}>
                  {s.active ? 'Ativo' : 'Inativo'}
                </span>
              </td>
              <td>
                <button onClick={() => handleToggle(s.id, s.active)}>
                  {s.active ? 'Desativar' : 'Ativar'}
                </button>
                <button onClick={() => handleDelete(s.id)}>Excluir</button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
```

### 8.4 Aba "Historico"

```jsx
function HistoryTab() {
  const [reports, setReports] = useState([]);

  useEffect(() => { fetchReports(); }, []);

  const fetchReports = async () => {
    const res = await fetch('/api/v1/admin/reports', {
      headers: { 'Authorization': `Bearer ${localStorage.getItem('token')}` },
    });
    const data = await res.json();
    setReports(data.reports || []);
  };

  const formatSize = (bytes) => {
    if (bytes < 1024) return `${bytes} B`;
    return `${(bytes / 1024).toFixed(1)} KB`;
  };

  return (
    <div className="history-tab">
      <table>
        <thead>
          <tr>
            <th>Arquivo</th>
            <th>Formato</th>
            <th>Periodo</th>
            <th>Tamanho</th>
            <th>Status</th>
            <th>Gerado em</th>
            <th>Acao</th>
          </tr>
        </thead>
        <tbody>
          {reports.map(r => (
            <tr key={r.id}>
              <td>{r.file_name}</td>
              <td>{r.format.toUpperCase()}</td>
              <td>{r.period.start} - {r.period.end}</td>
              <td>{formatSize(r.file_size)}</td>
              <td>
                <span className={`badge-${r.status}`}>
                  {r.status === 'sent' ? 'Enviado' : r.status === 'generated' ? 'Gerado' : 'Falhou'}
                </span>
              </td>
              <td>{new Date(r.generated_at).toLocaleString('pt-BR')}</td>
              <td>
                <a
                  href={`/api/v1/admin/reports/${r.id}/download`}
                  className="download-link"
                >
                  Download
                </a>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
```

---

## 9. APIs Externas

### 9.1 Instagram Graph API (v21.0)

Reutiliza as mesmas chamadas de `instagram_analytics.go`:

| Dado | Endpoint | Funcao existente |
|------|----------|-----------------|
| Seguidores | `GET /{account_id}?fields=followers_count` | `fetchFollowersCount()` |
| Posts + metricas | `GET /{account_id}/media?fields=id,caption,media_url,media_type,like_count,comments_count,timestamp&limit=25` | `fetchMediaWithInsights()` |
| Melhores horarios | Calculado a partir dos posts | `computeBestHours()` |

### 9.2 Meta Ads Insights API (v21.0)

Reutiliza `metaGraphGet()` e `adAccountPath()` de `meta_ads.go`:

| Dado | Endpoint |
|------|----------|
| Insights de conta | `GET /act_{id}/insights?fields=impressions,reach,clicks,spend,ctr,cpc,cpm&time_range=...` |
| Insights por campanha | `GET /act_{id}/insights?fields=campaign_name,impressions,clicks,spend,ctr,cpc&level=campaign&time_range=...` |

### 9.3 Resend API (envio de email com anexo)

Segue o mesmo padrao de `email_marketing.go` e `newsletter_broadcast.go`:

```
POST https://api.resend.com/emails
Authorization: Bearer {RESEND_API_KEY}
Content-Type: application/json

{
  "from": "noreply@whodo.com.br",
  "to": ["destinatario@email.com"],
  "subject": "Relatorio de Performance - 2025-05-19 a 2025-05-26",
  "html": "<html>...</html>",
  "attachments": [
    {
      "filename": "relatorio-semanal-2025-05-26.pdf",
      "content": "<base64-encoded-file>",
      "content_type": "application/pdf"
    }
  ]
}
```

---

## 10. Codigo Reutilizado

Esta feature reutiliza extensivamente funcoes e padroes ja existentes no projeto:

| Funcao/Padrao | Arquivo de origem | Uso nesta feature |
|--------------|-------------------|-------------------|
| `getInstagramCredentials()` | `instagram_analytics.go` | Obter token/account_id do IG |
| `fetchFollowersCount()` | `instagram_analytics.go` | Seguidores no relatorio |
| `fetchMediaWithInsights()` | `instagram_analytics.go` | Posts com metricas |
| `computeBestHours()` | `instagram_analytics.go` | Melhores horarios |
| `getMetaAdsCredentials()` | `meta_ads.go` | Obter token/ad_account_id |
| `metaGraphGet()` | `meta_ads.go` | Chamadas ao Meta Graph API |
| `adAccountPath()` | `meta_ads.go` | Formata path `/act_xxx` |
| `requireMetaAdsCreds()` | `meta_ads.go` | Padrao de validacao (referencia) |
| `middleware.Auth()` | `middleware/auth.go` | Autenticacao JWT |
| `middleware.RequireRole()` | `middleware/role.go` | Controle de acesso admin/superuser |
| `middleware.GetUserID()` | `middleware/auth.go` | Extrair user ID do contexto |
| `database.CollectionName()` | `database/mongo.go` | Padrao de acesso a collections |
| `config.Get()` | `config/config.go` | Acesso a configuracoes (ResendAPIKey, etc.) |
| `time.NewTicker` em goroutine | `cmd/api/main.go` | Padrao de background jobs |
| Resend API pattern | `email_marketing.go`, `newsletter_broadcast.go` | Envio de emails |

---

## 11. Fluxo Completo

### 11.1 Cenario: Agendamento semanal

1. **Configuracao**: O admin acessa a pagina de Relatorios no painel administrativo
2. **Criacao do agendamento**: Na aba "Agendamentos", clica em "Novo Agendamento" e preenche:
   - Nome: "Relatorio Semanal Completo"
   - Frequencia: Semanal
   - Formato: PDF
   - Secoes: IG Engajamento, IG Posts, Meta Ads Campanhas, Meta Ads Insights
   - Periodo: Ultimos 7 dias
   - Destinatarios: cliente@email.com, gerente@email.com
3. **Persistencia**: O sistema salva o `ReportSchedule` no MongoDB com `active: true` e `next_run_at` calculado para a proxima segunda-feira as 08:00 UTC
4. **Execucao automatica**: Na proxima segunda-feira, o background job (`reportScheduleChecker`) detecta o agendamento:
   - Busca credenciais IG e Meta Ads do usuario
   - Faz GET na IG Graph API para seguidores e posts
   - Faz GET na Meta Ads Insights API para metricas de conta e campanhas
   - Monta o `ReportSummary` consolidado
   - Gera o PDF com tabelas e metricas usando `gofpdf`
   - Salva o `GeneratedReport` no MongoDB (com `file_data` binario)
   - Envia email via Resend API com o PDF em anexo
   - Atualiza `next_run_at` para a proxima segunda-feira
5. **Consulta**: O admin pode ver o relatorio na aba "Historico" ou baixar novamente pelo link de download

### 11.2 Cenario: Relatorio sob demanda

1. O admin acessa a aba "Gerar Relatorio"
2. Seleciona formato CSV, secoes "Meta Ads - Insights" e "Meta Ads - Campanhas", periodo 30 dias
3. Opcionalmente informa emails de destinatarios
4. Clica em "Gerar Relatorio"
5. O handler `GenerateReport` coleta os dados, gera o CSV, salva no MongoDB e retorna o ID
6. O admin pode baixar imediatamente pelo link retornado

---

## 12. Verificacao

### 12.1 Testes manuais

1. **Dependencia**: Instalar `gofpdf`
   ```bash
   cd tron-legacy-api
   go get github.com/jung-kurt/gofpdf
   ```

2. **Criar agendamento via API**:
   ```bash
   curl -X POST http://localhost:8088/api/v1/admin/reports/schedules \
     -H "Authorization: Bearer <TOKEN>" \
     -H "Content-Type: application/json" \
     -d '{
       "name": "Teste Semanal",
       "frequency": "weekly",
       "format": "pdf",
       "recipients": ["teste@email.com"],
       "sections": ["ig_engagement", "ads_insights"],
       "date_range": 7
     }'
   ```

3. **Gerar relatorio sob demanda**:
   ```bash
   curl -X POST http://localhost:8088/api/v1/admin/reports/generate \
     -H "Authorization: Bearer <TOKEN>" \
     -H "Content-Type: application/json" \
     -d '{
       "format": "pdf",
       "sections": ["ig_engagement", "ig_posts", "ads_campaigns", "ads_insights"],
       "date_range": 30,
       "recipients": ["teste@email.com"]
     }'
   ```

4. **Listar relatorios gerados**:
   ```bash
   curl http://localhost:8088/api/v1/admin/reports \
     -H "Authorization: Bearer <TOKEN>"
   ```

5. **Download do relatorio**:
   ```bash
   curl -o relatorio.pdf \
     http://localhost:8088/api/v1/admin/reports/<REPORT_ID>/download \
     -H "Authorization: Bearer <TOKEN>"
   ```

6. **Listar agendamentos**:
   ```bash
   curl http://localhost:8088/api/v1/admin/reports/schedules \
     -H "Authorization: Bearer <TOKEN>"
   ```

7. **Ativar/desativar agendamento**:
   ```bash
   curl -X PATCH http://localhost:8088/api/v1/admin/reports/schedules/<ID> \
     -H "Authorization: Bearer <TOKEN>" \
     -H "Content-Type: application/json" \
     -d '{"active": false}'
   ```

### 12.2 Checklist de verificacao

- [ ] `go build ./...` compila sem erros
- [ ] `ReportSchedules()` e `GeneratedReports()` adicionados em `mongo.go`
- [ ] Indexes criados em `EnsureIndexes()` (incluindo TTL de 90 dias)
- [ ] Rotas registradas em `router.go`
- [ ] `go reportScheduleChecker()` adicionado em `main.go`
- [ ] CRUD de agendamentos funciona (criar, listar, atualizar, toggle, deletar)
- [ ] Geracao sob demanda funciona (PDF e CSV)
- [ ] Download do relatorio retorna o arquivo correto com headers adequados
- [ ] Email com anexo e recebido corretamente via Resend
- [ ] Background job processa agendamentos com `next_run_at` no passado
- [ ] `next_run_at` e atualizado corretamente apos cada execucao
- [ ] TTL de 90 dias funciona na collection `generated_reports`
- [ ] Frontend exibe as 3 abas corretamente (Gerar, Agendamentos, Historico)
- [ ] Credenciais IG/Meta Ads ausentes nao causam crash (graceful skip)

### 12.3 Verificacao no MongoDB

```javascript
// Verificar agendamentos
db.report_schedules.find({ active: true })

// Verificar relatorios gerados (sem file_data para leitura rapida)
db.generated_reports.find({}, { file_data: 0 }).sort({ generated_at: -1 }).limit(5)

// Verificar indexes
db.report_schedules.getIndexes()
db.generated_reports.getIndexes()

// Verificar TTL index
db.generated_reports.getIndexes().forEach(idx => {
  if (idx.expireAfterSeconds !== undefined) print(JSON.stringify(idx))
})
```
