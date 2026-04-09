package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type ctaPostStat struct {
	Slug          string `json:"slug"`
	Title         string `json:"title"`
	ViewCount     int64  `json:"view_count"`
	CTAClickCount int64  `json:"cta_click_count"`
	ClickRate     string `json:"click_rate"`
}

type ctaRecentClick struct {
	CTA       string `json:"cta"`
	Slug      string `json:"slug"`
	PostTitle string `json:"post_title"`
	IP        string `json:"ip"`
	CreatedAt string `json:"created_at"`
}

type ctaAnalyticsResponse struct {
	TotalClicks   int64            `json:"total_clicks"`
	ClicksToday   int64            `json:"clicks_today"`
	ClicksWeek    int64            `json:"clicks_week"`
	TopPosts      []ctaPostStat    `json:"top_posts"`
	RecentClicks  []ctaRecentClick `json:"recent_clicks"`
	DailyClicks   []dailyCount     `json:"daily_clicks"`
}

type dailyCount struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

// GetCTAAnalytics returns CTA click analytics for admin dashboard.
// @Summary Obter analytics de CTA
// @Description Retorna estatísticas de cliques em CTA para o dashboard admin
// @Tags analytics
// @Produce json
// @Security BearerAuth
// @Param days query int false "Período em dias (padrão 30, máx 365)"
// @Success 200 {object} ctaAnalyticsResponse
// @Failure 401 {string} string "Unauthorized"
// @Router /admin/cta-analytics [get]
func GetCTAAnalytics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	orgID := middleware.GetOrgID(r)

	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 1 || days > 365 {
		days = 30
	}

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	weekStart := todayStart.AddDate(0, 0, -7)

	clicksCol := database.CTAClicks()

	// Build org-scoped filter for CTA clicks
	clickFilter := bson.M{}
	if orgID != primitive.NilObjectID {
		clickFilter["org_id"] = orgID
	}

	// Total clicks (all time)
	totalClicks, _ := clicksCol.CountDocuments(ctx, clickFilter)

	// Clicks today
	clicksTodayFilter := bson.M{"created_at": bson.M{"$gte": todayStart}}
	if orgID != primitive.NilObjectID {
		clicksTodayFilter["org_id"] = orgID
	}
	clicksToday, _ := clicksCol.CountDocuments(ctx, clicksTodayFilter)

	// Clicks this week
	clicksWeekFilter := bson.M{"created_at": bson.M{"$gte": weekStart}}
	if orgID != primitive.NilObjectID {
		clicksWeekFilter["org_id"] = orgID
	}
	clicksWeek, _ := clicksCol.CountDocuments(ctx, clicksWeekFilter)

	// Top posts by cta_click_count (scoped to org)
	postsFilter := bson.M{"cta_click_count": bson.M{"$gt": 0}}
	if orgID != primitive.NilObjectID {
		postsFilter["org_id"] = orgID
	}
	postsCursor, err := database.Posts().Find(ctx,
		postsFilter,
		options.Find().
			SetSort(bson.D{{Key: "cta_click_count", Value: -1}}).
			SetLimit(20).
			SetProjection(bson.M{
				"slug":            1,
				"title":           1,
				"view_count":      1,
				"cta_click_count": 1,
			}),
	)

	var topPosts []ctaPostStat
	if err == nil {
		defer postsCursor.Close(ctx)
		for postsCursor.Next(ctx) {
			var doc struct {
				Slug          string `bson:"slug"`
				Title         string `bson:"title"`
				ViewCount     int64  `bson:"view_count"`
				CTAClickCount int64  `bson:"cta_click_count"`
			}
			if postsCursor.Decode(&doc) == nil {
				rate := "0%"
				if doc.ViewCount > 0 {
					pct := float64(doc.CTAClickCount) / float64(doc.ViewCount) * 100
					rate = strconv.FormatFloat(pct, 'f', 1, 64) + "%"
				}
				topPosts = append(topPosts, ctaPostStat{
					Slug:          doc.Slug,
					Title:         doc.Title,
					ViewCount:     doc.ViewCount,
					CTAClickCount: doc.CTAClickCount,
					ClickRate:     rate,
				})
			}
		}
	}
	if topPosts == nil {
		topPosts = []ctaPostStat{}
	}

	// Recent clicks (last 50, scoped to org)
	clicksCursor, err := clicksCol.Find(ctx,
		clickFilter,
		options.Find().
			SetSort(bson.D{{Key: "created_at", Value: -1}}).
			SetLimit(50),
	)

	var recentClicks []ctaRecentClick
	if err == nil {
		defer clicksCursor.Close(ctx)

		// Build a map of post IDs to titles
		type clickDoc struct {
			PostID    interface{} `bson:"post_id"`
			CTA       string      `bson:"cta"`
			IP        string      `bson:"ip"`
			CreatedAt time.Time   `bson:"created_at"`
		}

		var clicks []clickDoc
		clicksCursor.All(ctx, &clicks)

		// Get post info for these clicks
		postMap := make(map[string]struct{ Slug, Title string })
		for _, c := range clicks {
			key := ""
			switch v := c.PostID.(type) {
			case string:
				key = v
			default:
				// primitive.ObjectID
				if oid, ok := c.PostID.(interface{ Hex() string }); ok {
					key = oid.Hex()
				}
			}
			if _, exists := postMap[key]; !exists {
				postMap[key] = struct{ Slug, Title string }{}
			}
		}

		// Fetch posts in bulk
		if len(clicks) > 0 {
			allPostsCursor, _ := database.Posts().Find(ctx,
				bson.M{"status": "published"},
				options.Find().SetProjection(bson.M{"slug": 1, "title": 1}),
			)
			if allPostsCursor != nil {
				defer allPostsCursor.Close(ctx)
				for allPostsCursor.Next(ctx) {
					var p struct {
						ID    interface{} `bson:"_id"`
						Slug  string      `bson:"slug"`
						Title string      `bson:"title"`
					}
					if allPostsCursor.Decode(&p) == nil {
						key := ""
						if oid, ok := p.ID.(interface{ Hex() string }); ok {
							key = oid.Hex()
						}
						postMap[key] = struct{ Slug, Title string }{p.Slug, p.Title}
					}
				}
			}
		}

		for _, c := range clicks {
			key := ""
			if oid, ok := c.PostID.(interface{ Hex() string }); ok {
				key = oid.Hex()
			}
			info := postMap[key]

			// Mask IP for privacy
			maskedIP := c.IP
			if len(maskedIP) > 8 {
				maskedIP = maskedIP[:8] + "***"
			}

			recentClicks = append(recentClicks, ctaRecentClick{
				CTA:       c.CTA,
				Slug:      info.Slug,
				PostTitle: info.Title,
				IP:        maskedIP,
				CreatedAt: c.CreatedAt.Format("02/01 15:04"),
			})
		}
	}
	if recentClicks == nil {
		recentClicks = []ctaRecentClick{}
	}

	// Daily clicks for the period (scoped to org)
	var dailyClicks []dailyCount
	for i := days - 1; i >= 0; i-- {
		dayStart := todayStart.AddDate(0, 0, -i)
		dayEnd := dayStart.Add(24 * time.Hour)
		dayFilter := bson.M{
			"created_at": bson.M{"$gte": dayStart, "$lt": dayEnd},
		}
		if orgID != primitive.NilObjectID {
			dayFilter["org_id"] = orgID
		}
		count, _ := clicksCol.CountDocuments(ctx, dayFilter)
		if count > 0 || i < 14 { // Always show last 14 days, others only if have data
			dailyClicks = append(dailyClicks, dailyCount{
				Date:  dayStart.Format("02/01"),
				Count: count,
			})
		}
	}
	if dailyClicks == nil {
		dailyClicks = []dailyCount{}
	}

	json.NewEncoder(w).Encode(ctaAnalyticsResponse{
		TotalClicks:  totalClicks,
		ClicksToday:  clicksToday,
		ClicksWeek:   clicksWeek,
		TopPosts:     topPosts,
		RecentClicks: recentClicks,
		DailyClicks:  dailyClicks,
	})
}
