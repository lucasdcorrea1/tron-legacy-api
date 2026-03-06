package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// InstagramLead represents a user who interacted via Instagram auto-reply.
type InstagramLead struct {
	ID               primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	SenderIGID       string             `json:"sender_ig_id" bson:"sender_ig_id"`
	SenderUsername   string             `json:"sender_username" bson:"sender_username"`
	FirstInteraction time.Time          `json:"first_interaction" bson:"first_interaction"`
	LastInteraction  time.Time          `json:"last_interaction" bson:"last_interaction"`
	InteractionCount int                `json:"interaction_count" bson:"interaction_count"`
	Sources          []string           `json:"sources" bson:"sources"`           // "comment", "dm"
	RulesTriggered   []string           `json:"rules_triggered" bson:"rules_triggered"` // rule names
	Tags             []string           `json:"tags" bson:"tags"`
	CreatedAt        time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at" bson:"updated_at"`
}

// UpdateLeadTagsRequest is the request body for updating lead tags.
type UpdateLeadTagsRequest struct {
	Tags []string `json:"tags"`
}

// LeadStatsResponse is the summary stats for leads.
type LeadStatsResponse struct {
	Total       int64            `json:"total"`
	NewThisWeek int64            `json:"new_this_week"`
	BySource    map[string]int64 `json:"by_source"`
}

// LeadListResponse is a paginated list of leads.
type LeadListResponse struct {
	Leads []InstagramLead `json:"leads"`
	Total int64           `json:"total"`
	Page  int             `json:"page"`
	Limit int             `json:"limit"`
}
