package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/tron-legacy/api/internal/config"
)

// NotifySubscribersNewPost sends an email to all audience contacts about a new post.
// Should be called in a goroutine to avoid blocking the request.
func NotifySubscribersNewPost(title, excerpt, slug, authorName, category string, tags []string) {
	cfg := config.Get()
	if cfg.ResendAPIKey == "" || cfg.ResendAudienceID == "" {
		slog.Warn("newsletter_broadcast: skipped, RESEND_API_KEY or RESEND_AUDIENCE_ID not set")
		return
	}

	postURL := fmt.Sprintf("%s/blog/%s", cfg.FrontendURL, slug)
	emailHTML := buildNewPostEmailHTML(title, excerpt, postURL, authorName, category, tags)

	// Use Resend Broadcast API: send to entire audience
	subject := fmt.Sprintf("Novo artigo: %s", title)
	broadcastBody := map[string]interface{}{
		"name":        subject,
		"from":        cfg.FromEmail,
		"audience_id": cfg.ResendAudienceID,
		"subject":     subject,
		"html":        emailHTML,
	}

	bodyBytes, err := json.Marshal(broadcastBody)
	if err != nil {
		slog.Error("newsletter_broadcast: failed to marshal request", "error", err)
		return
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("POST", "https://api.resend.com/broadcasts", bytes.NewReader(bodyBytes))
	if err != nil {
		slog.Error("newsletter_broadcast: failed to create request", "error", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.ResendAPIKey)

	resp, err := client.Do(req)
	if err != nil {
		slog.Error("newsletter_broadcast: resend API call failed", "error", err)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		// Broadcast created, now send it
		var result struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(respBody, &result) == nil && result.ID != "" {
			SendBroadcast(cfg.ResendAPIKey, result.ID)
		}
		slog.Info("newsletter_broadcast: sent", "post_slug", slug, "title", title)
		return
	}

	slog.Error("newsletter_broadcast: unexpected response",
		"status", resp.StatusCode,
		"body", string(respBody),
		"post_slug", slug,
	)
}

// SendBroadcast triggers the send of a previously created Resend broadcast.
func SendBroadcast(apiKey, broadcastID string) {
	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("https://api.resend.com/broadcasts/%s/send", broadcastID)

	req, err := http.NewRequest("POST", url, bytes.NewReader([]byte("{}")))
	if err != nil {
		slog.Error("newsletter_broadcast: failed to send broadcast", "error", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		slog.Error("newsletter_broadcast: send request failed", "error", err)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		slog.Info("newsletter_broadcast: broadcast sent successfully", "broadcast_id", broadcastID)
		return
	}

	slog.Error("newsletter_broadcast: failed to send",
		"status", resp.StatusCode,
		"body", string(respBody),
		"broadcast_id", broadcastID,
	)
}

func buildNewPostEmailHTML(title, excerpt, postURL, authorName, category string, tags []string) string {
	categoryHTML := ""
	if category != "" {
		categoryHTML = fmt.Sprintf(`<span style="display:inline-block;padding:4px 12px;background:rgba(168,85,247,0.15);color:#c084fc;font-size:12px;font-weight:600;border-radius:20px;margin-bottom:12px;">%s</span><br>`, category)
	}

	tagsHTML := ""
	if len(tags) > 0 {
		tagsHTML = `<div style="margin-top:16px;">`
		for _, tag := range tags {
			tagsHTML += fmt.Sprintf(`<span style="display:inline-block;padding:3px 10px;background:rgba(255,255,255,0.05);border:1px solid rgba(255,255,255,0.08);color:rgba(255,255,255,0.5);font-size:11px;border-radius:6px;margin-right:6px;margin-bottom:4px;">%s</span>`, tag)
		}
		tagsHTML += `</div>`
	}

	excerptHTML := ""
	if excerpt != "" {
		excerptHTML = fmt.Sprintf(`<p style="margin:0 0 20px;font-size:15px;color:#d4d4d8;line-height:1.6;">%s</p>`, excerpt)
	}

	authorHTML := ""
	if authorName != "" {
		authorHTML = fmt.Sprintf(`<p style="margin:0 0 20px;font-size:13px;color:#a1a1aa;">Por <strong style="color:#d4d4d8;">%s</strong></p>`, authorName)
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="pt-BR">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="margin:0;padding:0;background-color:#09090b;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">
  <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="background-color:#09090b;padding:40px 20px;">
    <tr>
      <td align="center">
        <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="max-width:520px;background:linear-gradient(145deg,rgba(255,255,255,0.03),rgba(255,255,255,0.01));border:1px solid rgba(255,255,255,0.06);border-radius:16px;overflow:hidden;">
          <!-- Header -->
          <tr>
            <td style="padding:32px 40px 0;text-align:center;">
              <h1 style="margin:0;font-size:28px;font-weight:700;color:#a855f7;">Whodo</h1>
              <p style="margin:8px 0 0;font-size:13px;color:#a1a1aa;">Novo artigo no blog</p>
            </td>
          </tr>

          <!-- Divider -->
          <tr>
            <td style="padding:20px 40px;">
              <div style="height:1px;background:linear-gradient(90deg,transparent,rgba(168,85,247,0.3),transparent);"></div>
            </td>
          </tr>

          <!-- Content -->
          <tr>
            <td style="padding:0 40px;">
              %s
              <h2 style="margin:0 0 12px;font-size:22px;font-weight:700;color:#fafafa;line-height:1.3;">%s</h2>
              %s
              %s
              %s
            </td>
          </tr>

          <!-- Button -->
          <tr>
            <td style="padding:8px 40px 0;" align="center">
              <a href="%s" target="_blank" style="display:inline-block;padding:14px 36px;background:linear-gradient(135deg,#a855f7,#6366f1);color:#ffffff;font-size:15px;font-weight:600;text-decoration:none;border-radius:10px;">
                Ler artigo completo
              </a>
            </td>
          </tr>

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
</html>`, categoryHTML, title, authorHTML, excerptHTML, tagsHTML, postURL, time.Now().Year())
}
