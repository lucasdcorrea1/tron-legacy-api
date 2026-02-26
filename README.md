# Tron Legacy API

Backend API em Go com autenticacao JWT e gerenciamento de usuarios.

## Stack

| Componente | Tecnologia |
|------------|------------|
| API | Go 1.24 |
| Database | MongoDB 7 |
| Cache | Redis 7 |
| Monitoramento | Prometheus + Grafana + Loki |

## Quick Start

```bash
docker-compose up -d
```

### Rebuild (apos alteracoes no codigo)

```bash
docker-compose down --rmi local && docker-compose up -d --build
```

## URLs

| Servico | URL |
|---------|-----|
| API | http://localhost:8088 |
| Swagger | http://localhost:8088/swagger/ |
| Health | http://localhost:8088/api/v1/health |
| Metrics | http://localhost:8088/metrics |
| Mongo Express | http://localhost:8082 |
| Grafana | http://localhost:3003 |
| Prometheus | http://localhost:9092 |

## Endpoints

### Auth

```
POST /api/v1/auth/register   # Criar conta
POST /api/v1/auth/login      # Login
GET  /api/v1/auth/me         # Usuario logado
```

### Profile

```
GET  /api/v1/profile         # Ver perfil
PUT  /api/v1/profile         # Atualizar perfil
POST /api/v1/profile/avatar  # Upload avatar
```

## Variaveis de Ambiente

| Variavel | Default |
|----------|---------|
| MONGO_URI | mongodb://mongo:27017/tron_legacy |
| REDIS_URL | redis://redis:6379 |
| PORT | 8080 |
| JWT_SECRET | (obrigatorio) |
| JWT_EXPIRY | 168h |

## Desenvolvimento

### Rodar localmente (sem Docker)

```bash
# Requer MongoDB e Redis rodando
go run cmd/api/main.go
```

### Regenerar Swagger

```bash
go install github.com/swaggo/swag/cmd/swag@latest
swag init -g cmd/api/main.go -o docs
```

### Build

```bash
go build -o bin/api ./cmd/api
```

### Logs

```bash
docker-compose logs -f api
```

## Credenciais (Dev)

| Servico | Usuario | Senha |
|---------|---------|-------|
| Grafana | admin | tron123 |
