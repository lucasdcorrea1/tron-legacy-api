package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// AIConfig stores per-org AI (Claude) API key in the database
type AIConfig struct {
	ID        primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID    primitive.ObjectID `json:"user_id" bson:"user_id"`
	OrgID     primitive.ObjectID `json:"org_id" bson:"org_id"`
	APIKeyEnc string             `json:"-" bson:"api_key_enc"` // never sent to client
	Model     string             `json:"model" bson:"model"`
	CreatedAt time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time          `json:"updated_at" bson:"updated_at"`
}

// SaveAIConfigRequest is the request body for saving AI config
type SaveAIConfigRequest struct {
	APIKey string `json:"api_key"`
	Model  string `json:"model,omitempty"` // default "claude-sonnet-4-5-20250929"
}

// AIConfigResponse indicates whether AI is configured
type AIConfigResponse struct {
	Configured bool   `json:"configured"`
	Model      string `json:"model,omitempty"`
	KeyPrefix  string `json:"key_prefix,omitempty"` // masked key
}

// AIGenerateRequest is the request body for generating AI content
type AIGenerateRequest struct {
	Type       string `json:"type"`                  // "caption" or "campaign_name"
	Context    string `json:"context,omitempty"`      // optional user context
	MediaCount int    `json:"media_count,omitempty"`  // number of media items
	MediaType  string `json:"media_type,omitempty"`   // "image" or "carousel"
	Language   string `json:"language,omitempty"`      // default "pt-BR"
}

// AIGenerateResponse is the response for generated AI content
type AIGenerateResponse struct {
	Text         string `json:"text"`
	CampaignName string `json:"campaign_name,omitempty"`
	TokensUsed   int    `json:"tokens_used,omitempty"`
}
