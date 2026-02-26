package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// RequireRole returns a middleware that checks if the authenticated user
// has one of the allowed roles. Must be used after Auth middleware.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := GetUserID(r)
			if userID == primitive.NilObjectID {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()

			var profile models.Profile
			err := database.Profiles().FindOne(ctx, bson.M{"user_id": userID}).Decode(&profile)
			if err != nil {
				http.Error(w, "Profile not found", http.StatusForbidden)
				return
			}

			if !allowed[profile.Role] {
				http.Error(w, "Forbidden: insufficient permissions", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
