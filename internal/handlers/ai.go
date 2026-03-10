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

// maskAPIKey returns a masked version of the API key
func maskAPIKey(key string) string {
	if len(key) > 12 {
		return key[:7] + "..." + key[len(key)-3:]
	}
	if len(key) > 6 {
		return key[:4] + "..."
	}
	return "****"
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
		if key, decErr := crypto.Decrypt(cfg.APIKeyEnc); decErr == nil {
			resp.Configured = true
			resp.Provider = cfg.Provider
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

	provider := req.Provider
	if provider == "" {
		provider = "gemini"
	}
	if provider != "gemini" && provider != "claude" {
		http.Error(w, "provider must be 'gemini' or 'claude'", http.StatusBadRequest)
		return
	}

	// Validate key prefix
	if provider == "claude" && !strings.HasPrefix(req.APIKey, "sk-ant-") {
		http.Error(w, "API key da Anthropic deve comecar com sk-ant-", http.StatusBadRequest)
		return
	}
	if provider == "gemini" && !strings.HasPrefix(req.APIKey, "AIza") {
		http.Error(w, "API key do Google deve comecar com AIza", http.StatusBadRequest)
		return
	}

	// Default models
	model := req.Model
	if model == "" {
		switch provider {
		case "gemini":
			model = "gemini-2.0-flash"
		case "claude":
			model = "claude-sonnet-4-5-20250929"
		}
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
				"provider":    provider,
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
		Provider:   provider,
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

// GenerateAIContent generates content using the configured AI provider
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

	var prompt string
	switch req.Type {
	case "caption":
		prompt = buildCaptionPrompt(req.Context, req.MediaCount, req.MediaType, lang)
	case "campaign_name":
		prompt = buildCampaignNamePrompt(req.Context, lang)
	}

	provider := cfg.Provider
	if provider == "" {
		provider = "claude" // backwards compat for existing configs
	}

	var text string
	var tokensUsed int

	switch provider {
	case "gemini":
		text, tokensUsed, err = callGemini(apiKey, cfg.Model, prompt)
	case "claude":
		text, tokensUsed, err = callClaude(apiKey, cfg.Model, prompt)
	default:
		http.Error(w, "Unknown provider", http.StatusInternalServerError)
		return
	}

	if err != nil {
		// err message is already user-friendly
		slog.Error("ai_generate_error", "provider", provider, "error", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	result := models.AIGenerateResponse{
		Text:       text,
		TokensUsed: tokensUsed,
	}

	if req.Type == "caption" {
		result.CampaignName = extractCampaignSuggestion(text)
	}

	json.NewEncoder(w).Encode(result)
}

// ── Gemini ──────────────────────────────────────────────────────────

type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

func callGemini(apiKey, model, prompt string) (string, int, error) {
	if model == "" {
		model = "gemini-2.0-flash"
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)

	reqBody := geminiRequest{
		Contents: []geminiContent{{
			Parts: []geminiPart{{Text: prompt}},
		}},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", 0, fmt.Errorf("Erro interno ao montar requisicao")
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("Erro ao conectar com a IA. Tente novamente.")
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("Erro ao ler resposta da IA")
	}

	var gemResp geminiResponse
	if err := json.Unmarshal(respBody, &gemResp); err != nil {
		slog.Error("gemini_parse_error", "body", string(respBody))
		return "", 0, fmt.Errorf("Erro ao processar resposta da IA")
	}

	if gemResp.Error != nil {
		slog.Error("gemini_api_error", "code", gemResp.Error.Code, "message", gemResp.Error.Message)
		if gemResp.Error.Code == 400 && strings.Contains(gemResp.Error.Message, "API key") {
			return "", 0, fmt.Errorf("API key invalida. Verifique sua chave em Perfil > IA.")
		}
		if gemResp.Error.Code == 429 {
			return "", 0, fmt.Errorf("Limite de requisicoes atingido. Aguarde e tente novamente.")
		}
		return "", 0, fmt.Errorf("Erro da IA: %s", gemResp.Error.Message)
	}

	if len(gemResp.Candidates) == 0 || len(gemResp.Candidates[0].Content.Parts) == 0 {
		return "", 0, fmt.Errorf("Resposta vazia da IA")
	}

	text := gemResp.Candidates[0].Content.Parts[0].Text
	tokens := 0
	if gemResp.UsageMetadata != nil {
		tokens = gemResp.UsageMetadata.TotalTokenCount
	}

	return text, tokens, nil
}

// ── Claude ──────────────────────────────────────────────────────────

type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type claudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []claudeMessage `json:"messages"`
}

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

func callClaude(apiKey, model, prompt string) (string, int, error) {
	if model == "" {
		model = "claude-sonnet-4-5-20250929"
	}

	claudeReq := claudeRequest{
		Model:     model,
		MaxTokens: 1024,
		Messages:  []claudeMessage{{Role: "user", Content: prompt}},
	}

	body, err := json.Marshal(claudeReq)
	if err != nil {
		return "", 0, fmt.Errorf("Erro interno ao montar requisicao")
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	httpReq, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("Erro interno ao criar requisicao")
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", 0, fmt.Errorf("Erro ao conectar com a IA. Tente novamente.")
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("Erro ao ler resposta da IA")
	}

	var claudeResp claudeResponse
	if err := json.Unmarshal(respBody, &claudeResp); err != nil {
		slog.Error("claude_parse_error", "body", string(respBody))
		return "", 0, fmt.Errorf("Erro ao processar resposta da IA")
	}

	if claudeResp.Error != nil {
		slog.Error("claude_api_error", "type", claudeResp.Error.Type, "message", claudeResp.Error.Message)
		if strings.Contains(claudeResp.Error.Message, "invalid x-api-key") ||
			strings.Contains(claudeResp.Error.Type, "authentication_error") {
			return "", 0, fmt.Errorf("API key invalida. Verifique sua chave em Perfil > IA.")
		}
		if strings.Contains(claudeResp.Error.Message, "credit balance is too low") ||
			strings.Contains(claudeResp.Error.Message, "billing") {
			return "", 0, fmt.Errorf("Sem creditos na Anthropic. Adicione creditos em console.anthropic.com/settings/billing")
		}
		if strings.Contains(claudeResp.Error.Type, "rate_limit_error") {
			return "", 0, fmt.Errorf("Limite de requisicoes atingido. Aguarde e tente novamente.")
		}
		if strings.Contains(claudeResp.Error.Type, "overloaded_error") {
			return "", 0, fmt.Errorf("A IA esta sobrecarregada. Tente novamente em alguns segundos.")
		}
		return "", 0, fmt.Errorf("Erro da IA: %s", claudeResp.Error.Message)
	}

	if len(claudeResp.Content) == 0 {
		return "", 0, fmt.Errorf("Resposta vazia da IA")
	}

	text := claudeResp.Content[0].Text
	tokens := claudeResp.Usage.InputTokens + claudeResp.Usage.OutputTokens

	return text, tokens, nil
}

// ── Prompts ─────────────────────────────────────────────────────────

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

func extractCampaignSuggestion(caption string) string {
	lines := strings.SplitN(caption, "\n", 2)
	first := strings.TrimSpace(lines[0])

	cleaned := strings.Map(func(r rune) rune {
		if r >= 0x1F600 && r <= 0x1FAFF {
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
