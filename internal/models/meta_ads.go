package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ── Meta Ads Config ─────────────────────────────────────────────────

// MetaAdsConfig stores per-user Meta Ads credentials in the database
type MetaAdsConfig struct {
	ID              primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID          primitive.ObjectID `json:"user_id" bson:"user_id"`
	AdAccountID     string             `json:"ad_account_id" bson:"ad_account_id"`
	BusinessID      string             `json:"business_id,omitempty" bson:"business_id,omitempty"`
	AccessTokenEnc  string             `json:"-" bson:"access_token_enc"` // never sent to client
	CreatedAt       time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at" bson:"updated_at"`
}

type SaveMetaAdsConfigRequest struct {
	AdAccountID string `json:"ad_account_id"`
	AccessToken string `json:"access_token"`
	BusinessID  string `json:"business_id,omitempty"`
}

type MetaAdsConfigResponse struct {
	Configured  bool   `json:"configured"`
	AdAccountID string `json:"ad_account_id"`
	Source      string `json:"source"` // "user" or "env"
}

// ── Campaigns ───────────────────────────────────────────────────────

type MetaAdsCampaign struct {
	ID             primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID         primitive.ObjectID `json:"user_id" bson:"user_id"`
	MetaCampaignID string             `json:"meta_campaign_id" bson:"meta_campaign_id"`
	Name           string             `json:"name" bson:"name"`
	Objective      string             `json:"objective" bson:"objective"`
	Status         string             `json:"status" bson:"status"`
	BuyingType     string             `json:"buying_type" bson:"buying_type"`
	DailyBudget    int64              `json:"daily_budget,omitempty" bson:"daily_budget,omitempty"`       // in cents
	LifetimeBudget int64              `json:"lifetime_budget,omitempty" bson:"lifetime_budget,omitempty"` // in cents
	BidStrategy    string             `json:"bid_strategy,omitempty" bson:"bid_strategy,omitempty"`
	SpecialAdCategories []string      `json:"special_ad_categories" bson:"special_ad_categories"`
	CreatedAt      time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at" bson:"updated_at"`
}

type CreateCampaignRequest struct {
	Name                string   `json:"name"`
	Objective           string   `json:"objective"`
	Status              string   `json:"status,omitempty"`
	BuyingType          string   `json:"buying_type,omitempty"`
	DailyBudget         int64    `json:"daily_budget,omitempty"`
	LifetimeBudget      int64    `json:"lifetime_budget,omitempty"`
	BidStrategy         string   `json:"bid_strategy,omitempty"`
	SpecialAdCategories []string `json:"special_ad_categories,omitempty"`
}

type UpdateCampaignRequest struct {
	Name           *string `json:"name,omitempty"`
	Status         *string `json:"status,omitempty"`
	DailyBudget    *int64  `json:"daily_budget,omitempty"`
	LifetimeBudget *int64  `json:"lifetime_budget,omitempty"`
	BidStrategy    *string `json:"bid_strategy,omitempty"`
}

type UpdateStatusRequest struct {
	Status string `json:"status"`
}

// ── Ad Sets ─────────────────────────────────────────────────────────

type MetaAdsAdSet struct {
	ID             primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID         primitive.ObjectID `json:"user_id" bson:"user_id"`
	MetaAdSetID    string             `json:"meta_adset_id" bson:"meta_adset_id"`
	CampaignID     string             `json:"campaign_id" bson:"campaign_id"` // Meta campaign ID
	Name           string             `json:"name" bson:"name"`
	Status         string             `json:"status" bson:"status"`
	DailyBudget    int64              `json:"daily_budget,omitempty" bson:"daily_budget,omitempty"`
	LifetimeBudget int64              `json:"lifetime_budget,omitempty" bson:"lifetime_budget,omitempty"`
	BidAmount      int64              `json:"bid_amount,omitempty" bson:"bid_amount,omitempty"`
	BillingEvent   string             `json:"billing_event" bson:"billing_event"`
	OptimizationGoal string           `json:"optimization_goal" bson:"optimization_goal"`
	StartTime      string             `json:"start_time,omitempty" bson:"start_time,omitempty"`
	EndTime        string             `json:"end_time,omitempty" bson:"end_time,omitempty"`
	Targeting      AdSetTargeting     `json:"targeting" bson:"targeting"`
	CreatedAt      time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at" bson:"updated_at"`
}

type AdSetTargeting struct {
	GeoLocations    *GeoLocation   `json:"geo_locations,omitempty" bson:"geo_locations,omitempty"`
	AgeMin          int            `json:"age_min,omitempty" bson:"age_min,omitempty"`
	AgeMax          int            `json:"age_max,omitempty" bson:"age_max,omitempty"`
	Genders         []int          `json:"genders,omitempty" bson:"genders,omitempty"` // 0=all, 1=male, 2=female
	Interests       []TargetEntity `json:"interests,omitempty" bson:"interests,omitempty"`
	CustomAudiences []TargetEntity `json:"custom_audiences,omitempty" bson:"custom_audiences,omitempty"`
	Locales         []int          `json:"locales,omitempty" bson:"locales,omitempty"`
	PublisherPlatforms []string    `json:"publisher_platforms,omitempty" bson:"publisher_platforms,omitempty"`
	DevicePlatforms    []string    `json:"device_platforms,omitempty" bson:"device_platforms,omitempty"`
}

type GeoLocation struct {
	Countries []string        `json:"countries,omitempty" bson:"countries,omitempty"`
	Cities    []LocationEntry `json:"cities,omitempty" bson:"cities,omitempty"`
	Regions   []LocationEntry `json:"regions,omitempty" bson:"regions,omitempty"`
}

type LocationEntry struct {
	Key  string `json:"key" bson:"key"`
	Name string `json:"name" bson:"name"`
}

type TargetEntity struct {
	ID   string `json:"id" bson:"id"`
	Name string `json:"name" bson:"name"`
}

type CreateAdSetRequest struct {
	CampaignID       string         `json:"campaign_id"`
	Name             string         `json:"name"`
	Status           string         `json:"status,omitempty"`
	DailyBudget      int64          `json:"daily_budget,omitempty"`
	LifetimeBudget   int64          `json:"lifetime_budget,omitempty"`
	BidAmount        int64          `json:"bid_amount,omitempty"`
	BillingEvent     string         `json:"billing_event"`
	OptimizationGoal string         `json:"optimization_goal"`
	StartTime        string         `json:"start_time,omitempty"`
	EndTime          string         `json:"end_time,omitempty"`
	Targeting        AdSetTargeting `json:"targeting"`
}

type UpdateAdSetRequest struct {
	Name             *string         `json:"name,omitempty"`
	Status           *string         `json:"status,omitempty"`
	DailyBudget      *int64          `json:"daily_budget,omitempty"`
	LifetimeBudget   *int64          `json:"lifetime_budget,omitempty"`
	BidAmount        *int64          `json:"bid_amount,omitempty"`
	Targeting        *AdSetTargeting `json:"targeting,omitempty"`
	StartTime        *string         `json:"start_time,omitempty"`
	EndTime          *string         `json:"end_time,omitempty"`
}

// ── Ads ─────────────────────────────────────────────────────────────

type MetaAdsAd struct {
	ID          primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID      primitive.ObjectID `json:"user_id" bson:"user_id"`
	MetaAdID    string             `json:"meta_ad_id" bson:"meta_ad_id"`
	AdSetID     string             `json:"adset_id" bson:"adset_id"` // Meta adset ID
	Name        string             `json:"name" bson:"name"`
	Status      string             `json:"status" bson:"status"`
	Creative    AdCreative         `json:"creative" bson:"creative"`
	CreatedAt   time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at" bson:"updated_at"`
}

type AdCreative struct {
	Name        string          `json:"name,omitempty" bson:"name,omitempty"`
	Title       string          `json:"title,omitempty" bson:"title,omitempty"`
	Body        string          `json:"body,omitempty" bson:"body,omitempty"`
	ImageHash   string          `json:"image_hash,omitempty" bson:"image_hash,omitempty"`
	ImageURL    string          `json:"image_url,omitempty" bson:"image_url,omitempty"`
	VideoID     string          `json:"video_id,omitempty" bson:"video_id,omitempty"`
	LinkURL     string          `json:"link_url,omitempty" bson:"link_url,omitempty"`
	CallToAction string         `json:"call_to_action,omitempty" bson:"call_to_action,omitempty"`
	Description string          `json:"description,omitempty" bson:"description,omitempty"`
	Format      string          `json:"format" bson:"format"` // "image", "video", "carousel"
	CarouselCards []CarouselCard `json:"carousel_cards,omitempty" bson:"carousel_cards,omitempty"`
}

type CarouselCard struct {
	ImageHash   string `json:"image_hash,omitempty" bson:"image_hash,omitempty"`
	ImageURL    string `json:"image_url,omitempty" bson:"image_url,omitempty"`
	Title       string `json:"title,omitempty" bson:"title,omitempty"`
	Description string `json:"description,omitempty" bson:"description,omitempty"`
	LinkURL     string `json:"link_url,omitempty" bson:"link_url,omitempty"`
}

type CreateAdRequest struct {
	AdSetID  string     `json:"adset_id"`
	Name     string     `json:"name"`
	Status   string     `json:"status,omitempty"`
	Creative AdCreative `json:"creative"`
}

type UpdateAdRequest struct {
	Name     *string     `json:"name,omitempty"`
	Status   *string     `json:"status,omitempty"`
	Creative *AdCreative `json:"creative,omitempty"`
}

// ── Insights ────────────────────────────────────────────────────────

type MetaAdsInsight struct {
	ID           primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID       primitive.ObjectID `json:"user_id" bson:"user_id"`
	ObjectID     string             `json:"object_id" bson:"object_id"` // campaign/adset/ad ID
	Level        string             `json:"level" bson:"level"`         // "account", "campaign", "adset", "ad"
	DateStart    string             `json:"date_start" bson:"date_start"`
	DateStop     string             `json:"date_stop" bson:"date_stop"`
	Impressions  int64              `json:"impressions" bson:"impressions"`
	Reach        int64              `json:"reach" bson:"reach"`
	Clicks       int64              `json:"clicks" bson:"clicks"`
	Spend        float64            `json:"spend" bson:"spend"`
	CTR          float64            `json:"ctr" bson:"ctr"`
	CPC          float64            `json:"cpc" bson:"cpc"`
	CPM          float64            `json:"cpm" bson:"cpm"`
	Conversions  int64              `json:"conversions" bson:"conversions"`
	CampaignName string             `json:"campaign_name,omitempty" bson:"campaign_name,omitempty"`
	AdSetName    string             `json:"adset_name,omitempty" bson:"adset_name,omitempty"`
	AdName       string             `json:"ad_name,omitempty" bson:"ad_name,omitempty"`
	FetchedAt    time.Time          `json:"fetched_at" bson:"fetched_at"`
}

// ── Targeting Presets ───────────────────────────────────────────────

type TargetingPreset struct {
	ID        primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID    primitive.ObjectID `json:"user_id" bson:"user_id"`
	Name      string             `json:"name" bson:"name"`
	Targeting AdSetTargeting     `json:"targeting" bson:"targeting"`
	CreatedAt time.Time          `json:"created_at" bson:"created_at"`
}

type CreateTargetingPresetRequest struct {
	Name      string         `json:"name"`
	Targeting AdSetTargeting `json:"targeting"`
}

// ── Campaign Templates ──────────────────────────────────────────────

type CampaignTemplate struct {
	ID               primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID           primitive.ObjectID `json:"user_id" bson:"user_id"`
	Name             string             `json:"name" bson:"name"`
	Objective        string             `json:"objective" bson:"objective"`
	BuyingType       string             `json:"buying_type,omitempty" bson:"buying_type,omitempty"`
	BidStrategy      string             `json:"bid_strategy,omitempty" bson:"bid_strategy,omitempty"`
	DailyBudget      int64              `json:"daily_budget,omitempty" bson:"daily_budget,omitempty"`
	LifetimeBudget   int64              `json:"lifetime_budget,omitempty" bson:"lifetime_budget,omitempty"`
	Targeting        *AdSetTargeting    `json:"targeting,omitempty" bson:"targeting,omitempty"`
	BillingEvent     string             `json:"billing_event,omitempty" bson:"billing_event,omitempty"`
	OptimizationGoal string             `json:"optimization_goal,omitempty" bson:"optimization_goal,omitempty"`
	CreatedAt        time.Time          `json:"created_at" bson:"created_at"`
}

type CreateCampaignTemplateRequest struct {
	Name             string          `json:"name"`
	Objective        string          `json:"objective"`
	BuyingType       string          `json:"buying_type,omitempty"`
	BidStrategy      string          `json:"bid_strategy,omitempty"`
	DailyBudget      int64           `json:"daily_budget,omitempty"`
	LifetimeBudget   int64           `json:"lifetime_budget,omitempty"`
	Targeting        *AdSetTargeting `json:"targeting,omitempty"`
	BillingEvent     string          `json:"billing_event,omitempty"`
	OptimizationGoal string          `json:"optimization_goal,omitempty"`
}

// ── Budget Alerts ───────────────────────────────────────────────────

type BudgetAlert struct {
	ID          primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID      primitive.ObjectID `json:"user_id" bson:"user_id"`
	CampaignID  string             `json:"campaign_id,omitempty" bson:"campaign_id,omitempty"` // empty = account-level
	AlertType   string             `json:"alert_type" bson:"alert_type"`                       // "daily_spend", "total_spend"
	Threshold   float64            `json:"threshold" bson:"threshold"`                         // in currency
	Active      bool               `json:"active" bson:"active"`
	LastTriggered *time.Time       `json:"last_triggered,omitempty" bson:"last_triggered,omitempty"`
	CreatedAt   time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at" bson:"updated_at"`
}

type CreateBudgetAlertRequest struct {
	CampaignID string  `json:"campaign_id,omitempty"`
	AlertType  string  `json:"alert_type"`
	Threshold  float64 `json:"threshold"`
}

type UpdateBudgetAlertRequest struct {
	AlertType *string  `json:"alert_type,omitempty"`
	Threshold *float64 `json:"threshold,omitempty"`
	Active    *bool    `json:"active,omitempty"`
}
