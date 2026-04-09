package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ContabilUserMapping links a tron-legacy user to a contabil user within an org.
type ContabilUserMapping struct {
	ID             primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	TronUserID     primitive.ObjectID `json:"tronUserId" bson:"tron_user_id"`
	TronEmail      string             `json:"tronEmail" bson:"tron_email"`
	ContabilRole   string             `json:"contabilRole" bson:"contabil_role"` // ADMIN, OPERATOR, VIEWER
	OrgID          primitive.ObjectID `json:"-" bson:"org_id"`
	ContabilOrgID  string             `json:"contabilOrgId,omitempty" bson:"contabil_org_id,omitempty"`
	IsActive       bool               `json:"isActive" bson:"is_active"`
	CreatedAt      time.Time          `json:"createdAt" bson:"created_at"`
	UpdatedAt      time.Time          `json:"updatedAt" bson:"updated_at"`
}

// ValidContabilRole checks if a contabil role string is valid.
func ValidContabilRole(role string) bool {
	switch role {
	case "ADMIN", "OPERATOR", "VIEWER":
		return true
	}
	return false
}

// DefaultContabilRole maps tron org roles to default contabil roles.
func DefaultContabilRole(orgRole string) string {
	switch orgRole {
	case "owner", "admin":
		return "ADMIN"
	case "member":
		return "OPERATOR"
	case "viewer":
		return "VIEWER"
	default:
		return "VIEWER"
	}
}

// ── Request/Response types ──────────────────────────────────────────

type CreateContabilMappingRequest struct {
	TronUserID   string `json:"tronUserId"`
	Email        string `json:"email"`
	ContabilRole string `json:"contabilRole"`
}

type UpdateContabilMappingRequest struct {
	ContabilRole string `json:"contabilRole"`
	IsActive     *bool  `json:"isActive,omitempty"`
}

type ContabilMappingResponse struct {
	ContabilUserMapping `json:",inline"`
	UserName            string `json:"userName,omitempty"`
	UserEmail           string `json:"userEmail,omitempty"`
}

type ContabilMappingListResponse struct {
	Mappings []ContabilMappingResponse `json:"mappings"`
	Total    int                       `json:"total"`
}
