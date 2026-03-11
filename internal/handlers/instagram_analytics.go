package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// GetAutoReplyAnalytics returns aggregated metrics for auto-reply.
// GET /api/v1/admin/instagram/analytics/autoreply?days=30
func GetAutoReplyAnalytics(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 1 || days > 365 {
		days = 30
	}

	since := time.Now().AddDate(0, 0, -days)
	baseFilter := bson.M{"org_id": orgID, "created_at": bson.M{"$gte": since}}

	col := database.AutoReplyLogs()

	// Count by status
	totalSent, _ := col.CountDocuments(ctx, mergeFilter(baseFilter, bson.M{"status": "sent"}))
	totalFailed, _ := col.CountDocuments(ctx, mergeFilter(baseFilter, bson.M{"status": "failed"}))
	totalSkipped, _ := col.CountDocuments(ctx, mergeFilter(baseFilter, bson.M{"status": "skipped_cooldown"}))

	total := totalSent + totalFailed + totalSkipped
	var successRate float64
	if total > 0 {
		successRate = float64(totalSent) / float64(total) * 100
	}

	// Top rules (sent only)
	topRules := aggregateTopRules(ctx, since, orgID)

	// Hourly distribution (sent only)
	hourlyDist := aggregateHourly(ctx, since, orgID)

	// Daily trend
	dailyTrend := aggregateDaily(ctx, since, orgID)

	// Top keywords from rules that were triggered
	topKeywords := aggregateTopKeywords(ctx, since, orgID)

	json.NewEncoder(w).Encode(models.AutoReplyAnalytics{
		TotalSent:    totalSent,
		TotalFailed:  totalFailed,
		TotalSkipped: totalSkipped,
		SuccessRate:  successRate,
		TopRules:     topRules,
		HourlyDist:   hourlyDist,
		DailyTrend:   dailyTrend,
		TopKeywords:  topKeywords,
	})
}

func aggregateTopRules(ctx context.Context, since time.Time, orgID primitive.ObjectID) []models.RuleCount {
	pipeline := []bson.M{
		{"$match": bson.M{"org_id": orgID, "created_at": bson.M{"$gte": since}, "status": "sent"}},
		{"$group": bson.M{"_id": "$rule_name", "count": bson.M{"$sum": 1}}},
		{"$sort": bson.M{"count": -1}},
		{"$limit": 10},
	}
	cursor, err := database.AutoReplyLogs().Aggregate(ctx, pipeline)
	if err != nil {
		return nil
	}
	defer cursor.Close(ctx)

	var results []models.RuleCount
	cursor.All(ctx, &results)
	if results == nil {
		results = []models.RuleCount{}
	}
	return results
}

func aggregateHourly(ctx context.Context, since time.Time, orgID primitive.ObjectID) []models.HourlyCount {
	pipeline := []bson.M{
		{"$match": bson.M{"org_id": orgID, "created_at": bson.M{"$gte": since}, "status": "sent"}},
		{"$group": bson.M{
			"_id":   bson.M{"$hour": "$created_at"},
			"count": bson.M{"$sum": 1},
		}},
		{"$sort": bson.M{"_id": 1}},
	}
	cursor, err := database.AutoReplyLogs().Aggregate(ctx, pipeline)
	if err != nil {
		return nil
	}
	defer cursor.Close(ctx)

	var results []models.HourlyCount
	cursor.All(ctx, &results)
	if results == nil {
		results = []models.HourlyCount{}
	}
	return results
}

func aggregateDaily(ctx context.Context, since time.Time, orgID primitive.ObjectID) []models.DailyTrend {
	pipeline := []bson.M{
		{"$match": bson.M{"org_id": orgID, "created_at": bson.M{"$gte": since}}},
		{"$group": bson.M{
			"_id": bson.M{"$dateToString": bson.M{"format": "%Y-%m-%d", "date": "$created_at"}},
			"sent": bson.M{"$sum": bson.M{"$cond": []interface{}{
				bson.M{"$eq": []string{"$status", "sent"}}, 1, 0,
			}}},
			"failed": bson.M{"$sum": bson.M{"$cond": []interface{}{
				bson.M{"$eq": []string{"$status", "failed"}}, 1, 0,
			}}},
		}},
		{"$sort": bson.M{"_id": 1}},
	}
	cursor, err := database.AutoReplyLogs().Aggregate(ctx, pipeline)
	if err != nil {
		return nil
	}
	defer cursor.Close(ctx)

	var results []models.DailyTrend
	cursor.All(ctx, &results)
	if results == nil {
		results = []models.DailyTrend{}
	}
	return results
}

func aggregateTopKeywords(ctx context.Context, since time.Time, orgID primitive.ObjectID) []models.KeywordCount {
	// Get all rules that were triggered in the period, then count keywords
	pipeline := []bson.M{
		{"$match": bson.M{"org_id": orgID, "created_at": bson.M{"$gte": since}, "status": "sent"}},
		{"$group": bson.M{"_id": "$rule_id", "count": bson.M{"$sum": 1}}},
	}
	cursor, err := database.AutoReplyLogs().Aggregate(ctx, pipeline)
	if err != nil {
		return nil
	}
	defer cursor.Close(ctx)

	type ruleAgg struct {
		RuleID primitive.ObjectID `bson:"_id"`
		Count  int64              `bson:"count"`
	}
	var ruleAggs []ruleAgg
	cursor.All(ctx, &ruleAggs)

	// Fetch keywords from each rule
	kwMap := map[string]int64{}
	for _, ra := range ruleAggs {
		var rule models.AutoReplyRule
		err := database.AutoReplyRules().FindOne(ctx, bson.M{"_id": ra.RuleID}).Decode(&rule)
		if err != nil {
			continue
		}
		for _, kw := range rule.Keywords {
			kwMap[kw] += ra.Count
		}
	}

	var results []models.KeywordCount
	for kw, count := range kwMap {
		results = append(results, models.KeywordCount{Keyword: kw, Count: count})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Count > results[j].Count })
	if len(results) > 10 {
		results = results[:10]
	}
	if results == nil {
		results = []models.KeywordCount{}
	}
	return results
}

// GetEngagementReport fetches engagement data from the Instagram Graph API.
// GET /api/v1/admin/instagram/analytics/engagement
func GetEngagementReport(w http.ResponseWriter, r *http.Request) {
	_, creds, ok := requireInstagramCreds(w, r)
	if !ok {
		return
	}

	// Fetch user profile for followers count
	followersCount := fetchFollowersCount(creds.AccountID, creds.Token)

	// Fetch recent media with insights
	posts := fetchMediaWithInsights(creds.AccountID, creds.Token, followersCount)

	// Compute averages
	var totalLikes, totalComments int64
	for _, p := range posts {
		totalLikes += p.LikeCount
		totalComments += p.CommentsCount
	}

	var avgLikes, avgComments, avgEngRate float64
	if len(posts) > 0 {
		avgLikes = float64(totalLikes) / float64(len(posts))
		avgComments = float64(totalComments) / float64(len(posts))
		var engSum float64
		for _, p := range posts {
			engSum += p.EngagementRate
		}
		avgEngRate = engSum / float64(len(posts))
	}

	// Best posting hours
	bestHours := computeBestHours(posts)

	json.NewEncoder(w).Encode(models.EngagementReport{
		TotalPosts:        len(posts),
		AvgLikes:          avgLikes,
		AvgComments:       avgComments,
		AvgEngagementRate: avgEngRate,
		FollowersCount:    followersCount,
		Posts:             posts,
		BestPostingHours:  bestHours,
	})
}

func fetchFollowersCount(accountID, token string) int64 {
	url := fmt.Sprintf("https://graph.facebook.com/v21.0/%s?fields=followers_count&access_token=%s", accountID, token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	var result struct {
		FollowersCount int64 `json:"followers_count"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.FollowersCount
}

func fetchMediaWithInsights(accountID, token string, followers int64) []models.PostEngagement {
	url := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/media?fields=id,caption,media_url,media_type,like_count,comments_count,timestamp&limit=25&access_token=%s", accountID, token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var result struct {
		Data []struct {
			ID            string `json:"id"`
			Caption       string `json:"caption"`
			MediaURL      string `json:"media_url"`
			MediaType     string `json:"media_type"`
			LikeCount     int64  `json:"like_count"`
			CommentsCount int64  `json:"comments_count"`
			Timestamp     string `json:"timestamp"`
		} `json:"data"`
	}
	json.Unmarshal(body, &result)

	var posts []models.PostEngagement
	for _, d := range result.Data {
		var engRate float64
		if followers > 0 {
			engRate = float64(d.LikeCount+d.CommentsCount) / float64(followers) * 100
		}
		posts = append(posts, models.PostEngagement{
			ID:             d.ID,
			Caption:        d.Caption,
			MediaURL:       d.MediaURL,
			MediaType:      d.MediaType,
			LikeCount:      d.LikeCount,
			CommentsCount:  d.CommentsCount,
			EngagementRate: engRate,
			Timestamp:      d.Timestamp,
		})
	}
	if posts == nil {
		posts = []models.PostEngagement{}
	}
	return posts
}

func computeBestHours(posts []models.PostEngagement) []models.PostingHourStat {
	type hourAcc struct {
		totalEng float64
		count    int
	}
	hourMap := map[int]*hourAcc{}

	for _, p := range posts {
		t, err := time.Parse(time.RFC3339, p.Timestamp)
		if err != nil {
			continue
		}
		h := t.Hour()
		if _, ok := hourMap[h]; !ok {
			hourMap[h] = &hourAcc{}
		}
		hourMap[h].totalEng += p.EngagementRate
		hourMap[h].count++
	}

	var stats []models.PostingHourStat
	for h, acc := range hourMap {
		stats = append(stats, models.PostingHourStat{
			Hour:          h,
			AvgEngagement: acc.totalEng / float64(acc.count),
			PostCount:     acc.count,
		})
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].AvgEngagement > stats[j].AvgEngagement })
	if stats == nil {
		stats = []models.PostingHourStat{}
	}
	return stats
}

// mergeFilter merges two bson.M filters.
func mergeFilter(base, extra bson.M) bson.M {
	merged := bson.M{}
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	return merged
}
