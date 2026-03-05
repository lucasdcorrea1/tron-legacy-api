package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/config"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

type resetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

type passwordResetToken struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	UserID    primitive.ObjectID `bson:"user_id"`
	Token     string             `bson:"token"`
	ExpiresAt time.Time          `bson:"expires_at"`
	Used      bool               `bson:"used"`
	CreatedAt time.Time          `bson:"created_at"`
}

// ForgotPassword godoc
// @Summary Solicitar recuperação de senha
// @Description Envia email com link para redefinir a senha via Resend
// @Tags auth
// @Accept json
// @Produce json
// @Param request body forgotPasswordRequest true "Email do usuário"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Router /auth/forgot-password [post]
func ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Corpo da requisição inválido"})
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Email é obrigatório"})
		return
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Email inválido"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Always return success to prevent email enumeration
	successMsg := map[string]string{"message": "Se este email estiver cadastrado, você receberá um link para redefinir sua senha."}

	// Find user
	var user models.User
	err := database.Users().FindOne(ctx, bson.M{"email": req.Email}).Decode(&user)
	if err != nil {
		slog.Info("forgot_password: email not found", "email", req.Email)
		writeJSON(w, http.StatusOK, successMsg)
		return
	}

	// Get user name from profile
	var profile models.Profile
	database.Profiles().FindOne(ctx, bson.M{"user_id": user.ID}).Decode(&profile)
	userName := profile.Name
	if userName == "" {
		userName = "Usuário"
	}

	// Generate secure token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		slog.Error("forgot_password: failed to generate token", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro interno"})
		return
	}
	token := hex.EncodeToString(tokenBytes)

	// Invalidate existing tokens for this user
	database.PasswordResets().UpdateMany(ctx,
		bson.M{"user_id": user.ID, "used": false},
		bson.M{"$set": bson.M{"used": true}},
	)

	// Save token (expires in 1 hour)
	resetToken := passwordResetToken{
		ID:        primitive.NewObjectID(),
		UserID:    user.ID,
		Token:     token,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		Used:      false,
		CreatedAt: time.Now(),
	}

	_, err = database.PasswordResets().InsertOne(ctx, resetToken)
	if err != nil {
		slog.Error("forgot_password: failed to save token", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro interno"})
		return
	}

	// Send email via Resend
	cfg := config.Get()
	if cfg.ResendAPIKey == "" {
		slog.Error("forgot_password: RESEND_API_KEY not configured")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Serviço de email não configurado"})
		return
	}

	resetURL := fmt.Sprintf("%s/reset-password?token=%s", cfg.FrontendURL, token)

	emailHTML := buildResetEmailHTML(userName, resetURL)

	resendBody := map[string]interface{}{
		"from":    cfg.FromEmail,
		"to":      []string{req.Email},
		"subject": "Redefinir sua senha - Whodo",
		"html":    emailHTML,
	}

	bodyBytes, _ := json.Marshal(resendBody)

	client := &http.Client{Timeout: 10 * time.Second}
	resendReq, _ := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewReader(bodyBytes))
	resendReq.Header.Set("Content-Type", "application/json")
	resendReq.Header.Set("Authorization", "Bearer "+cfg.ResendAPIKey)

	resp, err := client.Do(resendReq)
	if err != nil {
		slog.Error("forgot_password: resend API call failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro ao enviar email"})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		slog.Info("forgot_password: reset email sent", "email", req.Email, "user_id", user.ID.Hex())
		writeJSON(w, http.StatusOK, successMsg)
		return
	}

	slog.Error("forgot_password: resend error",
		"status", resp.StatusCode,
		"body", string(respBody),
	)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro ao enviar email"})
}

// ResetPassword godoc
// @Summary Redefinir senha
// @Description Valida o token e atualiza a senha do usuário
// @Tags auth
// @Accept json
// @Produce json
// @Param request body resetPasswordRequest true "Token e nova senha"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Router /auth/reset-password [post]
func ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Corpo da requisição inválido"})
		return
	}

	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Token é obrigatório"})
		return
	}
	if len(req.NewPassword) < 6 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Senha deve ter no mínimo 6 caracteres"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Find valid token
	var resetToken passwordResetToken
	err := database.PasswordResets().FindOne(ctx, bson.M{
		"token":      req.Token,
		"used":       false,
		"expires_at": bson.M{"$gt": time.Now()},
	}).Decode(&resetToken)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Token inválido ou expirado. Solicite um novo link."})
		return
	}

	// Hash new password
	passwordHash, err := models.HashPassword(req.NewPassword)
	if err != nil {
		slog.Error("reset_password: failed to hash password", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro interno"})
		return
	}

	// Update password
	_, err = database.Users().UpdateOne(ctx,
		bson.M{"_id": resetToken.UserID},
		bson.M{"$set": bson.M{"password_hash": passwordHash}},
	)
	if err != nil {
		slog.Error("reset_password: failed to update password", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro ao atualizar senha"})
		return
	}

	// Mark token as used
	database.PasswordResets().UpdateOne(ctx,
		bson.M{"_id": resetToken.ID},
		bson.M{"$set": bson.M{"used": true}},
	)

	// Invalidate all refresh tokens for this user (force re-login on all devices)
	database.RefreshTokens().DeleteMany(ctx, bson.M{"user_id": resetToken.UserID})

	slog.Info("reset_password: password updated", "user_id", resetToken.UserID.Hex())
	writeJSON(w, http.StatusOK, map[string]string{"message": "Senha atualizada com sucesso!"})
}

func buildResetEmailHTML(userName, resetURL string) string {
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
              <h2 style="margin:0 0 8px;font-size:20px;font-weight:600;color:#fafafa;">Olá, %s</h2>
              <p style="margin:0 0 24px;font-size:15px;color:#d4d4d8;line-height:1.6;">
                Recebemos uma solicitação para redefinir a senha da sua conta. Clique no botão abaixo para criar uma nova senha.
              </p>
            </td>
          </tr>

          <!-- Button -->
          <tr>
            <td style="padding:0 40px;" align="center">
              <a href="%s" target="_blank" style="display:inline-block;padding:14px 36px;background:linear-gradient(135deg,#a855f7,#6366f1);color:#ffffff;font-size:15px;font-weight:600;text-decoration:none;border-radius:10px;">
                Redefinir minha senha
              </a>
            </td>
          </tr>

          <!-- Expiry notice -->
          <tr>
            <td style="padding:24px 40px 0;">
              <p style="margin:0;font-size:13px;color:#a1a1aa;line-height:1.5;text-align:center;">
                Este link expira em <strong style="color:#d4d4d8;">1 hora</strong>. Se você não solicitou esta alteração, ignore este email.
              </p>
            </td>
          </tr>

          <!-- Link fallback -->
          <tr>
            <td style="padding:20px 40px;">
              <div style="background:rgba(255,255,255,0.02);border:1px solid rgba(255,255,255,0.05);border-radius:8px;padding:12px 16px;">
                <p style="margin:0 0 4px;font-size:11px;color:#a1a1aa;">Se o botão não funcionar, copie e cole este link:</p>
                <p style="margin:0;font-size:12px;color:#a78bfa;word-break:break-all;">%s</p>
              </div>
            </td>
          </tr>

          <!-- Footer -->
          <tr>
            <td style="padding:20px 40px 32px;">
              <div style="height:1px;background:linear-gradient(90deg,transparent,rgba(255,255,255,0.05),transparent);margin-bottom:20px;"></div>
              <p style="margin:0;font-size:12px;color:#71717a;text-align:center;line-height:1.5;">
                &copy; %d Whodo Group LTDA<br>Todos os direitos reservados.
              </p>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`, userName, resetURL, resetURL, time.Now().Year())
}
