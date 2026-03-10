package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Organization represents a company/team in the multi-tenant system.
type Organization struct {
	ID          primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	Name        string             `json:"name" bson:"name"`
	Slug        string             `json:"slug" bson:"slug"`
	LogoURL     string             `json:"logo_url,omitempty" bson:"logo_url,omitempty"`
	OwnerUserID primitive.ObjectID `json:"owner_user_id" bson:"owner_user_id"`
	Settings    OrgSettings        `json:"settings" bson:"settings"`
	CreatedAt   time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at" bson:"updated_at"`
}

// OrgSettings holds organization-level preferences.
type OrgSettings struct {
	DefaultLanguage string `json:"default_language,omitempty" bson:"default_language,omitempty"`
	DefaultCurrency string `json:"default_currency,omitempty" bson:"default_currency,omitempty"`
}

// OrgMembership links a user to an organization with a role.
type OrgMembership struct {
	ID          primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	OrgID       primitive.ObjectID `json:"org_id" bson:"org_id"`
	UserID      primitive.ObjectID `json:"user_id" bson:"user_id"`
	OrgRole     string             `json:"org_role" bson:"org_role"` // "owner", "admin", "member", "viewer"
	Permissions []string           `json:"permissions,omitempty" bson:"permissions,omitempty"`
	JoinedAt    time.Time          `json:"joined_at" bson:"joined_at"`
}

// OrgInvitation represents a pending invitation to join an organization.
type OrgInvitation struct {
	ID          primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	OrgID       primitive.ObjectID `json:"org_id" bson:"org_id"`
	Email       string             `json:"email" bson:"email"`
	OrgRole     string             `json:"org_role" bson:"org_role"`
	Permissions []string           `json:"permissions,omitempty" bson:"permissions,omitempty"`
	Token       string             `json:"token" bson:"token"`
	Status      string             `json:"status" bson:"status"` // "pending", "accepted", "expired"
	InvitedBy   primitive.ObjectID `json:"invited_by" bson:"invited_by"`
	ExpiresAt   time.Time          `json:"expires_at" bson:"expires_at"`
	CreatedAt   time.Time          `json:"created_at" bson:"created_at"`
}

// Subscription represents a plan subscription for an organization.
type Subscription struct {
	ID                  primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	OrgID               primitive.ObjectID `json:"org_id" bson:"org_id"`
	PlanID              string             `json:"plan_id" bson:"plan_id"` // "free", "starter", "pro", "enterprise"
	Status              string             `json:"status" bson:"status"`   // "active", "trialing", "canceled"
	CurrentPeriodEnd    time.Time          `json:"current_period_end" bson:"current_period_end"`
	AsaasCustomerID     string             `json:"asaas_customer_id,omitempty" bson:"asaas_customer_id,omitempty"`
	AsaasSubscriptionID string             `json:"asaas_subscription_id,omitempty" bson:"asaas_subscription_id,omitempty"`
	CreatedAt           time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at" bson:"updated_at"`
}

// PlanLimits defines the resource limits for a subscription plan.
type PlanLimits struct {
	MaxMembers          int `json:"max_members"`
	MaxScheduledPosts   int `json:"max_scheduled_posts"`
	MaxAutoReplyRules   int `json:"max_auto_reply_rules"`
	MaxAutoBoostRules   int `json:"max_auto_boost_rules"`
	MaxBudgetAlerts     int `json:"max_budget_alerts"`
	MaxCampaigns        int `json:"max_campaigns"`
	MaxIntegratedPubs   int `json:"max_integrated_pubs"`
}

// Plans defines the resource limits for each subscription plan. -1 = unlimited.
var Plans = map[string]PlanLimits{
	"free": {
		MaxMembers:        1,
		MaxScheduledPosts: 10,
		MaxAutoReplyRules: 3,
		MaxAutoBoostRules: 1,
		MaxBudgetAlerts:   2,
		MaxCampaigns:      3,
		MaxIntegratedPubs: 5,
	},
	"starter": {
		MaxMembers:        3,
		MaxScheduledPosts: 50,
		MaxAutoReplyRules: 10,
		MaxAutoBoostRules: 5,
		MaxBudgetAlerts:   10,
		MaxCampaigns:      10,
		MaxIntegratedPubs: 25,
	},
	"pro": {
		MaxMembers:        10,
		MaxScheduledPosts: -1,
		MaxAutoReplyRules: -1,
		MaxAutoBoostRules: -1,
		MaxBudgetAlerts:   -1,
		MaxCampaigns:      -1,
		MaxIntegratedPubs: -1,
	},
	"enterprise": {
		MaxMembers:        -1,
		MaxScheduledPosts: -1,
		MaxAutoReplyRules: -1,
		MaxAutoBoostRules: -1,
		MaxBudgetAlerts:   -1,
		MaxCampaigns:      -1,
		MaxIntegratedPubs: -1,
	},
}

// ── Request/Response types ───────────────────────────────────────────

type CreateOrgRequest struct {
	Name string `json:"name"`
}

type UpdateOrgRequest struct {
	Name    *string      `json:"name,omitempty"`
	LogoURL *string      `json:"logo_url,omitempty"`
	Settings *OrgSettings `json:"settings,omitempty"`
}

type InviteMemberRequest struct {
	Email       string   `json:"email"`
	OrgRole     string   `json:"org_role"`
	Permissions []string `json:"permissions,omitempty"`
}

type UpdateMemberRoleRequest struct {
	OrgRole string `json:"org_role"`
}

type UpdateMemberPermissionsRequest struct {
	Permissions []string `json:"permissions"`
}

// AllPermissions lists every granular permission available for members.
var AllPermissions = []string{
	"instagram:schedule",
	"instagram:autoreply",
	"instagram:leads",
	"instagram:config",
	"meta_ads:manage",
	"meta_ads:budget",
	"auto_boost:manage",
	"blog:manage",
	"email:manage",
	"ai:generate",
}

// ValidPermission checks if a permission string is valid.
func ValidPermission(p string) bool {
	for _, v := range AllPermissions {
		if v == p {
			return true
		}
	}
	return false
}

type OrgResponse struct {
	Organization  `json:",inline"`
	MemberCount   int      `json:"member_count"`
	MyRole        string   `json:"my_role"`
	MyPermissions []string `json:"my_permissions,omitempty"`
}

type OrgListResponse struct {
	Organizations []OrgResponse `json:"organizations"`
}

type MemberResponse struct {
	UserID      primitive.ObjectID `json:"user_id"`
	Name        string             `json:"name"`
	Email       string             `json:"email"`
	Avatar      string             `json:"avatar,omitempty"`
	OrgRole     string             `json:"org_role"`
	Permissions []string           `json:"permissions,omitempty"`
	JoinedAt    time.Time          `json:"joined_at"`
}

type MemberListResponse struct {
	Members     []MemberResponse `json:"members"`
	Invitations []OrgInvitation  `json:"invitations"`
}

type UsageResponse struct {
	PlanID string     `json:"plan_id"`
	Limits PlanLimits `json:"limits"`
	Usage  PlanUsage  `json:"usage"`
}

type PlanUsage struct {
	Members        int `json:"members"`
	ScheduledPosts int `json:"scheduled_posts"`
	AutoReplyRules int `json:"auto_reply_rules"`
	AutoBoostRules int `json:"auto_boost_rules"`
	BudgetAlerts   int `json:"budget_alerts"`
	Campaigns      int `json:"campaigns"`
	IntegratedPubs int `json:"integrated_pubs"`
}
