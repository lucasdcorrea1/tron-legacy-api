package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

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

// CreateOrg creates a new organization and makes the authenticated user the owner.
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

// ListOrgs lists all organizations the authenticated user is a member of.
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

// GetOrg returns details of a specific organization.
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

// UpdateOrg updates organization details.
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

// DeleteOrg deletes an organization and all memberships. Owner only.
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

// ListMembers lists all members and pending invitations for the org.
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

// UpdateMemberRole changes a member's role within the organization.
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

// UpdateMemberPermissions sets the granular permissions for a member.
// Only owner/admin can call this. Permissions only apply to "member" role.
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

// RemoveMember removes a member from the organization.
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

// InviteMember sends an invitation to join the organization.
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

	slog.Info("org_invitation_sent",
		"org_id", orgID.Hex(),
		"email", req.Email,
		"role", req.OrgRole,
	)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(invitation)
}

// AcceptInvitation accepts a pending invitation and adds the user to the organization.
func AcceptInvitation(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "Token is required", http.StatusBadRequest)
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
	err := database.OrgInvitations().FindOne(ctx, bson.M{
		"token":  token,
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

// ══════════════════════════════════════════════════════════════════════
// SWITCH ORG
// ══════════════════════════════════════════════════════════════════════

// SwitchOrg generates a new JWT with the specified org_id.
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

// GetSubscription returns the current plan for the organization.
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

// GetUsage returns current resource usage vs plan limits.
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

