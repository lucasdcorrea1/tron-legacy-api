package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/tron-legacy/api/internal/config"
	"github.com/tron-legacy/api/internal/crypto"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/handlers"
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

	// Initialize encryption (optional — needed for per-user Instagram config)
	if cfg.EncryptionKey != "" {
		if err := crypto.Init(); err != nil {
			log.Fatalf("Failed to initialize encryption: %v", err)
		}
		log.Println("Encryption initialized (per-user Instagram config enabled)")
	} else {
		log.Println("ENCRYPTION_KEY not set — per-user Instagram config disabled, using env vars only")
	}

	// Create router
	r := router.New()

	// ── Register background jobs ──────────────────────────────────
	handlers.RegisterJob("instagram_scheduler", "Instagram Scheduler", "Publica posts agendados do Instagram", "1 min", handlers.ProcessScheduledInstagramPosts)
	handlers.RegisterJob("facebook_scheduler", "Facebook Scheduler", "Publica posts agendados do Facebook", "1 min", handlers.ProcessScheduledFacebookPosts)
	handlers.RegisterJob("meta_ads_budget", "Meta Ads Budget Checker", "Verifica alertas de orçamento do Meta Ads", "15 min", handlers.CheckBudgetAlerts)
	handlers.RegisterJob("auto_boost", "Auto-Boost Processor", "Avalia posts e cria campanhas automáticas", "5 min", handlers.ProcessAutoBoosts)
	handlers.RegisterJob("integrated_publish", "Integrated Publish", "Processa publicações integradas agendadas", "1 min", handlers.ProcessScheduledIntegratedPublishes)
	handlers.RegisterJob("billing_grace", "Billing Grace Enforcer", "Rebaixa assinaturas inadimplentes após período de graça", "10 min", handlers.ProcessBillingGracePeriod)
	handlers.RegisterJob("billing_sync", "Billing Asaas Sync", "Sincroniza estado das assinaturas com Asaas", fmt.Sprintf("%d min", cfg.BillingSyncIntervalMins), handlers.SyncBillingWithAsaas)

	// ── Start background schedulers ───────────────────────────────
	if selfURL := os.Getenv("RENDER_EXTERNAL_URL"); selfURL != "" {
		go keepAlive(selfURL + "/api/v1/health")
	}
	go instagramScheduler()
	go facebookScheduler()
	go metaAdsBudgetChecker()
	go autoBoostProcessor()
	go integratedPublishScheduler()
	go billingGraceEnforcer()
	go billingAsaasSync()

	// Start server
	addr := ":" + cfg.Port
	log.Printf("Server starting on http://localhost%s", addr)
	log.Printf("Swagger UI: http://localhost%s/swagger/", addr)
	log.Printf("Health check: http://localhost%s/api/v1/health", addr)

	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// instagramScheduler runs every minute and processes due scheduled Instagram posts.
func instagramScheduler() {
	time.Sleep(15 * time.Second)
	log.Println("Instagram scheduler started (1 min interval)")
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		handlers.RunJobWithTracking("instagram_scheduler")
	}
}

func facebookScheduler() {
	time.Sleep(18 * time.Second)
	log.Println("Facebook scheduler started (1 min interval)")
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		handlers.RunJobWithTracking("facebook_scheduler")
	}
}

func metaAdsBudgetChecker() {
	time.Sleep(30 * time.Second)
	log.Println("Meta Ads budget checker started (15 min interval)")
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		handlers.RunJobWithTracking("meta_ads_budget")
	}
}

func integratedPublishScheduler() {
	time.Sleep(20 * time.Second)
	log.Println("Integrated publish scheduler started (1 min interval)")
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		handlers.RunJobWithTracking("integrated_publish")
	}
}

func autoBoostProcessor() {
	time.Sleep(45 * time.Second)
	log.Println("Auto-Boost processor started (5 min interval)")
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		handlers.RunJobWithTracking("auto_boost")
	}
}

func billingGraceEnforcer() {
	time.Sleep(60 * time.Second)
	log.Println("Billing grace period enforcer started (10 min interval)")
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		handlers.RunJobWithTracking("billing_grace")
	}
}

func billingAsaasSync() {
	cfg := config.Get()
	interval := time.Duration(cfg.BillingSyncIntervalMins) * time.Minute
	if interval < 10*time.Minute {
		interval = 60 * time.Minute
	}
	time.Sleep(90 * time.Second)
	log.Printf("Billing Asaas sync started (%v interval)", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		handlers.RunJobWithTracking("billing_sync")
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
