package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// RoleContextKey is the context key for the user's role
type roleContextKey string

const UserRoleKey roleContextKey = "userRole"

// GetUserRole extracts the user role from request context
func GetUserRole(r *http.Request) string {
	role, _ := r.Context().Value(UserRoleKey).(string)
	return role
}

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
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{
					"message": "Unauthorized: user not identified",
				})
				return
			}

			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()

			var profile models.Profile
			err := database.Profiles().FindOne(ctx, bson.M{"user_id": userID}).Decode(&profile)
			if err != nil {
				slog.Warn("role_check_failed",
					"reason", "profile_not_found",
					"user_id", userID.Hex(),
					"error", err.Error(),
				)
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{
					"message": "Profile not found for this user",
				})
				return
			}

			if !allowed[profile.Role] {
				slog.Warn("role_check_failed",
					"reason", "insufficient_role",
					"user_id", userID.Hex(),
					"current_role", profile.Role,
					"required_roles", strings.Join(roles, ", "),
				)
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{
					"message":        "Forbidden: insufficient permissions",
					"current_role":   profile.Role,
					"required_roles": strings.Join(roles, ", "),
				})
				return
			}

			// Inject role into context for downstream handlers
			ctx2 := context.WithValue(r.Context(), UserRoleKey, profile.Role)
			next.ServeHTTP(w, r.WithContext(ctx2))
		})
	}
}
