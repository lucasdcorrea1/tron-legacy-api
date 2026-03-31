package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// FacebookSchedule represents a scheduled Facebook Page post
type FacebookSchedule struct {
	ID           primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID       primitive.ObjectID `json:"user_id" bson:"user_id"`
	OrgID        primitive.ObjectID `json:"org_id" bson:"org_id"`
	Message      string             `json:"message" bson:"message"`
	MediaType    string             `json:"media_type" bson:"media_type"` // "text", "image", "carousel", "link"
	ImageIDs     []string           `json:"image_ids" bson:"image_ids"`   // IDs of images in the images collection
	LinkURL      string             `json:"link_url,omitempty" bson:"link_url,omitempty"`
	ScheduledAt  time.Time          `json:"scheduled_at" bson:"scheduled_at"`
	Status       string             `json:"status" bson:"status"` // "scheduled", "publishing", "published", "failed"
	FBPostID     string             `json:"fb_post_id,omitempty" bson:"fb_post_id,omitempty"`
	ErrorMessage string             `json:"error_message,omitempty" bson:"error_message,omitempty"`
	CreatedAt    time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at" bson:"updated_at"`
}

// CreateFacebookScheduleRequest is the request body for creating a scheduled post
type CreateFacebookScheduleRequest struct {
	Message     string   `json:"message"`
	MediaType   string   `json:"media_type"`
	ImageIDs    []string `json:"image_ids"`
	LinkURL     string   `json:"link_url,omitempty"`
	ScheduledAt string   `json:"scheduled_at"` // ISO 8601
}

// UpdateFacebookScheduleRequest is the request body for updating a scheduled post
type UpdateFacebookScheduleRequest struct {
	Message     *string  `json:"message,omitempty"`
	MediaType   *string  `json:"media_type,omitempty"`
	ImageIDs    []string `json:"image_ids,omitempty"`
	LinkURL     *string  `json:"link_url,omitempty"`
	ScheduledAt *string  `json:"scheduled_at,omitempty"` // ISO 8601
}

// FacebookScheduleResponse is the response for a single schedule with image URLs
type FacebookScheduleResponse struct {
	FacebookSchedule `json:",inline"`
	ImageURLs        []string `json:"image_urls"`
}

// FacebookScheduleListResponse is the paginated response for listing schedules
type FacebookScheduleListResponse struct {
	Schedules []FacebookScheduleResponse `json:"schedules"`
	Total     int64                      `json:"total"`
	Page      int                        `json:"page"`
	Limit     int                        `json:"limit"`
}

// FacebookConfig stores per-org Facebook Page credentials in the database
type FacebookConfig struct {
	ID              primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID          primitive.ObjectID `json:"user_id" bson:"user_id"`
	OrgID           primitive.ObjectID `json:"org_id" bson:"org_id"`
	PageID          string             `json:"page_id" bson:"page_id"`
	PageAccessToken string             `json:"-" bson:"page_access_token_enc"` // never sent to client (encrypted)
	PageName        string             `json:"page_name,omitempty" bson:"page_name,omitempty"`
	CreatedAt       time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at" bson:"updated_at"`
}

// SaveFacebookConfigRequest is the request body for saving Facebook config
type SaveFacebookConfigRequest struct {
	PageID          string `json:"page_id"`
	PageAccessToken string `json:"page_access_token"`
}

// FacebookConfigResponse indicates whether Facebook is configured
type FacebookConfigResponse struct {
	Configured bool   `json:"configured"`
	HasToken   bool   `json:"has_token"`
	PageID     string `json:"page_id"`
	PageName   string `json:"page_name,omitempty"`
	Source     string `json:"source"` // "user" or "env"
}

// FacebookPage represents a Facebook Page accessible to the user
type FacebookPage struct {
	PageID      string `json:"page_id"`
	PageName    string `json:"page_name"`
	AccessToken string `json:"access_token,omitempty"` // Page access token (only in internal use)
	Category    string `json:"category,omitempty"`
}
