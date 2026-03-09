package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// InstagramSchedule represents a scheduled Instagram post
type InstagramSchedule struct {
	ID           primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID       primitive.ObjectID `json:"user_id" bson:"user_id"`
	OrgID        primitive.ObjectID `json:"org_id" bson:"org_id"`
	Caption      string             `json:"caption" bson:"caption"`
	MediaType    string             `json:"media_type" bson:"media_type"` // "image" or "carousel"
	ImageIDs     []string           `json:"image_ids" bson:"image_ids"`   // IDs of images in the images collection
	ScheduledAt  time.Time          `json:"scheduled_at" bson:"scheduled_at"`
	Status       string             `json:"status" bson:"status"` // "scheduled", "publishing", "published", "failed"
	IGMediaID    string             `json:"ig_media_id,omitempty" bson:"ig_media_id,omitempty"`
	ErrorMessage string             `json:"error_message,omitempty" bson:"error_message,omitempty"`
	CreatedAt    time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at" bson:"updated_at"`
}

// CreateInstagramScheduleRequest is the request body for creating a scheduled post
type CreateInstagramScheduleRequest struct {
	Caption     string   `json:"caption"`
	MediaType   string   `json:"media_type"`
	ImageIDs    []string `json:"image_ids"`
	ScheduledAt string   `json:"scheduled_at"` // ISO 8601
}

// UpdateInstagramScheduleRequest is the request body for updating a scheduled post
type UpdateInstagramScheduleRequest struct {
	Caption     *string  `json:"caption,omitempty"`
	MediaType   *string  `json:"media_type,omitempty"`
	ImageIDs    []string `json:"image_ids,omitempty"`
	ScheduledAt *string  `json:"scheduled_at,omitempty"` // ISO 8601
}

// InstagramScheduleResponse is the response for a single schedule with image URLs
type InstagramScheduleResponse struct {
	InstagramSchedule `json:",inline"`
	ImageURLs         []string `json:"image_urls"`
}

// InstagramScheduleListResponse is the paginated response for listing schedules
type InstagramScheduleListResponse struct {
	Schedules []InstagramScheduleResponse `json:"schedules"`
	Total     int64                       `json:"total"`
	Page      int                         `json:"page"`
	Limit     int                         `json:"limit"`
}

// InstagramConfig stores per-user Instagram credentials in the database
type InstagramConfig struct {
	ID                 primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID             primitive.ObjectID `json:"user_id" bson:"user_id"`
	OrgID              primitive.ObjectID `json:"org_id" bson:"org_id"`
	InstagramAccountID string             `json:"instagram_account_id" bson:"instagram_account_id"`
	AccessTokenEnc     string             `json:"-" bson:"access_token_enc"` // never sent to client
	AdAccountID        string             `json:"ad_account_id,omitempty" bson:"ad_account_id,omitempty"`
	BusinessID         string             `json:"business_id,omitempty" bson:"business_id,omitempty"`
	CreatedAt          time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at" bson:"updated_at"`
}

// SaveInstagramConfigRequest is the request body for saving Instagram config
type SaveInstagramConfigRequest struct {
	InstagramAccountID string `json:"instagram_account_id"`
	AccessToken        string `json:"access_token"`
	AdAccountID        string `json:"ad_account_id,omitempty"`
	BusinessID         string `json:"business_id,omitempty"`
}

// InstagramConfigResponse indicates whether Instagram is configured
type InstagramConfigResponse struct {
	Configured  bool   `json:"configured"`
	AccountID   string `json:"account_id"`
	Source      string `json:"source"` // "user" or "env"
	AdAccountID string `json:"ad_account_id,omitempty"`
	BusinessID  string `json:"business_id,omitempty"`
}
