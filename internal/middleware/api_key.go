package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
)

// APIKeyHeader carries an org API key (see models.APIKey).
const APIKeyHeader = "X-Api-Key"

// APIKeyPrefix starts every key, so leaked keys are easy to spot in scanners.
const APIKeyPrefix = "tl_"

// HashAPIKey is the only form of a key that is ever stored.
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// APIKeyOrAuth accepts either a Bearer JWT (normal session) or an org API key
// holding the given scope. A valid key acts as the member who created it, in the
// key's org, so the RequireOrg/RequirePlan/RequirePermission chain that follows
// still applies (membership removed = key stops working).
func APIKeyOrAuth(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		jwtChain := Auth(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := strings.TrimSpace(r.Header.Get(APIKeyHeader))
			if key == "" {
				jwtChain.ServeHTTP(w, r)
				return
			}
			if !strings.HasPrefix(key, APIKeyPrefix) {
				IncAuthError()
				http.Error(w, "Invalid API key", http.StatusUnauthorized)
				return
			}

			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()

			var apiKey models.APIKey
			err := database.APIKeys().FindOne(ctx, bson.M{
				"key_hash":   HashAPIKey(key),
				"revoked_at": bson.M{"$exists": false},
			}).Decode(&apiKey)
			if err != nil {
				IncAuthError()
				http.Error(w, "Invalid API key", http.StatusUnauthorized)
				return
			}
			if !hasScope(apiKey.Scopes, scope) {
				http.Error(w, "API key lacks scope "+scope, http.StatusForbidden)
				return
			}

			touchAPIKey(apiKey)

			reqCtx := context.WithValue(r.Context(), UserIDKey, apiKey.UserID)
			reqCtx = context.WithValue(reqCtx, orgIDClaimKey, apiKey.OrgID.Hex())
			next.ServeHTTP(w, r.WithContext(reqCtx))
		})
	}
}

func hasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

// touchAPIKey records usage at most once a minute, off the request path.
func touchAPIKey(k models.APIKey) {
	now := time.Now()
	if k.LastUsedAt != nil && now.Sub(*k.LastUsedAt) < time.Minute {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		database.APIKeys().UpdateOne(ctx, bson.M{"_id": k.ID}, bson.M{"$set": bson.M{"last_used_at": now}})
	}()
}
