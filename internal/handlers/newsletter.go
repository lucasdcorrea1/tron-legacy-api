package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/config"
)

type subscribeRequest struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// SubscribeNewsletter godoc
// @Summary Inscrever na newsletter
// @Description Adiciona um contato à audience de newsletter via Resend API
// @Tags newsletter
// @Accept json
// @Produce json
// @Param request body subscribeRequest true "Email e nome opcional"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /newsletter/subscribe [post]
func SubscribeNewsletter(w http.ResponseWriter, r *http.Request) {
	var req subscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Corpo da requisição inválido"})
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Name = strings.TrimSpace(req.Name)

	if req.Email == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Email é obrigatório"})
		return
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Email inválido"})
		return
	}

	cfg := config.Get()
	if cfg.ResendAPIKey == "" || cfg.ResendAudienceID == "" {
		slog.Error("newsletter_subscribe: RESEND_API_KEY or RESEND_AUDIENCE_ID not configured")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Serviço de newsletter não configurado"})
		return
	}

	// Build Resend API request body
	resendBody := map[string]interface{}{
		"email":        req.Email,
		"unsubscribed": false,
	}

	if req.Name != "" {
		resendBody["first_name"] = req.Name
	}

	bodyBytes, err := json.Marshal(resendBody)
	if err != nil {
		slog.Error("newsletter_subscribe: failed to marshal request", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro interno"})
		return
	}

	// Call Resend Audiences API: POST /audiences/{audienceId}/contacts
	url := fmt.Sprintf("https://api.resend.com/audiences/%s/contacts", cfg.ResendAudienceID)

	client := &http.Client{Timeout: 10 * time.Second}
	resendReq, err := http.NewRequest("POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		slog.Error("newsletter_subscribe: failed to create request", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro interno"})
		return
	}

	resendReq.Header.Set("Content-Type", "application/json")
	resendReq.Header.Set("Authorization", "Bearer "+cfg.ResendAPIKey)

	resp, err := client.Do(resendReq)
	if err != nil {
		slog.Error("newsletter_subscribe: resend API call failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro ao conectar com serviço de newsletter"})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		slog.Info("newsletter_subscribe: contact created", "email", req.Email)
		writeJSON(w, http.StatusOK, map[string]string{"message": "Inscrição realizada com sucesso!"})
		return
	}

	// Handle errors
	var resendErr struct {
		StatusCode int    `json:"statusCode"`
		Message    string `json:"message"`
		Name       string `json:"name"`
	}
	if json.Unmarshal(respBody, &resendErr) == nil {
		// Already exists is not an error for the user
		if resp.StatusCode == http.StatusConflict || strings.Contains(strings.ToLower(resendErr.Message), "already") {
			slog.Info("newsletter_subscribe: contact already exists", "email", req.Email)
			writeJSON(w, http.StatusOK, map[string]string{"message": "Este email já está inscrito!"})
			return
		}
	}

	slog.Error("newsletter_subscribe: unexpected resend response",
		"status", resp.StatusCode,
		"body", string(respBody),
	)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro no serviço de newsletter"})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
