package config

import (
	"os"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	MongoURI         string
	Port             string
	DBName           string
	JWTSecret        string
	JWTExpiry        time.Duration
	ResendAPIKey     string
	ResendAudienceID string
	FromEmail        string
	FrontendURL      string
}

var cfg *Config

func Load() *Config {
	// Load .env file if exists (ignored in production)
	godotenv.Load()

	expiry, err := time.ParseDuration(getEnv("JWT_EXPIRY", "168h"))
	if err != nil {
		expiry = 168 * time.Hour // 7 days default
	}

	cfg = &Config{
		MongoURI:         getEnv("MONGO_URI", "mongodb://localhost:27017"),
		Port:             getEnv("PORT", "8080"),
		DBName:           getEnv("DB_NAME", "tron_legacy"),
		JWTSecret:        getEnv("JWT_SECRET", "change-me-in-production"),
		JWTExpiry:        expiry,
		ResendAPIKey:     getEnv("RESEND_API_KEY", ""),
		ResendAudienceID: getEnv("RESEND_AUDIENCE_ID", ""),
		FromEmail:        getEnv("FROM_EMAIL", "noreply@whodo.com.br"),
		FrontendURL:      getEnv("FRONTEND_URL", "https://whodo.com.br"),
	}

	return cfg
}

// Get returns the current config (must call Load first)
func Get() *Config {
	return cfg
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
