package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/tron-legacy/api/internal/crypto"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	contaazul "github.com/tron-legacy/api/internal/services/conta_azul"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ── Helpers ────────────────────────────────────────────────────────

// loadCAClient finds the authenticated EndClient, decrypts its OAuth tokens
// and returns a ready-to-use Conta Azul client. It also returns a callback
// that persists any refreshed tokens back to MongoDB — handlers MUST call it
// (typically with defer) so a mid-call refresh doesn't get lost.
func loadCAClient(r *http.Request) (*contaazul.Client, *models.EndClient, func(), error) {
	if !crypto.Available() {
		return nil, nil, nil, errors.New("encryption not initialized")
	}
	ecID := middleware.GetEndClientID(r)
	if ecID == primitive.NilObjectID {
		return nil, nil, nil, errors.New("unauthorized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var ec models.EndClient
	if err := database.EndClients().FindOne(ctx, bson.M{"_id": ecID}).Decode(&ec); err != nil {
		return nil, nil, nil, errors.New("end client not found")
	}
	if ec.ContaAzulConn == nil || ec.ContaAzulConn.AccessTokenEnc == "" {
		return nil, nil, nil, errors.New("conta azul not connected")
	}

	access, err := crypto.Decrypt(ec.ContaAzulConn.AccessTokenEnc)
	if err != nil {
		return nil, nil, nil, errors.New("failed to decrypt access token")
	}
	refresh := ""
	if ec.ContaAzulConn.RefreshTokenEnc != "" {
		if r, err := crypto.Decrypt(ec.ContaAzulConn.RefreshTokenEnc); err == nil {
			refresh = r
		}
	}

	client := contaazul.New(access, refresh, ec.ContaAzulConn.ExpiresAt)

	persistIfRefreshed := func() {
		// If the tokens or expiry changed during the call, save the new state.
		if client.AccessToken() == access && client.ExpiresAt().Equal(ec.ContaAzulConn.ExpiresAt) {
			return
		}
		newAccessEnc, err := crypto.Encrypt(client.AccessToken())
		if err != nil {
			return
		}
		update := bson.M{
			"conta_azul_conn.access_token_enc": newAccessEnc,
			"conta_azul_conn.expires_at":       client.ExpiresAt(),
			"conta_azul_conn.last_sync_at":     time.Now(),
			"updated_at":                       time.Now(),
		}
		if client.RefreshToken() != refresh && client.RefreshToken() != "" {
			if enc, err := crypto.Encrypt(client.RefreshToken()); err == nil {
				update["conta_azul_conn.refresh_token_enc"] = enc
			}
		}
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		_, _ = database.EndClients().UpdateOne(ctx2, bson.M{"_id": ec.ID}, bson.M{"$set": update})
	}

	return client, &ec, persistIfRefreshed, nil
}

// parsePeriod reads ?from=YYYY-MM-DD&to=YYYY-MM-DD with sensible defaults
// (current month if missing).
func parsePeriod(r *http.Request) (time.Time, time.Time) {
	const layout = "2006-01-02"
	now := time.Now()
	defaultFrom := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	defaultTo := defaultFrom.AddDate(0, 1, 0).Add(-time.Second)

	q := r.URL.Query()
	from := defaultFrom
	to := defaultTo
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(layout, v); err == nil {
			from = t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(layout, v); err == nil {
			to = t
		}
	}
	return from, to
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

// hasDashboardAccess returns true if the EndClient has been granted the given
// widget by the org admin.
func hasDashboardAccess(ec *models.EndClient, key string) bool {
	for _, k := range ec.DashboardAccess {
		if k == key {
			return true
		}
	}
	return false
}

// ── Handlers ──────────────────────────────────────────────────────

// PortalDashboardSummary returns the headline numbers (revenue / expense / profit)
// for the requested period, plus open receivables/payables.
// GET /api/v1/portal/conta-azul/dashboard/summary?from=&to=&compare=true
func PortalDashboardSummary(w http.ResponseWriter, r *http.Request) {
	client, ec, persist, err := loadCAClient(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer persist()

	if !hasDashboardAccess(ec, "revenue") && !hasDashboardAccess(ec, "expense") && !hasDashboardAccess(ec, "profit") {
		writeErr(w, http.StatusForbidden, "Sem permissão para este widget")
		return
	}

	from, to := parsePeriod(r)
	withCompare, _ := strconv.ParseBool(r.URL.Query().Get("compare"))

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	s, err := client.BuildSummary(ctx, from, to, withCompare)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Falha ao consultar Conta Azul: "+err.Error())
		return
	}

	// Mask widgets the user shouldn't see (keep keys to preserve UI shape).
	if !hasDashboardAccess(ec, "revenue") {
		s.TotalRevenue = 0
		s.OpenReceivable = 0
	}
	if !hasDashboardAccess(ec, "expense") {
		s.TotalExpense = 0
		s.OpenPayable = 0
	}
	if !hasDashboardAccess(ec, "profit") {
		s.Profit = 0
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}

// PortalDashboardCashflow returns monthly cashflow points for [from, to].
// GET /api/v1/portal/conta-azul/dashboard/cashflow?from=&to=
func PortalDashboardCashflow(w http.ResponseWriter, r *http.Request) {
	client, ec, persist, err := loadCAClient(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer persist()
	if !hasDashboardAccess(ec, "cashflow") {
		writeErr(w, http.StatusForbidden, "Sem permissão para este widget")
		return
	}

	from, to := parsePeriod(r)
	// For cashflow default we want a wider window — last 6 months if both missing.
	if r.URL.Query().Get("from") == "" && r.URL.Query().Get("to") == "" {
		now := time.Now()
		to = time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location()).Add(-time.Second)
		from = time.Date(now.Year(), now.Month()-5, 1, 0, 0, 0, 0, now.Location())
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	pts, err := client.BuildCashflow(ctx, from, to)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Falha ao consultar Conta Azul: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"period_start": from,
		"period_end":   to,
		"points":       pts,
	})
}

// PortalDashboardCategories returns DRE category breakdown for the period.
// GET /api/v1/portal/conta-azul/dashboard/categories?from=&to=
func PortalDashboardCategories(w http.ResponseWriter, r *http.Request) {
	client, ec, persist, err := loadCAClient(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer persist()
	if !hasDashboardAccess(ec, "categories") {
		writeErr(w, http.StatusForbidden, "Sem permissão para este widget")
		return
	}

	from, to := parsePeriod(r)
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	items, err := client.BuildCategoryBreakdown(ctx, from, to)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Falha ao consultar Conta Azul: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"period_start": from,
		"period_end":   to,
		"items":        items,
	})
}

// PortalDashboardUpcoming returns next pending receivables/payables.
// GET /api/v1/portal/conta-azul/dashboard/upcoming?days=30
func PortalDashboardUpcoming(w http.ResponseWriter, r *http.Request) {
	client, ec, persist, err := loadCAClient(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer persist()
	if !hasDashboardAccess(ec, "upcoming") {
		writeErr(w, http.StatusForbidden, "Sem permissão para este widget")
		return
	}

	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d > 0 && d <= 365 {
			days = d
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	items, err := client.BuildUpcoming(ctx, days)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Falha ao consultar Conta Azul: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items": items,
		"days":  days,
	})
}
