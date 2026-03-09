package router

import (
	"net/http"

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

	// Auth routes (public)
	mux.HandleFunc("POST /api/v1/auth/register", handlers.Register)
	mux.HandleFunc("POST /api/v1/auth/login", handlers.Login)
	mux.HandleFunc("POST /api/v1/auth/forgot-password", handlers.ForgotPassword)
	mux.HandleFunc("POST /api/v1/auth/reset-password", handlers.ResetPassword)
	mux.HandleFunc("POST /api/v1/auth/refresh", handlers.Refresh)
	mux.HandleFunc("POST /api/v1/auth/logout", handlers.Logout)

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

	// Accept invitation (auth only — no org context)
	mux.Handle("POST /api/v1/invitations/{token}/accept", middleware.Auth(http.HandlerFunc(handlers.AcceptInvitation)))

	// Org detail routes (require org context)
	mux.Handle("GET /api/v1/orgs/current", middleware.Auth(middleware.RequireOrg(http.HandlerFunc(handlers.GetOrg))))
	mux.Handle("PUT /api/v1/orgs/current", middleware.Auth(middleware.RequireOrg(middleware.RequireOrgRole("owner", "admin")(http.HandlerFunc(handlers.UpdateOrg)))))
	mux.Handle("DELETE /api/v1/orgs/current", middleware.Auth(middleware.RequireOrg(middleware.RequireOrgRole("owner")(http.HandlerFunc(handlers.DeleteOrg)))))

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

	// ==========================================
	// ORG-SCOPED ADMIN ROUTES
	// ==========================================
	// Helper: auth + org + org role check
	orgRoute := func(roles ...string) func(http.Handler) http.Handler {
		return func(h http.Handler) http.Handler {
			return middleware.Auth(middleware.RequireOrg(middleware.RequireOrgRole(roles...)(h)))
		}
	}

	// Users/members (org-scoped)
	mux.Handle("GET /api/v1/users", orgRoute("owner", "admin")(http.HandlerFunc(handlers.ListUsers)))
	mux.Handle("PUT /api/v1/users/{id}/role", orgRoute("owner", "admin")(http.HandlerFunc(handlers.UpdateUserRole)))

	// Email Marketing routes (org-scoped)
	mux.Handle("GET /api/v1/admin/email-marketing/templates", orgRoute("owner", "admin")(http.HandlerFunc(handlers.ListEmailTemplates)))
	mux.Handle("POST /api/v1/admin/email-marketing/templates/{id}/preview", orgRoute("owner", "admin")(http.HandlerFunc(handlers.PreviewEmailTemplate)))
	mux.Handle("GET /api/v1/admin/email-marketing/audience", orgRoute("owner", "admin")(http.HandlerFunc(handlers.GetEmailAudience)))
	mux.Handle("POST /api/v1/admin/email-marketing/send", orgRoute("owner", "admin")(http.HandlerFunc(handlers.SendMarketingEmail)))
	mux.Handle("GET /api/v1/admin/email-marketing/subscribers", orgRoute("owner", "admin")(http.HandlerFunc(handlers.ListSubscribers)))
	mux.Handle("DELETE /api/v1/admin/email-marketing/subscribers/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteSubscriber)))
	mux.Handle("GET /api/v1/admin/email-marketing/broadcasts", orgRoute("owner", "admin")(http.HandlerFunc(handlers.ListBroadcasts)))
	mux.Handle("GET /api/v1/admin/email-marketing/broadcasts/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.GetBroadcast)))

	// CTA analytics (org-scoped)
	mux.Handle("GET /api/v1/admin/cta-analytics", orgRoute("owner", "admin")(http.HandlerFunc(handlers.GetCTAAnalytics)))

	// Instagram scheduling routes (org-scoped)
	mux.Handle("GET /api/v1/admin/instagram/config", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetInstagramConfig)))
	mux.Handle("PUT /api/v1/admin/instagram/config", orgRoute("owner", "admin")(http.HandlerFunc(handlers.SaveInstagramConfig)))
	mux.Handle("DELETE /api/v1/admin/instagram/config", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteInstagramConfig)))
	mux.Handle("GET /api/v1/admin/instagram/test", orgRoute("owner", "admin")(http.HandlerFunc(handlers.TestInstagramConnection)))
	mux.Handle("GET /api/v1/admin/instagram/feed", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetInstagramFeed)))
	mux.Handle("GET /api/v1/admin/instagram/schedules", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListInstagramSchedules)))
	mux.Handle("POST /api/v1/admin/instagram/schedules", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.CreateInstagramSchedule)))
	mux.Handle("GET /api/v1/admin/instagram/schedules/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetInstagramSchedule)))
	mux.Handle("PUT /api/v1/admin/instagram/schedules/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.UpdateInstagramSchedule)))
	mux.Handle("DELETE /api/v1/admin/instagram/schedules/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.DeleteInstagramSchedule)))
	mux.Handle("POST /api/v1/admin/instagram/upload", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.UploadInstagramImage)))

	// Instagram auto-reply routes (org-scoped)
	mux.Handle("GET /api/v1/admin/instagram/autoreply/rules", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListAutoReplyRules)))
	mux.Handle("POST /api/v1/admin/instagram/autoreply/rules", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.CreateAutoReplyRule)))
	mux.Handle("PUT /api/v1/admin/instagram/autoreply/rules/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.UpdateAutoReplyRule)))
	mux.Handle("PATCH /api/v1/admin/instagram/autoreply/rules/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ToggleAutoReplyRule)))
	mux.Handle("DELETE /api/v1/admin/instagram/autoreply/rules/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteAutoReplyRule)))
	mux.Handle("GET /api/v1/admin/instagram/autoreply/logs", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListAutoReplyLogs)))

	// Instagram auto-reply live feed (SSE — auth via query param, validated internally)
	mux.HandleFunc("GET /api/v1/admin/instagram/autoreply/live", handlers.AutoReplySSE)

	// Instagram leads routes (org-scoped)
	mux.Handle("GET /api/v1/admin/instagram/leads/export", orgRoute("owner", "admin")(http.HandlerFunc(handlers.ExportLeadsCSV)))
	mux.Handle("GET /api/v1/admin/instagram/leads/stats", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetLeadStats)))
	mux.Handle("GET /api/v1/admin/instagram/leads", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListInstagramLeads)))
	mux.Handle("PUT /api/v1/admin/instagram/leads/{id}/tags", orgRoute("owner", "admin")(http.HandlerFunc(handlers.UpdateLeadTags)))

	// Instagram analytics routes (org-scoped)
	mux.Handle("GET /api/v1/admin/instagram/analytics/autoreply", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetAutoReplyAnalytics)))
	mux.Handle("GET /api/v1/admin/instagram/analytics/engagement", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetEngagementReport)))

	// Meta Ads campaign routes (org-scoped)
	mux.Handle("GET /api/v1/admin/meta-ads/campaigns", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListMetaAdsCampaigns)))
	mux.Handle("POST /api/v1/admin/meta-ads/campaigns", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.CreateMetaAdsCampaign)))
	mux.Handle("GET /api/v1/admin/meta-ads/campaigns/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetMetaAdsCampaign)))
	mux.Handle("PUT /api/v1/admin/meta-ads/campaigns/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.UpdateMetaAdsCampaign)))
	mux.Handle("DELETE /api/v1/admin/meta-ads/campaigns/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteMetaAdsCampaign)))
	mux.Handle("PATCH /api/v1/admin/meta-ads/campaigns/{id}/status", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.UpdateMetaAdsCampaignStatus)))

	// Meta Ads ad set routes (org-scoped)
	mux.Handle("GET /api/v1/admin/meta-ads/adsets", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListMetaAdsAdSets)))
	mux.Handle("POST /api/v1/admin/meta-ads/adsets", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.CreateMetaAdsAdSet)))
	mux.Handle("GET /api/v1/admin/meta-ads/adsets/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetMetaAdsAdSet)))
	mux.Handle("PUT /api/v1/admin/meta-ads/adsets/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.UpdateMetaAdsAdSet)))
	mux.Handle("DELETE /api/v1/admin/meta-ads/adsets/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteMetaAdsAdSet)))
	mux.Handle("PATCH /api/v1/admin/meta-ads/adsets/{id}/status", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.UpdateMetaAdsAdSetStatus)))

	// Meta Ads ad routes (org-scoped)
	mux.Handle("GET /api/v1/admin/meta-ads/ads", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListMetaAdsAds)))
	mux.Handle("POST /api/v1/admin/meta-ads/ads", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.CreateMetaAdsAd)))
	mux.Handle("GET /api/v1/admin/meta-ads/ads/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetMetaAdsAd)))
	mux.Handle("PUT /api/v1/admin/meta-ads/ads/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.UpdateMetaAdsAd)))
	mux.Handle("DELETE /api/v1/admin/meta-ads/ads/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteMetaAdsAd)))
	mux.Handle("PATCH /api/v1/admin/meta-ads/ads/{id}/status", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.UpdateMetaAdsAdStatus)))

	// Meta Ads insights (org-scoped)
	mux.Handle("GET /api/v1/admin/meta-ads/insights", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetMetaAdsInsights)))
	mux.Handle("GET /api/v1/admin/meta-ads/campaigns/{id}/insights", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetMetaAdsCampaignInsights)))

	// Meta Ads upload (org-scoped)
	mux.Handle("POST /api/v1/admin/meta-ads/upload/image", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.UploadMetaAdsImage)))
	mux.Handle("POST /api/v1/admin/meta-ads/upload/video", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.UploadMetaAdsVideo)))

	// Meta Ads targeting search (org-scoped)
	mux.Handle("GET /api/v1/admin/meta-ads/targeting/interests", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.SearchMetaAdsInterests)))
	mux.Handle("GET /api/v1/admin/meta-ads/targeting/locations", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.SearchMetaAdsLocations)))
	mux.Handle("GET /api/v1/admin/meta-ads/targeting/audiences", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListMetaAdsAudiences)))

	// Meta Ads targeting presets (org-scoped)
	mux.Handle("GET /api/v1/admin/meta-ads/presets", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListMetaAdsPresets)))
	mux.Handle("POST /api/v1/admin/meta-ads/presets", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.CreateMetaAdsPreset)))
	mux.Handle("DELETE /api/v1/admin/meta-ads/presets/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteMetaAdsPreset)))

	// Meta Ads campaign templates (org-scoped)
	mux.Handle("GET /api/v1/admin/meta-ads/templates", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListMetaAdsTemplates)))
	mux.Handle("POST /api/v1/admin/meta-ads/templates", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.CreateMetaAdsTemplate)))
	mux.Handle("DELETE /api/v1/admin/meta-ads/templates/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteMetaAdsTemplate)))

	// Meta Ads budget alerts (org-scoped)
	mux.Handle("GET /api/v1/admin/meta-ads/alerts", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListMetaAdsBudgetAlerts)))
	mux.Handle("POST /api/v1/admin/meta-ads/alerts", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.CreateMetaAdsBudgetAlert)))
	mux.Handle("PUT /api/v1/admin/meta-ads/alerts/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.UpdateMetaAdsBudgetAlert)))
	mux.Handle("DELETE /api/v1/admin/meta-ads/alerts/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteMetaAdsBudgetAlert)))

	// Integrated Publish routes (org-scoped)
	mux.Handle("GET /api/v1/admin/integrated-publish", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListIntegratedPublishes)))
	mux.Handle("POST /api/v1/admin/integrated-publish", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.CreateIntegratedPublish)))
	mux.Handle("GET /api/v1/admin/integrated-publish/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetIntegratedPublish)))
	mux.Handle("DELETE /api/v1/admin/integrated-publish/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteIntegratedPublish)))

	// Auto-Boost rules (org-scoped)
	mux.Handle("GET /api/v1/admin/auto-boost/rules", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListAutoBoostRules)))
	mux.Handle("POST /api/v1/admin/auto-boost/rules", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.CreateAutoBoostRule)))
	mux.Handle("GET /api/v1/admin/auto-boost/rules/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.GetAutoBoostRule)))
	mux.Handle("PUT /api/v1/admin/auto-boost/rules/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.UpdateAutoBoostRule)))
	mux.Handle("PATCH /api/v1/admin/auto-boost/rules/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ToggleAutoBoostRule)))
	mux.Handle("DELETE /api/v1/admin/auto-boost/rules/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeleteAutoBoostRule)))

	// Auto-Boost logs (org-scoped)
	mux.Handle("GET /api/v1/admin/auto-boost/logs", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.ListAutoBoostLogs)))

	// Blog routes (auth required)
	mux.Handle("GET /api/v1/blog/posts/me", middleware.Auth(http.HandlerFunc(handlers.MyPosts)))

	// Blog routes (org-scoped — owner/admin/member can create content)
	mux.Handle("POST /api/v1/blog/posts", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.CreatePost)))
	mux.Handle("PUT /api/v1/blog/posts/{id}", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.UpdatePost)))
	mux.Handle("DELETE /api/v1/blog/posts/{id}", orgRoute("owner", "admin")(http.HandlerFunc(handlers.DeletePost)))
	mux.Handle("POST /api/v1/blog/upload", orgRoute("owner", "admin", "member")(http.HandlerFunc(handlers.UploadPostImage)))

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
