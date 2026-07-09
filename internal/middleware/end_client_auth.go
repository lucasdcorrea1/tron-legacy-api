package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/tron-legacy/api/internal/config"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	endClientIDKey    contextKey = "endClientID"
	endClientOrgIDKey contextKey = "endClientOrgID"

	// EndClientTokenSubject identifies portal session tokens.
	// Tokens signed with a different subject MUST NOT be accepted by EndClientAuth.
	EndClientTokenSubject = "end_client_session"

	// ContaAzulOAuthStateSubject identifies the short-lived state used in the
	// Conta Azul OAuth redirect.
	ContaAzulOAuthStateSubject = "ca_oauth_state"
)

// EndClientClaims are JWT claims for portal sessions.
type EndClientClaims struct {
	EndClientID string `json:"ec_id"`
	OrgID       string `json:"org_id"`
	Email       string `json:"email,omitempty"`
	jwt.RegisteredClaims
}

// EndClientAuth validates an EndClient portal session token.
func EndClientAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Authorization header required", http.StatusUnauthorized)
			return
		}
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(w, "Invalid authorization format", http.StatusUnauthorized)
			return
		}

		claims := &EndClientClaims{}
		token, err := jwt.ParseWithClaims(parts[1], claims, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return []byte(config.Get().JWTSecret), nil
		})
		if err != nil || !token.Valid {
			http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
			return
		}
		// Hard isolation from admin user tokens: reject any token whose subject
		// is not exactly the portal session marker.
		if claims.Subject != EndClientTokenSubject {
			http.Error(w, "Invalid token subject", http.StatusUnauthorized)
			return
		}

		ecID, err := primitive.ObjectIDFromHex(claims.EndClientID)
		if err != nil {
			http.Error(w, "Invalid end_client ID in token", http.StatusUnauthorized)
			return
		}
		orgID, err := primitive.ObjectIDFromHex(claims.OrgID)
		if err != nil {
			http.Error(w, "Invalid org_id in token", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), endClientIDKey, ecID)
		ctx = context.WithValue(ctx, endClientOrgIDKey, orgID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetEndClientID extracts the EndClient ID from request context.
func GetEndClientID(r *http.Request) primitive.ObjectID {
	id, ok := r.Context().Value(endClientIDKey).(primitive.ObjectID)
	if !ok {
		return primitive.NilObjectID
	}
	return id
}

// GetEndClientOrgID extracts the EndClient's org ID from request context.
func GetEndClientOrgID(r *http.Request) primitive.ObjectID {
	id, ok := r.Context().Value(endClientOrgIDKey).(primitive.ObjectID)
	if !ok {
		return primitive.NilObjectID
	}
	return id
}
