package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// APIKey lets another system (e.g. an online store) call a limited set of
// org-scoped routes without a user session. Only the SHA-256 hash of the key
// is stored; the plain key is shown once, at creation.
type APIKey struct {
	ID         primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	OrgID      primitive.ObjectID `json:"org_id" bson:"org_id"`
	UserID     primitive.ObjectID `json:"user_id" bson:"user_id"` // creator; requests act as this member
	Name       string             `json:"name" bson:"name"`
	Prefix     string             `json:"prefix" bson:"prefix"` // first chars, to tell keys apart in the UI
	KeyHash    string             `json:"-" bson:"key_hash"`
	Scopes     []string           `json:"scopes" bson:"scopes"`
	LastUsedAt *time.Time         `json:"last_used_at,omitempty" bson:"last_used_at,omitempty"`
	RevokedAt  *time.Time         `json:"revoked_at,omitempty" bson:"revoked_at,omitempty"`
	CreatedAt  time.Time          `json:"created_at" bson:"created_at"`
}

// CreateAPIKeyRequest is the body of POST /admin/api-keys.
type CreateAPIKeyRequest struct {
	Name string `json:"name"`
}

// CreateAPIKeyResponse carries the plain key — the only time it is returned.
type CreateAPIKeyResponse struct {
	APIKey `json:",inline"`
	Key    string `json:"key"`
}
