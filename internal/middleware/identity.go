package middleware

import (
	"context"
	"net/http"
)

// identityHolder is a per-request mailbox the access-log middleware injects
// BEFORE the handler chain runs, so that downstream Auth/RequireOrg can record
// who the request belonged to. Context values set downstream don't propagate
// back up to the outer middleware, so we hand it a pointer to fill instead.
//
// Single goroutine per request, written then read in sequence — no locking.
type identityHolder struct {
	userID string
	orgID  string
}

type identityKeyType struct{}

var identityKey = identityKeyType{}

// withIdentity attaches a fresh holder to the request context.
func withIdentity(r *http.Request) (*http.Request, *identityHolder) {
	h := &identityHolder{}
	return r.WithContext(context.WithValue(r.Context(), identityKey, h)), h
}

// noteIdentity fills the holder (if present) with the user/org. Empty values are
// ignored so a later resolver (RequireOrg) can add the org after Auth set the user.
func noteIdentity(r *http.Request, userID, orgID string) {
	h, ok := r.Context().Value(identityKey).(*identityHolder)
	if !ok {
		return
	}
	if userID != "" {
		h.userID = userID
	}
	if orgID != "" {
		h.orgID = orgID
	}
}
