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

const OrgIDKey contextKey = "orgID"
const OrgRoleKey contextKey = "orgRole"
const OrgPermissionsKey contextKey = "orgPermissions"

// GetOrgID extracts the active organization ID from request context.
func GetOrgID(r *http.Request) primitive.ObjectID {
	orgID, ok := r.Context().Value(OrgIDKey).(primitive.ObjectID)
	if !ok {
		return primitive.NilObjectID
	}
	return orgID
}

// GetOrgRole extracts the user's org role from request context.
func GetOrgRole(r *http.Request) string {
	role, _ := r.Context().Value(OrgRoleKey).(string)
	return role
}

// GetOrgPermissions extracts the user's granular permissions from request context.
func GetOrgPermissions(r *http.Request) []string {
	perms, _ := r.Context().Value(OrgPermissionsKey).([]string)
	return perms
}

// RequireOrg validates that the JWT contains a valid org_id and that the user
// is a member of that organization. Injects orgID and orgRole into the context.
// Must be used after Auth middleware.
func RequireOrg(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := GetUserID(r)
		if userID == primitive.NilObjectID {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"message": "Unauthorized"})
			return
		}

		// Extract org_id from JWT claims in context
		orgIDStr, _ := r.Context().Value(orgIDClaimKey).(string)
		if orgIDStr == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"message": "No organization selected. Please switch to an organization."})
			return
		}

		orgID, err := primitive.ObjectIDFromHex(orgIDStr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"message": "Invalid organization ID in token"})
			return
		}

		// Verify membership
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		var membership models.OrgMembership
		err = database.OrgMemberships().FindOne(ctx, bson.M{
			"org_id":  orgID,
			"user_id": userID,
		}).Decode(&membership)
		if err != nil {
			slog.Warn("org_access_denied",
				"user_id", userID.Hex(),
				"org_id", orgIDStr,
				"error", err.Error(),
			)
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"message": "You are not a member of this organization"})
			return
		}

		// Inject org context
		ctx2 := context.WithValue(r.Context(), OrgIDKey, orgID)
		ctx2 = context.WithValue(ctx2, OrgRoleKey, membership.OrgRole)
		ctx2 = context.WithValue(ctx2, OrgPermissionsKey, membership.Permissions)
		next.ServeHTTP(w, r.WithContext(ctx2))
	})
}

// RequireOrgRole checks that the user's org role is one of the allowed roles.
// Must be used after RequireOrg middleware.
func RequireOrgRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			orgRole := GetOrgRole(r)
			if !allowed[orgRole] {
				slog.Warn("org_role_check_failed",
					"user_id", GetUserID(r).Hex(),
					"org_id", GetOrgID(r).Hex(),
					"current_role", orgRole,
					"required_roles", strings.Join(roles, ", "),
				)
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{
					"message":        "Forbidden: insufficient organization permissions",
					"current_role":   orgRole,
					"required_roles": strings.Join(roles, ", "),
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequirePlan checks that the org's subscription is active and meets the minimum plan.
// Blocks access immediately if subscription is canceled, past_due, or on a lower plan.
// Must be used after RequireOrg middleware.
func RequirePlan(minPlan string) func(http.Handler) http.Handler {
	planRank := map[string]int{"free": 0, "starter": 1, "pro": 2, "enterprise": 3}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			orgID := GetOrgID(r)
			if orgID == primitive.NilObjectID {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"message": "No organization selected"})
				return
			}

			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()

			var sub models.Subscription
			err := database.Subscriptions().FindOne(ctx, bson.M{"org_id": orgID}).Decode(&sub)
			if err != nil {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{"message": "Nenhuma assinatura encontrada"})
				return
			}

			// Treat non-active subscriptions as free
			effectivePlan := sub.PlanID
			if sub.Status != "active" {
				effectivePlan = "free"
			}

			// Check plan rank
			if (planRank[effectivePlan]) < (planRank[minPlan]) {
				slog.Warn("plan_check_failed",
					"org_id", orgID.Hex(),
					"current_plan", effectivePlan,
					"status", sub.Status,
					"required_plan", minPlan,
				)
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"message":       "Recurso requer plano " + minPlan + " ou superior",
					"current_plan":  effectivePlan,
					"status":        sub.Status,
					"required_plan": minPlan,
					"upgrade":       true,
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequirePermission checks that the user has a specific granular permission.
// Owner and Admin roles always pass. Members need the permission explicitly.
// Viewers are always blocked. Must be used after RequireOrg middleware.
func RequirePermission(perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			orgRole := GetOrgRole(r)

			// Owner and admin always have all permissions
			if orgRole == "owner" || orgRole == "admin" {
				next.ServeHTTP(w, r)
				return
			}

			// Viewer never has permissions
			if orgRole == "viewer" {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{
					"message":             "Forbidden: insufficient permissions",
					"required_permission": perm,
				})
				return
			}

			// Member: check explicit permission
			perms := GetOrgPermissions(r)
			for _, p := range perms {
				if p == perm {
					next.ServeHTTP(w, r)
					return
				}
			}

			slog.Warn("org_permission_check_failed",
				"user_id", GetUserID(r).Hex(),
				"org_id", GetOrgID(r).Hex(),
				"current_role", orgRole,
				"required_permission", perm,
			)
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{
				"message":             "Forbidden: insufficient permissions",
				"required_permission": perm,
			})
		})
	}
}
