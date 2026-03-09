package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// IntegratedPublish represents a unified Instagram post + Meta Ads campaign publish.
type IntegratedPublish struct {
	ID     primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID primitive.ObjectID `json:"user_id" bson:"user_id"`
	OrgID  primitive.ObjectID `json:"org_id" bson:"org_id"`

	// Instagram fields
	Caption   string   `json:"caption" bson:"caption"`
	MediaType string   `json:"media_type" bson:"media_type"` // "image" or "carousel"
	ImageIDs  []string `json:"image_ids" bson:"image_ids"`

	// Scheduling
	ScheduledAt time.Time `json:"scheduled_at" bson:"scheduled_at"`
	Status      string    `json:"status" bson:"status"`
	// Statuses: "scheduled", "publishing_ig", "publishing_ads", "completed", "failed"

	// Instagram result
	IGMediaID string `json:"ig_media_id,omitempty" bson:"ig_media_id,omitempty"`

	// Meta Ads campaign config
	Campaign IntegratedCampaignConfig `json:"campaign" bson:"campaign"`

	// Meta Ads result
	MetaCampaignID string `json:"meta_campaign_id,omitempty" bson:"meta_campaign_id,omitempty"`
	MetaAdSetID    string `json:"meta_adset_id,omitempty" bson:"meta_adset_id,omitempty"`
	MetaAdID       string `json:"meta_ad_id,omitempty" bson:"meta_ad_id,omitempty"`

	// Error tracking
	ErrorMessage string `json:"error_message,omitempty" bson:"error_message,omitempty"`
	ErrorPhase   string `json:"error_phase,omitempty" bson:"error_phase,omitempty"` // "ig" or "ads"

	CreatedAt time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
}

// IntegratedCampaignConfig holds the Meta Ads campaign configuration.
type IntegratedCampaignConfig struct {
	Name         string         `json:"name" bson:"name"`
	Objective    string         `json:"objective" bson:"objective"`
	DailyBudget  int64          `json:"daily_budget" bson:"daily_budget"` // in cents
	DurationDays int            `json:"duration_days" bson:"duration_days"`
	Targeting    AdSetTargeting `json:"targeting" bson:"targeting"`
	Creative     IntegratedCreativeConfig `json:"creative" bson:"creative"`
}

// IntegratedCreativeConfig holds ad creative settings.
type IntegratedCreativeConfig struct {
	Title        string `json:"title,omitempty" bson:"title,omitempty"`
	Body         string `json:"body,omitempty" bson:"body,omitempty"`
	CallToAction string `json:"call_to_action,omitempty" bson:"call_to_action,omitempty"`
	LinkURL      string `json:"link_url,omitempty" bson:"link_url,omitempty"`
}

type CreateIntegratedPublishRequest struct {
	Caption     string                   `json:"caption"`
	MediaType   string                   `json:"media_type"`
	ImageIDs    []string                 `json:"image_ids"`
	ScheduledAt string                   `json:"scheduled_at"` // ISO 8601
	Campaign    IntegratedCampaignConfig `json:"campaign"`
}

type IntegratedPublishResponse struct {
	IntegratedPublish `json:",inline"`
	ImageURLs         []string `json:"image_urls"`
}

type IntegratedPublishListResponse struct {
	Items []IntegratedPublishResponse `json:"items"`
	Total int64                       `json:"total"`
	Page  int                         `json:"page"`
	Limit int                         `json:"limit"`
}
