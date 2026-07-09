package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ── Defaults ──────────────────────────────────────────────────────

// systemDefaultTheme is the absolute fallback used when nothing is
// configured at either the org or client level.
var systemDefaultTheme = models.PortalTheme{
	LogoURL:        "",
	BrandName:      "Painel Financeiro",
	PrimaryColor:   "#4f46e5",
	SuccessColor:   "#10b981",
	DangerColor:    "#ef4444",
	BgColor:        "#fafbff",
	SurfaceColor:   "#ffffff",
	TextColor:      "#0f172a",
	TextMutedColor: "#64748b",
}

// merge overlays src onto dst — non-empty fields in src win.
func mergeTheme(dst models.PortalTheme, src *models.PortalTheme) models.PortalTheme {
	if src == nil {
		return dst
	}
	if src.LogoURL != "" {
		dst.LogoURL = src.LogoURL
	}
	if src.BrandName != "" {
		dst.BrandName = src.BrandName
	}
	if src.PrimaryColor != "" {
		dst.PrimaryColor = src.PrimaryColor
	}
	if src.SuccessColor != "" {
		dst.SuccessColor = src.SuccessColor
	}
	if src.DangerColor != "" {
		dst.DangerColor = src.DangerColor
	}
	if src.BgColor != "" {
		dst.BgColor = src.BgColor
	}
	if src.SurfaceColor != "" {
		dst.SurfaceColor = src.SurfaceColor
	}
	if src.TextColor != "" {
		dst.TextColor = src.TextColor
	}
	if src.TextMutedColor != "" {
		dst.TextMutedColor = src.TextMutedColor
	}
	return dst
}

// sanitizeColor accepts only "" or strings starting with "#" of length 4 or 7.
// Lightweight protection against injection in CSS variables.
func sanitizeColor(c string) string {
	c = strings.TrimSpace(c)
	if c == "" {
		return ""
	}
	if !strings.HasPrefix(c, "#") {
		return ""
	}
	if len(c) != 4 && len(c) != 7 {
		return ""
	}
	for _, ch := range c[1:] {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return ""
		}
	}
	return c
}

func sanitizeTheme(t *models.PortalTheme) models.PortalTheme {
	if t == nil {
		return models.PortalTheme{}
	}
	return models.PortalTheme{
		LogoURL:        strings.TrimSpace(t.LogoURL),
		BrandName:      strings.TrimSpace(t.BrandName),
		PrimaryColor:   sanitizeColor(t.PrimaryColor),
		SuccessColor:   sanitizeColor(t.SuccessColor),
		DangerColor:    sanitizeColor(t.DangerColor),
		BgColor:        sanitizeColor(t.BgColor),
		SurfaceColor:   sanitizeColor(t.SurfaceColor),
		TextColor:      sanitizeColor(t.TextColor),
		TextMutedColor: sanitizeColor(t.TextMutedColor),
	}
}

// ── Admin: org-level default theme ─────────────────────────────────

// GetOrgPortalTheme returns the org's default Conta Azul portal theme.
// GET /api/v1/admin/conta-azul/portal-theme
func GetOrgPortalTheme(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	if orgID == primitive.NilObjectID {
		http.Error(w, "Organization context required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var org models.Organization
	if err := database.Organizations().FindOne(ctx, bson.M{"_id": orgID}).Decode(&org); err != nil {
		http.Error(w, "Organization not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if org.Settings.ContaAzulPortalTheme == nil {
		json.NewEncoder(w).Encode(models.PortalTheme{})
		return
	}
	json.NewEncoder(w).Encode(*org.Settings.ContaAzulPortalTheme)
}

// SetOrgPortalTheme saves the org-level default theme.
// PUT /api/v1/admin/conta-azul/portal-theme
func SetOrgPortalTheme(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	if orgID == primitive.NilObjectID {
		http.Error(w, "Organization context required", http.StatusBadRequest)
		return
	}
	var req models.PortalTheme
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	clean := sanitizeTheme(&req)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := database.Organizations().UpdateOne(ctx, bson.M{"_id": orgID}, bson.M{
		"$set": bson.M{
			"settings.conta_azul_portal_theme": clean,
			"updated_at":                       time.Now(),
		},
	})
	if err != nil {
		http.Error(w, "Erro ao salvar tema", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(clean)
}

// ── Admin: per-client theme override ───────────────────────────────

// SetEndClientTheme stores a theme override on a specific EndClient.
// PATCH /api/v1/admin/conta-azul/clients/{id}/theme
// Body: PortalTheme or null to clear the override.
func SetEndClientTheme(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	if orgID == primitive.NilObjectID {
		http.Error(w, "Organization context required", http.StatusBadRequest)
		return
	}
	idStr := r.PathValue("id")
	id, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	// Decode into a pointer so we can detect explicit null payload.
	var req *models.PortalTheme
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var update bson.M
	if req == nil {
		update = bson.M{
			"$unset": bson.M{"portal_theme": ""},
			"$set":   bson.M{"updated_at": time.Now()},
		}
	} else {
		clean := sanitizeTheme(req)
		update = bson.M{"$set": bson.M{"portal_theme": clean, "updated_at": time.Now()}}
	}

	res, err := database.EndClients().UpdateOne(ctx, bson.M{"_id": id, "org_id": orgID}, update)
	if err != nil {
		http.Error(w, "Erro ao atualizar tema", http.StatusInternalServerError)
		return
	}
	if res.MatchedCount == 0 {
		http.Error(w, "Cliente não encontrado", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ── Portal: resolved theme for the logged-in EndClient ─────────────

// GetPortalResolvedTheme returns the merged theme (system → org → client).
// GET /api/v1/portal/theme
func GetPortalResolvedTheme(w http.ResponseWriter, r *http.Request) {
	ecID := middleware.GetEndClientID(r)
	orgID := middleware.GetEndClientOrgID(r)
	if ecID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var org models.Organization
	_ = database.Organizations().FindOne(ctx, bson.M{"_id": orgID}).Decode(&org)

	var ec models.EndClient
	if err := database.EndClients().FindOne(ctx, bson.M{"_id": ecID}).Decode(&ec); err != nil {
		http.Error(w, "End client not found", http.StatusNotFound)
		return
	}

	resolved := systemDefaultTheme
	resolved = mergeTheme(resolved, org.Settings.ContaAzulPortalTheme)
	resolved = mergeTheme(resolved, ec.PortalTheme)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resolved)
}
