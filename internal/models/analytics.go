package models

// AutoReplyAnalytics is the response for auto-reply metrics.
type AutoReplyAnalytics struct {
	TotalSent    int64               `json:"total_sent"`
	TotalFailed  int64               `json:"total_failed"`
	TotalSkipped int64               `json:"total_skipped"`
	SuccessRate  float64             `json:"success_rate"`
	TopRules     []RuleCount         `json:"top_rules"`
	HourlyDist   []HourlyCount       `json:"hourly_distribution"`
	DailyTrend   []DailyTrend        `json:"daily_trend"`
	TopKeywords  []KeywordCount      `json:"top_keywords"`
}

// RuleCount counts how many times a rule was triggered.
type RuleCount struct {
	RuleName string `json:"rule_name" bson:"_id"`
	Count    int64  `json:"count" bson:"count"`
}

// HourlyCount counts events per hour of day.
type HourlyCount struct {
	Hour  int   `json:"hour" bson:"_id"`
	Count int64 `json:"count" bson:"count"`
}

// DailyTrend tracks sent/failed per day.
type DailyTrend struct {
	Date   string `json:"date" bson:"_id"`
	Sent   int64  `json:"sent" bson:"sent"`
	Failed int64  `json:"failed" bson:"failed"`
}

// KeywordCount tracks keyword frequency.
type KeywordCount struct {
	Keyword string `json:"keyword"`
	Count   int64  `json:"count"`
}

// EngagementReport is the response for Instagram post engagement.
type EngagementReport struct {
	TotalPosts        int                `json:"total_posts"`
	AvgLikes          float64            `json:"avg_likes"`
	AvgComments       float64            `json:"avg_comments"`
	AvgEngagementRate float64            `json:"avg_engagement_rate"`
	FollowersCount    int64              `json:"followers_count"`
	Posts             []PostEngagement   `json:"posts"`
	BestPostingHours  []PostingHourStat  `json:"best_posting_hours"`
}

// PostEngagement holds engagement data for a single Instagram post.
type PostEngagement struct {
	ID             string  `json:"id"`
	Caption        string  `json:"caption"`
	MediaURL       string  `json:"media_url"`
	MediaType      string  `json:"media_type"`
	LikeCount      int64   `json:"like_count"`
	CommentsCount  int64   `json:"comments_count"`
	EngagementRate float64 `json:"engagement_rate"`
	Timestamp      string  `json:"timestamp"`
}

// PostingHourStat aggregates engagement by hour.
type PostingHourStat struct {
	Hour          int     `json:"hour"`
	AvgEngagement float64 `json:"avg_engagement"`
	PostCount     int     `json:"post_count"`
}
