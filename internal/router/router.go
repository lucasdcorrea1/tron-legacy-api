package router

import (
	"net/http"

	"github.com/tron-legacy/api/internal/config"
	"github.com/tron-legacy/api/internal/handlers"
	"github.com/tron-legacy/api/internal/middleware"
	httpSwagger "github.com/swaggo/http-swagger"
)

func New() http.Handler {
	mux := http.NewServeMux()

	// ==========================================
	// PUBLIC ROUTES (no auth required)
	// ==========================================

	// Swagger UI
	mux.HandleFunc("/swagger/", httpSwagger.WrapHandler)

	// Health check
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	})

	// SEO routes
	mux.HandleFunc("GET /api/v1/sitemap.xml", handlers.Sitemap)
	mux.HandleFunc("GET /robots.txt", handlers.RobotsTxt)

	// Prometheus metrics endpoint
	mux.Handle("GET /metrics", middleware.PrometheusHandler())

	// Auth routes (public, rate-limited)
	mux.HandleFunc("POST /api/v1/auth/register", middleware.RateLimit(handlers.Register))
	mux.HandleFunc("POST /api/v1/auth/login", middleware.RateLimit(handlers.Login))
	mux.HandleFunc("POST /api/v1/auth/forgot-password", middleware.RateLimit(handlers.ForgotPassword))
	mux.HandleFunc("POST /api/v1/auth/reset-password", middleware.RateLimit(handlers.ResetPassword))
	mux.HandleFunc("POST /api/v1/auth/refresh", handlers.Refresh)
	mux.HandleFunc("POST /api/v1/auth/logout", handlers.Logout)

	// Register + Subscribe (public, rate-limited)
	mux.HandleFunc("POST /api/v1/auth/register-and-subscribe", middleware.RateLimit(handlers.RegisterAndSubscribe))

	// Meta OAuth (auth required, no org context needed)
	mux.Handle("GET /api/v1/auth/meta/url", middleware.Auth(http.HandlerFunc(handlers.MetaOAuthURL)))
	mux.Handle("POST /api/v1/auth/meta/callback", middleware.Auth(http.HandlerFunc(handlers.MetaOAuthCallback)))

	// Blog routes (public)
	mux.HandleFunc("GET /api/v1/blog/posts", handlers.ListPosts)
	mux.Handle("GET /api/v1/blog/posts/{slug}", middleware.OptionalAuth(http.HandlerFunc(handlers.GetPostBySlug)))
	mux.HandleFunc("GET /api/v1/blog/images/group/{groupId}", handlers.ServeImageByGroup)
	mux.HandleFunc("GET /api/v1/blog/images/{id}", handlers.ServeImage)

	// Newsletter (public)
	mux.HandleFunc("POST /api/v1/newsletter/subscribe", handlers.SubscribeNewsletter)

	// Instagram webhooks (public — called by Meta)
	mux.HandleFunc("GET /api/v1/webhooks/instagram", handlers.WebhookVerify)
	mux.HandleFunc("POST /api/v1/webhooks/instagram", handlers.WebhookEvent)

	// Asaas webhooks (public — called by Asaas, validated by token)
	mux.HandleFunc("POST /api/v1/webhooks/asaas", handlers.AsaasWebhook)

	// Public invitation acceptance (via email link)
	mux.HandleFunc("POST /api/v1/invitations/accept-token/{token}", handlers.AcceptInvitationByToken)

	// Engagement routes (public)
	mux.HandleFunc("GET /api/v1/blog/posts/{slug}/comments", handlers.ListComments)

	// Engagement routes (optional auth — detect user if logged in)
	mux.Handle("POST /api/v1/blog/posts/{slug}/view", middleware.OptionalAuth(http.HandlerFunc(handlers.RecordView)))
	mux.HandleFunc("POST /api/v1/blog/posts/{slug}/cta-click", handlers.RecordCTAClick)
	mux.Handle("GET /api/v1/blog/posts/{slug}/stats", middleware.OptionalAuth(http.HandlerFunc(handlers.GetPostStats)))

	// ==========================================
	// PROTECTED ROUTES (auth required)
	// ==========================================

	// Auth - Me (protected)
	mux.Handle("GET /api/v1/auth/me", middleware.Auth(http.HandlerFunc(handlers.Me)))

	// Profile routes (protected)
	mux.Handle("GET /api/v1/profile", middleware.Auth(http.HandlerFunc(handlers.GetProfile)))
	mux.Handle("PUT /api/v1/profile", middleware.Auth(http.HandlerFunc(handlers.UpdateProfile)))
	mux.Handle("POST /api/v1/profile/avatar", middleware.Auth(http.HandlerFunc(handlers.UploadAvatar)))
	mux.Handle("POST /api/v1/profile/cover-image", middleware.Auth(http.HandlerFunc(handlers.UploadCoverImage)))

	// ==========================================
	// ORGANIZATION ROUTES (auth required)
	// ==========================================

	// Org CRUD (auth only — no org context needed for list/create)
	mux.Handle("POST /api/v1/orgs", middleware.Auth(http.HandlerFunc(handlers.CreateOrg)))
	mux.Handle("GET /api/v1/orgs", middleware.Auth(http.HandlerFunc(handlers.ListOrgs)))

	// Org switch (auth only)
	mux.Handle("POST /api/v1/orgs/switch/{id}", middleware.Auth(http.HandlerFunc(handlers.SwitchOrg)))

	// Invitations (auth only — no org context)
	mux.Handle("GET /api/v1/invitations/mine", middleware.Auth(http.HandlerFunc(handlers.MyInvitations)))
	mux.Handle("POST /api/v1/invitations/accept/{id}", middleware.Auth(http.HandlerFunc(handlers.AcceptInvitation)))

	// Org detail routes (require org context)
	mux.Handle("GET /api/v1/orgs/current", middleware.Auth(middleware.RequireOrg(http.HandlerFunc(handlers.GetOrg))))
	mux.Handle("PUT /api/v1/orgs/current", middleware.Auth(middleware.RequireOrg(middleware.RequireOrgRole("owner", "admin")(http.HandlerFunc(handlers.UpdateOrg)))))
	mux.Handle("DELETE /api/v1/orgs/current", middleware.Auth(middleware.RequireOrg(middleware.RequireOrgRole("owner")(http.HandlerFunc(handlers.DeleteOrg)))))

	// Org logo (owner only)
	mux.Handle("POST /api/v1/orgs/current/logo", middleware.Auth(middleware.RequireOrg(middleware.RequireOrgRole("owner")(http.HandlerFunc(handlers.UploadOrgLogo)))))
	mux.Handle("DELETE /api/v1/orgs/current/logo", middleware.Auth(middleware.RequireOrg(middleware.RequireOrgRole("owner")(http.HandlerFunc(handlers.RemoveOrgLogo)))))

	// Org members (require org context + admin/owner)
	mux.Handle("GET /api/v1/orgs/current/members", middleware.Auth(middleware.RequireOrg(http.HandlerFunc(handlers.ListMembers))))
	mux.Handle("POST /api/v1/orgs/current/invitations", middleware.Auth(middleware.RequireOrg(middleware.RequireOrgRole("owner", "admin")(http.HandlerFunc(handlers.InviteMember)))))
	mux.Handle("PUT /api/v1/orgs/current/members/{uid}/role", middleware.Auth(middleware.RequireOrg(middleware.RequireOrgRole("owner", "admin")(http.HandlerFunc(handlers.UpdateMemberRole)))))
	mux.Handle("DELETE /api/v1/orgs/current/members/{uid}", middleware.Auth(middleware.RequireOrg(http.HandlerFunc(handlers.RemoveMember))))

	// Org subscription/usage
	mux.Handle("GET /api/v1/orgs/current/subscription", middleware.Auth(middleware.RequireOrg(http.HandlerFunc(handlers.GetSubscription))))
	mux.Handle("GET /api/v1/orgs/current/usage", middleware.Auth(middleware.RequireOrg(http.HandlerFunc(handlers.GetUsage))))

	// Billing (checkout/cancel — owner/admin only)
	mux.Handle("POST /api/v1/orgs/current/subscription/checkout", middleware.Auth(middleware.RequireOrg(middleware.RequireOrgRole("owner", "admin")(http.HandlerFunc(handlers.Checkout)))))
	mux.Handle("POST /api/v1/orgs/current/subscription/cancel", middleware.Auth(middleware.RequireOrg(middleware.RequireOrgRole("owner", "admin")(http.HandlerFunc(handlers.CancelSubscription)))))

	// Permissions endpoint (owner/admin only)
	mux.Handle("PUT /api/v1/orgs/current/members/{uid}/permissions", middleware.Auth(middleware.RequireOrg(middleware.RequireOrgRole("owner", "admin")(http.HandlerFunc(handlers.UpdateMemberPermissions)))))

	// ==========================================
	// ORG-SCOPED ADMIN ROUTES
	// ==========================================
	// Helper: auth + org + org role check
	orgRoute := func(roles ...string) func(http.Handler) http.Handler {
		return func(h http.Handler) http.Handler {
			return middleware.Auth(middleware.RequireOrg(middleware.RequireOrgRole(roles...)(h)))
		}
	}

	// Helper: auth + org + granular permission check
	// Owner/admin always pass; members need the specific permission.
	orgPerm := func(perm string) func(http.Handler) http.Handler {
		return func(h http.Handler) http.Handler {
			return middleware.Auth(middleware.RequireOrg(middleware.RequirePermission(perm)(h)))
		}
	}

	// Helper: auth + org + role + plan check (blocks if subscription not active or plan too low)
	orgRoutePlan := func(minPlan string, roles ...string) func(http.Handler) http.Handler {
		return func(h http.Handler) http.Handler {
			return middleware.Auth(middleware.RequireOrg(middleware.RequirePlan(minPlan)(middleware.RequireOrgRole(roles...)(h))))
		}
	}

	// Helper: auth + org + plan + permission check
	orgPermPlan := func(minPlan, perm string) func(http.Handler) http.Handler {
		return func(h http.Handler) http.Handler {
			return middleware.Auth(middleware.RequireOrg(middleware.RequirePlan(minPlan)(middleware.RequirePermission(perm)(h))))
		}
	}

	// Users/members (org-scoped)
	mux.Handle("GET /api/v1/users", orgRoute("owner", "admin")(http.HandlerFunc(handlers.ListUsers)))
	mux.Handle("PUT /api/v1/users/{id}/role", orgRoute("owner", "admin")(http.HandlerFunc(handlers.UpdateUserRole)))

	// Email Marketing routes (superuser only — internal Whodo tool)
	suOnly := middleware.RequireRole("superadmin", "superuser")
	mux.Handle("GET /api/v1/admin/email-marketing/templates", middleware.Auth(suOnly(http.HandlerFunc(handlers.ListEmailTemplates))))
	mux.Handle("POST /api/v1/admin/email-marketing/templates/{id}/preview", middleware.Auth(suOnly(http.HandlerFunc(handlers.PreviewEmailTemplate))))
	mux.Handle("GET /api/v1/admin/email-marketing/audience", middleware.Auth(suOnly(http.HandlerFunc(handlers.GetEmailAudience))))
	mux.Handle("POST /api/v1/admin/email-marketing/send", middleware.Auth(suOnly(http.HandlerFunc(handlers.SendMarketingEmail))))
	mux.Handle("GET /api/v1/admin/email-marketing/subscribers", middleware.Auth(suOnly(http.HandlerFunc(handlers.ListSubscribers))))
	mux.Handle("DELETE /api/v1/admin/email-marketing/subscribers/{id}", middleware.Auth(suOnly(http.HandlerFunc(handlers.DeleteSubscriber))))
	mux.Handle("GET /api/v1/admin/email-marketing/broadcasts", middleware.Auth(suOnly(http.HandlerFunc(handlers.ListBroadcasts))))
	mux.Handle("GET /api/v1/admin/email-marketing/broadcasts/{id}", middleware.Auth(suOnly(http.HandlerFunc(handlers.GetBroadcast))))

	// Billing balance (superuser only — Whodo financial dashboard)
	mux.Handle("GET /api/v1/admin/billing/balance", middleware.Auth(suOnly(http.HandlerFunc(handlers.GetBillingBalance))))

	// CTA analytics (superuser only — internal Whodo tool)
	mux.Handle("GET /api/v1/admin/cta-analytics", middleware.Auth(suOnly(http.HandlerFunc(handlers.GetCTAAnalytics))))

	// Instagram cross-org profiles (auth only — no org context needed)
	mux.Handle("GET /api/v1/admin/instagram/all-profiles", middleware.Auth(http.HandlerFunc(handlers.ListAllOrgInstagramProfiles)))

	// Instagram scheduling routes (org-scoped, requires starter+)
	mux.Handle("GET /api/v1/admin/instagram/config", orgRoutePlan("starter", "owner", "admin", "member")(http.HandlerFunc(handlers.GetInstagramConfig)))
	mux.Handle("PUT /api/v1/admin/instagram/config", orgRoutePlan("starter", "owner", "admin")(http.HandlerFunc(handlers.SaveInstagramConfig)))
	mux.Handle("DELETE /api/v1/admin/instagram/config", orgRoutePlan("starter", "owner", "admin")(http.HandlerFunc(handlers.DeleteInstagramConfig)))
	mux.Handle("GET /api/v1/admin/instagram/connected-accounts", orgRoutePlan("starter", "owner", "admin", "member")(http.HandlerFunc(handlers.ListConnectedIGAccounts)))
	mux.Handle("GET /api/v1/admin/instagram/test", orgRoutePlan("starter", "owner", "admin")(http.HandlerFunc(handlers.TestInstagramConnection)))
	mux.Handle("GET /api/v1/admin/instagram/accounts", orgRoutePlan("starter", "owner", "admin", "member")(http.HandlerFunc(handlers.ListInstagramAccounts)))
	mux.Handle("GET /api/v1/admin/instagram/feed", orgRoutePlan("starter", "owner", "admin", "member")(http.HandlerFunc(handlers.GetInstagramFeed)))
	mux.Handle("GET /api/v1/admin/instagram/schedules", orgRoutePlan("starter", "owner", "admin", "member")(http.HandlerFunc(handlers.ListInstagramSchedules)))
	mux.Handle("POST /api/v1/admin/instagram/schedules", orgPermPlan("starter", "instagram:schedule")(http.HandlerFunc(handlers.CreateInstagramSchedule)))
	mux.Handle("GET /api/v1/admin/instagram/schedules/{id}", orgRoutePlan("starter", "owner", "admin", "member")(http.HandlerFunc(handlers.GetInstagramSchedule)))
	mux.Handle("PUT /api/v1/admin/instagram/schedules/{id}", orgPermPlan("starter", "instagram:schedule")(http.HandlerFunc(handlers.UpdateInstagramSchedule)))
	mux.Handle("DELETE /api/v1/admin/instagram/schedules/{id}", orgRoutePlan("starter", "owner", "admin")(http.HandlerFunc(handlers.DeleteInstagramSchedule)))
	mux.Handle("POST /api/v1/admin/instagram/upload", orgPermPlan("starter", "instagram:schedule")(http.HandlerFunc(handlers.UploadInstagramImage)))

	// Instagram auto-reply routes (org-scoped, requires starter+)
	mux.Handle("GET /api/v1/admin/instagram/autoreply/rules", orgRoutePlan("starter", "owner", "admin", "member")(http.HandlerFunc(handlers.ListAutoReplyRules)))
	mux.Handle("POST /api/v1/admin/instagram/autoreply/rules", orgPermPlan("starter", "instagram:autoreply")(http.HandlerFunc(handlers.CreateAutoReplyRule)))
	mux.Handle("PUT /api/v1/admin/instagram/autoreply/rules/{id}", orgPermPlan("starter", "instagram:autoreply")(http.HandlerFunc(handlers.UpdateAutoReplyRule)))
	mux.Handle("PATCH /api/v1/admin/instagram/autoreply/rules/{id}", orgPermPlan("starter", "instagram:autoreply")(http.HandlerFunc(handlers.ToggleAutoReplyRule)))
	mux.Handle("DELETE /api/v1/admin/instagram/autoreply/rules/{id}", orgRoutePlan("starter", "owner", "admin")(http.HandlerFunc(handlers.DeleteAutoReplyRule)))
	mux.Handle("GET /api/v1/admin/instagram/autoreply/logs", orgRoutePlan("starter", "owner", "admin", "member")(http.HandlerFunc(handlers.ListAutoReplyLogs)))

	// Instagram auto-reply live feed (SSE — auth via query param, validated internally)
	mux.HandleFunc("GET /api/v1/admin/instagram/autoreply/live", handlers.AutoReplySSE)

	// Instagram leads routes (org-scoped, requires starter+)
	mux.Handle("GET /api/v1/admin/instagram/leads/export", orgRoutePlan("starter", "owner", "admin")(http.HandlerFunc(handlers.ExportLeadsCSV)))
	mux.Handle("GET /api/v1/admin/instagram/leads/stats", orgPermPlan("starter", "instagram:leads")(http.HandlerFunc(handlers.GetLeadStats)))
	mux.Handle("GET /api/v1/admin/instagram/leads", orgPermPlan("starter", "instagram:leads")(http.HandlerFunc(handlers.ListInstagramLeads)))
	mux.Handle("PUT /api/v1/admin/instagram/leads/{id}/tags", orgPermPlan("starter", "instagram:leads")(http.HandlerFunc(handlers.UpdateLeadTags)))

	// Instagram analytics routes (org-scoped)
	mux.Handle("GET /api/v1/admin/instagram/analytics/autoreply", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetAutoReplyAnalytics)))
	mux.Handle("GET /api/v1/admin/instagram/analytics/engagement", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetEngagementReport)))

	// Meta Ads accounts (org-scoped, requires starter+)
	mux.Handle("GET /api/v1/admin/meta-ads/accounts", orgRoutePlan("starter", "owner", "admin", "member")(http.HandlerFunc(handlers.ListMetaAdsAccounts)))

	// Meta Ads campaign routes (org-scoped, requires starter+)
	mux.Handle("GET /api/v1/admin/meta-ads/campaigns", orgRoutePlan("starter", "owner", "admin", "member")(http.HandlerFunc(handlers.ListMetaAdsCampaigns)))
	mux.Handle("POST /api/v1/admin/meta-ads/campaigns", orgPermPlan("starter", "meta_ads:manage")(http.HandlerFunc(handlers.CreateMetaAdsCampaign)))
	mux.Handle("GET /api/v1/admin/meta-ads/campaigns/{id}", orgRoutePlan("starter", "owner", "admin", "member")(http.HandlerFunc(handlers.GetMetaAdsCampaign)))
	mux.Handle("PUT /api/v1/admin/meta-ads/campaigns/{id}", orgPermPlan("starter", "meta_ads:manage")(http.HandlerFunc(handlers.UpdateMetaAdsCampaign)))
	mux.Handle("DELETE /api/v1/admin/meta-ads/campaigns/{id}", orgRoutePlan("starter", "owner", "admin")(http.HandlerFunc(handlers.DeleteMetaAdsCampaign)))
	mux.Handle("PATCH /api/v1/admin/meta-ads/campaigns/{id}/status", orgPermPlan("starter", "meta_ads:manage")(http.HandlerFunc(handlers.UpdateMetaAdsCampaignStatus)))

	// Meta Ads ad set routes (org-scoped)
	mux.Handle("GET /api/v1/admin/meta-ads/adsets", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListMetaAdsAdSets)))
	mux.Handle("POST /api/v1/admin/meta-ads/adsets", orgPerm("meta_ads:manage")(http.HandlerFunc(handlers.CreateMetaAdsAdSet)))
	mux.Handle("GET /api/v1/admin/meta-ads/adsets/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetMetaAdsAdSet)))
	mux.Handle("PUT /api/v1/admin/meta-ads/adsets/{id}", orgPerm("meta_ads:manage")(http.HandlerFunc(handlers.UpdateMetaAdsAdSet)))
	mux.Handle("DELETE /api/v1/admin/meta-ads/adsets/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteMetaAdsAdSet)))
	mux.Handle("PATCH /api/v1/admin/meta-ads/adsets/{id}/status", orgPerm("meta_ads:manage")(http.HandlerFunc(handlers.UpdateMetaAdsAdSetStatus)))

	// Meta Ads ad routes (org-scoped)
	mux.Handle("GET /api/v1/admin/meta-ads/ads", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListMetaAdsAds)))
	mux.Handle("POST /api/v1/admin/meta-ads/ads", orgPerm("meta_ads:manage")(http.HandlerFunc(handlers.CreateMetaAdsAd)))
	mux.Handle("GET /api/v1/admin/meta-ads/ads/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetMetaAdsAd)))
	mux.Handle("PUT /api/v1/admin/meta-ads/ads/{id}", orgPerm("meta_ads:manage")(http.HandlerFunc(handlers.UpdateMetaAdsAd)))
	mux.Handle("DELETE /api/v1/admin/meta-ads/ads/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteMetaAdsAd)))
	mux.Handle("PATCH /api/v1/admin/meta-ads/ads/{id}/status", orgPerm("meta_ads:manage")(http.HandlerFunc(handlers.UpdateMetaAdsAdStatus)))

	// Meta Ads insights (org-scoped)
	mux.Handle("GET /api/v1/admin/meta-ads/insights", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetMetaAdsInsights)))
	mux.Handle("GET /api/v1/admin/meta-ads/campaigns/{id}/insights", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetMetaAdsCampaignInsights)))

	// Meta Ads account finance (org-scoped)
	mux.Handle("GET /api/v1/admin/meta-ads/account/finance", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetMetaAdsAccountFinance)))
	mux.Handle("GET /api/v1/admin/meta-ads/account/recommendations", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetMetaAdsRecommendations)))

	// Meta Ads upload (org-scoped)
	mux.Handle("POST /api/v1/admin/meta-ads/upload/image", orgPerm("meta_ads:manage")(http.HandlerFunc(handlers.UploadMetaAdsImage)))
	mux.Handle("POST /api/v1/admin/meta-ads/upload/video", orgPerm("meta_ads:manage")(http.HandlerFunc(handlers.UploadMetaAdsVideo)))

	// Meta Ads targeting search (org-scoped)
	mux.Handle("GET /api/v1/admin/meta-ads/targeting/interests", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.SearchMetaAdsInterests)))
	mux.Handle("GET /api/v1/admin/meta-ads/targeting/locations", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.SearchMetaAdsLocations)))
	mux.Handle("GET /api/v1/admin/meta-ads/targeting/audiences", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListMetaAdsAudiences)))

	// Meta Ads targeting presets (org-scoped)
	mux.Handle("GET /api/v1/admin/meta-ads/presets", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListMetaAdsPresets)))
	mux.Handle("POST /api/v1/admin/meta-ads/presets", orgPerm("meta_ads:manage")(http.HandlerFunc(handlers.CreateMetaAdsPreset)))
	mux.Handle("DELETE /api/v1/admin/meta-ads/presets/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteMetaAdsPreset)))

	// Meta Ads campaign templates (org-scoped)
	mux.Handle("GET /api/v1/admin/meta-ads/templates", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListMetaAdsTemplates)))
	mux.Handle("POST /api/v1/admin/meta-ads/templates", orgPerm("meta_ads:manage")(http.HandlerFunc(handlers.CreateMetaAdsTemplate)))
	mux.Handle("DELETE /api/v1/admin/meta-ads/templates/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteMetaAdsTemplate)))

	// Meta Ads budget alerts (org-scoped)
	mux.Handle("GET /api/v1/admin/meta-ads/alerts", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListMetaAdsBudgetAlerts)))
	mux.Handle("POST /api/v1/admin/meta-ads/alerts", orgPerm("meta_ads:budget")(http.HandlerFunc(handlers.CreateMetaAdsBudgetAlert)))
	mux.Handle("PUT /api/v1/admin/meta-ads/alerts/{id}", orgPerm("meta_ads:budget")(http.HandlerFunc(handlers.UpdateMetaAdsBudgetAlert)))
	mux.Handle("DELETE /api/v1/admin/meta-ads/alerts/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteMetaAdsBudgetAlert)))

	// Integrated Publish routes (org-scoped)
	mux.Handle("GET /api/v1/admin/integrated-publish", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListIntegratedPublishes)))
	mux.Handle("POST /api/v1/admin/integrated-publish", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.CreateIntegratedPublish)))
	mux.Handle("GET /api/v1/admin/integrated-publish/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetIntegratedPublish)))
	mux.Handle("PUT /api/v1/admin/integrated-publish/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.UpdateIntegratedPublish)))
	mux.Handle("DELETE /api/v1/admin/integrated-publish/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteIntegratedPublish)))

	// Facebook Page scheduling routes (org-scoped)
	mux.Handle("GET /api/v1/admin/facebook/config", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetFacebookConfig)))
	mux.Handle("PUT /api/v1/admin/facebook/config", orgRoute("owner", "admin")(http.HandlerFunc(handlers.SaveFacebookConfig)))
	mux.Handle("DELETE /api/v1/admin/facebook/config", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteFacebookConfig)))
	mux.Handle("GET /api/v1/admin/facebook/test", orgRoute("owner", "admin")(http.HandlerFunc(handlers.TestFacebookConnection)))
	mux.Handle("GET /api/v1/admin/facebook/pages", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListFacebookPages)))
	mux.Handle("GET /api/v1/admin/facebook/feed", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetFacebookFeed)))
	mux.Handle("GET /api/v1/admin/facebook/schedules", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListFacebookSchedules)))
	mux.Handle("POST /api/v1/admin/facebook/schedules", orgPerm("facebook:schedule")(http.HandlerFunc(handlers.CreateFacebookSchedule)))
	mux.Handle("GET /api/v1/admin/facebook/schedules/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetFacebookSchedule)))
	mux.Handle("PUT /api/v1/admin/facebook/schedules/{id}", orgPerm("facebook:schedule")(http.HandlerFunc(handlers.UpdateFacebookSchedule)))
	mux.Handle("DELETE /api/v1/admin/facebook/schedules/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteFacebookSchedule)))
	mux.Handle("POST /api/v1/admin/facebook/upload", orgPerm("facebook:schedule")(http.HandlerFunc(handlers.UploadFacebookImage)))

	// AI (Claude) routes (org-scoped)
	mux.Handle("GET /api/v1/admin/ai/config", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetAIConfig)))
	mux.Handle("PUT /api/v1/admin/ai/config", orgRoute("owner", "admin")(http.HandlerFunc(handlers.SaveAIConfig)))
	mux.Handle("DELETE /api/v1/admin/ai/config", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteAIConfig)))
	mux.Handle("POST /api/v1/admin/ai/generate", orgPerm("ai:generate")(http.HandlerFunc(handlers.GenerateAIContent)))

	// Auto-Boost rules (org-scoped)
	mux.Handle("GET /api/v1/admin/auto-boost/rules", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListAutoBoostRules)))
	mux.Handle("POST /api/v1/admin/auto-boost/rules", orgPerm("auto_boost:manage")(http.HandlerFunc(handlers.CreateAutoBoostRule)))
	mux.Handle("GET /api/v1/admin/auto-boost/rules/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetAutoBoostRule)))
	mux.Handle("PUT /api/v1/admin/auto-boost/rules/{id}", orgPerm("auto_boost:manage")(http.HandlerFunc(handlers.UpdateAutoBoostRule)))
	mux.Handle("PATCH /api/v1/admin/auto-boost/rules/{id}", orgPerm("auto_boost:manage")(http.HandlerFunc(handlers.ToggleAutoBoostRule)))
	mux.Handle("DELETE /api/v1/admin/auto-boost/rules/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteAutoBoostRule)))

	// Auto-Boost logs (org-scoped)
	mux.Handle("GET /api/v1/admin/auto-boost/logs", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListAutoBoostLogs)))

	// Blog routes (auth required)
	mux.Handle("GET /api/v1/blog/posts/me", middleware.Auth(http.HandlerFunc(handlers.MyPosts)))

	// Blog routes (org-scoped — owner/admin/member can create content)
	mux.Handle("POST /api/v1/blog/posts", orgPerm("blog:manage")(http.HandlerFunc(handlers.CreatePost)))
	mux.Handle("PUT /api/v1/blog/posts/{id}", orgPerm("blog:manage")(http.HandlerFunc(handlers.UpdatePost)))
	mux.Handle("DELETE /api/v1/blog/posts/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeletePost)))
	mux.Handle("POST /api/v1/blog/upload", orgPerm("blog:manage")(http.HandlerFunc(handlers.UploadPostImage)))

	// Engagement routes (auth required)
	mux.Handle("POST /api/v1/blog/posts/{slug}/like", middleware.Auth(http.HandlerFunc(handlers.ToggleLike)))
	mux.Handle("POST /api/v1/blog/posts/{slug}/comments", middleware.Auth(http.HandlerFunc(handlers.CreateComment)))
	mux.Handle("DELETE /api/v1/blog/posts/{slug}/comments/{id}", middleware.Auth(http.HandlerFunc(handlers.DeleteComment)))

	// ==========================================
	// PLATFORM ADMIN (superadmin/superuser only)
	// ==========================================
	mux.Handle("GET /api/v1/platform/orgs", middleware.Auth(middleware.RequireRole("superadmin", "superuser")(http.HandlerFunc(handlers.PlatformListOrgs))))
	mux.Handle("GET /api/v1/platform/orgs-with-members", middleware.Auth(middleware.RequireRole("superadmin", "superuser")(http.HandlerFunc(handlers.PlatformOrgsWithMembers))))
	mux.Handle("GET /api/v1/platform/stats", middleware.Auth(middleware.RequireRole("superadmin", "superuser")(http.HandlerFunc(handlers.PlatformStats))))
	mux.Handle("PUT /api/v1/platform/orgs/{id}/plan", middleware.Auth(middleware.RequireRole("superadmin", "superuser")(http.HandlerFunc(handlers.PlatformUpdatePlan))))
	mux.Handle("GET /api/v1/platform/subscriptions", middleware.Auth(middleware.RequireRole("superadmin", "superuser")(http.HandlerFunc(handlers.PlatformListSubscriptions))))
	mux.Handle("GET /api/v1/platform/webhook-logs", middleware.Auth(middleware.RequireRole("superadmin", "superuser")(http.HandlerFunc(handlers.ListWebhookLogs))))
	mux.Handle("GET /api/v1/platform/webhook-stats", middleware.Auth(middleware.RequireRole("superadmin", "superuser")(http.HandlerFunc(handlers.WebhookStats))))
	mux.Handle("PUT /api/v1/platform/orgs/{id}/subscription-status", middleware.Auth(middleware.RequireRole("superadmin", "superuser")(http.HandlerFunc(handlers.PlatformUpdateSubscriptionStatus))))

	// ==========================================
	// CONTABIL MODULE ROUTES
	// ==========================================

	// Contabil user mappings management (org owner/admin only)
	mux.Handle("GET /api/v1/admin/contabil/mappings", orgRoute("owner", "admin")(http.HandlerFunc(handlers.ListContabilMappings)))
	mux.Handle("POST /api/v1/admin/contabil/mappings", orgRoute("owner", "admin")(http.HandlerFunc(handlers.CreateContabilMapping)))
	mux.Handle("PUT /api/v1/admin/contabil/mappings/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.UpdateContabilMapping)))
	mux.Handle("DELETE /api/v1/admin/contabil/mappings/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteContabilMapping)))

	// Contabil proxy — forward all /api/v1/admin/contabil/* to the contabil API
	contabilProxy := handlers.ContabilProxy(config.Get().ContabilAPIURL)
	mux.Handle("GET /api/v1/admin/contabil/clients", orgPerm("contabil:access")(http.HandlerFunc(contabilProxy)))
	mux.Handle("GET /api/v1/admin/contabil/clients/{id}", orgPerm("contabil:access")(http.HandlerFunc(contabilProxy)))
	mux.Handle("POST /api/v1/admin/contabil/clients", orgPerm("contabil:manage")(http.HandlerFunc(contabilProxy)))
	mux.Handle("PUT /api/v1/admin/contabil/clients/{id}", orgPerm("contabil:manage")(http.HandlerFunc(contabilProxy)))
	mux.Handle("DELETE /api/v1/admin/contabil/clients/{id}", orgPerm("contabil:manage")(http.HandlerFunc(contabilProxy)))

	mux.Handle("GET /api/v1/admin/contabil/bills", orgPerm("contabil:access")(http.HandlerFunc(contabilProxy)))
	mux.Handle("GET /api/v1/admin/contabil/bills/{id}", orgPerm("contabil:access")(http.HandlerFunc(contabilProxy)))
	mux.Handle("POST /api/v1/admin/contabil/bills/generate", orgPerm("contabil:manage")(http.HandlerFunc(contabilProxy)))
	mux.Handle("PUT /api/v1/admin/contabil/bills/{id}", orgPerm("contabil:manage")(http.HandlerFunc(contabilProxy)))
	mux.Handle("PUT /api/v1/admin/contabil/bills/{id}/status", orgPerm("contabil:manage")(http.HandlerFunc(contabilProxy)))
	mux.Handle("PATCH /api/v1/admin/contabil/bills/{id}/paid", orgPerm("contabil:manage")(http.HandlerFunc(contabilProxy)))

	mux.Handle("GET /api/v1/admin/contabil/services", orgPerm("contabil:access")(http.HandlerFunc(contabilProxy)))
	mux.Handle("POST /api/v1/admin/contabil/services", orgPerm("contabil:manage")(http.HandlerFunc(contabilProxy)))
	mux.Handle("PUT /api/v1/admin/contabil/services/{id}", orgPerm("contabil:manage")(http.HandlerFunc(contabilProxy)))
	mux.Handle("DELETE /api/v1/admin/contabil/services/{id}", orgPerm("contabil:manage")(http.HandlerFunc(contabilProxy)))

	mux.Handle("GET /api/v1/admin/contabil/dashboard/summary", orgPerm("contabil:access")(http.HandlerFunc(contabilProxy)))
	mux.Handle("GET /api/v1/admin/contabil/dashboard/revenue", orgPerm("contabil:access")(http.HandlerFunc(contabilProxy)))

	mux.Handle("POST /api/v1/admin/contabil/import/clients/preview", orgPerm("contabil:import")(http.HandlerFunc(contabilProxy)))
	mux.Handle("POST /api/v1/admin/contabil/import/clients", orgPerm("contabil:import")(http.HandlerFunc(contabilProxy)))
	mux.Handle("POST /api/v1/admin/contabil/import/services/preview", orgPerm("contabil:import")(http.HandlerFunc(contabilProxy)))
	mux.Handle("POST /api/v1/admin/contabil/import/services", orgPerm("contabil:import")(http.HandlerFunc(contabilProxy)))

	mux.Handle("GET /api/v1/admin/contabil/organizations", orgPerm("contabil:admin")(http.HandlerFunc(contabilProxy)))
	mux.Handle("POST /api/v1/admin/contabil/organizations", orgPerm("contabil:admin")(http.HandlerFunc(contabilProxy)))
	mux.Handle("GET /api/v1/admin/contabil/organizations/{id}", orgPerm("contabil:admin")(http.HandlerFunc(contabilProxy)))
	mux.Handle("PUT /api/v1/admin/contabil/organizations/{id}", orgPerm("contabil:admin")(http.HandlerFunc(contabilProxy)))
	mux.Handle("DELETE /api/v1/admin/contabil/organizations/{id}", orgPerm("contabil:admin")(http.HandlerFunc(contabilProxy)))

	mux.Handle("GET /api/v1/admin/contabil/users", orgPerm("contabil:admin")(http.HandlerFunc(contabilProxy)))
	mux.Handle("POST /api/v1/admin/contabil/users", orgPerm("contabil:admin")(http.HandlerFunc(contabilProxy)))
	mux.Handle("GET /api/v1/admin/contabil/users/{id}", orgPerm("contabil:admin")(http.HandlerFunc(contabilProxy)))
	mux.Handle("PUT /api/v1/admin/contabil/users/{id}", orgPerm("contabil:admin")(http.HandlerFunc(contabilProxy)))
	mux.Handle("PATCH /api/v1/admin/contabil/users/{id}/toggle-active", orgPerm("contabil:admin")(http.HandlerFunc(contabilProxy)))

	mux.Handle("GET /api/v1/admin/contabil/audit-logs", orgPerm("contabil:admin")(http.HandlerFunc(contabilProxy)))

	mux.Handle("GET /api/v1/admin/contabil/roles", orgPerm("contabil:access")(http.HandlerFunc(contabilProxy)))
	mux.Handle("GET /api/v1/admin/contabil/roles/{role}/permissions", orgPerm("contabil:access")(http.HandlerFunc(contabilProxy)))

	// ==========================================
	// GLOBAL MIDDLEWARES
	// ==========================================

	var handler http.Handler = mux
	handler = middleware.JSON(handler)
	handler = middleware.CORS(handler)
	handler = middleware.MetricsMiddleware(handler)
	handler = middleware.Logger(handler)

	return handler
}
