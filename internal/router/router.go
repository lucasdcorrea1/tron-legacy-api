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

	// Users routes (admin/superuser only)
	mux.Handle("GET /api/v1/users", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListUsers))))
	mux.Handle("PUT /api/v1/users/{id}/role", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.UpdateUserRole))))

	// Email Marketing routes (admin/superuser only)
	mux.Handle("GET /api/v1/admin/email-marketing/templates", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListEmailTemplates))))
	mux.Handle("POST /api/v1/admin/email-marketing/templates/{id}/preview", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.PreviewEmailTemplate))))
	mux.Handle("GET /api/v1/admin/email-marketing/audience", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetEmailAudience))))
	mux.Handle("POST /api/v1/admin/email-marketing/send", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.SendMarketingEmail))))
	mux.Handle("GET /api/v1/admin/email-marketing/subscribers", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListSubscribers))))
	mux.Handle("DELETE /api/v1/admin/email-marketing/subscribers/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.DeleteSubscriber))))
	mux.Handle("GET /api/v1/admin/email-marketing/broadcasts", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListBroadcasts))))
	mux.Handle("GET /api/v1/admin/email-marketing/broadcasts/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetBroadcast))))

	// CTA analytics (admin only)
	mux.Handle("GET /api/v1/admin/cta-analytics", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetCTAAnalytics))))

	// Instagram scheduling routes (superuser + admin)
	mux.Handle("GET /api/v1/admin/instagram/config", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetInstagramConfig))))
	mux.Handle("PUT /api/v1/admin/instagram/config", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.SaveInstagramConfig))))
	mux.Handle("DELETE /api/v1/admin/instagram/config", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.DeleteInstagramConfig))))
	mux.Handle("GET /api/v1/admin/instagram/test", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.TestInstagramConnection))))
	mux.Handle("GET /api/v1/admin/instagram/feed", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetInstagramFeed))))
	mux.Handle("GET /api/v1/admin/instagram/schedules", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListInstagramSchedules))))
	mux.Handle("POST /api/v1/admin/instagram/schedules", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.CreateInstagramSchedule))))
	mux.Handle("GET /api/v1/admin/instagram/schedules/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetInstagramSchedule))))
	mux.Handle("PUT /api/v1/admin/instagram/schedules/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.UpdateInstagramSchedule))))
	mux.Handle("DELETE /api/v1/admin/instagram/schedules/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.DeleteInstagramSchedule))))
	mux.Handle("POST /api/v1/admin/instagram/upload", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.UploadInstagramImage))))

	// Instagram auto-reply routes (superuser + admin)
	mux.Handle("GET /api/v1/admin/instagram/autoreply/rules", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListAutoReplyRules))))
	mux.Handle("POST /api/v1/admin/instagram/autoreply/rules", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.CreateAutoReplyRule))))
	mux.Handle("PUT /api/v1/admin/instagram/autoreply/rules/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.UpdateAutoReplyRule))))
	mux.Handle("PATCH /api/v1/admin/instagram/autoreply/rules/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ToggleAutoReplyRule))))
	mux.Handle("DELETE /api/v1/admin/instagram/autoreply/rules/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.DeleteAutoReplyRule))))
	mux.Handle("GET /api/v1/admin/instagram/autoreply/logs", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListAutoReplyLogs))))

	// Instagram auto-reply live feed (SSE — auth via query param, validated internally)
	mux.HandleFunc("GET /api/v1/admin/instagram/autoreply/live", handlers.AutoReplySSE)

	// Instagram leads routes (superuser + admin)
	mux.Handle("GET /api/v1/admin/instagram/leads/export", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ExportLeadsCSV))))
	mux.Handle("GET /api/v1/admin/instagram/leads/stats", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetLeadStats))))
	mux.Handle("GET /api/v1/admin/instagram/leads", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.ListInstagramLeads))))
	mux.Handle("PUT /api/v1/admin/instagram/leads/{id}/tags", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.UpdateLeadTags))))

	// Instagram analytics routes (superuser + admin)
	mux.Handle("GET /api/v1/admin/instagram/analytics/autoreply", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetAutoReplyAnalytics))))
	mux.Handle("GET /api/v1/admin/instagram/analytics/engagement", middleware.Auth(middleware.RequireRole("superuser", "admin")(http.HandlerFunc(handlers.GetEngagementReport))))

	// Blog routes (auth required)
	mux.Handle("GET /api/v1/blog/posts/me", middleware.Auth(http.HandlerFunc(handlers.MyPosts)))

	// Blog routes (auth + role superuser/admin/author)
	mux.Handle("POST /api/v1/blog/posts", middleware.Auth(middleware.RequireRole("superuser", "admin", "author")(http.HandlerFunc(handlers.CreatePost))))
	mux.Handle("PUT /api/v1/blog/posts/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin", "author")(http.HandlerFunc(handlers.UpdatePost))))
	mux.Handle("DELETE /api/v1/blog/posts/{id}", middleware.Auth(middleware.RequireRole("superuser", "admin", "author")(http.HandlerFunc(handlers.DeletePost))))
	mux.Handle("POST /api/v1/blog/upload", middleware.Auth(middleware.RequireRole("superuser", "admin", "author")(http.HandlerFunc(handlers.UploadPostImage))))

	// Engagement routes (auth required)
	mux.Handle("POST /api/v1/blog/posts/{slug}/like", middleware.Auth(http.HandlerFunc(handlers.ToggleLike)))
	mux.Handle("POST /api/v1/blog/posts/{slug}/comments", middleware.Auth(http.HandlerFunc(handlers.CreateComment)))
	mux.Handle("DELETE /api/v1/blog/posts/{slug}/comments/{id}", middleware.Auth(http.HandlerFunc(handlers.DeleteComment)))

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
