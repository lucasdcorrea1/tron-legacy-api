package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/tron-legacy/api/internal/config"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/router"

	_ "github.com/tron-legacy/api/docs"
)

// @title Tron Legacy API
// @version 1.0
// @description API de autenticacao e gerenciamento de usuarios
// @host localhost:8088
// @BasePath /api/v1

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Digite: Bearer {seu_token_aqui}
func main() {
	// Load configuration
	cfg := config.Load()

	// Connect to MongoDB
	if err := database.Connect(cfg.MongoURI, cfg.DBName); err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer database.Disconnect()

	// Ensure engagement indexes
	if err := database.EnsureIndexes(); err != nil {
		log.Printf("Warning: failed to ensure indexes: %v", err)
	}

	// Create router
	r := router.New()

	// Keep-alive: prevent Render free tier from sleeping
	if selfURL := os.Getenv("RENDER_EXTERNAL_URL"); selfURL != "" {
		go keepAlive(selfURL + "/api/v1/health")
	}

	// Start server
	addr := ":" + cfg.Port
	log.Printf("Server starting on http://localhost%s", addr)
	log.Printf("Swagger UI: http://localhost%s/swagger/", addr)
	log.Printf("Health check: http://localhost%s/api/v1/health", addr)

	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// keepAlive pings the health endpoint every 14 minutes to prevent Render free tier sleep.
func keepAlive(url string) {
	// Wait for server to start
	time.Sleep(10 * time.Second)
	log.Printf("Keep-alive started: pinging %s every 14 min", url)

	ticker := time.NewTicker(14 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		resp, err := http.Get(url)
		if err != nil {
			log.Printf("Keep-alive ping failed: %v", err)
			continue
		}
		resp.Body.Close()
		log.Printf("Keep-alive ping: %d", resp.StatusCode)
	}
}
