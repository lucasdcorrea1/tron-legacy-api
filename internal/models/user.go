package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/crypto/bcrypt"
)

// User represents authentication data
type User struct {
	ID           primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	Email        string             `json:"email" bson:"email"`
	PasswordHash string             `json:"-" bson:"password_hash"` // Never expose in JSON
	CreatedAt    time.Time          `json:"created_at" bson:"created_at"`
}

// Profile represents user profile data (separate from auth)
type Profile struct {
	ID        primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID    primitive.ObjectID `json:"user_id" bson:"user_id"`
	Name      string             `json:"name" bson:"name"`
	Avatar    string             `json:"avatar,omitempty" bson:"avatar,omitempty"`
	Bio       string             `json:"bio,omitempty" bson:"bio,omitempty"`
	Settings  ProfileSettings    `json:"settings" bson:"settings"`
	CreatedAt time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time          `json:"updated_at" bson:"updated_at"`
}

// ProfileSettings holds user preferences
type ProfileSettings struct {
	Currency       string        `json:"currency" bson:"currency"`
	Language       string        `json:"language" bson:"language"`
	Theme          ThemeSettings `json:"theme" bson:"theme"`
	FirstDayOfWeek int           `json:"first_day_of_week" bson:"first_day_of_week"` // 0=Sunday, 1=Monday
	DateFormat     string        `json:"date_format" bson:"date_format"`             // "DD/MM/YYYY", "MM/DD/YYYY", etc
}

// ThemeSettings holds customizable theme preferences
type ThemeSettings struct {
	Mode         string `json:"mode" bson:"mode"`                   // "dark", "light", "system"
	PrimaryColor string `json:"primary_color" bson:"primary_color"` // Hex color, default: #D4AF37 (gold)
	AccentColor  string `json:"accent_color" bson:"accent_color"`   // Hex color for accents
}

// ConnectedAccount represents a linked bank account
type ConnectedAccount struct {
	ID            primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID        primitive.ObjectID `json:"user_id" bson:"user_id"`
	Provider      string             `json:"provider" bson:"provider"`             // "nubank", "itau", "bradesco", etc
	AccountType   string             `json:"account_type" bson:"account_type"`     // "checking", "savings", "credit"
	AccountName   string             `json:"account_name" bson:"account_name"`     // User-defined name
	LastFour      string             `json:"last_four" bson:"last_four"`           // Last 4 digits
	Balance       float64            `json:"balance" bson:"balance"`               // Current balance
	Color         string             `json:"color" bson:"color"`                   // Hex color for UI
	Icon          string             `json:"icon" bson:"icon"`                     // Icon identifier
	IsActive      bool               `json:"is_active" bson:"is_active"`
	LastSync      time.Time          `json:"last_sync" bson:"last_sync"`
	CreatedAt     time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at" bson:"updated_at"`
}

// ProfileStats holds computed statistics for the user
type ProfileStats struct {
	TotalBalance       float64                `json:"total_balance"`
	MonthlyIncome      float64                `json:"monthly_income"`
	MonthlyExpenses    float64                `json:"monthly_expenses"`
	MonthlySavings     float64                `json:"monthly_savings"`
	TransactionCount   int64                  `json:"transaction_count"`
	TopCategories      []CategoryStat         `json:"top_categories"`
	MonthlyTrend       []MonthlyTrendPoint    `json:"monthly_trend"`
	ExpensesByCategory []CategoryStat         `json:"expenses_by_category"`
	ComparisonLastMonth ComparisonStats       `json:"comparison_last_month"`
	ConnectedAccounts  int                    `json:"connected_accounts"`
}

// CategoryStat represents spending per category
type CategoryStat struct {
	Category   string  `json:"category"`
	Amount     float64 `json:"amount"`
	Percentage float64 `json:"percentage"`
	Color      string  `json:"color"`
}

// MonthlyTrendPoint represents a point in the monthly trend chart
type MonthlyTrendPoint struct {
	Month    string  `json:"month"`     // "2024-01", "2024-02", etc
	Income   float64 `json:"income"`
	Expenses float64 `json:"expenses"`
	Balance  float64 `json:"balance"`
}

// ComparisonStats compares current month with previous
type ComparisonStats struct {
	IncomeChange   float64 `json:"income_change"`   // Percentage change
	ExpenseChange  float64 `json:"expense_change"`  // Percentage change
	SavingsChange  float64 `json:"savings_change"`  // Percentage change
}

// RegisterRequest is the request body for user registration
type RegisterRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

// LoginRequest is the request body for user login
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// AuthResponse is the response for register/login
type AuthResponse struct {
	User    UserResponse `json:"user"`
	Profile Profile      `json:"profile"`
	Token   string       `json:"token"`
}

// UserResponse is the public user data (without password)
type UserResponse struct {
	ID        primitive.ObjectID `json:"id"`
	Email     string             `json:"email"`
	CreatedAt time.Time          `json:"created_at"`
}

// UpdateProfileRequest is the request body for updating profile
type UpdateProfileRequest struct {
	Name     string          `json:"name,omitempty"`
	Avatar   string          `json:"avatar,omitempty"`
	Bio      string          `json:"bio,omitempty"`
	Settings ProfileSettings `json:"settings,omitempty"`
}

// UpdateThemeRequest is the request for updating theme settings
type UpdateThemeRequest struct {
	Mode         string `json:"mode,omitempty"`
	PrimaryColor string `json:"primary_color,omitempty"`
	AccentColor  string `json:"accent_color,omitempty"`
}

// ConnectAccountRequest is the request for adding a connected account
type ConnectAccountRequest struct {
	Provider    string  `json:"provider"`
	AccountType string  `json:"account_type"`
	AccountName string  `json:"account_name"`
	LastFour    string  `json:"last_four"`
	Balance     float64 `json:"balance"`
	Color       string  `json:"color,omitempty"`
	Icon        string  `json:"icon,omitempty"`
}

// UpdateConnectedAccountRequest for updating account details
type UpdateConnectedAccountRequest struct {
	AccountName string  `json:"account_name,omitempty"`
	Balance     float64 `json:"balance,omitempty"`
	Color       string  `json:"color,omitempty"`
	Icon        string  `json:"icon,omitempty"`
	IsActive    *bool   `json:"is_active,omitempty"`
}

// CategoryColors maps categories to their default colors
var CategoryColors = map[string]string{
	"food":      "#FF6B6B",
	"transport": "#4ECDC4",
	"housing":   "#45B7D1",
	"leisure":   "#96CEB4",
	"health":    "#FFEAA7",
	"education": "#DDA0DD",
	"salary":    "#98D8C8",
	"freelance": "#F7DC6F",
	"other":     "#B0B0B0",
}

// BankProviders available for connection
var BankProviders = map[string]BankProviderInfo{}

// BankProviderInfo holds bank provider metadata
type BankProviderInfo struct {
	Name  string `json:"name"`
	Icon  string `json:"icon"`
	Color string `json:"color"`
}

func init() {
	BankProviders = map[string]BankProviderInfo{
		"nubank":    {Name: "Nubank", Icon: "nubank", Color: "#8A05BE"},
		"itau":      {Name: "Itaú", Icon: "itau", Color: "#EC7000"},
		"bradesco":  {Name: "Bradesco", Icon: "bradesco", Color: "#CC092F"},
		"santander": {Name: "Santander", Icon: "santander", Color: "#EC0000"},
		"bb":        {Name: "Banco do Brasil", Icon: "bb", Color: "#FFEF00"},
		"caixa":     {Name: "Caixa", Icon: "caixa", Color: "#005CA9"},
		"inter":     {Name: "Inter", Icon: "inter", Color: "#FF7A00"},
		"c6":        {Name: "C6 Bank", Icon: "c6", Color: "#242424"},
		"picpay":    {Name: "PicPay", Icon: "picpay", Color: "#21C25E"},
		"mercadopago": {Name: "Mercado Pago", Icon: "mercadopago", Color: "#00B1EA"},
		"outros":    {Name: "Outros", Icon: "bank", Color: "#808080"},
	}
}

// HashPassword generates bcrypt hash from password
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return string(bytes), err
}

// CheckPassword compares password with hash
func CheckPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// ToResponse converts User to UserResponse (without password)
func (u *User) ToResponse() UserResponse {
	return UserResponse{
		ID:        u.ID,
		Email:     u.Email,
		CreatedAt: u.CreatedAt,
	}
}
