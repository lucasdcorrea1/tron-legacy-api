package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// AutoReplyRule defines a keyword-triggered auto-response rule for Instagram.
type AutoReplyRule struct {
	ID              primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID          primitive.ObjectID `json:"user_id" bson:"user_id"`
	Name            string             `json:"name" bson:"name"`
	TriggerType     string             `json:"trigger_type" bson:"trigger_type"` // "comment", "dm", "both"
	Keywords        []string           `json:"keywords" bson:"keywords"`
	ResponseMessage string             `json:"response_message" bson:"response_message"`
	CommentReply    string             `json:"comment_reply,omitempty" bson:"comment_reply,omitempty"`
	Active          bool               `json:"active" bson:"active"`
	PostIDs         []string           `json:"post_ids,omitempty" bson:"post_ids,omitempty"` // limit to specific posts (optional)
	CreatedAt       time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at" bson:"updated_at"`
}

// AutoReplyLog records each auto-reply action (sent, failed, skipped).
type AutoReplyLog struct {
	ID               primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	RuleID           primitive.ObjectID `json:"rule_id" bson:"rule_id"`
	RuleName         string             `json:"rule_name" bson:"rule_name"`
	TriggerType      string             `json:"trigger_type" bson:"trigger_type"` // "comment" or "dm"
	SenderIGID       string             `json:"sender_ig_id" bson:"sender_ig_id"`
	SenderUsername   string             `json:"sender_username,omitempty" bson:"sender_username,omitempty"`
	TriggerText      string             `json:"trigger_text" bson:"trigger_text"`
	ResponseSent     string             `json:"response_sent" bson:"response_sent"`
	CommentReplySent string             `json:"comment_reply_sent,omitempty" bson:"comment_reply_sent,omitempty"`
	Status           string             `json:"status" bson:"status"` // "sent", "failed", "skipped_cooldown"
	ErrorMessage     string             `json:"error_message,omitempty" bson:"error_message,omitempty"`
	CreatedAt        time.Time          `json:"created_at" bson:"created_at"`
}

// CreateAutoReplyRuleRequest is the request body for creating a rule.
type CreateAutoReplyRuleRequest struct {
	Name            string   `json:"name"`
	TriggerType     string   `json:"trigger_type"`
	Keywords        []string `json:"keywords"`
	ResponseMessage string   `json:"response_message"`
	CommentReply    string   `json:"comment_reply,omitempty"`
	PostIDs         []string `json:"post_ids,omitempty"`
}

// UpdateAutoReplyRuleRequest is the request body for updating a rule.
type UpdateAutoReplyRuleRequest struct {
	Name            *string  `json:"name,omitempty"`
	TriggerType     *string  `json:"trigger_type,omitempty"`
	Keywords        []string `json:"keywords,omitempty"`
	ResponseMessage *string  `json:"response_message,omitempty"`
	CommentReply    *string  `json:"comment_reply,omitempty"`
	PostIDs         []string `json:"post_ids,omitempty"`
}

// AutoReplyRuleResponse is the API response for a single rule.
type AutoReplyRuleResponse struct {
	AutoReplyRule `json:",inline"`
}

// AutoReplyRuleListResponse is a paginated list of rules.
type AutoReplyRuleListResponse struct {
	Rules []AutoReplyRule `json:"rules"`
	Total int64           `json:"total"`
}

// AutoReplyLogListResponse is a paginated list of logs.
type AutoReplyLogListResponse struct {
	Logs  []AutoReplyLog `json:"logs"`
	Total int64          `json:"total"`
	Page  int            `json:"page"`
	Limit int            `json:"limit"`
}
