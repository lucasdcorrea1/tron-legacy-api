package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/config"
	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ══════════════════════════════════════════════════════════════════════
// ORG CRUD
// ══════════════════════════════════════════════════════════════════════

// CreateOrg godoc
// @Summary Criar nova organização
// @Description Cria uma nova organização e define o usuário autenticado como proprietário
// @Tags organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body models.CreateOrgRequest true "Dados da organização"
// @Success 201 {object} models.Organization
// @Failure 400 {string} string "Invalid request body"
// @Failure 401 {string} string "Unauthorized"
// @Failure 409 {object} map[string]string "Já existe uma empresa com este nome"
// @Router /orgs [post]
func CreateOrg(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req models.CreateOrgRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, "Organization name is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Prevent users from creating multiple orgs — each user gets one org at registration
	ownerCount, _ := database.OrgMemberships().CountDocuments(ctx, bson.M{
		"user_id":  userID,
		"org_role": "owner",
	})
	if ownerCount > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Você já possui uma empresa. Use as configurações para alterar o nome.",
		})
		return
	}

	// Block duplicate org names (case-insensitive)
	escapedName := regexp.QuoteMeta(req.Name)
	nameFilter := bson.M{"name": primitive.Regex{Pattern: "^" + escapedName + "$", Options: "i"}}
	if cnt, _ := database.Organizations().CountDocuments(ctx, nameFilter); cnt > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Já existe uma empresa com este nome. Peça um convite ao proprietário.",
		})
		return
	}

	slug := generateOrgSlug(req.Name)
	slug = ensureUniqueOrgSlug(ctx, slug)

	now := time.Now()
	org := models.Organization{
		ID:          primitive.NewObjectID(),
		Name:        req.Name,
		Slug:        slug,
		OwnerUserID: userID,
		Settings: models.OrgSettings{
			DefaultLanguage: "pt-BR",
			DefaultCurrency: "BRL",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	_, err := database.Organizations().InsertOne(ctx, org)
	if err != nil {
		slog.Error("create_org_error", "error", err)
		http.Error(w, "Error creating organization", http.StatusInternalServerError)
		return
	}

	// Create owner membership
	membership := models.OrgMembership{
		ID:       primitive.NewObjectID(),
		OrgID:    org.ID,
		UserID:   userID,
		OrgRole:  "owner",
		JoinedAt: now,
	}
	_, err = database.OrgMemberships().InsertOne(ctx, membership)
	if err != nil {
		database.Organizations().DeleteOne(ctx, bson.M{"_id": org.ID})
		slog.Error("create_org_membership_error", "error", err)
		http.Error(w, "Error creating organization membership", http.StatusInternalServerError)
		return
	}

	// Create free subscription
	sub := models.Subscription{
		ID:               primitive.NewObjectID(),
		OrgID:            org.ID,
		PlanID:           "free",
		Status:           "active",
		CurrentPeriodEnd: now.AddDate(100, 0, 0), // effectively never expires for free
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	database.Subscriptions().InsertOne(ctx, sub)

	slog.Info("org_created",
		"org_id", org.ID.Hex(),
		"name", org.Name,
		"owner", userID.Hex(),
	)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(org)
}

// ListOrgs godoc
// @Summary Listar organizações do usuário
// @Description Retorna todas as organizações das quais o usuário autenticado é membro
// @Tags organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} models.OrgListResponse
// @Failure 401 {string} string "Unauthorized"
// @Router /orgs [get]
func ListOrgs(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Find all memberships for this user
	cursor, err := database.OrgMemberships().Find(ctx, bson.M{"user_id": userID})
	if err != nil {
		http.Error(w, "Error listing organizations", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var memberships []models.OrgMembership
	if err := cursor.All(ctx, &memberships); err != nil {
		http.Error(w, "Error decoding memberships", http.StatusInternalServerError)
		return
	}

	var orgs []models.OrgResponse
	for _, m := range memberships {
		var org models.Organization
		err := database.Organizations().FindOne(ctx, bson.M{"_id": m.OrgID}).Decode(&org)
		if err != nil {
			continue
		}

		memberCount, _ := database.OrgMemberships().CountDocuments(ctx, bson.M{"org_id": m.OrgID})

		orgs = append(orgs, models.OrgResponse{
			Organization:  org,
			MemberCount:   int(memberCount),
			MyRole:        m.OrgRole,
			MyPermissions: m.Permissions,
		})
	}

	if orgs == nil {
		orgs = []models.OrgResponse{}
	}

	json.NewEncoder(w).Encode(models.OrgListResponse{Organizations: orgs})
}

// GetOrg godoc
// @Summary Detalhes da organização atual
// @Description Retorna informações detalhadas da organização selecionada
// @Tags organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} models.OrgResponse
// @Failure 404 {string} string "Organization not found"
// @Router /orgs/current [get]
func GetOrg(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	if orgID == primitive.NilObjectID {
		http.Error(w, "Organization not found", http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var org models.Organization
	err := database.Organizations().FindOne(ctx, bson.M{"_id": orgID}).Decode(&org)
	if err != nil {
		http.Error(w, "Organization not found", http.StatusNotFound)
		return
	}

	memberCount, _ := database.OrgMemberships().CountDocuments(ctx, bson.M{"org_id": orgID})

	json.NewEncoder(w).Encode(models.OrgResponse{
		Organization:  org,
		MemberCount:   int(memberCount),
		MyRole:        middleware.GetOrgRole(r),
		MyPermissions: middleware.GetOrgPermissions(r),
	})
}

// UpdateOrg godoc
// @Summary Atualizar organização atual
// @Description Atualiza os dados da organização selecionada
// @Tags organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body models.UpdateOrgRequest true "Dados para atualizar"
// @Success 200 {object} models.Organization
// @Failure 400 {string} string "Invalid request body"
// @Failure 500 {string} string "Error updating organization"
// @Router /orgs/current [put]
func UpdateOrg(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	var req models.UpdateOrgRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := bson.M{"updated_at": time.Now()}
	if req.Name != nil {
		update["name"] = *req.Name
	}
	if req.LogoURL != nil {
		update["logo_url"] = *req.LogoURL
	}
	if req.Settings != nil {
		update["settings"] = *req.Settings
	}

	_, err := database.Organizations().UpdateOne(ctx, bson.M{"_id": orgID}, bson.M{"$set": update})
	if err != nil {
		http.Error(w, "Error updating organization", http.StatusInternalServerError)
		return
	}

	var org models.Organization
	database.Organizations().FindOne(ctx, bson.M{"_id": orgID}).Decode(&org)
	json.NewEncoder(w).Encode(org)
}

// UploadOrgLogo handles logo image upload for the current organization.
func UploadOrgLogo(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	// 2MB limit
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		http.Error(w, "Image too large (max 2MB)", http.StatusRequestEntityTooLarge)
		return
	}

	file, header, err := r.FormFile("logo")
	if err != nil {
		http.Error(w, "No image provided", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ct := header.Header.Get("Content-Type")
	if ct != "image/jpeg" && ct != "image/png" && ct != "image/webp" && ct != "image/svg+xml" {
		http.Error(w, "Only JPEG, PNG, WebP and SVG images are allowed", http.StatusBadRequest)
		return
	}

	imgData, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Failed to read image", http.StatusBadRequest)
		return
	}

	// For raster images, resize to 128x128
	var logoURI string
	if ct == "image/svg+xml" {
		logoURI = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(imgData)
	} else {
		img, _, decErr := image.Decode(bytes.NewReader(imgData))
		if decErr != nil {
			http.Error(w, "Invalid image format", http.StatusBadRequest)
			return
		}
		resized := resizeImage(img, 128, 128)
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 85}); err != nil {
			http.Error(w, "Failed to process image", http.StatusInternalServerError)
			return
		}
		logoURI = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = database.Organizations().UpdateOne(ctx, bson.M{"_id": orgID}, bson.M{
		"$set": bson.M{"logo_url": logoURI, "updated_at": time.Now()},
	})
	if err != nil {
		http.Error(w, "Error saving logo", http.StatusInternalServerError)
		return
	}

	var org models.Organization
	database.Organizations().FindOne(ctx, bson.M{"_id": orgID}).Decode(&org)

	slog.Info("org_logo_uploaded", "org_id", orgID.Hex())
	json.NewEncoder(w).Encode(org)
}

// RemoveOrgLogo removes the logo for the current organization.
func RemoveOrgLogo(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := database.Organizations().UpdateOne(ctx, bson.M{"_id": orgID}, bson.M{
		"$set": bson.M{"logo_url": "", "updated_at": time.Now()},
	})
	if err != nil {
		http.Error(w, "Error removing logo", http.StatusInternalServerError)
		return
	}

	var org models.Organization
	database.Organizations().FindOne(ctx, bson.M{"_id": orgID}).Decode(&org)
	json.NewEncoder(w).Encode(org)
}

// DeleteOrg godoc
// @Summary Excluir organização atual
// @Description Exclui a organização e todos os membros. Somente o proprietário pode executar
// @Tags organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]string
// @Failure 403 {string} string "Only the organization owner can delete it"
// @Failure 404 {string} string "Organization not found"
// @Router /orgs/current [delete]
func DeleteOrg(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	userID := middleware.GetUserID(r)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Verify ownership
	var org models.Organization
	err := database.Organizations().FindOne(ctx, bson.M{"_id": orgID}).Decode(&org)
	if err != nil {
		http.Error(w, "Organization not found", http.StatusNotFound)
		return
	}
	if org.OwnerUserID != userID {
		http.Error(w, "Only the organization owner can delete it", http.StatusForbidden)
		return
	}

	// Delete memberships, invitations, subscription
	database.OrgMemberships().DeleteMany(ctx, bson.M{"org_id": orgID})
	database.OrgInvitations().DeleteMany(ctx, bson.M{"org_id": orgID})
	database.Subscriptions().DeleteOne(ctx, bson.M{"org_id": orgID})
	database.Organizations().DeleteOne(ctx, bson.M{"_id": orgID})

	slog.Info("org_deleted", "org_id", orgID.Hex(), "by", userID.Hex())
	json.NewEncoder(w).Encode(map[string]string{"message": "Organization deleted"})
}

// ══════════════════════════════════════════════════════════════════════
// MEMBERS
// ══════════════════════════════════════════════════════════════════════

// ListMembers godoc
// @Summary Listar membros da organização
// @Description Retorna todos os membros e convites pendentes da organização atual
// @Tags organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} models.MemberListResponse
// @Failure 500 {string} string "Error listing members"
// @Router /orgs/current/members [get]
func ListMembers(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get memberships
	cursor, err := database.OrgMemberships().Find(ctx, bson.M{"org_id": orgID})
	if err != nil {
		http.Error(w, "Error listing members", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var memberships []models.OrgMembership
	cursor.All(ctx, &memberships)

	var members []models.MemberResponse
	for _, m := range memberships {
		var user models.User
		database.Users().FindOne(ctx, bson.M{"_id": m.UserID}).Decode(&user)
		var profile models.Profile
		database.Profiles().FindOne(ctx, bson.M{"user_id": m.UserID}).Decode(&profile)

		members = append(members, models.MemberResponse{
			UserID:      m.UserID,
			Name:        profile.Name,
			Email:       user.Email,
			Avatar:      profile.Avatar,
			OrgRole:     m.OrgRole,
			Permissions: m.Permissions,
			JoinedAt:    m.JoinedAt,
		})
	}

	// Get pending invitations
	invCursor, err := database.OrgInvitations().Find(ctx, bson.M{
		"org_id": orgID,
		"status": "pending",
	})
	if err != nil {
		http.Error(w, "Error listing invitations", http.StatusInternalServerError)
		return
	}
	defer invCursor.Close(ctx)

	var invitations []models.OrgInvitation
	invCursor.All(ctx, &invitations)

	if members == nil {
		members = []models.MemberResponse{}
	}
	if invitations == nil {
		invitations = []models.OrgInvitation{}
	}

	json.NewEncoder(w).Encode(models.MemberListResponse{
		Members:     members,
		Invitations: invitations,
	})
}

// UpdateMemberRole godoc
// @Summary Alterar papel de um membro
// @Description Altera o papel (role) de um membro dentro da organização
// @Tags organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param uid path string true "ID do usuário"
// @Param request body models.UpdateMemberRoleRequest true "Novo papel"
// @Success 200 {object} map[string]string
// @Failure 400 {string} string "Invalid user ID"
// @Failure 403 {string} string "Only the owner can promote to admin"
// @Failure 404 {string} string "Member not found"
// @Router /orgs/current/members/{uid}/role [put]
func UpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	actorRole := middleware.GetOrgRole(r)

	targetUID, err := primitive.ObjectIDFromHex(r.PathValue("uid"))
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	var req models.UpdateMemberRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	validRoles := map[string]bool{"admin": true, "member": true, "viewer": true}
	if !validRoles[req.OrgRole] {
		http.Error(w, "Invalid role. Must be admin, member, or viewer", http.StatusBadRequest)
		return
	}

	// Only owner can promote to admin; admins can set member/viewer
	if req.OrgRole == "admin" && actorRole != "owner" {
		http.Error(w, "Only the owner can promote to admin", http.StatusForbidden)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Cannot change owner's role
	var targetMembership models.OrgMembership
	err = database.OrgMemberships().FindOne(ctx, bson.M{
		"org_id":  orgID,
		"user_id": targetUID,
	}).Decode(&targetMembership)
	if err != nil {
		http.Error(w, "Member not found", http.StatusNotFound)
		return
	}
	if targetMembership.OrgRole == "owner" {
		http.Error(w, "Cannot change the owner's role", http.StatusForbidden)
		return
	}

	_, err = database.OrgMemberships().UpdateOne(ctx,
		bson.M{"org_id": orgID, "user_id": targetUID},
		bson.M{"$set": bson.M{"org_role": req.OrgRole}},
	)
	if err != nil {
		http.Error(w, "Error updating role", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"message": "Role updated"})
}

// UpdateMemberPermissions godoc
// @Summary Atualizar permissões de um membro
// @Description Define as permissões granulares de um membro. Somente owner/admin pode executar
// @Tags organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param uid path string true "ID do usuário"
// @Param request body models.UpdateMemberPermissionsRequest true "Lista de permissões"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {string} string "Invalid user ID"
// @Failure 404 {string} string "Member not found"
// @Router /orgs/current/members/{uid}/permissions [put]
func UpdateMemberPermissions(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	targetUID, err := primitive.ObjectIDFromHex(r.PathValue("uid"))
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	var req models.UpdateMemberPermissionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate all permissions
	var validPerms []string
	for _, p := range req.Permissions {
		if !models.ValidPermission(p) {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"message": "Invalid permission: " + p,
			})
			return
		}
		validPerms = append(validPerms, p)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Verify target exists and is a member
	var targetMembership models.OrgMembership
	err = database.OrgMemberships().FindOne(ctx, bson.M{
		"org_id":  orgID,
		"user_id": targetUID,
	}).Decode(&targetMembership)
	if err != nil {
		http.Error(w, "Member not found", http.StatusNotFound)
		return
	}

	// Cannot change permissions for owner/admin (they have all permissions implicitly)
	if targetMembership.OrgRole == "owner" || targetMembership.OrgRole == "admin" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Cannot set permissions for owner or admin roles (they have all permissions)",
		})
		return
	}

	_, err = database.OrgMemberships().UpdateOne(ctx,
		bson.M{"org_id": orgID, "user_id": targetUID},
		bson.M{"$set": bson.M{"permissions": validPerms}},
	)
	if err != nil {
		http.Error(w, "Error updating permissions", http.StatusInternalServerError)
		return
	}

	slog.Info("member_permissions_updated",
		"org_id", orgID.Hex(),
		"target_user_id", targetUID.Hex(),
		"permissions", strings.Join(validPerms, ", "),
	)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":     "Permissions updated",
		"permissions": validPerms,
	})
}

// RemoveMember godoc
// @Summary Remover membro da organização
// @Description Remove um membro da organização. Membros podem se remover; admins/owners podem remover qualquer um
// @Tags organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param uid path string true "ID do usuário"
// @Success 200 {object} map[string]string
// @Failure 403 {string} string "Cannot remove the organization owner"
// @Failure 404 {string} string "Member not found"
// @Router /orgs/current/members/{uid} [delete]
func RemoveMember(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	actorID := middleware.GetUserID(r)

	targetUID, err := primitive.ObjectIDFromHex(r.PathValue("uid"))
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Cannot remove the owner
	var org models.Organization
	database.Organizations().FindOne(ctx, bson.M{"_id": orgID}).Decode(&org)
	if org.OwnerUserID == targetUID {
		http.Error(w, "Cannot remove the organization owner", http.StatusForbidden)
		return
	}

	// Members can remove themselves; admins/owners can remove anyone
	if targetUID != actorID {
		actorRole := middleware.GetOrgRole(r)
		if actorRole != "owner" && actorRole != "admin" {
			http.Error(w, "Insufficient permissions", http.StatusForbidden)
			return
		}
	}

	result, err := database.OrgMemberships().DeleteOne(ctx, bson.M{
		"org_id":  orgID,
		"user_id": targetUID,
	})
	if err != nil || result.DeletedCount == 0 {
		http.Error(w, "Member not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"message": "Member removed"})
}

// ══════════════════════════════════════════════════════════════════════
// INVITATIONS
// ══════════════════════════════════════════════════════════════════════

// InviteMember godoc
// @Summary Convidar membro para a organização
// @Description Envia um convite por email para ingressar na organização
// @Tags organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body models.InviteMemberRequest true "Dados do convite"
// @Success 201 {object} models.OrgInvitation
// @Failure 400 {string} string "Email is required"
// @Failure 402 {object} map[string]string "Member limit reached"
// @Failure 409 {string} string "User is already a member"
// @Router /orgs/current/invitations [post]
func InviteMember(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	userID := middleware.GetUserID(r)

	var req models.InviteMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Email == "" {
		http.Error(w, "Email is required", http.StatusBadRequest)
		return
	}

	validRoles := map[string]bool{"admin": true, "member": true, "viewer": true}
	if !validRoles[req.OrgRole] {
		http.Error(w, "Invalid role", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Check plan limit for members
	memberCount, _ := database.OrgMemberships().CountDocuments(ctx, bson.M{"org_id": orgID})
	pendingCount, _ := database.OrgInvitations().CountDocuments(ctx, bson.M{
		"org_id": orgID,
		"status": "pending",
	})

	var sub models.Subscription
	database.Subscriptions().FindOne(ctx, bson.M{"org_id": orgID}).Decode(&sub)
	limits := models.Plans[sub.PlanID]
	if limits.MaxMembers > 0 && int(memberCount+pendingCount) >= limits.MaxMembers {
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Member limit reached for your plan. Please upgrade.",
		})
		return
	}

	// Check if user is already a member
	var existingUser models.User
	err := database.Users().FindOne(ctx, bson.M{"email": req.Email}).Decode(&existingUser)
	if err == nil {
		count, _ := database.OrgMemberships().CountDocuments(ctx, bson.M{
			"org_id":  orgID,
			"user_id": existingUser.ID,
		})
		if count > 0 {
			http.Error(w, "User is already a member of this organization", http.StatusConflict)
			return
		}
	}

	// Check for existing pending invitation
	count, _ := database.OrgInvitations().CountDocuments(ctx, bson.M{
		"org_id": orgID,
		"email":  req.Email,
		"status": "pending",
	})
	if count > 0 {
		http.Error(w, "An invitation is already pending for this email", http.StatusConflict)
		return
	}

	// Validate permissions if provided
	var validPerms []string
	if req.OrgRole == "member" && len(req.Permissions) > 0 {
		for _, p := range req.Permissions {
			if models.ValidPermission(p) {
				validPerms = append(validPerms, p)
			}
		}
	}

	// Generate invitation token
	tokenBytes := make([]byte, 32)
	rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)

	invitation := models.OrgInvitation{
		ID:          primitive.NewObjectID(),
		OrgID:       orgID,
		Email:       req.Email,
		OrgRole:     req.OrgRole,
		Permissions: validPerms,
		Token:       token,
		Status:      "pending",
		InvitedBy:   userID,
		ExpiresAt:   time.Now().Add(7 * 24 * time.Hour), // 7 days
		CreatedAt:   time.Now(),
	}

	_, err = database.OrgInvitations().InsertOne(ctx, invitation)
	if err != nil {
		http.Error(w, "Error creating invitation", http.StatusInternalServerError)
		return
	}

	// Send invitation email — synchronous so we can return errors to the user
	var org models.Organization
	orgName := "a organização"
	if err := database.Organizations().FindOne(ctx, bson.M{"_id": orgID}).Decode(&org); err == nil {
		orgName = org.Name
	}
	if emailErr := sendInvitationEmail(req.Email, orgName, invitation.OrgRole, token); emailErr != nil {
		// Roll back the invitation since the email failed
		database.OrgInvitations().DeleteOne(ctx, bson.M{"_id": invitation.ID})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Falha ao enviar email do convite: " + emailErr.Error(),
		})
		return
	}

	slog.Info("org_invitation_sent",
		"org_id", orgID.Hex(),
		"email", req.Email,
		"role", req.OrgRole,
	)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(invitation)
}

// ResendInvitation godoc
// @Summary Reenviar convite
// @Description Reenvia o email de um convite pendente, gerando novo token e estendendo a expiração
// @Tags organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "ID do convite"
// @Success 200 {object} map[string]string
// @Failure 404 {string} string "Invitation not found"
// @Router /orgs/current/invitations/{id}/resend [post]
func ResendInvitation(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	idHex := r.PathValue("id")
	invID, err := primitive.ObjectIDFromHex(idHex)
	if err != nil {
		http.Error(w, "Invalid invitation ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Find the invitation (must belong to current org and still be pending)
	var invitation models.OrgInvitation
	err = database.OrgInvitations().FindOne(ctx, bson.M{
		"_id":    invID,
		"org_id": orgID,
		"status": "pending",
	}).Decode(&invitation)
	if err != nil {
		http.Error(w, "Convite não encontrado ou já aceito", http.StatusNotFound)
		return
	}

	// Generate a new token + extend expiration
	tokenBytes := make([]byte, 32)
	rand.Read(tokenBytes)
	newToken := hex.EncodeToString(tokenBytes)
	newExpiry := time.Now().Add(7 * 24 * time.Hour)

	_, err = database.OrgInvitations().UpdateOne(ctx,
		bson.M{"_id": invID},
		bson.M{"$set": bson.M{
			"token":      newToken,
			"expires_at": newExpiry,
		}},
	)
	if err != nil {
		http.Error(w, "Erro ao atualizar convite", http.StatusInternalServerError)
		return
	}

	// Resend email — synchronous to surface errors
	var org models.Organization
	orgName := "a organização"
	if err := database.Organizations().FindOne(ctx, bson.M{"_id": orgID}).Decode(&org); err == nil {
		orgName = org.Name
	}
	if emailErr := sendInvitationEmail(invitation.Email, orgName, invitation.OrgRole, newToken); emailErr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Falha ao reenviar email: " + emailErr.Error(),
		})
		return
	}

	slog.Info("org_invitation_resent",
		"org_id", orgID.Hex(),
		"invitation_id", invID.Hex(),
		"email", invitation.Email,
	)

	json.NewEncoder(w).Encode(map[string]string{"message": "Convite reenviado"})
}

// CancelInvitation godoc
// @Summary Cancelar convite pendente
// @Description Remove um convite pendente
// @Tags organizations
// @Security BearerAuth
// @Param id path string true "ID do convite"
// @Success 200 {object} map[string]string
// @Router /orgs/current/invitations/{id} [delete]
func CancelInvitation(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	idHex := r.PathValue("id")
	invID, err := primitive.ObjectIDFromHex(idHex)
	if err != nil {
		http.Error(w, "Invalid invitation ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := database.OrgInvitations().DeleteOne(ctx, bson.M{
		"_id":    invID,
		"org_id": orgID,
		"status": "pending",
	})
	if err != nil {
		http.Error(w, "Erro ao cancelar convite", http.StatusInternalServerError)
		return
	}
	if result.DeletedCount == 0 {
		http.Error(w, "Convite não encontrado", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"message": "Convite cancelado"})
}

// MyInvitations godoc
// @Summary Listar meus convites pendentes
// @Description Retorna todos os convites pendentes para o email do usuário autenticado
// @Tags organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {array} models.OrgInvitation
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "User not found"
// @Router /invitations/mine [get]
func MyInvitations(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var user models.User
	if err := database.Users().FindOne(ctx, bson.M{"_id": userID}).Decode(&user); err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	cursor, err := database.OrgInvitations().Find(ctx, bson.M{
		"email":  user.Email,
		"status": "pending",
	})
	if err != nil {
		http.Error(w, "Error fetching invitations", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var invitations []models.OrgInvitation
	cursor.All(ctx, &invitations)
	if invitations == nil {
		invitations = []models.OrgInvitation{}
	}

	// Enrich with org names
	type invWithOrg struct {
		models.OrgInvitation
		OrgName string `json:"org_name"`
	}
	var result []invWithOrg
	for _, inv := range invitations {
		name := ""
		var org models.Organization
		if err := database.Organizations().FindOne(ctx, bson.M{"_id": inv.OrgID}).Decode(&org); err == nil {
			name = org.Name
		}
		result = append(result, invWithOrg{OrgInvitation: inv, OrgName: name})
	}
	if result == nil {
		result = []invWithOrg{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// AcceptInvitation godoc
// @Summary Aceitar convite de organização
// @Description Aceita um convite pendente e adiciona o usuário à organização
// @Tags organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "ID do convite"
// @Success 200 {object} map[string]string
// @Failure 400 {string} string "Invalid invitation ID"
// @Failure 401 {string} string "Unauthorized"
// @Failure 403 {string} string "This invitation was sent to a different email address"
// @Failure 404 {string} string "Invalid or expired invitation"
// @Failure 409 {string} string "You are already a member"
// @Router /invitations/{id}/accept [post]
func AcceptInvitation(w http.ResponseWriter, r *http.Request) {
	invID := r.PathValue("id")
	if invID == "" {
		http.Error(w, "Invitation ID is required", http.StatusBadRequest)
		return
	}
	objID, err := primitive.ObjectIDFromHex(invID)
	if err != nil {
		http.Error(w, "Invalid invitation ID", http.StatusBadRequest)
		return
	}

	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Find invitation
	var invitation models.OrgInvitation
	err = database.OrgInvitations().FindOne(ctx, bson.M{
		"_id":    objID,
		"status": "pending",
	}).Decode(&invitation)
	if err != nil {
		http.Error(w, "Invalid or expired invitation", http.StatusNotFound)
		return
	}

	// Verify the logged-in user's email matches the invitation
	var user models.User
	err = database.Users().FindOne(ctx, bson.M{"_id": userID}).Decode(&user)
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}
	if user.Email != invitation.Email {
		http.Error(w, "This invitation was sent to a different email address", http.StatusForbidden)
		return
	}

	// Check if already a member
	count, _ := database.OrgMemberships().CountDocuments(ctx, bson.M{
		"org_id":  invitation.OrgID,
		"user_id": userID,
	})
	if count > 0 {
		// Mark invitation as accepted anyway
		database.OrgInvitations().UpdateOne(ctx,
			bson.M{"_id": invitation.ID},
			bson.M{"$set": bson.M{"status": "accepted"}},
		)
		http.Error(w, "You are already a member of this organization", http.StatusConflict)
		return
	}

	// Create membership (copy permissions from invitation)
	membership := models.OrgMembership{
		ID:          primitive.NewObjectID(),
		OrgID:       invitation.OrgID,
		UserID:      userID,
		OrgRole:     invitation.OrgRole,
		Permissions: invitation.Permissions,
		JoinedAt:    time.Now(),
	}
	_, err = database.OrgMemberships().InsertOne(ctx, membership)
	if err != nil {
		http.Error(w, "Error joining organization", http.StatusInternalServerError)
		return
	}

	// Mark invitation as accepted
	database.OrgInvitations().UpdateOne(ctx,
		bson.M{"_id": invitation.ID},
		bson.M{"$set": bson.M{"status": "accepted"}},
	)

	slog.Info("org_invitation_accepted",
		"org_id", invitation.OrgID.Hex(),
		"user_id", userID.Hex(),
		"role", invitation.OrgRole,
	)

	json.NewEncoder(w).Encode(map[string]string{
		"message": "Invitation accepted",
		"org_id":  invitation.OrgID.Hex(),
	})
}

// AcceptInvitationByToken godoc
// @Summary Aceitar convite por token (público)
// @Description Aceita convite via link do email, sem necessidade de login
// @Tags organizations
// @Accept json
// @Produce json
// @Param token path string true "Token do convite"
// @Success 200 {object} map[string]string
// @Failure 400 {string} string "Token is required"
// @Failure 404 {string} string "Invalid or expired invitation"
// @Router /invitations/accept-token/{token} [post]
func AcceptInvitationByToken(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, `{"message":"Token is required"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Find invitation by token
	var invitation models.OrgInvitation
	err := database.OrgInvitations().FindOne(ctx, bson.M{
		"token": token,
	}).Decode(&invitation)
	if err != nil {
		http.Error(w, `{"message":"Convite não encontrado"}`, http.StatusNotFound)
		return
	}

	// Get org name for response
	orgName := ""
	var org models.Organization
	if err := database.Organizations().FindOne(ctx, bson.M{"_id": invitation.OrgID}).Decode(&org); err == nil {
		orgName = org.Name
	}

	// Already accepted?
	if invitation.Status == "accepted" {
		json.NewEncoder(w).Encode(map[string]string{
			"message":  "Convite já foi aceito anteriormente",
			"status":   "already_accepted",
			"org_name": orgName,
		})
		return
	}

	// Expired?
	if invitation.Status != "pending" && invitation.Status != "pre_accepted" {
		http.Error(w, `{"message":"Este convite não está mais disponível"}`, http.StatusGone)
		return
	}
	if time.Now().After(invitation.ExpiresAt) {
		database.OrgInvitations().UpdateOne(ctx,
			bson.M{"_id": invitation.ID},
			bson.M{"$set": bson.M{"status": "expired"}},
		)
		http.Error(w, `{"message":"Este convite expirou. Solicite um novo convite."}`, http.StatusGone)
		return
	}

	// Check if user with this email already exists
	var user models.User
	err = database.Users().FindOne(ctx, bson.M{"email": invitation.Email}).Decode(&user)
	if err != nil {
		// User doesn't exist yet — mark as pre_accepted
		database.OrgInvitations().UpdateOne(ctx,
			bson.M{"_id": invitation.ID},
			bson.M{"$set": bson.M{"status": "pre_accepted"}},
		)

		slog.Info("org_invitation_pre_accepted",
			"email", invitation.Email,
			"org_id", invitation.OrgID.Hex(),
		)

		json.NewEncoder(w).Encode(map[string]string{
			"message":  "Convite aceito! Faça login ou cadastre-se para acessar.",
			"status":   "pre_accepted",
			"org_name": orgName,
		})
		return
	}

	// User exists — check if already a member
	count, _ := database.OrgMemberships().CountDocuments(ctx, bson.M{
		"org_id":  invitation.OrgID,
		"user_id": user.ID,
	})
	if count > 0 {
		database.OrgInvitations().UpdateOne(ctx,
			bson.M{"_id": invitation.ID},
			bson.M{"$set": bson.M{"status": "accepted"}},
		)
		json.NewEncoder(w).Encode(map[string]string{
			"message":  "Você já faz parte desta organização!",
			"status":   "already_member",
			"org_name": orgName,
		})
		return
	}

	// Create membership
	membership := models.OrgMembership{
		ID:          primitive.NewObjectID(),
		OrgID:       invitation.OrgID,
		UserID:      user.ID,
		OrgRole:     invitation.OrgRole,
		Permissions: invitation.Permissions,
		JoinedAt:    time.Now(),
	}
	_, err = database.OrgMemberships().InsertOne(ctx, membership)
	if err != nil {
		http.Error(w, `{"message":"Erro ao entrar na organização"}`, http.StatusInternalServerError)
		return
	}

	// Mark invitation as accepted
	database.OrgInvitations().UpdateOne(ctx,
		bson.M{"_id": invitation.ID},
		bson.M{"$set": bson.M{"status": "accepted"}},
	)

	slog.Info("org_invitation_accepted_by_token",
		"org_id", invitation.OrgID.Hex(),
		"user_id", user.ID.Hex(),
		"email", invitation.Email,
	)

	json.NewEncoder(w).Encode(map[string]string{
		"message":  "Convite aceito! Faça login para acessar.",
		"status":   "accepted",
		"org_name": orgName,
	})
}

// AutoAcceptInvitations checks for pre-accepted invitations for a user's email
// and creates org memberships. Called during login/register.
func AutoAcceptInvitations(ctx context.Context, userID primitive.ObjectID, email string) {
	cursor, err := database.OrgInvitations().Find(ctx, bson.M{
		"email":  email,
		"status": "pre_accepted",
	})
	if err != nil {
		return
	}
	defer cursor.Close(ctx)

	var invitations []models.OrgInvitation
	if err := cursor.All(ctx, &invitations); err != nil {
		return
	}

	for _, inv := range invitations {
		// Check not already a member
		count, _ := database.OrgMemberships().CountDocuments(ctx, bson.M{
			"org_id":  inv.OrgID,
			"user_id": userID,
		})
		if count > 0 {
			database.OrgInvitations().UpdateOne(ctx,
				bson.M{"_id": inv.ID},
				bson.M{"$set": bson.M{"status": "accepted"}},
			)
			continue
		}

		membership := models.OrgMembership{
			ID:          primitive.NewObjectID(),
			OrgID:       inv.OrgID,
			UserID:      userID,
			OrgRole:     inv.OrgRole,
			Permissions: inv.Permissions,
			JoinedAt:    time.Now(),
		}
		if _, err := database.OrgMemberships().InsertOne(ctx, membership); err == nil {
			database.OrgInvitations().UpdateOne(ctx,
				bson.M{"_id": inv.ID},
				bson.M{"$set": bson.M{"status": "accepted"}},
			)
			slog.Info("auto_accepted_invitation",
				"user_id", userID.Hex(),
				"org_id", inv.OrgID.Hex(),
				"email", email,
			)
		}
	}
}

// sendInvitationEmail sends the invitation email via Resend.
func sendInvitationEmail(email, orgName, role, token string) error {
	cfg := config.Get()
	if cfg.ResendAPIKey == "" {
		slog.Error("invite_email: RESEND_API_KEY not configured")
		return fmt.Errorf("serviço de email não configurado")
	}
	if cfg.FromEmail == "" {
		slog.Error("invite_email: FROM_EMAIL not configured")
		return fmt.Errorf("FROM_EMAIL não configurado")
	}

	rolePT := map[string]string{
		"admin":  "Administrador",
		"member": "Membro",
		"viewer": "Visualizador",
	}[role]
	if rolePT == "" {
		rolePT = "Membro"
	}

	inviteURL := fmt.Sprintf("%s/invite/%s", cfg.FrontendURL, token)
	emailHTML := buildInvitationEmailHTML(orgName, rolePT, inviteURL)

	resendBody := map[string]interface{}{
		"from":    cfg.FromEmail,
		"to":      []string{email},
		"subject": fmt.Sprintf("Convite para %s - Whodo", orgName),
		"html":    emailHTML,
	}

	bodyBytes, _ := json.Marshal(resendBody)

	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.ResendAPIKey)

	resp, err := client.Do(req)
	if err != nil {
		slog.Error("invite_email: resend call failed", "error", err, "email", email)
		return fmt.Errorf("falha ao chamar Resend: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		slog.Info("invite_email: sent", "email", email, "org", orgName)
		return nil
	}

	slog.Error("invite_email: resend error",
		"status", resp.StatusCode,
		"body", string(respBody),
		"to", email,
		"from", cfg.FromEmail,
	)
	return fmt.Errorf("Resend retornou status %d: %s", resp.StatusCode, string(respBody))
}

func buildInvitationEmailHTML(orgName, role, inviteURL string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="pt-BR">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="margin:0;padding:0;background-color:#09090b;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">
  <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="background-color:#09090b;padding:40px 20px;">
    <tr>
      <td align="center">
        <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="max-width:520px;background:linear-gradient(145deg,rgba(255,255,255,0.03),rgba(255,255,255,0.01));border:1px solid rgba(255,255,255,0.06);border-radius:16px;overflow:hidden;">
          <!-- Header -->
          <tr>
            <td style="padding:32px 40px 0;text-align:center;">
              <h1 style="margin:0;font-size:28px;font-weight:700;color:#a855f7;">Whodo</h1>
            </td>
          </tr>

          <!-- Divider -->
          <tr>
            <td style="padding:20px 40px;">
              <div style="height:1px;background:linear-gradient(90deg,transparent,rgba(168,85,247,0.3),transparent);"></div>
            </td>
          </tr>

          <!-- Content -->
          <tr>
            <td style="padding:0 40px;">
              <h2 style="margin:0 0 8px;font-size:20px;font-weight:600;color:#fafafa;">Você foi convidado!</h2>
              <p style="margin:0 0 24px;font-size:15px;color:#d4d4d8;line-height:1.6;">
                Você recebeu um convite para fazer parte da equipe <strong style="color:#fafafa;">%s</strong> como <strong style="color:#a855f7;">%s</strong>.
              </p>
              <p style="margin:0 0 24px;font-size:15px;color:#d4d4d8;line-height:1.6;">
                Clique no botão abaixo para aceitar o convite.
              </p>
            </td>
          </tr>

          <!-- Button -->
          <tr>
            <td style="padding:0 40px;" align="center">
              <a href="%s" target="_blank" style="display:inline-block;padding:14px 36px;background:linear-gradient(135deg,#a855f7,#6366f1);color:#ffffff;font-size:15px;font-weight:600;text-decoration:none;border-radius:10px;">
                Aceitar convite
              </a>
            </td>
          </tr>

          <!-- Expiry notice -->
          <tr>
            <td style="padding:24px 40px 0;">
              <p style="margin:0;font-size:13px;color:#a1a1aa;line-height:1.5;text-align:center;">
                Este convite expira em <strong style="color:#d4d4d8;">7 dias</strong>.
              </p>
            </td>
          </tr>

          <!-- Link fallback -->
          <tr>
            <td style="padding:20px 40px;">
              <div style="background:rgba(255,255,255,0.02);border:1px solid rgba(255,255,255,0.05);border-radius:8px;padding:12px 16px;">
                <p style="margin:0 0 4px;font-size:11px;color:#a1a1aa;">Se o botão não funcionar, copie e cole este link:</p>
                <p style="margin:0;font-size:12px;color:#a78bfa;word-break:break-all;">%s</p>
              </div>
            </td>
          </tr>

          <!-- Footer -->
          <tr>
            <td style="padding:20px 40px 32px;">
              <div style="height:1px;background:linear-gradient(90deg,transparent,rgba(255,255,255,0.05),transparent);margin-bottom:20px;"></div>
              <p style="margin:0;font-size:12px;color:#71717a;text-align:center;line-height:1.5;">
                &copy; %d Whodo Group LTDA<br>Todos os direitos reservados.
              </p>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`, orgName, role, inviteURL, inviteURL, time.Now().Year())
}

// ══════════════════════════════════════════════════════════════════════
// SWITCH ORG
// ══════════════════════════════════════════════════════════════════════

// SwitchOrg godoc
// @Summary Trocar de organização
// @Description Gera um novo JWT com o org_id especificado
// @Tags organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "ID da organização"
// @Success 200 {object} map[string]string
// @Failure 400 {string} string "Invalid organization ID"
// @Failure 401 {string} string "Unauthorized"
// @Failure 403 {string} string "You are not a member of this organization"
// @Router /orgs/switch/{id} [post]
func SwitchOrg(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	targetOrgID, err := primitive.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		http.Error(w, "Invalid organization ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Verify membership
	var membership models.OrgMembership
	err = database.OrgMemberships().FindOne(ctx, bson.M{
		"org_id":  targetOrgID,
		"user_id": userID,
	}).Decode(&membership)
	if err != nil {
		http.Error(w, "You are not a member of this organization", http.StatusForbidden)
		return
	}

	// Get user for token generation
	var user models.User
	database.Users().FindOne(ctx, bson.M{"_id": userID}).Decode(&user)

	// Generate new token with this org
	token, err := GenerateTokenWithOrg(user, targetOrgID.Hex())
	if err != nil {
		http.Error(w, "Error generating token", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"token":  token,
		"org_id": targetOrgID.Hex(),
	})
}

// ══════════════════════════════════════════════════════════════════════
// SUBSCRIPTION / USAGE
// ══════════════════════════════════════════════════════════════════════

// GetSubscription godoc
// @Summary Obter assinatura da organização
// @Description Retorna o plano atual da organização
// @Tags organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} models.Subscription
// @Failure 404 {string} string "Subscription not found"
// @Router /orgs/current/subscription [get]
func GetSubscription(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var sub models.Subscription
	err := database.Subscriptions().FindOne(ctx, bson.M{"org_id": orgID}).Decode(&sub)
	if err != nil {
		http.Error(w, "Subscription not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(sub)
}

// GetUsage godoc
// @Summary Obter uso de recursos da organização
// @Description Retorna o uso atual de recursos comparado aos limites do plano
// @Tags organizations
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} models.UsageResponse
// @Failure 404 {string} string "Subscription not found"
// @Router /orgs/current/usage [get]
func GetUsage(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var sub models.Subscription
	err := database.Subscriptions().FindOne(ctx, bson.M{"org_id": orgID}).Decode(&sub)
	if err != nil {
		http.Error(w, "Subscription not found", http.StatusNotFound)
		return
	}

	limits := models.Plans[sub.PlanID]

	// Count current usage
	members, _ := database.OrgMemberships().CountDocuments(ctx, bson.M{"org_id": orgID})
	schedules, _ := database.InstagramSchedules().CountDocuments(ctx, bson.M{"org_id": orgID, "status": "scheduled"})
	autoReply, _ := database.AutoReplyRules().CountDocuments(ctx, bson.M{"org_id": orgID, "active": true})
	autoBoost, _ := database.AutoBoostRules().CountDocuments(ctx, bson.M{"org_id": orgID, "active": true})
	alerts, _ := database.MetaAdsBudgetAlerts().CountDocuments(ctx, bson.M{"org_id": orgID, "active": true})
	campaigns, _ := database.MetaAdsCampaigns().CountDocuments(ctx, bson.M{"org_id": orgID})
	integrated, _ := database.IntegratedPublishes().CountDocuments(ctx, bson.M{"org_id": orgID, "status": "scheduled"})

	json.NewEncoder(w).Encode(models.UsageResponse{
		PlanID: sub.PlanID,
		Limits: limits,
		Usage: models.PlanUsage{
			Members:        int(members),
			ScheduledPosts: int(schedules),
			AutoReplyRules: int(autoReply),
			AutoBoostRules: int(autoBoost),
			BudgetAlerts:   int(alerts),
			Campaigns:      int(campaigns),
			IntegratedPubs: int(integrated),
		},
	})
}

// ══════════════════════════════════════════════════════════════════════
// HELPERS
// ══════════════════════════════════════════════════════════════════════

// CreateOrgForUser creates an organization for a user during registration.
// Returns the org ID. Used internally by the Register handler.
func CreateOrgForUser(ctx context.Context, userID primitive.ObjectID, userName string) (primitive.ObjectID, error) {
	slug := generateOrgSlug(userName)
	slug = ensureUniqueOrgSlug(ctx, slug)

	now := time.Now()
	org := models.Organization{
		ID:          primitive.NewObjectID(),
		Name:        userName,
		Slug:        slug,
		OwnerUserID: userID,
		Settings: models.OrgSettings{
			DefaultLanguage: "pt-BR",
			DefaultCurrency: "BRL",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	_, err := database.Organizations().InsertOne(ctx, org)
	if err != nil {
		return primitive.NilObjectID, err
	}

	membership := models.OrgMembership{
		ID:       primitive.NewObjectID(),
		OrgID:    org.ID,
		UserID:   userID,
		OrgRole:  "owner",
		JoinedAt: now,
	}
	_, err = database.OrgMemberships().InsertOne(ctx, membership)
	if err != nil {
		database.Organizations().DeleteOne(ctx, bson.M{"_id": org.ID})
		return primitive.NilObjectID, err
	}

	// Create free subscription
	sub := models.Subscription{
		ID:               primitive.NewObjectID(),
		OrgID:            org.ID,
		PlanID:           "free",
		Status:           "active",
		CurrentPeriodEnd: now.AddDate(100, 0, 0),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	database.Subscriptions().InsertOne(ctx, sub)

	return org.ID, nil
}

// GetDefaultOrgForUser returns the first org the user belongs to.
func GetDefaultOrgForUser(ctx context.Context, userID primitive.ObjectID) (primitive.ObjectID, error) {
	var membership models.OrgMembership
	opts := options.FindOne().SetSort(bson.D{{Key: "joined_at", Value: 1}})
	err := database.OrgMemberships().FindOne(ctx, bson.M{"user_id": userID}, opts).Decode(&membership)
	if err != nil {
		return primitive.NilObjectID, err
	}
	return membership.OrgID, nil
}

var orgNonAlphaNum = regexp.MustCompile(`[^a-z0-9-]+`)
var orgMultiDash = regexp.MustCompile(`-{2,}`)

func generateOrgSlug(name string) string {
	slug := strings.ToLower(name)
	// Transliterate common accented characters
	replacer := strings.NewReplacer(
		"á", "a", "à", "a", "ã", "a", "â", "a",
		"é", "e", "ê", "e", "í", "i", "ó", "o",
		"ô", "o", "õ", "o", "ú", "u", "ç", "c",
	)
	slug = replacer.Replace(slug)
	// Remove non-alphanumeric (keep dashes)
	slug = orgNonAlphaNum.ReplaceAllString(slug, "-")
	slug = orgMultiDash.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "org"
	}
	// Truncate to reasonable length
	runes := []rune(slug)
	if len(runes) > 50 {
		slug = string(runes[:50])
	}
	return slug
}

func ensureUniqueOrgSlug(ctx context.Context, slug string) string {
	candidate := slug
	for i := 1; ; i++ {
		count, _ := database.Organizations().CountDocuments(ctx, bson.M{"slug": candidate})
		if count == 0 {
			return candidate
		}
		// Generate random suffix
		b := make([]byte, 3)
		rand.Read(b)
		suffix := hex.EncodeToString(b)
		candidate = slug + "-" + suffix
	}
}

