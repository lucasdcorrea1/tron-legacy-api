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

	// Blog routes (public)
	mux.HandleFunc("GET /api/v1/blog/posts", handlers.ListPosts)
	mux.Handle("GET /api/v1/blog/posts/{slug}", middleware.OptionalAuth(http.HandlerFunc(handlers.GetPostBySlug)))
	mux.HandleFunc("GET /api/v1/blog/images/group/{groupId}", handlers.ServeImageByGroup)
	mux.HandleFunc("GET /api/v1/blog/images/{id}", handlers.ServeImage)

	// Newsletter (public)
	mux.HandleFunc("POST /api/v1/newsletter/subscribe", handlers.SubscribeNewsletter)

	// Engagement routes (public)
	mux.HandleFunc("GET /api/v1/blog/posts/{slug}/comments", handlers.ListComments)

	// Engagement routes (optional auth — detect user if logged in)
	mux.Handle("POST /api/v1/blog/posts/{slug}/view", middleware.OptionalAuth(http.HandlerFunc(handlers.RecordView)))
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

	// Users routes (admin only)
	mux.Handle("GET /api/v1/users", middleware.Auth(middleware.RequireRole("admin")(http.HandlerFunc(handlers.ListUsers))))
	mux.Handle("PUT /api/v1/users/{id}/role", middleware.Auth(middleware.RequireRole("admin")(http.HandlerFunc(handlers.UpdateUserRole))))

	// Email Marketing routes (admin only)
	mux.Handle("GET /api/v1/admin/email-marketing/templates", middleware.Auth(middleware.RequireRole("admin")(http.HandlerFunc(handlers.ListEmailTemplates))))
	mux.Handle("POST /api/v1/admin/email-marketing/templates/{id}/preview", middleware.Auth(middleware.RequireRole("admin")(http.HandlerFunc(handlers.PreviewEmailTemplate))))
	mux.Handle("GET /api/v1/admin/email-marketing/audience", middleware.Auth(middleware.RequireRole("admin")(http.HandlerFunc(handlers.GetEmailAudience))))
	mux.Handle("POST /api/v1/admin/email-marketing/send", middleware.Auth(middleware.RequireRole("admin")(http.HandlerFunc(handlers.SendMarketingEmail))))
	mux.Handle("GET /api/v1/admin/email-marketing/subscribers", middleware.Auth(middleware.RequireRole("admin")(http.HandlerFunc(handlers.ListSubscribers))))
	mux.Handle("DELETE /api/v1/admin/email-marketing/subscribers/{id}", middleware.Auth(middleware.RequireRole("admin")(http.HandlerFunc(handlers.DeleteSubscriber))))
	mux.Handle("GET /api/v1/admin/email-marketing/broadcasts", middleware.Auth(middleware.RequireRole("admin")(http.HandlerFunc(handlers.ListBroadcasts))))
	mux.Handle("GET /api/v1/admin/email-marketing/broadcasts/{id}", middleware.Auth(middleware.RequireRole("admin")(http.HandlerFunc(handlers.GetBroadcast))))

	// Blog routes (auth required)
	mux.Handle("GET /api/v1/blog/posts/me", middleware.Auth(http.HandlerFunc(handlers.MyPosts)))

	// Blog routes (auth + role admin/author)
	mux.Handle("POST /api/v1/blog/posts", middleware.Auth(middleware.RequireRole("admin", "author")(http.HandlerFunc(handlers.CreatePost))))
	mux.Handle("PUT /api/v1/blog/posts/{id}", middleware.Auth(middleware.RequireRole("admin", "author")(http.HandlerFunc(handlers.UpdatePost))))
	mux.Handle("DELETE /api/v1/blog/posts/{id}", middleware.Auth(middleware.RequireRole("admin", "author")(http.HandlerFunc(handlers.DeletePost))))
	mux.Handle("POST /api/v1/blog/upload", middleware.Auth(middleware.RequireRole("admin", "author")(http.HandlerFunc(handlers.UploadPostImage))))

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
