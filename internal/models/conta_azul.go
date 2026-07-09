package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// PortalTheme is the white-label configuration of the Conta Azul portal.
// Can be set at the org level (default for all EndClients) or per-EndClient
// (overrides the org default). All fields optional — empty falls back.
type PortalTheme struct {
	LogoURL        string `json:"logo_url,omitempty" bson:"logo_url,omitempty"`
	BrandName      string `json:"brand_name,omitempty" bson:"brand_name,omitempty"` // Replaces "Painel Financeiro"
	PrimaryColor   string `json:"primary_color,omitempty" bson:"primary_color,omitempty"`
	SuccessColor   string `json:"success_color,omitempty" bson:"success_color,omitempty"`
	DangerColor    string `json:"danger_color,omitempty" bson:"danger_color,omitempty"`
	BgColor        string `json:"bg_color,omitempty" bson:"bg_color,omitempty"`
	SurfaceColor   string `json:"surface_color,omitempty" bson:"surface_color,omitempty"`
	TextColor      string `json:"text_color,omitempty" bson:"text_color,omitempty"`
	TextMutedColor string `json:"text_muted_color,omitempty" bson:"text_muted_color,omitempty"`
}

// ── EndClient (cliente-do-cliente) ──────────────────────────────────
// Represents an end-customer of an organization. Not a SaaS user.
// Has restricted access only to its own Conta Azul dashboard via /portal.
type EndClient struct {
	ID              primitive.ObjectID    `json:"id" bson:"_id,omitempty"`
	OrgID           primitive.ObjectID    `json:"org_id" bson:"org_id"`
	Name            string                `json:"name" bson:"name"`
	Email           string                `json:"email" bson:"email"`
	PasswordHash    string                `json:"-" bson:"password_hash"`
	Phone           string                `json:"phone,omitempty" bson:"phone,omitempty"`
	Document        string                `json:"document,omitempty" bson:"document,omitempty"` // CPF/CNPJ
	DashboardAccess []string              `json:"dashboard_access" bson:"dashboard_access"`     // ["revenue","expense","profit","cashflow","categories","upcoming"]
	PortalTheme     *PortalTheme          `json:"portal_theme,omitempty" bson:"portal_theme,omitempty"`
	ContaAzulConn   *ContaAzulConnection  `json:"-" bson:"conta_azul_conn,omitempty"`
	IsActive        bool                  `json:"is_active" bson:"is_active"`
	LastLoginAt     time.Time             `json:"last_login_at,omitempty" bson:"last_login_at,omitempty"`
	CreatedAt       time.Time             `json:"created_at" bson:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at" bson:"updated_at"`
}

// ContaAzulConnection holds the OAuth tokens for an EndClient's Conta Azul account.
// Tokens are encrypted at rest using the same crypto package as Meta OAuth.
type ContaAzulConnection struct {
	AccessTokenEnc  string    `json:"-" bson:"access_token_enc"`
	RefreshTokenEnc string    `json:"-" bson:"refresh_token_enc"`
	ExpiresAt       time.Time `json:"expires_at" bson:"expires_at"`
	Scope           string    `json:"scope,omitempty" bson:"scope,omitempty"`
	ConnectedAt     time.Time `json:"connected_at" bson:"connected_at"`
	LastSyncAt      time.Time `json:"last_sync_at,omitempty" bson:"last_sync_at,omitempty"`
}

// EndClientRefreshToken is a hashed refresh token for the portal session.
type EndClientRefreshToken struct {
	ID          primitive.ObjectID `bson:"_id,omitempty"`
	EndClientID primitive.ObjectID `bson:"end_client_id"`
	TokenHash   string             `bson:"token_hash"`
	ExpiresAt   time.Time          `bson:"expires_at"`
	CreatedAt   time.Time          `bson:"created_at"`
}

// AllDashboardAccess lists every dashboard widget that can be toggled per EndClient.
var AllDashboardAccess = []string{
	"revenue",    // Total Receita
	"expense",    // Total Despesa
	"profit",     // Lucro
	"cashflow",   // Fluxo de Caixa (chart)
	"categories", // Top categorias DRE
	"upcoming",   // Próximos vencimentos
}

// ValidDashboardAccess checks if the access key is valid.
func ValidDashboardAccess(k string) bool {
	for _, v := range AllDashboardAccess {
		if v == k {
			return true
		}
	}
	return false
}

// ── Request/Response types ─────────────────────────────────────────

type CreateEndClientRequest struct {
	Name            string   `json:"name"`
	Email           string   `json:"email"`
	Password        string   `json:"password"`
	Phone           string   `json:"phone,omitempty"`
	Document        string   `json:"document,omitempty"`
	DashboardAccess []string `json:"dashboard_access,omitempty"`
}

type UpdateEndClientRequest struct {
	Name            *string   `json:"name,omitempty"`
	Phone           *string   `json:"phone,omitempty"`
	Document        *string   `json:"document,omitempty"`
	DashboardAccess *[]string `json:"dashboard_access,omitempty"`
	IsActive        *bool     `json:"is_active,omitempty"`
}

type EndClientLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// EndClientSession is the public view of the EndClient (no secrets).
type EndClientSession struct {
	ID              primitive.ObjectID `json:"id"`
	OrgID           primitive.ObjectID `json:"org_id"`
	Name            string             `json:"name"`
	Email           string             `json:"email"`
	DashboardAccess []string           `json:"dashboard_access"`
	HasContaAzul    bool               `json:"has_conta_azul"`
	IsActive        bool               `json:"is_active"`
}

type EndClientAuthResponse struct {
	EndClient    EndClientSession `json:"end_client"`
	Token        string           `json:"token"`
	RefreshToken string           `json:"refresh_token"`
}

type ContaAzulConnectURLResponse struct {
	URL string `json:"url"`
}

type ContaAzulCallbackRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

type EndClientListItem struct {
	ID              primitive.ObjectID `json:"id"`
	Name            string             `json:"name"`
	Email           string             `json:"email"`
	IsActive        bool               `json:"is_active"`
	HasContaAzul    bool               `json:"has_conta_azul"`
	DashboardAccess []string           `json:"dashboard_access"`
	PortalTheme     *PortalTheme       `json:"portal_theme,omitempty"`
	LastLoginAt     time.Time          `json:"last_login_at,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
}

type EndClientListResponse struct {
	EndClients []EndClientListItem `json:"end_clients"`
	Total      int64               `json:"total"`
}

// ── Conta Azul Dashboard DTOs ──────────────────────────────────────

// ContaAzulSummary is the headline numbers for the dashboard.
type ContaAzulSummary struct {
	PeriodStart    time.Time `json:"period_start"`
	PeriodEnd      time.Time `json:"period_end"`
	TotalRevenue   float64   `json:"total_revenue"`   // soma de contas a receber pagas
	TotalExpense   float64   `json:"total_expense"`   // soma de contas a pagar pagas
	Profit         float64   `json:"profit"`          // revenue - expense
	OpenReceivable float64   `json:"open_receivable"` // contas a receber em aberto
	OpenPayable    float64   `json:"open_payable"`    // contas a pagar em aberto
	Compare        *ContaAzulComparison `json:"compare,omitempty"`
}

// ContaAzulComparison compares with previous equivalent period.
type ContaAzulComparison struct {
	RevenueChangePct float64 `json:"revenue_change_pct"`
	ExpenseChangePct float64 `json:"expense_change_pct"`
	ProfitChangePct  float64 `json:"profit_change_pct"`
}

// CashFlowPoint is a single point in the cashflow chart.
type CashFlowPoint struct {
	Date    string  `json:"date"`    // "2026-06" or "2026-06-15" depending on grain
	Revenue float64 `json:"revenue"`
	Expense float64 `json:"expense"`
	Balance float64 `json:"balance"` // running balance
}

// CategoryBreakdown is one slice of the DRE category pie chart.
type CategoryBreakdown struct {
	CategoryID string  `json:"category_id"`
	Name       string  `json:"name"`
	Amount     float64 `json:"amount"`
	Percentage float64 `json:"percentage"`
	Kind       string  `json:"kind"` // "revenue" or "expense"
}

// UpcomingItem is one row in the upcoming due payments widget.
type UpcomingItem struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	DueDate     time.Time `json:"due_date"`
	Amount      float64   `json:"amount"`
	Kind        string    `json:"kind"` // "receivable" or "payable"
	Status      string    `json:"status"`
}
