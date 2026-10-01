package models

import (
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// AccessLog is one request recorded by the access-log middleware. It is written
// in batches, in the background, and auto-expires (TTL index). It never stores
// the query string or request body (tokens/PII travel there).
type AccessLog struct {
	ID         primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	Method     string             `json:"method" bson:"method"`
	Path       string             `json:"path" bson:"path"`
	Status     int                `json:"status" bson:"status"`
	DurationMs int64              `json:"duration_ms" bson:"duration_ms"`
	IP         string             `json:"ip" bson:"ip"`
	UserAgent  string             `json:"user_agent" bson:"user_agent"`
	UserID     string             `json:"user_id,omitempty" bson:"user_id,omitempty"`
	OrgID      string             `json:"org_id,omitempty" bson:"org_id,omitempty"`
	CreatedAt  time.Time          `json:"created_at" bson:"created_at"`
}

// Security event type codes. Identifiers stay in English; the frontend maps
// them to Portuguese copy.
const (
	SecEventRateLimited  = "rate_limited"
	SecEventBlocked      = "blocked"
	SecEventAutoBlocked  = "auto_blocked"
	SecEventLoginFailed  = "login_failed"
	SecEventLoginOK      = "login_ok"
	SecEventManualBlock  = "manual_block"
	SecEventManualUnblck = "manual_unblock"
)

// SecurityEvent is a noteworthy security occurrence (rate-limit hit, block,
// login outcome). Auto-expires via TTL index.
type SecurityEvent struct {
	ID        primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	Type      string             `json:"type" bson:"type"`
	IP        string             `json:"ip" bson:"ip"`
	Email     string             `json:"email,omitempty" bson:"email,omitempty"`
	Detail    string             `json:"detail,omitempty" bson:"detail,omitempty"`
	CreatedAt time.Time          `json:"created_at" bson:"created_at"`
}

// Block is an active ban. Kind is always "ip" for now. A zero ExpiresAt means
// the block never expires on its own; otherwise a TTL index removes it.
type Block struct {
	ID        primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	Kind      string             `json:"kind" bson:"kind"`
	Value     string             `json:"value" bson:"value"`
	Reason    string             `json:"reason,omitempty" bson:"reason,omitempty"`
	Auto      bool               `json:"auto" bson:"auto"`
	CreatedAt time.Time          `json:"created_at" bson:"created_at"`
	ExpiresAt time.Time          `json:"expires_at,omitempty" bson:"expires_at,omitempty"`
}

// SecurityPolicy is the admin-tunable protection config. Stored as a single doc
// in the security_settings collection. Zero on a limit disables that rule.
type SecurityPolicy struct {
	// Per-IP rate limits.
	GlobalPerMin int `json:"global_per_min" bson:"global_per_min"` // whole API, per minute
	AuthPer15Min int `json:"auth_per_15min" bson:"auth_per_15min"` // /auth/* per 15 minutes

	// Automatic blocking of repeat offenders.
	AutoBlockEnabled bool `json:"auto_block_enabled" bson:"auto_block_enabled"`
	AutoBlockHits    int  `json:"auto_block_hits" bson:"auto_block_hits"`       // rate-limit violations before a block
	AutoBlockMinutes int  `json:"auto_block_minutes" bson:"auto_block_minutes"` // how long an auto-block lasts

	// IPs that are never limited nor blocked (protects the admin).
	Allowlist []string `json:"allowlist" bson:"allowlist"`

	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
}

// DefaultSecurityPolicy returns safe defaults used when no policy doc exists yet.
// Tuned conservatively for a small free-tier compute budget: block abusive IPs
// early and keep them out for a while, so a flood can't burn the quota. The
// admin can loosen these in the Segurança panel.
func DefaultSecurityPolicy() SecurityPolicy {
	return SecurityPolicy{
		GlobalPerMin:     60, // ~1 req/s per IP (per-minute window tolerates page-load bursts)
		AuthPer15Min:     6,  // login/register/reset combined, per IP
		AutoBlockEnabled: true,
		AutoBlockHits:    3,   // block after 3 rate-limit violations
		AutoBlockMinutes: 120, // keep the offender out for 2h
		Allowlist:        []string{},
	}
}

// Normalize clamps the policy to sane ranges and trims the allowlist.
func (p *SecurityPolicy) Normalize() {
	if p.GlobalPerMin < 0 {
		p.GlobalPerMin = 0
	}
	if p.GlobalPerMin > 100000 {
		p.GlobalPerMin = 100000
	}
	if p.AuthPer15Min < 0 {
		p.AuthPer15Min = 0
	}
	if p.AuthPer15Min > 100000 {
		p.AuthPer15Min = 100000
	}
	if p.AutoBlockHits < 1 {
		p.AutoBlockHits = 1
	}
	if p.AutoBlockMinutes < 1 {
		p.AutoBlockMinutes = 1
	}
	if p.AutoBlockMinutes > 7*24*60 {
		p.AutoBlockMinutes = 7 * 24 * 60
	}
	clean := make([]string, 0, len(p.Allowlist))
	seen := map[string]bool{}
	for _, ip := range p.Allowlist {
		ip = strings.TrimSpace(ip)
		if ip == "" || seen[ip] {
			continue
		}
		seen[ip] = true
		clean = append(clean, ip)
	}
	p.Allowlist = clean
}

// Allows reports whether the IP is on the allowlist (never limited/blocked).
func (p SecurityPolicy) Allows(ip string) bool {
	for _, a := range p.Allowlist {
		if a == ip {
			return true
		}
	}
	return false
}

// RiskSignal is one reason a client scored risk, with the number behind it.
type RiskSignal struct {
	Code  string `json:"code"`
	Value int64  `json:"value"`
}

// RiskClient is an IP ranked by how risky its recent behaviour looks (0-100).
// Advisory only — nothing is blocked automatically from this score.
type RiskClient struct {
	IP       string       `json:"ip"`
	Score    int          `json:"score"`
	Requests int64        `json:"requests"`
	Blocked  bool         `json:"blocked"`
	Signals  []RiskSignal `json:"signals"`
}
