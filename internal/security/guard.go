// Package security holds the in-memory protection state (policy + active IP
// blocks) and the background writers for access logs and security events. It is
// the shared core used by the access-log / block-guard / rate-limit middleware
// and by the admin security handlers.
//
// The guard answers "is this IP blocked?" and "what are the current limits?"
// from memory, so the hot path never touches MongoDB — under a flood, one DB
// query per request would itself be the outage. State is refreshed from the DB
// on a short interval and immediately after an admin change.
package security

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Default is the process-wide guard. It is nil until Start is called; all call
// sites must tolerate a nil guard (fall back to no block / static limits).
var Default *Guard

// Guard keeps the active policy and block set in memory.
type Guard struct {
	mu       sync.RWMutex
	policy   models.SecurityPolicy
	blocked  map[string]time.Time // ip -> expiry (zero = permanent)
	rlHits   map[string]*counter  // ip -> recent rate-limit violations
	logCh    chan models.AccessLog
	dropped  int64
	droppedM sync.Mutex
}

type counter struct {
	count   int
	resetAt time.Time
}

// rlWindow is how long rate-limit violations are counted toward an auto-block.
const rlWindow = 10 * time.Minute

// Start initialises Default, loads state from the DB, and launches the reload
// loop and the access-log flusher. Safe to call once, from main, after the DB
// connection is up.
func Start() {
	g := &Guard{
		policy:  models.DefaultSecurityPolicy(),
		blocked: map[string]time.Time{},
		rlHits:  map[string]*counter{},
		logCh:   make(chan models.AccessLog, 4096),
	}
	Default = g
	g.reload()
	go g.reloadLoop()
	go g.flushLoop()
	log.Println("Security guard started (policy + block guard + access log)")
}

// Policy returns a copy of the current policy.
func (g *Guard) Policy() models.SecurityPolicy {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.policy
}

// Allows reports whether the IP is allowlisted (never limited/blocked).
func (g *Guard) Allows(ip string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.policy.Allows(ip)
}

// IsBlocked reports whether the IP is currently blocked. Allowlisted IPs are
// never blocked. Expired entries are treated as not blocked (cleaned up lazily
// by the reload loop and the Mongo TTL index).
func (g *Guard) IsBlocked(ip string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.policy.Allows(ip) {
		return false
	}
	exp, ok := g.blocked[ip]
	if !ok {
		return false
	}
	if !exp.IsZero() && time.Now().After(exp) {
		return false
	}
	return true
}

// NoteRateLimited records that the IP just hit a rate limit. When the count in
// the window crosses the policy threshold (and auto-block is on), the IP is
// blocked for the configured duration.
func (g *Guard) NoteRateLimited(ip string) {
	pol := g.Policy()
	if pol.Allows(ip) {
		return
	}
	g.RecordEvent(models.SecEventRateLimited, ip, "", "")
	if !pol.AutoBlockEnabled {
		return
	}

	now := time.Now()
	g.mu.Lock()
	c := g.rlHits[ip]
	if c == nil || now.After(c.resetAt) {
		c = &counter{count: 0, resetAt: now.Add(rlWindow)}
		g.rlHits[ip] = c
	}
	c.count++
	over := c.count >= pol.AutoBlockHits
	g.mu.Unlock()

	if over {
		g.autoBlock(ip, pol.AutoBlockMinutes)
	}
}

// autoBlock inserts (or refreshes) a timed block for the IP, in memory and in
// the DB, and records an event.
func (g *Guard) autoBlock(ip string, minutes int) {
	if g.Allows(ip) {
		return
	}
	expiry := time.Now().Add(time.Duration(minutes) * time.Minute)

	g.mu.Lock()
	if cur, ok := g.blocked[ip]; ok && !cur.IsZero() && cur.After(expiry) {
		// Already blocked for longer; keep the stronger block.
		g.mu.Unlock()
		return
	}
	g.blocked[ip] = expiry
	delete(g.rlHits, ip)
	g.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	block := models.Block{
		Kind:      "ip",
		Value:     ip,
		Reason:    "auto: repeated rate-limit violations",
		Auto:      true,
		CreatedAt: time.Now(),
		ExpiresAt: expiry,
	}
	_, err := database.Blocks().UpdateOne(ctx,
		bson.M{"kind": "ip", "value": ip},
		bson.M{"$set": block},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		log.Printf("security: autoblock upsert %s: %v", ip, err)
	}
	g.RecordEvent(models.SecEventAutoBlocked, ip, "", "repeated rate-limit violations")
	log.Printf("security: auto-blocked %s for %d min", ip, minutes)
}

// RecordEvent inserts a security event in the background (best-effort).
func (g *Guard) RecordEvent(eventType, ip, email, detail string) {
	ev := models.SecurityEvent{
		Type:      eventType,
		IP:        ip,
		Email:     email,
		Detail:    detail,
		CreatedAt: time.Now(),
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := database.SecurityEvents().InsertOne(ctx, ev); err != nil {
			log.Printf("security: record event: %v", err)
		}
	}()
}

// RecordAccess enqueues an access-log entry. Non-blocking: if the buffer is
// full the entry is dropped and counted (never slows a request).
func (g *Guard) RecordAccess(e models.AccessLog) {
	select {
	case g.logCh <- e:
	default:
		g.droppedM.Lock()
		g.dropped++
		g.droppedM.Unlock()
	}
}

// Dropped returns how many access-log entries were dropped due to a full buffer.
func (g *Guard) Dropped() int64 {
	g.droppedM.Lock()
	defer g.droppedM.Unlock()
	return g.dropped
}

func (g *Guard) flushLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	batch := make([]interface{}, 0, 256)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := database.AccessLogs().InsertMany(ctx, batch, options.InsertMany().SetOrdered(false))
		cancel()
		if err != nil {
			log.Printf("security: flush access logs: %v", err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case e := <-g.logCh:
			batch = append(batch, e)
			if len(batch) >= 200 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (g *Guard) reloadLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		g.reload()
	}
}

// reload refreshes policy and the active block set from the DB.
func (g *Guard) reload() {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	policy := models.DefaultSecurityPolicy()
	var stored models.SecurityPolicy
	err := database.SecuritySettings().FindOne(ctx, bson.M{"_id": "security"}).Decode(&stored)
	if err == nil {
		policy = stored
		policy.Normalize()
	} else if err != mongo.ErrNoDocuments {
		log.Printf("security: load policy: %v", err)
	}

	blocked := map[string]time.Time{}
	cur, err := database.Blocks().Find(ctx, bson.M{"kind": "ip"})
	if err == nil {
		var blocks []models.Block
		if err := cur.All(ctx, &blocks); err == nil {
			now := time.Now()
			for _, b := range blocks {
				if !b.ExpiresAt.IsZero() && now.After(b.ExpiresAt) {
					continue
				}
				blocked[b.Value] = b.ExpiresAt
			}
		}
	} else {
		log.Printf("security: load blocks: %v", err)
	}

	g.mu.Lock()
	g.policy = policy
	g.blocked = blocked
	g.mu.Unlock()
}

// Reload forces an immediate refresh (called after an admin change).
func (g *Guard) Reload() { g.reload() }
