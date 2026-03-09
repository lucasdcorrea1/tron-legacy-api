# Auto-Boost de Conteudo

## 1. Visao Geral

### O que e

O Auto-Boost de Conteudo e um sistema automatizado que monitora posts do Instagram e, quando um post atinge metricas pre-definidas pelo usuario (likes, comentarios, taxa de engajamento), cria automaticamente uma campanha de anuncios no Meta Ads para impulsionar aquele conteudo.

### Por que

Conteudo organico que ja demonstra alta performance tem maior probabilidade de performar bem como anuncio pago. A automacao elimina o atraso entre a deteccao de um post viral e a criacao manual da campanha, capturando o momentum no momento ideal.

### Valor de Negocio

- **Reducao de tempo**: Elimina o processo manual de criar campanha + adset + ad creative + ad para cada post de destaque
- **Captura de momentum**: Posts virais sao impulsionados em minutos, nao horas
- **Otimizacao de gasto**: So investe em conteudo que ja provou engajamento organico
- **Escalabilidade**: Funciona 24/7 sem intervencao humana

---

## 2. Arquitetura

```
Fluxo de dados:

[Instagram API]                    [Meta Ads API]
      |                                  ^
      | GET /{ig_id}/media               | POST /act_{id}/campaigns
      | (a cada 5 min)                   | POST /act_{id}/adsets
      v                                  | POST /act_{id}/adcreatives
[ProcessAutoBoosts()]                    | POST /act_{id}/ads
      |                                  |
      | 1. Busca regras ativas           |
      | 2. Busca posts recentes          |
      | 3. Verifica threshold            |
      | 4. Verifica cooldown             |
      | 5. Cria campanha completa -------+
      | 6. Registra log
      v
[MongoDB]
  - auto_boost_rules  (configuracoes do usuario)
  - auto_boost_logs   (historico de boosts executados)

[Frontend React]
      |
      | CRUD regras via API REST
      | Visualiza historico de boosts
      v
[Go API Handlers]
      |
      | /api/v1/admin/auto-boost/rules    (CRUD)
      | /api/v1/admin/auto-boost/logs     (leitura)
      v
[MongoDB collections]
```

---

## 3. Models (Go)

### AutoBoostRule

Arquivo: `internal/models/auto_boost.go`

```go
package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// AutoBoostRule defines the criteria and settings for automatically
// boosting Instagram posts that exceed performance thresholds.
type AutoBoostRule struct {
	ID        primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID    primitive.ObjectID `json:"user_id" bson:"user_id"`
	Name      string             `json:"name" bson:"name"`
	Active    bool               `json:"active" bson:"active"`

	// Metrica monitorada: "likes", "comments", "engagement_rate"
	Metric    string  `json:"metric" bson:"metric"`
	// Valor minimo para disparar o boost
	Threshold float64 `json:"threshold" bson:"threshold"`

	// Configuracoes de orcamento
	DailyBudget  int64 `json:"daily_budget" bson:"daily_budget"`   // em centavos (ex: 2000 = R$20,00)
	DurationDays int   `json:"duration_days" bson:"duration_days"` // quantos dias o anuncio roda

	// Targeting do ad set
	Targeting AdSetTargeting `json:"targeting" bson:"targeting"`

	// Objetivo da campanha Meta Ads
	Objective        string `json:"objective" bson:"objective"`                 // ex: "OUTCOME_ENGAGEMENT", "OUTCOME_AWARENESS"
	OptimizationGoal string `json:"optimization_goal" bson:"optimization_goal"` // ex: "POST_ENGAGEMENT", "REACH"
	BillingEvent     string `json:"billing_event" bson:"billing_event"`         // ex: "IMPRESSIONS"

	// Template do criativo — CTA e link opcionais para o ad
	CallToAction string `json:"call_to_action,omitempty" bson:"call_to_action,omitempty"` // ex: "LEARN_MORE"
	LinkURL      string `json:"link_url,omitempty" bson:"link_url,omitempty"`

	// Cooldown — evita boost duplicado
	CooldownHours int `json:"cooldown_hours" bson:"cooldown_hours"` // horas entre boosts do mesmo post (padrao: 72)

	// Filtro de idade do post — so avalia posts publicados nas ultimas N horas
	MaxPostAgeHours int `json:"max_post_age_hours" bson:"max_post_age_hours"` // padrao: 48

	CreatedAt time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
}

// CreateAutoBoostRuleRequest e o body para criar uma regra.
type CreateAutoBoostRuleRequest struct {
	Name             string         `json:"name"`
	Metric           string         `json:"metric"`
	Threshold        float64        `json:"threshold"`
	DailyBudget      int64          `json:"daily_budget"`
	DurationDays     int            `json:"duration_days"`
	Targeting        AdSetTargeting `json:"targeting"`
	Objective        string         `json:"objective"`
	OptimizationGoal string         `json:"optimization_goal"`
	BillingEvent     string         `json:"billing_event"`
	CallToAction     string         `json:"call_to_action,omitempty"`
	LinkURL          string         `json:"link_url,omitempty"`
	CooldownHours    int            `json:"cooldown_hours,omitempty"`
	MaxPostAgeHours  int            `json:"max_post_age_hours,omitempty"`
}

// UpdateAutoBoostRuleRequest e o body para atualizar uma regra.
type UpdateAutoBoostRuleRequest struct {
	Name             *string         `json:"name,omitempty"`
	Active           *bool           `json:"active,omitempty"`
	Metric           *string         `json:"metric,omitempty"`
	Threshold        *float64        `json:"threshold,omitempty"`
	DailyBudget      *int64          `json:"daily_budget,omitempty"`
	DurationDays     *int            `json:"duration_days,omitempty"`
	Targeting        *AdSetTargeting `json:"targeting,omitempty"`
	Objective        *string         `json:"objective,omitempty"`
	OptimizationGoal *string         `json:"optimization_goal,omitempty"`
	BillingEvent     *string         `json:"billing_event,omitempty"`
	CallToAction     *string         `json:"call_to_action,omitempty"`
	LinkURL          *string         `json:"link_url,omitempty"`
	CooldownHours    *int            `json:"cooldown_hours,omitempty"`
	MaxPostAgeHours  *int            `json:"max_post_age_hours,omitempty"`
}
```

### AutoBoostLog

```go
// AutoBoostLog registra cada execucao de boost automatico.
type AutoBoostLog struct {
	ID             primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	RuleID         primitive.ObjectID `json:"rule_id" bson:"rule_id"`
	RuleName       string             `json:"rule_name" bson:"rule_name"`
	UserID         primitive.ObjectID `json:"user_id" bson:"user_id"`

	// Post do Instagram que foi impulsionado
	IGMediaID      string `json:"ig_media_id" bson:"ig_media_id"`
	IGPermalink    string `json:"ig_permalink" bson:"ig_permalink"`
	IGMediaType    string `json:"ig_media_type" bson:"ig_media_type"` // "IMAGE", "VIDEO", "CAROUSEL_ALBUM"
	IGCaption      string `json:"ig_caption,omitempty" bson:"ig_caption,omitempty"`

	// Metrica que disparou o boost
	Metric         string  `json:"metric" bson:"metric"`
	MetricValue    float64 `json:"metric_value" bson:"metric_value"`
	Threshold      float64 `json:"threshold" bson:"threshold"`

	// IDs dos objetos criados no Meta Ads
	MetaCampaignID string `json:"meta_campaign_id,omitempty" bson:"meta_campaign_id,omitempty"`
	MetaAdSetID    string `json:"meta_adset_id,omitempty" bson:"meta_adset_id,omitempty"`
	MetaCreativeID string `json:"meta_creative_id,omitempty" bson:"meta_creative_id,omitempty"`
	MetaAdID       string `json:"meta_ad_id,omitempty" bson:"meta_ad_id,omitempty"`

	// Orcamento aplicado
	DailyBudget    int64  `json:"daily_budget" bson:"daily_budget"`
	DurationDays   int    `json:"duration_days" bson:"duration_days"`

	// Status do boost: "success", "failed", "skipped_cooldown"
	Status         string `json:"status" bson:"status"`
	ErrorMessage   string `json:"error_message,omitempty" bson:"error_message,omitempty"`

	CreatedAt      time.Time `json:"created_at" bson:"created_at"`
}
```

### Valores validos para `Metric`

| Valor              | Descricao                                            | Calculo                                      |
|--------------------|------------------------------------------------------|-----------------------------------------------|
| `likes`            | Numero absoluto de likes                              | `like_count` do media                        |
| `comments`         | Numero absoluto de comentarios                        | `comments_count` do media                    |
| `engagement_rate`  | Taxa de engajamento (%)                              | `(likes + comments) / followers_count * 100` |

---

## 4. Database

### Collections

#### `auto_boost_rules`

Armazena as regras configuradas pelo usuario. Cada regra define uma combinacao de metrica, threshold e configuracao de campanha.

Acessor em `internal/database/mongo.go`:

```go
func AutoBoostRules() *mongo.Collection {
	return DB.Collection("auto_boost_rules")
}
```

#### `auto_boost_logs`

Registra cada execucao (bem-sucedida ou nao) do auto-boost. Serve como historico e auditoria.

Acessor em `internal/database/mongo.go`:

```go
func AutoBoostLogs() *mongo.Collection {
	return DB.Collection("auto_boost_logs")
}
```

### Indexes

Adicionar em `database.EnsureIndexes()`:

```go
// auto_boost_rules: compound index on {user_id, active} for active rules lookup
_, err = AutoBoostRules().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "active", Value: 1}},
})
if err != nil {
	return err
}

// auto_boost_logs: compound index on {rule_id, ig_media_id} for cooldown check
_, err = AutoBoostLogs().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "rule_id", Value: 1}, {Key: "ig_media_id", Value: 1}},
})
if err != nil {
	return err
}

// auto_boost_logs: index on {user_id, created_at} for listing history
_, err = AutoBoostLogs().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}},
})
if err != nil {
	return err
}

// auto_boost_logs: TTL index — auto-delete logs after 180 days
_, err = AutoBoostLogs().Indexes().CreateOne(ctx, mongo.IndexModel{
	Keys:    bson.D{{Key: "created_at", Value: 1}},
	Options: options.Index().SetExpireAfterSeconds(180 * 24 * 3600),
})
if err != nil {
	return err
}
```

---

## 5. Handlers (Go)

Arquivo: `internal/handlers/auto_boost.go`

### ListAutoBoostRules

```go
func ListAutoBoostRules(w http.ResponseWriter, r *http.Request)
```

**Logica:**
1. Extrair `userID` via `middleware.GetUserID(r)` — retornar 401 se vazio
2. `ctx` com timeout de 5s
3. `database.AutoBoostRules().Find(ctx, bson.M{"user_id": userID})` ordenado por `created_at DESC`
4. Decodificar em `[]models.AutoBoostRule`
5. Se nil, retornar array vazio `[]`
6. Responder com JSON

### CreateAutoBoostRule

```go
func CreateAutoBoostRule(w http.ResponseWriter, r *http.Request)
```

**Logica:**
1. Extrair `userID` — retornar 401 se vazio
2. Decodificar body em `models.CreateAutoBoostRuleRequest`
3. Validar campos obrigatorios: `name`, `metric`, `threshold > 0`, `daily_budget > 0`, `duration_days > 0`, `objective`
4. Validar `metric` esta em `["likes", "comments", "engagement_rate"]`
5. Aplicar defaults:
   - `cooldown_hours`: se 0, usar 72
   - `max_post_age_hours`: se 0, usar 48
   - `billing_event`: se vazio, usar `"IMPRESSIONS"`
   - `optimization_goal`: se vazio, usar `"POST_ENGAGEMENT"`
6. Criar struct `AutoBoostRule` com `Active: true`, timestamps
7. `database.AutoBoostRules().InsertOne(ctx, rule)`
8. Responder 201 com a regra criada

### GetAutoBoostRule

```go
func GetAutoBoostRule(w http.ResponseWriter, r *http.Request)
```

**Logica:**
1. Extrair `userID` e `r.PathValue("id")` — converter para ObjectID
2. `database.AutoBoostRules().FindOne(ctx, bson.M{"_id": oid, "user_id": userID})`
3. Se nao encontrado, retornar 404
4. Responder com JSON

### UpdateAutoBoostRule

```go
func UpdateAutoBoostRule(w http.ResponseWriter, r *http.Request)
```

**Logica:**
1. Extrair `userID` e `r.PathValue("id")` — converter para ObjectID
2. Decodificar body em `models.UpdateAutoBoostRuleRequest`
3. Construir `bson.M{"$set": ...}` com campos nao-nil + `updated_at`
4. `database.AutoBoostRules().UpdateOne(ctx, bson.M{"_id": oid, "user_id": userID}, update)`
5. Se `MatchedCount == 0`, retornar 404
6. Responder com mensagem de sucesso

### ToggleAutoBoostRule

```go
func ToggleAutoBoostRule(w http.ResponseWriter, r *http.Request)
```

**Logica:**
1. Extrair `userID` e `r.PathValue("id")`
2. Decodificar body esperando `{"active": true/false}`
3. `UpdateOne` com `bson.M{"$set": bson.M{"active": *req.Active, "updated_at": time.Now()}}`
4. Se `MatchedCount == 0`, retornar 404
5. Responder com status atualizado

### DeleteAutoBoostRule

```go
func DeleteAutoBoostRule(w http.ResponseWriter, r *http.Request)
```

**Logica:**
1. Extrair `userID` e `r.PathValue("id")` — converter para ObjectID
2. `database.AutoBoostRules().DeleteOne(ctx, bson.M{"_id": oid, "user_id": userID})`
3. Se `DeletedCount == 0`, retornar 404
4. Responder com mensagem de sucesso

### ListAutoBoostLogs

```go
func ListAutoBoostLogs(w http.ResponseWriter, r *http.Request)
```

**Logica:**
1. Extrair `userID`
2. Ler query params opcionais: `rule_id`, `status`, `limit` (default 50, max 200)
3. Construir filtro: `bson.M{"user_id": userID}` + filtros opcionais
4. `database.AutoBoostLogs().Find(ctx, filter)` com `SetSort(bson.D{{Key: "created_at", Value: -1}})` e `SetLimit(limit)`
5. Responder com JSON

### ProcessAutoBoosts (Background Job)

```go
func ProcessAutoBoosts()
```

**Logica detalhada:**

```
1.  ctx com timeout de 120s (muitas chamadas externas)

2.  Buscar todas as regras ativas:
    database.AutoBoostRules().Find(ctx, bson.M{"active": true})

3.  Agrupar regras por user_id para evitar chamadas duplicadas ao Instagram

4.  Para cada user_id:
    a.  Obter credenciais Instagram:
        igCreds := getInstagramCredentials(ctx, userID)
        Se nil, pular (log warning)

    b.  Obter credenciais Meta Ads:
        adsCreds := getMetaAdsCredentials(ctx, userID)
        Se nil, pular (log warning)

    c.  Buscar posts recentes do Instagram:
        GET /{ig_account_id}/media?fields=id,caption,media_type,media_url,
            thumbnail_url,permalink,timestamp,like_count,comments_count
            &limit=25

    d.  (Se alguma regra usa "engagement_rate") Buscar followers_count:
        GET /{ig_account_id}?fields=followers_count

    e.  Para cada regra deste usuario:
        Para cada post retornado:

        i.   Verificar idade do post:
             Se timestamp do post > rule.MaxPostAgeHours atras, pular

        ii.  Calcular valor da metrica:
             - "likes": like_count
             - "comments": comments_count
             - "engagement_rate": (like_count + comments_count) / followers_count * 100

        iii. Se metricValue < rule.Threshold, pular

        iv.  Verificar cooldown:
             Buscar no auto_boost_logs se existe log com:
               rule_id == rule.ID
               AND ig_media_id == post.id
               AND status == "success"
               AND created_at > (now - rule.CooldownHours)
             Se existir, pular (registrar log com status "skipped_cooldown")

        v.   CRIAR CAMPANHA no Meta Ads:
             POST /act_{id}/campaigns
             params:
               name: "AutoBoost: {rule.Name} - {post.id}"
               objective: rule.Objective
               status: "PAUSED"
               special_ad_categories: "NONE"
             Salvar meta_campaign_id do resultado

        vi.  CRIAR AD SET:
             POST /act_{id}/adsets
             params:
               campaign_id: meta_campaign_id
               name: "AutoBoost AdSet - {post.id}"
               daily_budget: rule.DailyBudget
               billing_event: rule.BillingEvent
               optimization_goal: rule.OptimizationGoal
               targeting: JSON(rule.Targeting)
               status: "PAUSED"
               start_time: now (ISO 8601)
               end_time: now + rule.DurationDays (ISO 8601)
             Salvar meta_adset_id do resultado

        vii. CRIAR AD CREATIVE:
             POST /act_{id}/adcreatives
             params:
               name: "AutoBoost Creative - {post.id}"
               object_story_spec: {
                 "instagram_actor_id": igCreds.AccountID,
                 "source_instagram_media_id": post.id
               }
             Salvar meta_creative_id do resultado

             NOTA: Usar source_instagram_media_id permite usar o post
             original do Instagram diretamente como criativo do anuncio,
             sem necessidade de re-upload de midia.

        viii. CRIAR AD:
             POST /act_{id}/ads
             params:
               adset_id: meta_adset_id
               name: "AutoBoost Ad - {post.id}"
               creative: {"creative_id": meta_creative_id}
               status: "PAUSED"
             Salvar meta_ad_id

        ix.  ATIVAR CAMPANHA (mudar status para ACTIVE):
             POST /{meta_campaign_id}
             params:
               status: "ACTIVE"
             POST /{meta_adset_id}
             params:
               status: "ACTIVE"
             POST /{meta_ad_id}
             params:
               status: "ACTIVE"

        x.   REGISTRAR LOG de sucesso:
             database.AutoBoostLogs().InsertOne(ctx, AutoBoostLog{
               RuleID:         rule.ID,
               RuleName:       rule.Name,
               UserID:         userID,
               IGMediaID:      post.id,
               IGPermalink:    post.permalink,
               IGMediaType:    post.media_type,
               IGCaption:      post.caption (truncar em 200 chars),
               Metric:         rule.Metric,
               MetricValue:    metricValue,
               Threshold:      rule.Threshold,
               MetaCampaignID: meta_campaign_id,
               MetaAdSetID:    meta_adset_id,
               MetaCreativeID: meta_creative_id,
               MetaAdID:       meta_ad_id,
               DailyBudget:    rule.DailyBudget,
               DurationDays:   rule.DurationDays,
               Status:         "success",
               CreatedAt:      time.Now(),
             })

        xi.  Tambem salvar campanha/adset/ad no MongoDB local
             (seguindo o padrao existente em meta_ads.go):
             database.MetaAdsCampaigns().InsertOne(...)
             database.MetaAdsAdSets().InsertOne(...)
             database.MetaAdsAds().InsertOne(...)

5.  Em caso de ERRO em qualquer etapa (v-ix):
    - Registrar log com Status "failed" e ErrorMessage
    - slog.Error("auto_boost_error", ...)
    - Continuar para a proxima regra/post (nao abortar o loop)

6.  slog.Info("auto_boost_cycle_complete",
      "rules_processed", count,
      "boosts_created", successCount,
      "errors", errorCount)
```

---

## 6. Rotas API

Todas as rotas usam o pattern existente de middleware:

```go
middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.FuncName)))
```

| Metodo   | Path                                      | Handler                  | Descricao                        |
|----------|-------------------------------------------|--------------------------|----------------------------------|
| `GET`    | `/api/v1/admin/auto-boost/rules`          | `ListAutoBoostRules`     | Listar regras do usuario         |
| `POST`   | `/api/v1/admin/auto-boost/rules`          | `CreateAutoBoostRule`    | Criar nova regra                 |
| `GET`    | `/api/v1/admin/auto-boost/rules/{id}`     | `GetAutoBoostRule`       | Buscar regra por ID              |
| `PUT`    | `/api/v1/admin/auto-boost/rules/{id}`     | `UpdateAutoBoostRule`    | Atualizar regra                  |
| `PATCH`  | `/api/v1/admin/auto-boost/rules/{id}`     | `ToggleAutoBoostRule`    | Ativar/desativar regra           |
| `DELETE` | `/api/v1/admin/auto-boost/rules/{id}`     | `DeleteAutoBoostRule`    | Remover regra                    |
| `GET`    | `/api/v1/admin/auto-boost/logs`           | `ListAutoBoostLogs`      | Listar historico de boosts       |

### Registro no Router

Adicionar em `internal/router/router.go`:

```go
// Auto-Boost rules
mux.Handle("GET /api/v1/admin/auto-boost/rules", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListAutoBoostRules))))
mux.Handle("POST /api/v1/admin/auto-boost/rules", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.CreateAutoBoostRule))))
mux.Handle("GET /api/v1/admin/auto-boost/rules/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetAutoBoostRule))))
mux.Handle("PUT /api/v1/admin/auto-boost/rules/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.UpdateAutoBoostRule))))
mux.Handle("PATCH /api/v1/admin/auto-boost/rules/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ToggleAutoBoostRule))))
mux.Handle("DELETE /api/v1/admin/auto-boost/rules/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.DeleteAutoBoostRule))))

// Auto-Boost logs
mux.Handle("GET /api/v1/admin/auto-boost/logs", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListAutoBoostLogs))))
```

---

## 7. Background Jobs

### Configuracao em `cmd/api/main.go`

Adicionar a goroutine seguindo o padrao existente de `metaAdsBudgetChecker`:

```go
// Start Auto-Boost processor
go autoBoostProcessor()
```

```go
// autoBoostProcessor runs every 5 minutes and checks Instagram posts against boost rules.
func autoBoostProcessor() {
	// Wait for server to start
	time.Sleep(45 * time.Second)
	log.Println("Auto-Boost processor started (5 min interval)")

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		handlers.ProcessAutoBoosts()
	}
}
```

### Intervalo e Justificativa

- **5 minutos**: Balanco entre responsividade e rate limits da Meta API
- A Meta Graph API tem rate limit de ~200 chamadas/hora por token de usuario
- Com 25 posts e 1-2 regras ativas, cada ciclo usa ~5-10 chamadas (fetch media + checks)
- Criar uma campanha completa usa 4 chamadas extras (campaign + adset + creative + ad) + 3 ativacoes
- Total por boost: ~10 chamadas. Em 12 ciclos/hora, comporta ate ~15 boosts/hora com margem

### Tratamento de Erros

- **Erro ao obter credenciais**: Log warning, pula o usuario, continua com proximo
- **Erro na API do Instagram**: Log error, pula o usuario, continua
- **Erro na API do Meta Ads** (criacao de campanha/adset/ad): Log error, salva `AutoBoostLog` com `status: "failed"` e `error_message`, continua com proximo post/regra
- **Panic recovery**: Envolver o loop principal em `defer recover()` para evitar que um panic pare o ticker

```go
func ProcessAutoBoosts() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("auto_boost_panic_recovered", "panic", r)
		}
	}()

	// ... logica principal ...
}
```

---

## 8. Frontend

### Componentes

#### `AutoBoostPage.jsx`

Pagina principal com duas abas: **Regras** e **Historico**.

Localizacao: `src/pages/admin/AutoBoostPage.jsx`

```
AutoBoostPage
  |-- Tabs: [Regras | Historico]
  |
  |-- Tab "Regras":
  |     |-- Botao "Nova Regra" (abre modal/form)
  |     |-- Lista de cards de regras
  |     |     |-- RuleCard
  |     |           |-- Nome, metrica, threshold
  |     |           |-- Badge ativo/inativo (toggle switch)
  |     |           |-- Orcamento diario + duracao
  |     |           |-- Botoes: Editar, Excluir
  |     |
  |     |-- RuleFormModal (criar/editar)
  |           |-- Campo: Nome
  |           |-- Select: Metrica (Likes / Comentarios / Taxa de Engajamento)
  |           |-- Input: Threshold (numerico)
  |           |-- Input: Orcamento diario (R$, convertido para centavos)
  |           |-- Input: Duracao (dias)
  |           |-- Select: Objetivo (OUTCOME_ENGAGEMENT, OUTCOME_AWARENESS, etc.)
  |           |-- TargetingForm (reutilizar componente existente de Meta Ads)
  |           |-- Input: Cooldown (horas)
  |           |-- Input: Idade maxima do post (horas)
  |           |-- Botao: Salvar
  |
  |-- Tab "Historico":
        |-- Filtros: status (todos/sucesso/falha/cooldown), regra
        |-- Tabela de logs
              |-- Colunas: Data, Regra, Post (link), Metrica, Valor, Status, Campanha
              |-- Status com badge colorido:
              |     success = verde
              |     failed = vermelho
              |     skipped_cooldown = amarelo
              |-- Link para o post no Instagram (permalink)
              |-- Link para campanha no Meta Ads Manager (se success)
```

### API Service

Adicionar em `src/services/api.js`:

```javascript
export const autoBoost = {
  // Rules
  listRules: () => api.get('/api/v1/admin/auto-boost/rules'),
  getRule: (id) => api.get(`/api/v1/admin/auto-boost/rules/${id}`),
  createRule: (data) => api.post('/api/v1/admin/auto-boost/rules', data),
  updateRule: (id, data) => api.put(`/api/v1/admin/auto-boost/rules/${id}`, data),
  toggleRule: (id, active) => request(`/api/v1/admin/auto-boost/rules/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ active }),
  }),
  deleteRule: (id) => api.delete(`/api/v1/admin/auto-boost/rules/${id}`),

  // Logs
  listLogs: (params = {}) => {
    const query = new URLSearchParams();
    if (params.rule_id) query.append('rule_id', params.rule_id);
    if (params.status) query.append('status', params.status);
    if (params.limit) query.append('limit', params.limit);
    const qs = query.toString();
    return api.get(`/api/v1/admin/auto-boost/logs${qs ? `?${qs}` : ''}`);
  },
};
```

### Fluxo de UI

1. Usuario acessa `/admin/auto-boost` (rota protegida com role admin/superuser)
2. Tab "Regras" mostra todas as regras com toggle de ativo/inativo
3. Ao clicar "Nova Regra", modal com formulario completo abre
4. Formulario valida campos no frontend antes de enviar
5. Apos salvar, regra aparece na lista com badge "Ativo"
6. Tab "Historico" carrega ultimos 50 logs com paginacao lazy
7. Cada log mostra link clicavel para o post original no Instagram
8. Logs com status "success" mostram link para o Meta Ads Manager

---

## 9. APIs Externas

### Instagram Graph API

#### Buscar posts recentes da conta

```
GET https://graph.facebook.com/v21.0/{ig_account_id}/media
  ?fields=id,caption,media_type,media_url,thumbnail_url,permalink,timestamp,like_count,comments_count
  &limit=25
  &access_token={token}
```

**Resposta:**
```json
{
  "data": [
    {
      "id": "17895695668004550",
      "caption": "Nosso novo produto...",
      "media_type": "IMAGE",
      "media_url": "https://...",
      "permalink": "https://www.instagram.com/p/ABC123/",
      "timestamp": "2026-03-05T14:30:00+0000",
      "like_count": 245,
      "comments_count": 38
    }
  ],
  "paging": { ... }
}
```

#### Buscar followers_count (para engagement_rate)

```
GET https://graph.facebook.com/v21.0/{ig_account_id}
  ?fields=followers_count
  &access_token={token}
```

**Resposta:**
```json
{
  "followers_count": 15420,
  "id": "17841400123456789"
}
```

### Meta Ads API

#### Criar Campanha

```
POST https://graph.facebook.com/v21.0/act_{ad_account_id}/campaigns
  access_token={token}
  &name=AutoBoost: Regra Alta Performance - 17895695668004550
  &objective=OUTCOME_ENGAGEMENT
  &status=PAUSED
  &special_ad_categories=NONE
```

**Resposta:**
```json
{
  "id": "120330000000000001"
}
```

#### Criar Ad Set

```
POST https://graph.facebook.com/v21.0/act_{ad_account_id}/adsets
  access_token={token}
  &campaign_id=120330000000000001
  &name=AutoBoost AdSet - 17895695668004550
  &daily_budget=2000
  &billing_event=IMPRESSIONS
  &optimization_goal=POST_ENGAGEMENT
  &targeting={"geo_locations":{"countries":["BR"]},"age_min":18,"age_max":65}
  &status=PAUSED
  &start_time=2026-03-05T15:00:00-0300
  &end_time=2026-03-08T15:00:00-0300
```

**Resposta:**
```json
{
  "id": "120330000000000002"
}
```

#### Criar Ad Creative (usando post existente do Instagram)

```
POST https://graph.facebook.com/v21.0/act_{ad_account_id}/adcreatives
  access_token={token}
  &name=AutoBoost Creative - 17895695668004550
  &object_story_spec={"instagram_actor_id":"17841400123456789","source_instagram_media_id":"17895695668004550"}
```

**Resposta:**
```json
{
  "id": "120330000000000003"
}
```

> **Nota importante**: `source_instagram_media_id` permite usar o post original do Instagram diretamente como criativo. Isso preserva os likes/comentarios originais e nao requer upload de midia.

#### Criar Ad

```
POST https://graph.facebook.com/v21.0/act_{ad_account_id}/ads
  access_token={token}
  &adset_id=120330000000000002
  &name=AutoBoost Ad - 17895695668004550
  &creative={"creative_id":"120330000000000003"}
  &status=PAUSED
```

**Resposta:**
```json
{
  "id": "120330000000000004"
}
```

#### Ativar objetos (Campaign, AdSet, Ad)

```
POST https://graph.facebook.com/v21.0/{object_id}
  access_token={token}
  &status=ACTIVE
```

---

## 10. Codigo Reutilizado

### Funcoes existentes (em `internal/handlers/meta_ads.go`)

| Funcao                        | Uso no Auto-Boost                                                |
|-------------------------------|------------------------------------------------------------------|
| `getMetaAdsCredentials(ctx, userID)` | Obter token e ad_account_id para chamadas a Meta Ads API  |
| `requireMetaAdsCreds(w, r)`   | Usado nos handlers CRUD (nao no background job)                  |
| `metaGraphGet(endpoint, token, params)` | GET para buscar posts do Instagram e dados da conta     |
| `metaGraphPost(endpoint, token, params)` | POST para criar campaign, adset, creative, ad          |
| `adAccountPath(adAccountID)`  | Gerar path `/act_{id}` para endpoints do Meta Ads                |

### Funcoes existentes (em `internal/handlers/instagram.go`)

| Funcao                        | Uso no Auto-Boost                                                |
|-------------------------------|------------------------------------------------------------------|
| `getInstagramCredentials(ctx, userID)` | Obter AccountID e token do Instagram                    |

### Models existentes (em `internal/models/meta_ads.go`)

| Model                | Uso no Auto-Boost                                                     |
|----------------------|-----------------------------------------------------------------------|
| `AdSetTargeting`     | Reutilizado como tipo do campo `Targeting` em `AutoBoostRule`         |
| `GeoLocation`        | Sub-tipo dentro de `AdSetTargeting`                                   |
| `TargetEntity`       | Sub-tipo para interesses e audiences                                  |
| `MetaAdsCampaign`    | Struct para salvar campanha criada localmente                         |
| `MetaAdsAdSet`       | Struct para salvar adset criado localmente                            |
| `MetaAdsAd`          | Struct para salvar ad criado localmente                               |
| `AdCreative`         | Struct para o criativo do ad                                          |

### Models existentes (em `internal/models/instagram.go`)

| Model                | Uso no Auto-Boost                                              |
|----------------------|----------------------------------------------------------------|
| `InstagramConfig`    | Referenciado internamente por `getInstagramCredentials`        |

### Database accessors reutilizados

| Funcao                            | Uso                                     |
|-----------------------------------|-----------------------------------------|
| `database.MetaAdsCampaigns()`     | Salvar campanha criada pelo auto-boost  |
| `database.MetaAdsAdSets()`        | Salvar adset criado pelo auto-boost     |
| `database.MetaAdsAds()`           | Salvar ad criado pelo auto-boost        |

---

## 11. Fluxo Completo

### Jornada do Usuario

```
1.  CONFIGURACAO PREVIA (pre-requisitos):
    - Usuario ja configurou Instagram (GET /admin/instagram/config retorna configured: true)
    - Usuario ja configurou Meta Ads (GET /admin/meta-ads/config retorna configured: true)
    - Conta Instagram e conta de anuncios estao vinculadas ao mesmo Business ID

2.  CRIAR REGRA:
    - Usuario acessa /admin/auto-boost
    - Clica em "Nova Regra"
    - Preenche formulario:
      Nome: "Posts Virais"
      Metrica: "likes"
      Threshold: 100
      Orcamento: R$ 20,00/dia
      Duracao: 3 dias
      Objetivo: OUTCOME_ENGAGEMENT
      Targeting: Brasil, 18-55 anos
      Cooldown: 72 horas
    - Salva — regra fica ativa imediatamente

3.  MONITORAMENTO AUTOMATICO (a cada 5 minutos):
    - Background job busca posts do Instagram
    - Post "XYZ" tem 150 likes (acima do threshold de 100)
    - Verifica cooldown: post "XYZ" nunca foi boosted por esta regra
    - PROCEDE com boost

4.  CRIACAO AUTOMATICA DA CAMPANHA:
    - Cria campanha "AutoBoost: Posts Virais - XYZ"
    - Cria adset com R$ 20,00/dia, 3 dias, targeting Brasil 18-55
    - Cria creative usando o post original do Instagram
    - Cria ad vinculando creative ao adset
    - Ativa campanha, adset e ad
    - Registra log de sucesso

5.  VISUALIZACAO:
    - Usuario ve na tab "Historico" que post "XYZ" foi impulsionado
    - Badge verde "Sucesso" com link para o post e para a campanha
    - Campanha aparece tambem na listagem padrao de Meta Ads

6.  COOLDOWN:
    - Proximo ciclo (5 min depois): post "XYZ" ainda tem 150+ likes
    - Cooldown de 72h nao expirou — log com status "skipped_cooldown"
    - Nenhuma campanha duplicada e criada

7.  MONITORAMENTO CONTINUO:
    - 3 dias depois: campanha encerra automaticamente (end_time do adset)
    - Novo post "ABC" atinge 120 likes — novo boost e criado
```

---

## 12. Verificacao

### Testes End-to-End

#### Pre-requisitos

- [ ] Conta Instagram Business conectada com token valido
- [ ] Conta Meta Ads configurada com ad_account_id e token com permissoes `ads_management`, `instagram_basic`, `instagram_content_publish`
- [ ] Pelo menos 1 post no Instagram com metricas visiveis

#### Passo 1: Verificar CRUD de Regras

```bash
# Criar regra
curl -X POST http://localhost:8088/api/v1/admin/auto-boost/rules \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Teste Auto-Boost",
    "metric": "likes",
    "threshold": 5,
    "daily_budget": 500,
    "duration_days": 1,
    "objective": "OUTCOME_ENGAGEMENT",
    "optimization_goal": "POST_ENGAGEMENT",
    "billing_event": "IMPRESSIONS",
    "targeting": {
      "geo_locations": { "countries": ["BR"] },
      "age_min": 18,
      "age_max": 65
    },
    "cooldown_hours": 1,
    "max_post_age_hours": 720
  }'
# Esperado: 201 com a regra criada (incluindo id)

# Listar regras
curl http://localhost:8088/api/v1/admin/auto-boost/rules \
  -H "Authorization: Bearer $TOKEN"
# Esperado: array com a regra criada

# Toggle desativar
curl -X PATCH http://localhost:8088/api/v1/admin/auto-boost/rules/{ID} \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"active": false}'
# Esperado: 200

# Deletar
curl -X DELETE http://localhost:8088/api/v1/admin/auto-boost/rules/{ID} \
  -H "Authorization: Bearer $TOKEN"
# Esperado: 200
```

#### Passo 2: Testar Background Job Manualmente

Para testar sem esperar o ticker, chamar `ProcessAutoBoosts()` diretamente criando um endpoint temporario de debug (remover antes de producao):

```go
// TEMPORARIO — remover apos testes
mux.Handle("POST /api/v1/admin/auto-boost/process",
  middleware.Auth(middleware.RequireRole("superuser")(
    http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
      handlers.ProcessAutoBoosts()
      json.NewEncoder(w).Encode(map[string]string{"message": "Processing complete"})
    }),
  )),
)
```

```bash
# Disparar processamento manual
curl -X POST http://localhost:8088/api/v1/admin/auto-boost/process \
  -H "Authorization: Bearer $TOKEN"
```

#### Passo 3: Verificar Logs

```bash
# Listar logs
curl http://localhost:8088/api/v1/admin/auto-boost/logs \
  -H "Authorization: Bearer $TOKEN"
# Esperado: array com logs de "success", "failed" ou "skipped_cooldown"

# Filtrar por status
curl "http://localhost:8088/api/v1/admin/auto-boost/logs?status=success" \
  -H "Authorization: Bearer $TOKEN"
```

#### Passo 4: Verificar no Meta Ads Manager

1. Acessar [Meta Ads Manager](https://adsmanager.facebook.com/)
2. Verificar que a campanha "AutoBoost: ..." foi criada
3. Verificar que o status esta ACTIVE
4. Verificar que o creative usa o post original do Instagram
5. Verificar que o targeting corresponde ao configurado na regra
6. Verificar que o orcamento e a duracao estao corretos

#### Passo 5: Verificar Cooldown

1. Disparar o processamento novamente (Passo 2)
2. Verificar nos logs que o mesmo post recebeu status `skipped_cooldown`
3. Confirmar que nenhuma campanha duplicada foi criada

#### Passo 6: Verificar Tratamento de Erros

1. Criar regra com token invalido (alterar token no DB)
2. Disparar processamento
3. Verificar que log foi criado com `status: "failed"` e `error_message` descritivo
4. Verificar que o processo nao parou — outras regras/usuarios continuaram

### Checklist de Verificacao

- [ ] CRUD de regras funciona (criar, listar, buscar, atualizar, toggle, deletar)
- [ ] Validacao de campos obrigatorios retorna 400 com mensagem clara
- [ ] Background job executa a cada 5 minutos sem travar
- [ ] Posts que excedem threshold sao detectados corretamente
- [ ] Campanha completa e criada no Meta Ads (campaign + adset + creative + ad)
- [ ] Creative usa `source_instagram_media_id` (nao re-upload)
- [ ] Campanha e ativada automaticamente apos criacao
- [ ] Cooldown impede boost duplicado do mesmo post pela mesma regra
- [ ] Logs registram todas as acoes (success, failed, skipped_cooldown)
- [ ] Erros em um usuario/regra nao afetam processamento de outros
- [ ] Campanha criada aparece na listagem padrao de Meta Ads do dashboard
- [ ] Frontend exibe regras com toggle funcional
- [ ] Frontend exibe historico com filtros e links clicaveis
- [ ] TTL index limpa logs antigos apos 180 dias
