package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/tron-legacy/api/internal/config"
)

// ---------------------------------------------------------------------------
// Email Templates
// ---------------------------------------------------------------------------

type emailTemplate struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

var emailTemplates = []emailTemplate{
	{
		ID:          "announcement",
		Name:        "Anúncio",
		Description: "Comunicados gerais, novidades e atualizações importantes para sua audiência.",
		Category:    "Comunicação",
	},
	{
		ID:          "new-feature",
		Name:        "Nova Funcionalidade",
		Description: "Apresente novos lançamentos, melhorias e funcionalidades da plataforma.",
		Category:    "Produto",
	},
	{
		ID:          "promotional",
		Name:        "Promocional",
		Description: "Ofertas especiais, eventos, campanhas e promoções por tempo limitado.",
		Category:    "Marketing",
	},
}

// ---------------------------------------------------------------------------
// Request / Response types
// ---------------------------------------------------------------------------

type previewRequest struct {
	Subject    string `json:"subject"`
	PreviewTxt string `json:"preview_text"`
	Content    string `json:"content"`
	ButtonText string `json:"button_text"`
	ButtonURL  string `json:"button_url"`
}

type sendRequest struct {
	TemplateID string `json:"template_id"`
	Subject    string `json:"subject"`
	PreviewTxt string `json:"preview_text"`
	Content    string `json:"content"`
	ButtonText string `json:"button_text"`
	ButtonURL  string `json:"button_url"`
}

type broadcastRecord struct {
	ID         string `json:"id"`
	TemplateID string `json:"template_id"`
	Subject    string `json:"subject"`
	SentAt     string `json:"sent_at"`
	Status     string `json:"status"`
}

// In-memory broadcast history (replaced by DB in the future)
var broadcastHistory []broadcastRecord

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// ListEmailTemplates returns the available email templates.
func ListEmailTemplates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"templates": emailTemplates,
	})
}

// PreviewEmailTemplate generates an HTML preview for a given template.
func PreviewEmailTemplate(w http.ResponseWriter, r *http.Request) {
	templateID := r.PathValue("id")

	var found bool
	for _, t := range emailTemplates {
		if t.ID == templateID {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, `{"message":"Template não encontrado"}`, http.StatusNotFound)
		return
	}

	var req previewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"Dados inválidos"}`, http.StatusBadRequest)
		return
	}

	htmlContent := buildMarketingEmail(templateID, req.Subject, req.PreviewTxt, req.Content, req.ButtonText, req.ButtonURL)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"html": htmlContent,
	})
}

// GetEmailAudience returns audience stats from Resend.
func GetEmailAudience(w http.ResponseWriter, r *http.Request) {
	cfg := config.Get()
	if cfg.ResendAPIKey == "" || cfg.ResendAudienceID == "" {
		http.Error(w, `{"message":"Resend não configurado"}`, http.StatusInternalServerError)
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("https://api.resend.com/audiences/%s", cfg.ResendAudienceID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		slog.Error("email_marketing: failed to create audience request", "error", err)
		http.Error(w, `{"message":"Erro interno"}`, http.StatusInternalServerError)
		return
	}
	req.Header.Set("Authorization", "Bearer "+cfg.ResendAPIKey)

	resp, err := client.Do(req)
	if err != nil {
		slog.Error("email_marketing: audience API call failed", "error", err)
		http.Error(w, `{"message":"Erro ao consultar audiência"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		slog.Error("email_marketing: audience API error", "status", resp.StatusCode, "body", string(body))
		http.Error(w, `{"message":"Erro na API do Resend"}`, http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// SendMarketingEmail creates and sends a broadcast via Resend.
func SendMarketingEmail(w http.ResponseWriter, r *http.Request) {
	cfg := config.Get()
	if cfg.ResendAPIKey == "" || cfg.ResendAudienceID == "" {
		http.Error(w, `{"message":"Resend não configurado"}`, http.StatusInternalServerError)
		return
	}

	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"Dados inválidos"}`, http.StatusBadRequest)
		return
	}

	if req.Subject == "" {
		http.Error(w, `{"message":"Assunto é obrigatório"}`, http.StatusBadRequest)
		return
	}

	// Validate template
	var found bool
	for _, t := range emailTemplates {
		if t.ID == req.TemplateID {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, `{"message":"Template não encontrado"}`, http.StatusBadRequest)
		return
	}

	emailHTML := buildMarketingEmail(req.TemplateID, req.Subject, req.PreviewTxt, req.Content, req.ButtonText, req.ButtonURL)

	// Create broadcast via Resend
	broadcastBody := map[string]interface{}{
		"name":        req.Subject,
		"from":        cfg.FromEmail,
		"audience_id": cfg.ResendAudienceID,
		"subject":     req.Subject,
		"html":        emailHTML,
	}

	bodyBytes, err := json.Marshal(broadcastBody)
	if err != nil {
		slog.Error("email_marketing: failed to marshal broadcast", "error", err)
		http.Error(w, `{"message":"Erro interno"}`, http.StatusInternalServerError)
		return
	}

	client := &http.Client{Timeout: 15 * time.Second}
	apiReq, err := http.NewRequest("POST", "https://api.resend.com/broadcasts", bytes.NewReader(bodyBytes))
	if err != nil {
		slog.Error("email_marketing: failed to create request", "error", err)
		http.Error(w, `{"message":"Erro interno"}`, http.StatusInternalServerError)
		return
	}
	apiReq.Header.Set("Content-Type", "application/json")
	apiReq.Header.Set("Authorization", "Bearer "+cfg.ResendAPIKey)

	resp, err := client.Do(apiReq)
	if err != nil {
		slog.Error("email_marketing: resend API call failed", "error", err)
		http.Error(w, `{"message":"Erro ao enviar email"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		slog.Error("email_marketing: broadcast creation failed", "status", resp.StatusCode, "body", string(respBody))
		http.Error(w, `{"message":"Erro ao criar broadcast no Resend"}`, http.StatusBadGateway)
		return
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil || result.ID == "" {
		slog.Error("email_marketing: failed to parse broadcast ID", "body", string(respBody))
		http.Error(w, `{"message":"Erro ao processar resposta do Resend"}`, http.StatusInternalServerError)
		return
	}

	// Send the broadcast
	SendBroadcast(cfg.ResendAPIKey, result.ID)

	// Record in history
	record := broadcastRecord{
		ID:         result.ID,
		TemplateID: req.TemplateID,
		Subject:    req.Subject,
		SentAt:     time.Now().UTC().Format(time.RFC3339),
		Status:     "sent",
	}
	broadcastHistory = append(broadcastHistory, record)

	slog.Info("email_marketing: broadcast sent", "broadcast_id", result.ID, "subject", req.Subject)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"broadcast_id": result.ID,
		"status":       "sent",
		"message":      "Email enviado com sucesso!",
	})
}

// ListBroadcasts returns the history of sent broadcasts.
func ListBroadcasts(w http.ResponseWriter, r *http.Request) {
	history := broadcastHistory
	if history == nil {
		history = []broadcastRecord{}
	}

	// Return in reverse chronological order
	reversed := make([]broadcastRecord, len(history))
	for i, rec := range history {
		reversed[len(history)-1-i] = rec
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"broadcasts": reversed,
		"total":      len(reversed),
	})
}

// GetBroadcast returns details of a specific broadcast.
func GetBroadcast(w http.ResponseWriter, r *http.Request) {
	broadcastID := r.PathValue("id")

	for _, rec := range broadcastHistory {
		if rec.ID == broadcastID {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(rec)
			return
		}
	}

	http.Error(w, `{"message":"Broadcast não encontrado"}`, http.StatusNotFound)
}

// ---------------------------------------------------------------------------
// Email HTML Builders
// ---------------------------------------------------------------------------

func buildMarketingEmail(templateID, subject, previewText, content, buttonText, buttonURL string) string {
	// Escape user content to prevent XSS
	safeSubject := html.EscapeString(subject)
	safeContent := html.EscapeString(content)
	safeButtonText := html.EscapeString(buttonText)
	safeButtonURL := html.EscapeString(buttonURL)
	safePreviewText := html.EscapeString(previewText)

	if safeButtonText == "" {
		safeButtonText = "Saiba mais"
	}

	switch templateID {
	case "announcement":
		return buildAnnouncementEmail(safeSubject, safePreviewText, safeContent, safeButtonText, safeButtonURL)
	case "new-feature":
		return buildNewFeatureEmail(safeSubject, safePreviewText, safeContent, safeButtonText, safeButtonURL)
	case "promotional":
		return buildPromotionalEmail(safeSubject, safePreviewText, safeContent, safeButtonText, safeButtonURL)
	default:
		return buildAnnouncementEmail(safeSubject, safePreviewText, safeContent, safeButtonText, safeButtonURL)
	}
}

func emailShell(previewText, innerContent string) string {
	previewBlock := ""
	if previewText != "" {
		previewBlock = fmt.Sprintf(`<div style="display:none;font-size:1px;color:#09090b;line-height:1px;max-height:0;max-width:0;opacity:0;overflow:hidden;">%s</div>`, previewText)
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="pt-BR">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="margin:0;padding:0;background-color:#09090b;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">
  %s
  <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="background-color:#09090b;padding:40px 20px;">
    <tr>
      <td align="center">
        <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="max-width:520px;background:linear-gradient(145deg,rgba(255,255,255,0.03),rgba(255,255,255,0.01));border:1px solid rgba(255,255,255,0.06);border-radius:16px;overflow:hidden;">
          <!-- Header -->
          <tr>
            <td style="padding:32px 40px 0;text-align:center;">
              <h1 style="margin:0;font-size:28px;font-weight:700;color:#a855f7;">Whodo</h1>
            </td>
          </tr>

          <!-- Divider -->
          <tr>
            <td style="padding:20px 40px;">
              <div style="height:1px;background:linear-gradient(90deg,transparent,rgba(168,85,247,0.3),transparent);"></div>
            </td>
          </tr>

          %s

          <!-- Footer -->
          <tr>
            <td style="padding:32px 40px;">
              <div style="height:1px;background:linear-gradient(90deg,transparent,rgba(255,255,255,0.05),transparent);margin-bottom:20px;"></div>
              <p style="margin:0;font-size:12px;color:#71717a;text-align:center;line-height:1.5;">
                Você recebeu este email porque se inscreveu na newsletter do Whodo.<br>
                &copy; %d Whodo Group LTDA — Todos os direitos reservados.
              </p>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`, previewBlock, innerContent, time.Now().Year())
}

func buildAnnouncementEmail(subject, previewText, content, buttonText, buttonURL string) string {
	buttonHTML := ""
	if buttonURL != "" {
		buttonHTML = fmt.Sprintf(`
          <!-- Button -->
          <tr>
            <td style="padding:8px 40px 0;" align="center">
              <a href="%s" target="_blank" style="display:inline-block;padding:14px 36px;background:linear-gradient(135deg,#a855f7,#6366f1);color:#ffffff;font-size:15px;font-weight:600;text-decoration:none;border-radius:10px;">
                %s
              </a>
            </td>
          </tr>`, buttonURL, buttonText)
	}

	contentHTML := ""
	if content != "" {
		contentHTML = fmt.Sprintf(`<p style="margin:0 0 20px;font-size:15px;color:#d4d4d8;line-height:1.6;white-space:pre-line;">%s</p>`, content)
	}

	inner := fmt.Sprintf(`
          <!-- Content -->
          <tr>
            <td style="padding:0 40px;">
              <p style="margin:0 0 8px;font-size:13px;color:#a1a1aa;">Comunicado</p>
              <h2 style="margin:0 0 16px;font-size:22px;font-weight:700;color:#fafafa;line-height:1.3;">%s</h2>
              %s
            </td>
          </tr>
          %s`, subject, contentHTML, buttonHTML)

	return emailShell(previewText, inner)
}

func buildNewFeatureEmail(subject, previewText, content, buttonText, buttonURL string) string {
	buttonHTML := ""
	if buttonURL != "" {
		buttonHTML = fmt.Sprintf(`
          <!-- Button -->
          <tr>
            <td style="padding:8px 40px 0;" align="center">
              <a href="%s" target="_blank" style="display:inline-block;padding:14px 36px;background:linear-gradient(135deg,#a855f7,#6366f1);color:#ffffff;font-size:15px;font-weight:600;text-decoration:none;border-radius:10px;">
                %s
              </a>
            </td>
          </tr>`, buttonURL, buttonText)
	}

	contentHTML := ""
	if content != "" {
		contentHTML = fmt.Sprintf(`<p style="margin:0 0 20px;font-size:15px;color:#d4d4d8;line-height:1.6;white-space:pre-line;">%s</p>`, content)
	}

	inner := fmt.Sprintf(`
          <!-- Content -->
          <tr>
            <td style="padding:0 40px;">
              <span style="display:inline-block;padding:4px 12px;background:rgba(34,197,94,0.15);color:#22c55e;font-size:11px;font-weight:700;border-radius:20px;margin-bottom:16px;letter-spacing:0.5px;">NOVO</span>
              <h2 style="margin:0 0 16px;font-size:22px;font-weight:700;color:#fafafa;line-height:1.3;">%s</h2>
              <!-- Feature highlight box -->
              <div style="background:rgba(168,85,247,0.08);border:1px solid rgba(168,85,247,0.15);border-radius:12px;padding:20px;margin-bottom:20px;">
                %s
              </div>
            </td>
          </tr>
          %s`, subject, contentHTML, buttonHTML)

	return emailShell(previewText, inner)
}

func buildPromotionalEmail(subject, previewText, content, buttonText, buttonURL string) string {
	buttonHTML := ""
	if buttonURL != "" {
		buttonHTML = fmt.Sprintf(`
          <!-- Button -->
          <tr>
            <td style="padding:8px 40px 0;" align="center">
              <a href="%s" target="_blank" style="display:inline-block;padding:16px 40px;background:linear-gradient(135deg,#a855f7,#6366f1);color:#ffffff;font-size:16px;font-weight:700;text-decoration:none;border-radius:10px;box-shadow:0 4px 15px rgba(168,85,247,0.3);">
                %s
              </a>
            </td>
          </tr>`, buttonURL, buttonText)
	}

	contentHTML := ""
	if content != "" {
		contentHTML = fmt.Sprintf(`<p style="margin:0 0 20px;font-size:15px;color:#d4d4d8;line-height:1.6;white-space:pre-line;">%s</p>`, content)
	}

	inner := fmt.Sprintf(`
          <!-- Promotional Banner -->
          <tr>
            <td style="padding:0 40px;">
              <div style="background:linear-gradient(135deg,rgba(168,85,247,0.15),rgba(99,102,241,0.15));border:1px solid rgba(168,85,247,0.2);border-radius:12px;padding:24px;text-align:center;margin-bottom:20px;">
                <h2 style="margin:0 0 8px;font-size:24px;font-weight:700;color:#fafafa;line-height:1.3;">%s</h2>
                <p style="margin:0;font-size:13px;color:#a1a1aa;">Oferta por tempo limitado</p>
              </div>
              %s
            </td>
          </tr>
          %s`, subject, contentHTML, buttonHTML)

	return emailShell(previewText, inner)
}
