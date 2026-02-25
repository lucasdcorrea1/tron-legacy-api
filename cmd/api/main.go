package main

import (
	"log"
	"net/http"

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

	// Create router
	r := router.New()

	// Start server
	addr := ":" + cfg.Port
	log.Printf("Server starting on http://localhost%s", addr)
	log.Printf("Swagger UI: http://localhost%s/swagger/", addr)
	log.Printf("Health check: http://localhost%s/api/v1/health", addr)

	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
