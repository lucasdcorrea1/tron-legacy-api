package config

import (
	"net/url"
	"os"
	"strconv"
	"strings"
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
	EncryptionKey       string
	WebhookVerifyToken  string
	MetaAppID           string
	MetaAppSecret       string
	MetaAdsAccountID    string
	MetaAdsAccessToken  string
	AsaasAPIKey             string
	AsaasSandbox            bool
	AsaasWebhookToken       string
	ContabilAPIURL          string
	BillingGracePeriodDays  int
	BillingSyncIntervalMins int
	ContaAzulClientID       string
	ContaAzulClientSecret   string
	ContaAzulAuthURL        string
	ContaAzulTokenURL       string
	ContaAzulAPIBaseURL     string
	AllowedHosts            []string
	RateLimitGlobalMax      int
	RateLimitGlobalWindow   time.Duration
	LokiCloudURL            string
	LokiCloudUser           string
	LokiCloudAPIKey         string
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

	rateLimitGlobalWindow, err := time.ParseDuration(getEnv("RATE_LIMIT_GLOBAL_WINDOW", "1m"))
	if err != nil {
		rateLimitGlobalWindow = time.Minute
	}

	frontendURL := getEnv("FRONTEND_URL", "https://whodo.com.br")

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
		FrontendURL:        frontendURL,
		InstagramAccountID: getEnv("INSTAGRAM_ACCOUNT_ID", ""),
		InstagramToken:     getEnv("INSTAGRAM_ACCESS_TOKEN", ""),
		EncryptionKey:      getEnv("ENCRYPTION_KEY", ""),
		WebhookVerifyToken: getEnv("WEBHOOK_VERIFY_TOKEN", ""),
		MetaAppID:          getEnv("META_APP_ID", ""),
		MetaAppSecret:      getEnv("META_APP_SECRET", ""),
		MetaAdsAccountID:   getEnv("META_ADS_ACCOUNT_ID", ""),
		MetaAdsAccessToken: getEnv("META_ADS_ACCESS_TOKEN", ""),
		AsaasAPIKey:             getEnv("ASAAS_API_KEY", ""),
		AsaasSandbox:            getEnv("ASAAS_SANDBOX", "true") == "true",
		AsaasWebhookToken:      getEnv("ASAAS_WEBHOOK_TOKEN", ""),
		ContabilAPIURL:          getEnv("CONTABIL_API_URL", "http://localhost:8089"),
		BillingGracePeriodDays:  parseIntEnv("BILLING_GRACE_PERIOD_DAYS", 5),
		BillingSyncIntervalMins: parseIntEnv("BILLING_SYNC_INTERVAL_MINS", 60),
		ContaAzulClientID:       getEnv("CONTA_AZUL_CLIENT_ID", ""),
		ContaAzulClientSecret:   getEnv("CONTA_AZUL_CLIENT_SECRET", ""),
		ContaAzulAuthURL:        getEnv("CONTA_AZUL_AUTH_URL", "https://auth.contaazul.com/oauth2/authorize"),
		ContaAzulTokenURL:       getEnv("CONTA_AZUL_TOKEN_URL", "https://auth.contaazul.com/oauth2/token"),
		ContaAzulAPIBaseURL:     getEnv("CONTA_AZUL_API_BASE_URL", "https://api-v2.contaazul.com"),
		AllowedHosts:            allowedHosts(frontendURL),
		RateLimitGlobalMax:      parseIntEnv("RATE_LIMIT_GLOBAL_MAX", 60),
		RateLimitGlobalWindow:   rateLimitGlobalWindow,
		LokiCloudURL:            getEnv("LOKI_CLOUD_URL", ""),
		LokiCloudUser:           getEnv("LOKI_CLOUD_USER", ""),
		LokiCloudAPIKey:         getEnv("LOKI_CLOUD_API_KEY", ""),
	}

	return cfg
}

// allowedHosts builds the Host-header allowlist: the frontend domain (from
// FRONTEND_URL) plus any extras from ALLOWED_HOSTS, plus localhost for dev.
func allowedHosts(frontendURL string) []string {
	hosts := map[string]bool{
		"localhost": true,
		"127.0.0.1": true,
	}

	if u, err := url.Parse(frontendURL); err == nil && u.Hostname() != "" {
		hosts[u.Hostname()] = true
	}

	for _, h := range strings.Split(getEnv("ALLOWED_HOSTS", ""), ",") {
		h = strings.TrimSpace(h)
		if h != "" {
			hosts[h] = true
		}
	}

	result := make([]string, 0, len(hosts))
	for h := range hosts {
		result = append(result, h)
	}
	return result
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

func parseIntEnv(key string, fallback int) int {
	v := getEnv(key, "")
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
