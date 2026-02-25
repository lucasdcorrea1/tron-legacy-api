.PHONY: run build docker-up docker-down clean

# Run locally (requires MongoDB running)
run:
	go run ./cmd/api

# Build binary
build:
	go build -o bin/api ./cmd/api

# Start all services with Docker
docker-up:
	DOCKER_BUILDKIT=0 docker-compose up --build

# Start in background
docker-up-d:
	docker-compose up -d --build

# Stop all services
docker-down:
	docker-compose down

# Stop and remove volumes
docker-clean:
	docker-compose down -v

# View logs
logs:
	docker-compose logs -f api

# Download dependencies
deps:
	go mod download
	go mod tidy

# Clean build artifacts
clean:
	rm -rf bin/
