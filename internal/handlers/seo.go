package handlers

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const baseSiteURL = "https://whodo.com.br"

type urlset struct {
	XMLName xml.Name    `xml:"urlset"`
	XMLNS   string      `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc        string `xml:"loc"`
	LastMod    string `xml:"lastmod,omitempty"`
	ChangeFreq string `xml:"changefreq,omitempty"`
	Priority   string `xml:"priority,omitempty"`
}

// Sitemap generates a dynamic sitemap.xml from published blog posts
func Sitemap(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Static pages
	urls := []sitemapURL{
		{Loc: baseSiteURL, ChangeFreq: "weekly", Priority: "1.0"},
		{Loc: baseSiteURL + "/blog", ChangeFreq: "daily", Priority: "0.9"},
	}

	// Fetch all published posts (slug + updated_at only)
	opts := options.Find().
		SetSort(bson.D{{Key: "published_at", Value: -1}}).
		SetProjection(bson.M{"slug": 1, "updated_at": 1, "published_at": 1})

	cursor, err := database.Posts().Find(ctx, bson.M{"status": "published"}, opts)
	if err == nil {
		defer cursor.Close(ctx)
		var posts []models.BlogPost
		if cursor.All(ctx, &posts) == nil {
			for _, post := range posts {
				lastMod := post.UpdatedAt.Format("2006-01-02")
				urls = append(urls, sitemapURL{
					Loc:        fmt.Sprintf("%s/blog/%s", baseSiteURL, post.Slug),
					LastMod:    lastMod,
					ChangeFreq: "weekly",
					Priority:   "0.8",
				})
			}
		}
	}

	sitemap := urlset{
		XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9",
		URLs:  urls,
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write([]byte(xml.Header))
	xml.NewEncoder(w).Encode(sitemap)
}

// RobotsTxt serves a robots.txt pointing to the sitemap
func RobotsTxt(w http.ResponseWriter, r *http.Request) {
	apiURL := os.Getenv("RENDER_EXTERNAL_URL")
	if apiURL == "" {
		apiURL = "https://tron-legacy-api.onrender.com"
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	fmt.Fprintf(w, `User-agent: *
Allow: /

Sitemap: %s/api/v1/sitemap.xml
`, apiURL)
}
