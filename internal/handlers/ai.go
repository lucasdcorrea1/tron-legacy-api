package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/crypto"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// maskAPIKey returns a masked version of the API key (e.g. "sk-ant-...xYz")
func maskAPIKey(key string) string {
	if len(key) > 12 {
		return key[:7] + "..." + key[len(key)-3:]
	}
	return "sk-ant-***"
}

// GetAIConfig returns whether AI is configured for the current org
func GetAIConfig(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var cfg models.AIConfig
	err := database.AIConfigs().FindOne(ctx, bson.M{"org_id": orgID}).Decode(&cfg)

	resp := models.AIConfigResponse{}
	if err == nil {
		// Decrypt key just to get the prefix for display
		if key, decErr := crypto.Decrypt(cfg.APIKeyEnc); decErr == nil {
			resp.Configured = true
			resp.Model = cfg.Model
			resp.KeyPrefix = maskAPIKey(key)
		}
	}

	json.NewEncoder(w).Encode(resp)
}

// SaveAIConfig saves or updates AI config for the current org
func SaveAIConfig(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	orgID := middleware.GetOrgID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if !crypto.Available() {
		http.Error(w, "Encryption not configured (ENCRYPTION_KEY missing)", http.StatusServiceUnavailable)
		return
	}

	var req models.SaveAIConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.APIKey == "" {
		http.Error(w, "api_key is required", http.StatusBadRequest)
		return
	}

	if !strings.HasPrefix(req.APIKey, "sk-ant-") {
		http.Error(w, "API key must start with sk-ant-", http.StatusBadRequest)
		return
	}

	model := req.Model
	if model == "" {
		model = "claude-sonnet-4-5-20250929"
	}

	encrypted, err := crypto.Encrypt(req.APIKey)
	if err != nil {
		slog.Error("ai_config_encrypt_error", "error", err)
		http.Error(w, "Failed to encrypt API key", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	now := time.Now()
	_, err = database.AIConfigs().UpdateOne(
		ctx,
		bson.M{"org_id": orgID},
		bson.M{
			"$set": bson.M{
				"user_id":     userID,
				"api_key_enc": encrypted,
				"model":       model,
				"updated_at":  now,
			},
			"$setOnInsert": bson.M{
				"org_id":     orgID,
				"created_at": now,
			},
		},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		slog.Error("ai_config_save_error", "error", err)
		http.Error(w, "Failed to save AI config", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(models.AIConfigResponse{
		Configured: true,
		Model:      model,
		KeyPrefix:  maskAPIKey(req.APIKey),
	})
}

// DeleteAIConfig removes AI config for the current org
func DeleteAIConfig(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := database.AIConfigs().DeleteOne(ctx, bson.M{"org_id": orgID})
	if err != nil {
		slog.Error("ai_config_delete_error", "error", err)
		http.Error(w, "Failed to delete AI config", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"deleted": true})
}

// claudeMessage represents a message in the Claude API request
type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// claudeRequest represents the Claude API request body
type claudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []claudeMessage `json:"messages"`
}

// claudeResponse represents the Claude API response
type claudeResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// GenerateAIContent generates content using Claude API
func GenerateAIContent(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	var req models.AIGenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Type != "caption" && req.Type != "campaign_name" {
		http.Error(w, "type must be 'caption' or 'campaign_name'", http.StatusBadRequest)
		return
	}

	// Load AI config
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var cfg models.AIConfig
	err := database.AIConfigs().FindOne(ctx, bson.M{"org_id": orgID}).Decode(&cfg)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			http.Error(w, "IA nao configurada. Acesse Perfil > IA para configurar sua API key.", http.StatusBadRequest)
			return
		}
		slog.Error("ai_config_lookup_error", "error", err)
		http.Error(w, "Error loading AI config", http.StatusInternalServerError)
		return
	}

	apiKey, err := crypto.Decrypt(cfg.APIKeyEnc)
	if err != nil {
		slog.Error("ai_config_decrypt_error", "error", err)
		http.Error(w, "Failed to decrypt API key", http.StatusInternalServerError)
		return
	}

	lang := req.Language
	if lang == "" {
		lang = "pt-BR"
	}

	// Build prompt
	var prompt string
	switch req.Type {
	case "caption":
		prompt = buildCaptionPrompt(req.Context, req.MediaCount, req.MediaType, lang)
	case "campaign_name":
		prompt = buildCampaignNamePrompt(req.Context, lang)
	}

	// Call Claude API
	claudeReq := claudeRequest{
		Model:     cfg.Model,
		MaxTokens: 1024,
		Messages:  []claudeMessage{{Role: "user", Content: prompt}},
	}

	body, err := json.Marshal(claudeReq)
	if err != nil {
		http.Error(w, "Failed to build request", http.StatusInternalServerError)
		return
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	httpReq, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		slog.Error("claude_api_error", "error", err)
		http.Error(w, "Erro ao conectar com a IA. Tente novamente.", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "Failed to read AI response", http.StatusInternalServerError)
		return
	}

	var claudeResp claudeResponse
	if err := json.Unmarshal(respBody, &claudeResp); err != nil {
		slog.Error("claude_parse_error", "body", string(respBody))
		http.Error(w, "Failed to parse AI response", http.StatusInternalServerError)
		return
	}

	if claudeResp.Error != nil {
		slog.Error("claude_api_error", "type", claudeResp.Error.Type, "message", claudeResp.Error.Message)
		if strings.Contains(claudeResp.Error.Message, "invalid x-api-key") ||
			strings.Contains(claudeResp.Error.Type, "authentication_error") {
			http.Error(w, "API key invalida. Verifique sua chave em Perfil > IA.", http.StatusUnauthorized)
			return
		}
		if strings.Contains(claudeResp.Error.Message, "credit balance is too low") ||
			strings.Contains(claudeResp.Error.Message, "billing") {
			http.Error(w, "Sem creditos na Anthropic. Adicione creditos em console.anthropic.com/settings/billing", http.StatusPaymentRequired)
			return
		}
		if strings.Contains(claudeResp.Error.Type, "rate_limit_error") ||
			strings.Contains(claudeResp.Error.Message, "rate limit") {
			http.Error(w, "Limite de requisicoes atingido. Aguarde alguns segundos e tente novamente.", http.StatusTooManyRequests)
			return
		}
		if strings.Contains(claudeResp.Error.Type, "overloaded_error") {
			http.Error(w, "A IA esta sobrecarregada. Tente novamente em alguns segundos.", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, fmt.Sprintf("Erro da IA: %s", claudeResp.Error.Message), http.StatusBadGateway)
		return
	}

	if len(claudeResp.Content) == 0 {
		http.Error(w, "Empty response from AI", http.StatusInternalServerError)
		return
	}

	text := claudeResp.Content[0].Text
	tokensUsed := claudeResp.Usage.InputTokens + claudeResp.Usage.OutputTokens

	result := models.AIGenerateResponse{
		Text:       text,
		TokensUsed: tokensUsed,
	}

	// For caption, also suggest a campaign name
	if req.Type == "caption" {
		result.CampaignName = extractCampaignSuggestion(text)
	}

	json.NewEncoder(w).Encode(result)
}

func buildCaptionPrompt(userContext string, mediaCount int, mediaType string, lang string) string {
	var sb strings.Builder
	sb.WriteString("Voce e um especialista em marketing digital e Instagram. ")
	sb.WriteString("Gere uma legenda envolvente para um post do Instagram.\n\n")

	sb.WriteString("Regras:\n")
	sb.WriteString("- Idioma: " + lang + "\n")
	sb.WriteString("- Maximo 2200 caracteres\n")
	sb.WriteString("- Inclua de 5 a 10 hashtags relevantes ao final\n")
	sb.WriteString("- Comece com um gancho que prenda a atencao\n")
	sb.WriteString("- Inclua um CTA (call-to-action) sutil\n")
	sb.WriteString("- Use emojis de forma moderada\n")
	sb.WriteString("- Retorne APENAS a legenda, sem explicacoes extras\n\n")

	if mediaCount > 0 {
		sb.WriteString(fmt.Sprintf("O post possui %d midia(s)", mediaCount))
		if mediaType != "" {
			sb.WriteString(fmt.Sprintf(" do tipo %s", mediaType))
		}
		sb.WriteString(".\n")
	}

	if userContext != "" {
		sb.WriteString(fmt.Sprintf("\nContexto fornecido pelo usuario: %s\n", userContext))
	}

	return sb.String()
}

func buildCampaignNamePrompt(userContext string, lang string) string {
	var sb strings.Builder
	sb.WriteString("Gere um nome curto e criativo para uma campanha de Meta Ads (Facebook/Instagram Ads).\n\n")
	sb.WriteString("Regras:\n")
	sb.WriteString("- Idioma: " + lang + "\n")
	sb.WriteString("- Maximo 60 caracteres\n")
	sb.WriteString("- Retorne APENAS o nome da campanha, sem explicacoes ou aspas\n")

	if userContext != "" {
		sb.WriteString(fmt.Sprintf("\nContexto: %s\n", userContext))
	}

	return sb.String()
}

// extractCampaignSuggestion extracts a short campaign name suggestion from caption text
func extractCampaignSuggestion(caption string) string {
	// Take the first line as a base, cleaned up
	lines := strings.SplitN(caption, "\n", 2)
	first := strings.TrimSpace(lines[0])

	// Remove emojis and hashtags, keep first ~50 chars
	cleaned := strings.Map(func(r rune) rune {
		if r >= 0x1F600 && r <= 0x1FAFF { // emoji ranges
			return -1
		}
		if r == '#' {
			return -1
		}
		return r
	}, first)

	cleaned = strings.TrimSpace(cleaned)
	if len(cleaned) > 50 {
		cleaned = cleaned[:50]
	}

	if cleaned == "" {
		return ""
	}

	return cleaned
}
