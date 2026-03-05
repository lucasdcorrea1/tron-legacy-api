package config

import (
	"os"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	MongoURI            string
	Port                string
	DBName              string
	JWTSecret           string
	AccessTokenExpiry   time.Duration
	RefreshTokenExpiry  time.Duration
	ResendAPIKey        string
	ResendAudienceID    string
	FromEmail           string
	FrontendURL         string
	InstagramAccountID  string
	InstagramToken      string
}

var cfg *Config

func Load() *Config {
	// Load .env file if exists (ignored in production)
	godotenv.Load()

	accessExpiry, err := time.ParseDuration(getEnv("ACCESS_TOKEN_EXPIRY", "15m"))
	if err != nil {
		accessExpiry = 15 * time.Minute
	}

	refreshExpiry, err := time.ParseDuration(getEnv("REFRESH_TOKEN_EXPIRY", "720h"))
	if err != nil {
		refreshExpiry = 720 * time.Hour // 30 days
	}

	cfg = &Config{
		MongoURI:           getEnv("MONGO_URI", "mongodb://localhost:27017"),
		Port:               getEnv("PORT", "8080"),
		DBName:             getEnv("DB_NAME", "tron_legacy"),
		JWTSecret:          getEnv("JWT_SECRET", "change-me-in-production"),
		AccessTokenExpiry:  accessExpiry,
		RefreshTokenExpiry: refreshExpiry,
		ResendAPIKey:     getEnv("RESEND_API_KEY", ""),
		ResendAudienceID: getEnv("RESEND_AUDIENCE_ID", ""),
		FromEmail:        getEnv("FROM_EMAIL", "noreply@whodo.com.br"),
		FrontendURL:        getEnv("FRONTEND_URL", "https://whodo.com.br"),
		InstagramAccountID: getEnv("INSTAGRAM_ACCOUNT_ID", ""),
		InstagramToken:     getEnv("INSTAGRAM_ACCESS_TOKEN", ""),
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
