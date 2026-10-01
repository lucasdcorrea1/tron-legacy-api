package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/models"
	"github.com/tron-legacy/api/internal/security"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ── Risk scoring (adapted from the ecommerce security module) ──────────
// Signals and thresholds are named constants so the score stays explainable:
// the admin sees which signal fired and the number behind it.

const (
	riskDenied   = "denied"   // high share of 401/403 (probing restricted areas)
	riskScraping = "scraping" // many 404s / distinct routes (scanning for holes)
	riskVelocity = "velocity" // request rate too high and/or hitting rate limits
)

const (
	riskFloor      = 30  // lowest score worth surfacing
	riskCandidates = 200 // busiest IPs scored per window
	riskListLimit  = 10  // how many risky clients the overview shows

	deniedMinCount = 5
	deniedMinRatio = 0.30
	deniedMaxPts   = 35

	scrapingMinNotFound = 10
	scrapingMinRoutes   = 25
	scrapingMaxPts      = 30

	velocityMinPerMin = 60
	velocityMaxPts    = 35
)

// ipAgg is one IP's aggregated access-log activity over the window.
type ipAgg struct {
	IP             string    `bson:"_id"`
	Total          int64     `bson:"total"`
	Unauthorized   int64     `bson:"unauthorized"`
	NotFound       int64     `bson:"not_found"`
	RateLimited    int64     `bson:"rate_limited"`
	DistinctRoutes int64     `bson:"distinct_routes"`
	FirstSeen      time.Time `bson:"first_seen"`
	LastSeen       time.Time `bson:"last_seen"`
}

func clampPts(pts, max int) int {
	if pts < 0 {
		return 0
	}
	if pts > max {
		return max
	}
	return pts
}

// scoreClient turns one IP's aggregates into a 0-100 risk score and the signals
// behind it.
func scoreClient(a ipAgg, windowMinutes float64) models.RiskClient {
	client := models.RiskClient{IP: a.IP, Requests: a.Total}
	score := 0

	if a.Total > 0 && a.Unauthorized >= deniedMinCount {
		ratio := float64(a.Unauthorized) / float64(a.Total)
		if ratio >= deniedMinRatio {
			score += clampPts(int(ratio*float64(deniedMaxPts)), deniedMaxPts)
			client.Signals = append(client.Signals, models.RiskSignal{Code: riskDenied, Value: a.Unauthorized})
		}
	}

	if a.NotFound >= scrapingMinNotFound || a.DistinctRoutes >= scrapingMinRoutes {
		pts := int(a.NotFound)
		if r := int(a.DistinctRoutes) - scrapingMinRoutes; r > pts {
			pts = r
		}
		score += clampPts(pts, scrapingMaxPts)
		value := a.NotFound
		if a.DistinctRoutes > value {
			value = a.DistinctRoutes
		}
		client.Signals = append(client.Signals, models.RiskSignal{Code: riskScraping, Value: value})
	}

	span := a.LastSeen.Sub(a.FirstSeen).Minutes()
	if span < 1 {
		span = 1
	}
	if windowMinutes > 0 && span > windowMinutes {
		span = windowMinutes
	}
	perMin := int64(float64(a.Total) / span)
	if a.RateLimited > 0 || perMin >= velocityMinPerMin {
		pts := 0
		if a.RateLimited > 0 {
			pts += 10 + int(a.RateLimited)
		}
		if perMin >= velocityMinPerMin {
			pts += int(perMin / velocityMinPerMin * 10)
		}
		score += clampPts(pts, velocityMaxPts)
		value := perMin
		if a.RateLimited > value {
			value = a.RateLimited
		}
		client.Signals = append(client.Signals, models.RiskSignal{Code: riskVelocity, Value: value})
	}

	if score > 100 {
		score = 100
	}
	client.Score = score
	return client
}

// riskClients scores the busiest IPs over the window and returns the riskiest,
// dropping allowlisted addresses and anything below the noise floor.
func riskClients(ctx context.Context, hours int) ([]models.RiskClient, error) {
	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
	windowMinutes := float64(hours) * 60

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"created_at": bson.M{"$gte": since}}}},
		{{Key: "$group", Value: bson.M{
			"_id":          "$ip",
			"total":        bson.M{"$sum": 1},
			"unauthorized": bson.M{"$sum": bson.M{"$cond": []interface{}{bson.M{"$in": []interface{}{"$status", []int{401, 403}}}, 1, 0}}},
			"not_found":    bson.M{"$sum": bson.M{"$cond": []interface{}{bson.M{"$eq": []interface{}{"$status", 404}}, 1, 0}}},
			"rate_limited": bson.M{"$sum": bson.M{"$cond": []interface{}{bson.M{"$eq": []interface{}{"$status", 429}}, 1, 0}}},
			"routes":       bson.M{"$addToSet": "$path"},
			"first_seen":   bson.M{"$min": "$created_at"},
			"last_seen":    bson.M{"$max": "$created_at"},
		}}},
		{{Key: "$project", Value: bson.M{
			"total": 1, "unauthorized": 1, "not_found": 1, "rate_limited": 1,
			"first_seen": 1, "last_seen": 1,
			"distinct_routes": bson.M{"$size": "$routes"},
		}}},
		{{Key: "$sort", Value: bson.M{"total": -1}}},
		{{Key: "$limit", Value: riskCandidates}},
	}

	cur, err := database.AccessLogs().Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	var aggs []ipAgg
	if err := cur.All(ctx, &aggs); err != nil {
		return nil, err
	}

	out := make([]models.RiskClient, 0, len(aggs))
	for _, a := range aggs {
		if a.IP == "" || (security.Default != nil && security.Default.Allows(a.IP)) {
			continue
		}
		c := scoreClient(a, windowMinutes)
		if c.Score < riskFloor {
			continue
		}
		if security.Default != nil {
			c.Blocked = security.Default.IsBlocked(a.IP)
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Requests > out[j].Requests
	})
	if len(out) > riskListLimit {
		out = out[:riskListLimit]
	}
	return out, nil
}

// ── Admin handlers (superadmin/superuser only) ─────────────────────────

func secWriteErr(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

// SecurityOverview returns high-level stats + the riskiest clients + recent
// blocks, for the Segurança dashboard.
// @Router /admin/security/overview [get]
func SecurityOverview(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		if v, err := strconv.Atoi(h); err == nil && v > 0 && v <= 720 {
			hours = v
		}
	}
	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)

	totalReq, _ := database.AccessLogs().CountDocuments(ctx, bson.M{"created_at": bson.M{"$gte": since}})
	uniqueIPs, _ := database.AccessLogs().Distinct(ctx, "ip", bson.M{"created_at": bson.M{"$gte": since}})
	rateLimited, _ := database.SecurityEvents().CountDocuments(ctx, bson.M{
		"type": models.SecEventRateLimited, "created_at": bson.M{"$gte": since},
	})
	activeBlocks, _ := database.Blocks().CountDocuments(ctx, bson.M{"kind": "ip"})

	risk, err := riskClients(ctx, hours)
	if err != nil {
		risk = []models.RiskClient{}
	}

	blocks := listBlocksData(ctx, 20)

	policy := models.DefaultSecurityPolicy()
	if security.Default != nil {
		policy = security.Default.Policy()
	}

	var dropped int64
	if security.Default != nil {
		dropped = security.Default.Dropped()
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"window_hours":  hours,
		"total_requests": totalReq,
		"unique_ips":    len(uniqueIPs),
		"rate_limited":  rateLimited,
		"active_blocks": activeBlocks,
		"dropped_logs":  dropped,
		"risk_clients":  risk,
		"recent_blocks": blocks,
		"policy":        policy,
	})
}

// SecurityAccessLogs returns recent access logs, newest first, with optional
// ip/status filters.
// @Router /admin/security/access-logs [get]
func SecurityAccessLogs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	q := r.URL.Query()
	limit := int64(100)
	if l := q.Get("limit"); l != "" {
		if v, err := strconv.ParseInt(l, 10, 64); err == nil && v > 0 && v <= 500 {
			limit = v
		}
	}
	filter := bson.M{}
	if ip := q.Get("ip"); ip != "" {
		filter["ip"] = ip
	}
	if s := q.Get("status"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			filter["status"] = v
		}
	}
	if q.Get("errors") == "true" {
		filter["status"] = bson.M{"$gte": 400}
	}
	if m := q.Get("method"); m != "" {
		filter["method"] = strings.ToUpper(m)
	}
	if p := q.Get("path"); p != "" {
		// Substring match on the route (admin-only endpoint; escape to avoid
		// treating user input as a regex).
		filter["path"] = bson.M{"$regex": regexp.QuoteMeta(p), "$options": "i"}
	}
	if o := q.Get("org_id"); o != "" {
		filter["org_id"] = o
	}

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(limit)
	cur, err := database.AccessLogs().Find(ctx, filter, opts)
	if err != nil {
		secWriteErr(w, http.StatusInternalServerError, "failed to list access logs")
		return
	}
	var logs []models.AccessLog
	if err := cur.All(ctx, &logs); err != nil {
		secWriteErr(w, http.StatusInternalServerError, "failed to decode access logs")
		return
	}
	if logs == nil {
		logs = []models.AccessLog{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"logs": logs})
}

// SecurityEvents returns recent security events, newest first.
// @Router /admin/security/events [get]
func SecurityEvents(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	limit := int64(100)
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.ParseInt(l, 10, 64); err == nil && v > 0 && v <= 500 {
			limit = v
		}
	}
	filter := bson.M{}
	if t := r.URL.Query().Get("type"); t != "" {
		filter["type"] = t
	}

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(limit)
	cur, err := database.SecurityEvents().Find(ctx, filter, opts)
	if err != nil {
		secWriteErr(w, http.StatusInternalServerError, "failed to list events")
		return
	}
	var events []models.SecurityEvent
	if err := cur.All(ctx, &events); err != nil {
		secWriteErr(w, http.StatusInternalServerError, "failed to decode events")
		return
	}
	if events == nil {
		events = []models.SecurityEvent{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"events": events})
}

// GetSecurityPolicy returns the current protection policy.
// @Router /admin/security/policy [get]
func GetSecurityPolicy(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	policy := models.DefaultSecurityPolicy()
	var stored models.SecurityPolicy
	err := database.SecuritySettings().FindOne(ctx, bson.M{"_id": "security"}).Decode(&stored)
	if err == nil {
		policy = stored
	}
	policy.Normalize()
	json.NewEncoder(w).Encode(policy)
}

// UpdateSecurityPolicy saves the protection policy and refreshes the guard.
// @Router /admin/security/policy [put]
func UpdateSecurityPolicy(w http.ResponseWriter, r *http.Request) {
	var policy models.SecurityPolicy
	if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
		secWriteErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	policy.Normalize()
	policy.UpdatedAt = time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	doc := bson.M{
		"global_per_min":     policy.GlobalPerMin,
		"auth_per_15min":     policy.AuthPer15Min,
		"auto_block_enabled": policy.AutoBlockEnabled,
		"auto_block_hits":    policy.AutoBlockHits,
		"auto_block_minutes": policy.AutoBlockMinutes,
		"allowlist":          policy.Allowlist,
		"updated_at":         policy.UpdatedAt,
	}
	_, err := database.SecuritySettings().UpdateOne(ctx,
		bson.M{"_id": "security"},
		bson.M{"$set": doc},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		secWriteErr(w, http.StatusInternalServerError, "failed to save policy")
		return
	}
	if security.Default != nil {
		security.Default.Reload()
	}
	json.NewEncoder(w).Encode(policy)
}

// listBlocksData loads active blocks (helper shared by overview + list).
func listBlocksData(ctx context.Context, limit int64) []models.Block {
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(limit)
	cur, err := database.Blocks().Find(ctx, bson.M{"kind": "ip"}, opts)
	if err != nil {
		return []models.Block{}
	}
	var blocks []models.Block
	if err := cur.All(ctx, &blocks); err != nil || blocks == nil {
		return []models.Block{}
	}
	return blocks
}

// ListBlocks returns the active IP blocks.
// @Router /admin/security/blocks [get]
func ListBlocks(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	json.NewEncoder(w).Encode(map[string]interface{}{"blocks": listBlocksData(ctx, 200)})
}

type createBlockRequest struct {
	IP      string `json:"ip"`
	Reason  string `json:"reason"`
	Minutes int    `json:"minutes"` // 0 = permanent
}

// CreateBlock adds a manual IP block. Allowlisted IPs cannot be blocked.
// @Router /admin/security/blocks [post]
func CreateBlock(w http.ResponseWriter, r *http.Request) {
	var req createBlockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IP == "" {
		secWriteErr(w, http.StatusBadRequest, "ip is required")
		return
	}
	if security.Default != nil && security.Default.Allows(req.IP) {
		secWriteErr(w, http.StatusConflict, "IP está na lista de confiança e não pode ser bloqueado")
		return
	}

	block := models.Block{
		Kind:      "ip",
		Value:     req.IP,
		Reason:    req.Reason,
		Auto:      false,
		CreatedAt: time.Now(),
	}
	if req.Minutes > 0 {
		block.ExpiresAt = time.Now().Add(time.Duration(req.Minutes) * time.Minute)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := database.Blocks().UpdateOne(ctx,
		bson.M{"kind": "ip", "value": req.IP},
		bson.M{"$set": block},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		secWriteErr(w, http.StatusInternalServerError, "failed to create block")
		return
	}
	if security.Default != nil {
		security.Default.RecordEvent(models.SecEventManualBlock, req.IP, "", req.Reason)
		security.Default.Reload()
	}
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(block)
}

// DeleteBlock removes an IP block.
// @Router /admin/security/blocks/{ip} [delete]
func DeleteBlock(w http.ResponseWriter, r *http.Request) {
	ip := r.PathValue("ip")
	if ip == "" {
		secWriteErr(w, http.StatusBadRequest, "ip is required")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := database.Blocks().DeleteOne(ctx, bson.M{"kind": "ip", "value": ip})
	if err != nil {
		secWriteErr(w, http.StatusInternalServerError, "failed to remove block")
		return
	}
	if security.Default != nil {
		security.Default.RecordEvent(models.SecEventManualUnblck, ip, "", "")
		security.Default.Reload()
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "removed"})
}
