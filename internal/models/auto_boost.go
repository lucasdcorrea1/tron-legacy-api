package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// AutoBoostRule defines the criteria and settings for automatically
// boosting Instagram posts that exceed performance thresholds.
type AutoBoostRule struct {
	ID        primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID    primitive.ObjectID `json:"user_id" bson:"user_id"`
	Name      string             `json:"name" bson:"name"`
	Active    bool               `json:"active" bson:"active"`

	// Metrica monitorada: "likes", "comments", "engagement_rate"
	Metric    string  `json:"metric" bson:"metric"`
	Threshold float64 `json:"threshold" bson:"threshold"`

	// Configuracoes de orcamento
	DailyBudget  int64 `json:"daily_budget" bson:"daily_budget"`   // em centavos (ex: 2000 = R$20,00)
	DurationDays int   `json:"duration_days" bson:"duration_days"`

	// Targeting do ad set
	Targeting AdSetTargeting `json:"targeting" bson:"targeting"`

	// Objetivo da campanha Meta Ads
	Objective        string `json:"objective" bson:"objective"`
	OptimizationGoal string `json:"optimization_goal" bson:"optimization_goal"`
	BillingEvent     string `json:"billing_event" bson:"billing_event"`

	// Template do criativo
	CallToAction string `json:"call_to_action,omitempty" bson:"call_to_action,omitempty"`
	LinkURL      string `json:"link_url,omitempty" bson:"link_url,omitempty"`

	// Cooldown — evita boost duplicado
	CooldownHours int `json:"cooldown_hours" bson:"cooldown_hours"`

	// Filtro de idade do post
	MaxPostAgeHours int `json:"max_post_age_hours" bson:"max_post_age_hours"`

	CreatedAt time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
}

type CreateAutoBoostRuleRequest struct {
	Name             string         `json:"name"`
	Metric           string         `json:"metric"`
	Threshold        float64        `json:"threshold"`
	DailyBudget      int64          `json:"daily_budget"`
	DurationDays     int            `json:"duration_days"`
	Targeting        AdSetTargeting `json:"targeting"`
	Objective        string         `json:"objective"`
	OptimizationGoal string         `json:"optimization_goal"`
	BillingEvent     string         `json:"billing_event"`
	CallToAction     string         `json:"call_to_action,omitempty"`
	LinkURL          string         `json:"link_url,omitempty"`
	CooldownHours    int            `json:"cooldown_hours,omitempty"`
	MaxPostAgeHours  int            `json:"max_post_age_hours,omitempty"`
}

type UpdateAutoBoostRuleRequest struct {
	Name             *string         `json:"name,omitempty"`
	Active           *bool           `json:"active,omitempty"`
	Metric           *string         `json:"metric,omitempty"`
	Threshold        *float64        `json:"threshold,omitempty"`
	DailyBudget      *int64          `json:"daily_budget,omitempty"`
	DurationDays     *int            `json:"duration_days,omitempty"`
	Targeting        *AdSetTargeting `json:"targeting,omitempty"`
	Objective        *string         `json:"objective,omitempty"`
	OptimizationGoal *string         `json:"optimization_goal,omitempty"`
	BillingEvent     *string         `json:"billing_event,omitempty"`
	CallToAction     *string         `json:"call_to_action,omitempty"`
	LinkURL          *string         `json:"link_url,omitempty"`
	CooldownHours    *int            `json:"cooldown_hours,omitempty"`
	MaxPostAgeHours  *int            `json:"max_post_age_hours,omitempty"`
}

// AutoBoostLog registra cada execucao de boost automatico.
type AutoBoostLog struct {
	ID             primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	RuleID         primitive.ObjectID `json:"rule_id" bson:"rule_id"`
	RuleName       string             `json:"rule_name" bson:"rule_name"`
	UserID         primitive.ObjectID `json:"user_id" bson:"user_id"`

	IGMediaID   string `json:"ig_media_id" bson:"ig_media_id"`
	IGPermalink string `json:"ig_permalink" bson:"ig_permalink"`
	IGMediaType string `json:"ig_media_type" bson:"ig_media_type"`
	IGCaption   string `json:"ig_caption,omitempty" bson:"ig_caption,omitempty"`

	Metric      string  `json:"metric" bson:"metric"`
	MetricValue float64 `json:"metric_value" bson:"metric_value"`
	Threshold   float64 `json:"threshold" bson:"threshold"`

	MetaCampaignID string `json:"meta_campaign_id,omitempty" bson:"meta_campaign_id,omitempty"`
	MetaAdSetID    string `json:"meta_adset_id,omitempty" bson:"meta_adset_id,omitempty"`
	MetaCreativeID string `json:"meta_creative_id,omitempty" bson:"meta_creative_id,omitempty"`
	MetaAdID       string `json:"meta_ad_id,omitempty" bson:"meta_ad_id,omitempty"`

	DailyBudget  int64 `json:"daily_budget" bson:"daily_budget"`
	DurationDays int   `json:"duration_days" bson:"duration_days"`

	Status       string `json:"status" bson:"status"` // "success", "failed", "skipped_cooldown"
	ErrorMessage string `json:"error_message,omitempty" bson:"error_message,omitempty"`

	CreatedAt time.Time `json:"created_at" bson:"created_at"`
}
