# Arquitetura do Backend - Imperium API

## Padrão de Desenvolvimento

Este documento define os padrões que **DEVEM** ser seguidos em todo o desenvolvimento do backend.

---

## Estrutura de Pastas (Clean Architecture)

```
backend/
├── cmd/
│   └── api/
│       └── main.go              # Entry point (apenas bootstrap)
├── internal/
│   ├── config/
│   │   └── config.go            # Variáveis de ambiente
│   ├── database/
│   │   └── mongo.go             # Conexão e collections
│   ├── models/
│   │   ├── user.go              # User + Profile + Auth DTOs
│   │   ├── transaction.go       # Transaction + DTOs
│   │   ├── debt.go              # Debt + DTOs
│   │   └── schedule.go          # Schedule + DTOs
│   ├── handlers/
│   │   ├── auth.go              # Register, Login, Me
│   │   ├── profile.go           # Profile CRUD
│   │   ├── transaction.go       # Transaction CRUD
│   │   ├── debt.go              # Debt CRUD
│   │   └── schedule.go          # Schedule CRUD
│   ├── middleware/
│   │   ├── auth.go              # JWT validation
│   │   ├── cors.go              # CORS headers
│   │   ├── json.go              # Content-Type JSON
│   │   └── logger.go            # Request logging
│   └── router/
│       └── router.go            # Todas as rotas
├── docs/                        # Swagger gerado (swag init)
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
├── Makefile
└── ARCHITECTURE.md              # Este arquivo
```

---

## Regras de Código

### 1. Models (`internal/models/`)

Cada model segue este padrão:

```go
package models

// Entity principal (armazenada no MongoDB)
type Transaction struct {
    ID          primitive.ObjectID `json:"id" bson:"_id,omitempty"`
    UserID      primitive.ObjectID `json:"user_id" bson:"user_id"`     // OBRIGATÓRIO em todas as entidades
    // ... campos específicos
    CreatedAt   time.Time          `json:"created_at" bson:"created_at"`
}

// Request para criar
type CreateTransactionRequest struct {
    // Campos sem ID, UserID, CreatedAt (gerados automaticamente)
}

// Request para atualizar
type UpdateTransactionRequest struct {
    // Campos opcionais (ponteiros ou omitempty)
}

// Response customizado (se necessário)
type TransactionResponse struct {
    // Campos públicos
}
```

**Regras:**
- Toda entidade tem `UserID` para multi-tenancy
- JSON tags em camelCase: `json:"userId"`
- BSON tags em snake_case: `bson:"user_id"`
- Senhas nunca expostas: `json:"-"`

### 2. Handlers (`internal/handlers/`)

Cada handler segue este padrão:

```go
package handlers

// GetTransactions godoc
// @Summary Lista todas as transações
// @Description Retorna transações do usuário autenticado
// @Tags transactions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {array} models.Transaction
// @Failure 401 {string} string "Unauthorized"
// @Router /transactions [get]
func GetTransactions(w http.ResponseWriter, r *http.Request) {
    // 1. Extrair userID do contexto (middleware auth já validou)
    userID := r.Context().Value("userID").(primitive.ObjectID)

    // 2. Contexto com timeout
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    // 3. Query SEMPRE filtra por userID
    cursor, err := database.Transactions().Find(ctx, bson.M{"user_id": userID})

    // 4. Tratamento de erro padronizado
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // 5. Resposta JSON
    json.NewEncoder(w).Encode(result)
}
```

**Regras:**
- Swagger annotations em TODOS os handlers
- SEMPRE extrair `userID` do contexto
- SEMPRE filtrar por `user_id` nas queries
- Timeout de 10s em operações de banco
- Erros retornam HTTP status codes corretos

### 3. Middleware (`internal/middleware/`)

```go
package middleware

// Auth valida JWT e injeta userID no contexto
func Auth(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // 1. Extrair token do header Authorization
        // 2. Validar JWT
        // 3. Injetar userID no contexto
        // 4. Chamar next.ServeHTTP(w, r)
    })
}
```

### 4. Router (`internal/router/`)

```go
package router

func New() http.Handler {
    mux := http.NewServeMux()

    // Rotas PÚBLICAS (sem auth)
    mux.HandleFunc("POST /api/v1/auth/register", handlers.Register)
    mux.HandleFunc("POST /api/v1/auth/login", handlers.Login)
    mux.HandleFunc("GET /api/v1/health", handlers.Health)
    mux.HandleFunc("/swagger/", httpSwagger.WrapHandler)

    // Rotas PROTEGIDAS (com auth)
    // Usar sub-router ou aplicar middleware individualmente

    // Middlewares globais
    var handler http.Handler = mux
    handler = middleware.JSON(handler)
    handler = middleware.CORS(handler)
    handler = middleware.Logger(handler)

    return handler
}
```

---

## Endpoints Padrão

Toda entidade segue este padrão CRUD:

```
GET    /api/v1/{entity}           # Listar (filtrado por user)
GET    /api/v1/{entity}/{id}      # Buscar por ID (verificar ownership)
POST   /api/v1/{entity}           # Criar (associar ao user)
PUT    /api/v1/{entity}/{id}      # Atualizar (verificar ownership)
DELETE /api/v1/{entity}/{id}      # Deletar (verificar ownership)
```

**Exceções:**
- Auth: `/api/v1/auth/*` (público)
- Profile: `/api/v1/profile` (sem ID, usa userID do token)

---

## Autenticação

### JWT Claims
```go
type Claims struct {
    UserID string `json:"user_id"`
    Email  string `json:"email"`
    jwt.RegisteredClaims
}
```

### Header
```
Authorization: Bearer <token>
```

### Swagger Security
```go
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Enter "Bearer {token}"
```

---

## Respostas de Erro

| Status | Quando usar |
|--------|-------------|
| 400 | Request inválido (JSON malformado, campos faltando) |
| 401 | Não autenticado (token ausente ou inválido) |
| 403 | Não autorizado (tentando acessar recurso de outro user) |
| 404 | Recurso não encontrado |
| 409 | Conflito (email já existe, etc) |
| 500 | Erro interno do servidor |

---

## Variáveis de Ambiente

| Variável | Descrição | Default |
|----------|-----------|---------|
| `MONGO_URI` | Connection string MongoDB | `mongodb://localhost:27017` |
| `DB_NAME` | Nome do banco | `imperium` |
| `PORT` | Porta da API | `8080` |
| `JWT_SECRET` | Chave secreta para JWT | `change-me-in-production` |
| `JWT_EXPIRY` | Tempo de expiração do token | `168h` (7 dias) |

---

## Comandos

```bash
# Desenvolvimento
make docker-up          # Subir todos os serviços
make docker-down        # Parar todos os serviços
make logs               # Ver logs da API

# Swagger
swag init -g cmd/api/main.go -o docs   # Regenerar docs

# Testes
curl -X POST http://localhost:8080/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"email":"test@test.com","password":"123456","name":"Test"}'
```

---

## Checklist para Novos Endpoints

- [ ] Model criado em `internal/models/`
- [ ] Model tem `UserID` field
- [ ] Handler criado em `internal/handlers/`
- [ ] Handler tem Swagger annotations
- [ ] Handler filtra por `userID` do contexto
- [ ] Rota adicionada em `internal/router/`
- [ ] Rota usa middleware Auth (se protegida)
- [ ] Swagger regenerado (`swag init`)
- [ ] Testado manualmente
