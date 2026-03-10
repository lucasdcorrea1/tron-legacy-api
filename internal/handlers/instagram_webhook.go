package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/config"
	"github.com/tron-legacy/api/internal/crypto"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ─── Webhook verification (GET) ──────────────────────────────────────

// WebhookVerify handles the Meta webhook verification handshake.
// GET /api/v1/webhooks/instagram?hub.mode=subscribe&hub.verify_token=...&hub.challenge=...
func WebhookVerify(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	expected := config.Get().WebhookVerifyToken
	if expected == "" {
		slog.Warn("webhook_verify: WEBHOOK_VERIFY_TOKEN not set")
		http.Error(w, "Server not configured", http.StatusInternalServerError)
		return
	}

	if mode == "subscribe" && token == expected {
		slog.Info("webhook_verify: success")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(challenge))
		return
	}

	slog.Warn("webhook_verify: failed", "mode", mode, "token_match", token == expected)
	http.Error(w, "Forbidden", http.StatusForbidden)
}

// ─── Webhook event processing (POST) ─────────────────────────────────

// Instagram webhook payload structures
type webhookPayload struct {
	Object string         `json:"object"`
	Entry  []webhookEntry `json:"entry"`
}

type webhookEntry struct {
	ID        string              `json:"id"`
	Time      int64               `json:"time"`
	Changes   []webhookChange     `json:"changes"`
	Messaging []webhookMessaging  `json:"messaging"`
}

type webhookChange struct {
	Field string          `json:"field"`
	Value json.RawMessage `json:"value"`
}

type webhookCommentValue struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	From struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	} `json:"from"`
	Media struct {
		ID               string `json:"id"`
		MediaProductType string `json:"media_product_type"`
	} `json:"media"`
}

type webhookMessaging struct {
	Sender    struct{ ID string `json:"id"` }    `json:"sender"`
	Recipient struct{ ID string `json:"id"` }    `json:"recipient"`
	Timestamp int64                               `json:"timestamp"`
	Message   *struct {
		MID  string `json:"mid"`
		Text string `json:"text"`
	} `json:"message,omitempty"`
}

// WebhookEvent processes incoming Instagram webhook events.
// POST /api/v1/webhooks/instagram
func WebhookEvent(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		slog.Error("webhook_event: read body", "error", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// ── Validate X-Hub-Signature-256 ──
	appSecret := config.Get().MetaAppSecret
	if appSecret != "" {
		sigHeader := r.Header.Get("X-Hub-Signature-256")
		if sigHeader == "" {
			slog.Warn("webhook_event: missing X-Hub-Signature-256 header")
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if !strings.HasPrefix(sigHeader, "sha256=") {
			slog.Warn("webhook_event: invalid signature format")
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		sigHex := strings.TrimPrefix(sigHeader, "sha256=")
		sig, err := hex.DecodeString(sigHex)
		if err != nil {
			slog.Warn("webhook_event: invalid signature hex", "error", err)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		mac := hmac.New(sha256.New, []byte(appSecret))
		mac.Write(body)
		expected := mac.Sum(nil)
		if !hmac.Equal(sig, expected) {
			slog.Warn("webhook_event: signature mismatch")
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	} else {
		slog.Warn("webhook_event: META_APP_SECRET not set, skipping signature validation (dev mode)")
	}

	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		slog.Error("webhook_event: parse payload", "error", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Respond 200 immediately (Meta expects fast response)
	w.WriteHeader(http.StatusOK)

	// Process in background
	go processWebhookPayload(payload)
}

func processWebhookPayload(payload webhookPayload) {
	if payload.Object != "instagram" {
		return
	}

	for _, entry := range payload.Entry {
		igAccountID := entry.ID

		// Process comment events
		for _, change := range entry.Changes {
			if change.Field == "comments" {
				var comment webhookCommentValue
				if err := json.Unmarshal(change.Value, &comment); err != nil {
					slog.Error("webhook: parse comment", "error", err)
					continue
				}
				processComment(igAccountID, comment)
			}
		}

		// Process DM events
		for _, msg := range entry.Messaging {
			if msg.Message != nil && msg.Message.Text != "" {
				// Ignore messages sent by the page itself
				if msg.Sender.ID == igAccountID {
					continue
				}
				processDM(igAccountID, msg)
			}
		}
	}
}

func processComment(igAccountID string, comment webhookCommentValue) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	slog.Info("webhook_comment: received", "ig_account_id", igAccountID, "from", comment.From.Username, "text", comment.Text, "media_id", comment.Media.ID)

	// Resolve credentials for this IG account
	creds, err := resolveCredsByAccountID(ctx, igAccountID)
	if err != nil || creds == nil {
		slog.Warn("webhook_comment: no credentials for account", "ig_account_id", igAccountID, "error", err)
		BroadcastWebhookEvent(WebhookSSEEvent{
			Type: "comment", Sender: comment.From.Username,
			TriggerText: comment.Text, Status: "failed",
			Response:  "Erro: credenciais do Instagram não encontradas para a conta " + igAccountID,
			Timestamp: time.Now().Format(time.RFC3339),
		})
		return
	}

	// Find matching active rules (scoped to org)
	rules, err := findMatchingRules(ctx, comment.Text, "comment", comment.Media.ID, creds.OrgID)
	if err != nil {
		slog.Error("webhook_comment: find rules", "error", err)
		return
	}

	if len(rules) == 0 {
		slog.Info("webhook_comment: no matching rules", "text", comment.Text)
		BroadcastWebhookEvent(WebhookSSEEvent{
			Type: "comment", Sender: comment.From.Username,
			TriggerText: comment.Text, Status: "no_match",
			Response:  "Nenhuma regra ativa corresponde a este comentário",
			Timestamp: time.Now().Format(time.RFC3339),
		})
		return
	}

	for _, mr := range rules {
		rule := mr.Rule
		keyword := mr.Keyword

		// Check 24h cooldown
		if hasCooldown(ctx, comment.From.ID, rule.ID) {
			logAutoReply(ctx, rule, "comment", comment.From.ID, comment.From.Username, comment.Text, rule.ResponseMessage, "", "skipped_cooldown", "", creds.OrgID)
			BroadcastWebhookEvent(WebhookSSEEvent{
				Type: "comment", RuleName: rule.Name, Sender: comment.From.Username,
				TriggerText: comment.Text, Response: rule.ResponseMessage,
				Status: "skipped_cooldown", Timestamp: time.Now().Format(time.RFC3339),
			})
			continue
		}

		// 1) Public comment reply (if configured). Failure does NOT block DM.
		commentReplySent := ""
		if rule.CommentReply != "" {
			replyMsg := replaceTemplateVars(rule.CommentReply, comment.From.Username, keyword)
			if err := sendCommentReply(creds.Token, comment.ID, replyMsg); err != nil {
				slog.Error("webhook_comment: public reply failed", "error", err, "comment_id", comment.ID, "rule", rule.Name)
			} else {
				commentReplySent = replyMsg
				slog.Info("webhook_comment: public reply sent", "comment_id", comment.ID, "rule", rule.Name)
			}
		}

		// 2) Send DM via Private Reply (uses comment_id, not user ID)
		dmMsg := replaceTemplateVars(rule.ResponseMessage, comment.From.Username, keyword)
		err := sendPrivateReply(creds.AccountID, creds.Token, comment.ID, dmMsg)
		if err != nil {
			slog.Error("webhook_comment: send DM failed", "error", err, "sender", comment.From.ID, "rule", rule.Name)
			logAutoReply(ctx, rule, "comment", comment.From.ID, comment.From.Username, comment.Text, dmMsg, commentReplySent, "failed", err.Error(), creds.OrgID)
			BroadcastWebhookEvent(WebhookSSEEvent{
				Type: "comment", RuleName: rule.Name, Sender: comment.From.Username,
				TriggerText: comment.Text, Response: dmMsg, CommentReply: commentReplySent,
				Status: "failed", Timestamp: time.Now().Format(time.RFC3339),
			})
			continue
		}

		slog.Info("webhook_comment: DM sent", "sender", comment.From.ID, "rule", rule.Name)
		logAutoReply(ctx, rule, "comment", comment.From.ID, comment.From.Username, comment.Text, dmMsg, commentReplySent, "sent", "", creds.OrgID)
		BroadcastWebhookEvent(WebhookSSEEvent{
			Type: "comment", RuleName: rule.Name, Sender: comment.From.Username,
			TriggerText: comment.Text, Response: dmMsg, CommentReply: commentReplySent,
			Status: "sent", Timestamp: time.Now().Format(time.RFC3339),
		})
	}
}

func processDM(igAccountID string, msg webhookMessaging) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	senderID := msg.Sender.ID
	text := msg.Message.Text

	slog.Info("webhook_dm: received", "ig_account_id", igAccountID, "sender", senderID, "text", text)

	creds, err := resolveCredsByAccountID(ctx, igAccountID)
	if err != nil || creds == nil {
		slog.Warn("webhook_dm: no credentials for account", "ig_account_id", igAccountID, "error", err)
		BroadcastWebhookEvent(WebhookSSEEvent{
			Type: "dm", Sender: senderID,
			TriggerText: text, Status: "failed",
			Response:  "Erro: credenciais do Instagram não encontradas para a conta " + igAccountID,
			Timestamp: time.Now().Format(time.RFC3339),
		})
		return
	}

	rules, err := findMatchingRules(ctx, text, "dm", "", creds.OrgID)
	if err != nil {
		slog.Error("webhook_dm: find rules", "error", err)
		return
	}

	if len(rules) == 0 {
		slog.Info("webhook_dm: no matching rules", "text", text)
		BroadcastWebhookEvent(WebhookSSEEvent{
			Type: "dm", Sender: senderID,
			TriggerText: text, Status: "no_match",
			Response:  "Nenhuma regra ativa corresponde a esta DM",
			Timestamp: time.Now().Format(time.RFC3339),
		})
		return
	}

	for _, mr := range rules {
		rule := mr.Rule
		keyword := mr.Keyword

		if hasCooldown(ctx, senderID, rule.ID) {
			logAutoReply(ctx, rule, "dm", senderID, "", text, rule.ResponseMessage, "", "skipped_cooldown", "", creds.OrgID)
			BroadcastWebhookEvent(WebhookSSEEvent{
				Type: "dm", RuleName: rule.Name, Sender: senderID,
				TriggerText: text, Response: rule.ResponseMessage,
				Status: "skipped_cooldown", Timestamp: time.Now().Format(time.RFC3339),
			})
			continue
		}

		dmMsg := replaceTemplateVars(rule.ResponseMessage, "", keyword)
		err := sendInstagramDM(creds.AccountID, creds.Token, senderID, dmMsg)
		if err != nil {
			slog.Error("webhook_dm: send DM failed", "error", err, "sender", senderID, "rule", rule.Name)
			logAutoReply(ctx, rule, "dm", senderID, "", text, dmMsg, "", "failed", err.Error(), creds.OrgID)
			BroadcastWebhookEvent(WebhookSSEEvent{
				Type: "dm", RuleName: rule.Name, Sender: senderID,
				TriggerText: text, Response: dmMsg,
				Status: "failed", Timestamp: time.Now().Format(time.RFC3339),
			})
			continue
		}

		slog.Info("webhook_dm: DM sent", "sender", senderID, "rule", rule.Name)
		logAutoReply(ctx, rule, "dm", senderID, "", text, dmMsg, "", "sent", "", creds.OrgID)
		BroadcastWebhookEvent(WebhookSSEEvent{
			Type: "dm", RuleName: rule.Name, Sender: senderID,
			TriggerText: text, Response: dmMsg,
			Status: "sent", Timestamp: time.Now().Format(time.RFC3339),
		})
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────

// resolveCredsByAccountID finds Instagram credentials by account ID.
// Searches all user configs, then falls back to env vars.
func resolveCredsByAccountID(ctx context.Context, igAccountID string) (*instagramCredentials, error) {
	// Try to find a user config that matches this account ID
	if crypto.Available() {
		var cfg models.InstagramConfig
		err := database.InstagramConfigs().FindOne(ctx, bson.M{
			"instagram_account_id": igAccountID,
		}).Decode(&cfg)
		if err == nil {
			token, err := crypto.Decrypt(cfg.AccessTokenEnc)
			if err != nil {
				return nil, fmt.Errorf("decrypt token: %w", err)
			}
			return &instagramCredentials{
				AccountID: cfg.InstagramAccountID,
				Token:     token,
				Source:    "user",
				OrgID:     cfg.OrgID,
			}, nil
		}
		if err != mongo.ErrNoDocuments {
			return nil, fmt.Errorf("db error: %w", err)
		}
	}

	// Fallback to env
	envCfg := config.Get()
	if envCfg.InstagramAccountID == igAccountID && envCfg.InstagramToken != "" {
		return &instagramCredentials{
			AccountID: envCfg.InstagramAccountID,
			Token:     envCfg.InstagramToken,
			Source:    "env",
		}, nil
	}

	// If env account ID doesn't match but we have env creds, use them anyway
	// (webhooks are bound to the app, not a specific account)
	if envCfg.InstagramAccountID != "" && envCfg.InstagramToken != "" {
		return &instagramCredentials{
			AccountID: envCfg.InstagramAccountID,
			Token:     envCfg.InstagramToken,
			Source:    "env",
		}, nil
	}

	return nil, nil
}

// matchedRule pairs a rule with the keyword that triggered it.
type matchedRule struct {
	Rule    models.AutoReplyRule
	Keyword string
}

// findMatchingRules returns active rules whose keywords match the text, scoped to an org.
func findMatchingRules(ctx context.Context, text, triggerType, mediaID string, orgID primitive.ObjectID) ([]matchedRule, error) {
	filter := bson.M{
		"active": true,
		"$or": []bson.M{
			{"trigger_type": triggerType},
			{"trigger_type": "both"},
		},
	}

	// Scope to org if available
	if orgID != primitive.NilObjectID {
		filter["org_id"] = orgID
	}

	cursor, err := database.AutoReplyRules().Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var matched []matchedRule
	textLower := strings.ToLower(text)

	for cursor.Next(ctx) {
		var rule models.AutoReplyRule
		if err := cursor.Decode(&rule); err != nil {
			continue
		}

		// If rule is limited to specific posts, check if this media matches
		if triggerType == "comment" && len(rule.PostIDs) > 0 && mediaID != "" {
			found := false
			for _, pid := range rule.PostIDs {
				if pid == mediaID {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Check if any keyword matches
		for _, kw := range rule.Keywords {
			if strings.Contains(textLower, strings.ToLower(kw)) {
				matched = append(matched, matchedRule{Rule: rule, Keyword: kw})
				break
			}
		}
	}

	return matched, nil
}

// hasCooldown checks if a DM was already sent to this user for this rule in the last 24h.
func hasCooldown(ctx context.Context, senderIGID string, ruleID primitive.ObjectID) bool {
	cutoff := time.Now().Add(-24 * time.Hour)
	count, err := database.AutoReplyLogs().CountDocuments(ctx, bson.M{
		"sender_ig_id": senderIGID,
		"rule_id":      ruleID,
		"status":       "sent",
		"created_at":   bson.M{"$gte": cutoff},
	})
	if err != nil {
		slog.Error("cooldown_check_error", "error", err)
		return true // fail-safe: assume cooldown active
	}
	return count > 0
}

// sendInstagramDM sends a DM via the Instagram Messaging API using recipient user ID.
// Use this for DM-triggered auto-replies (user already has a conversation with you).
func sendInstagramDM(accountID, token, recipientID, message string) error {
	url := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/messages", accountID)

	payload := map[string]interface{}{
		"recipient": map[string]string{"id": recipientID},
		"message":   map[string]string{"text": message},
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("instagram API error %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// sendPrivateReply sends a DM via Instagram Private Replies API using comment_id.
// This is the correct way to initiate a DM from a comment — you cannot use recipient.id
// because Instagram only allows DMs to users who have messaged you first.
// Private Replies uses comment_id as the recipient identifier.
func sendPrivateReply(accountID, token, commentID, message string) error {
	url := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/messages", accountID)

	payload := map[string]interface{}{
		"recipient": map[string]string{"comment_id": commentID},
		"message":   map[string]string{"text": message},
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("instagram API error %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// replaceTemplateVars substitutes {{username}} and {{keyword}} in a message.
func replaceTemplateVars(message, username, keyword string) string {
	msg := strings.ReplaceAll(message, "{{username}}", username)
	msg = strings.ReplaceAll(msg, "{{keyword}}", keyword)
	return msg
}

// sendCommentReply posts a public reply to an Instagram comment.
func sendCommentReply(token, commentID, message string) error {
	url := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/replies", commentID)

	payload := map[string]string{"message": message}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("instagram API error %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// logAutoReply inserts an auto-reply log entry and upserts the lead when status is "sent".
func logAutoReply(ctx context.Context, rule models.AutoReplyRule, triggerType, senderIGID, senderUsername, triggerText, responseSent, commentReplySent, status, errMsg string, orgID primitive.ObjectID) {
	logEntry := models.AutoReplyLog{
		RuleID:           rule.ID,
		OrgID:            orgID,
		RuleName:         rule.Name,
		TriggerType:      triggerType,
		SenderIGID:       senderIGID,
		SenderUsername:   senderUsername,
		TriggerText:      triggerText,
		ResponseSent:     responseSent,
		CommentReplySent: commentReplySent,
		Status:           status,
		ErrorMessage:     errMsg,
		CreatedAt:        time.Now(),
	}

	_, err := database.AutoReplyLogs().InsertOne(ctx, logEntry)
	if err != nil {
		slog.Error("log_autoreply_insert_error", "error", err)
	}

	// Upsert lead when a DM was actually sent
	if status == "sent" {
		upsertInstagramLead(ctx, senderIGID, senderUsername, triggerType, rule.Name)
	}
}

// upsertInstagramLead creates or updates a lead entry.
func upsertInstagramLead(ctx context.Context, senderIGID, senderUsername, source, ruleName string) {
	now := time.Now()
	filter := bson.M{"sender_ig_id": senderIGID}
	update := bson.M{
		"$set": bson.M{
			"sender_username":  senderUsername,
			"last_interaction": now,
			"updated_at":       now,
		},
		"$inc":      bson.M{"interaction_count": 1},
		"$addToSet": bson.M{"sources": source, "rules_triggered": ruleName},
		"$setOnInsert": bson.M{
			"first_interaction": now,
			"tags":              []string{},
			"created_at":        now,
		},
	}

	opts := options.Update().SetUpsert(true)
	_, err := database.InstagramLeads().UpdateOne(ctx, filter, update, opts)
	if err != nil {
		slog.Error("upsert_lead_error", "error", err, "sender", senderIGID)
	}
}
