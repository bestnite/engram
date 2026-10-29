package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the SPA JSON endpoint for the admin panel's "registration and invites".
//
// The key to taking effect without a restart is the same as SSR: the settings table is read per request (the registration flow calls LoadSettings every time), so after a DB write
// the next registration attempt is judged by the new policy immediately. Validation and DB writes call exactly the same store/auth functions as the SSR forms.

// adminInvite is an invite's raw fields; role/status are stored values whose copy is mapped by the frontend.
type adminInvite struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	Link      string `json:"link"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
	UsedAt    string `json:"used_at"`
	UsedBy    string `json:"used_by"`
}

// adminRegistrationResponse is the read result for the registration and invites page.
type adminRegistrationResponse struct {
	Policy       string        `json:"policy"`
	EmailDomains string        `json:"email_domains"`
	Invites      []adminInvite `json:"invites"`
}

// adminRegistrationRequest is the request body for saving the registration policy and allowlist.
type adminRegistrationRequest struct {
	Policy       string `json:"policy"`
	EmailDomains string `json:"email_domains"`
}

// adminInviteCreateRequest is the request body for creating an invite.
// ExpiresDays of nil or 0 means no expiry; negative values are rejected.
type adminInviteCreateRequest struct {
	Email       string `json:"email"`
	Role        string `json:"role"`
	ExpiresDays *int   `json:"expires_days"`
	SendEmail   bool   `json:"send_email"`
}

// adminInviteCreateResponse returns the new invite and the mail result code (empty string means not requested or not applicable).
type adminInviteCreateResponse struct {
	Invite     adminInvite `json:"invite"`
	MailNotice string      `json:"mail_notice"`
}

// adminInviteJSON converts an invite into the response structure; used_by is resolved to a username, falling back to the decimal id when that fails.
func (s *Server) adminInviteJSON(c *gin.Context, inv store.Invite, now time.Time) adminInvite {
	ctx := c.Request.Context()
	email := ""
	if inv.Email != nil {
		email = *inv.Email
	}
	usedBy := ""
	if inv.UsedBy != nil {
		if u, err := s.users.ByID(ctx, *inv.UsedBy); err == nil && u != nil {
			usedBy = u.Username
		}
	}
	expires := ""
	if inv.ExpiresAt != nil {
		expires = inv.ExpiresAt.UTC().Format("2006-01-02 15:04")
	}
	return adminInvite{
		ID:        inv.PublicID,
		Token:     inv.Token,
		Link:      "/register?invite=" + inv.Token,
		Email:     email,
		Role:      inv.Role,
		Status:    inviteStatus(inv, now),
		CreatedAt: inv.CreatedAt.UTC().Format("2006-01-02 15:04"),
		ExpiresAt: expires,
		UsedAt:    formatTimePtr(inv.UsedAt),
		UsedBy:    usedBy,
	}
}

// adminRegistration returns the current registration policy, allowlist and invite list.
func (s *Server) adminRegistration(c *gin.Context) {
	ctx := c.Request.Context()
	settings, err := store.LoadSettings(ctx, s.db)
	if err != nil {
		s.logger.Error("spa admin: load settings failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	resp := adminRegistrationResponse{
		Policy:       auth.ParseRegistrationPolicy(settings[auth.SettingKeyRegistrationPolicy]),
		EmailDomains: strings.TrimSpace(settings[auth.SettingKeyEmailAllowlist]),
		Invites:      []adminInvite{},
	}
	if s.invites != nil {
		invites, err := s.invites.List(ctx)
		if err != nil {
			s.logger.Error("spa admin: list invites failed", "error", err)
			adminError(c, http.StatusInternalServerError, "internal_error")
			return
		}
		now := time.Now().UTC()
		for _, inv := range invites {
			resp.Invites = append(resp.Invites, s.adminInviteJSON(c, inv, now))
		}
	}
	c.JSON(http.StatusOK, resp)
}

// adminRegistrationSave writes the registration policy and the email domain allowlist (both plain settings) and writes an audit row.
func (s *Server) adminRegistrationSave(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req adminRegistrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	policy := strings.TrimSpace(req.Policy)
	switch policy {
	case auth.PolicyOpen, auth.PolicyInvite, auth.PolicyClosed:
	default:
		adminError(c, http.StatusBadRequest, "invalid_policy")
		return
	}
	// The allowlist is normalized and stored comma-separated (ParseEmailAllowlist accepts that form); an empty string means no restriction.
	allowlist := strings.Join(auth.ParseEmailAllowlist(req.EmailDomains), ",")
	ctx := c.Request.Context()
	now := time.Now().UTC()

	if err := store.PutSetting(ctx, s.db, auth.SettingKeyRegistrationPolicy, policy, store.Ptr(actor.ID), now); err != nil {
		s.logger.Error("spa admin: save registration policy failed", "error", err)
		adminError(c, http.StatusInternalServerError, "save_failed")
		return
	}
	if err := store.PutSetting(ctx, s.db, auth.SettingKeyEmailAllowlist, allowlist, store.Ptr(actor.ID), now); err != nil {
		s.logger.Error("spa admin: save email allowlist failed", "error", err)
		adminError(c, http.StatusInternalServerError, "save_failed")
		return
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionSettingUpdate,
		TargetType: "setting",
		Detail:     map[string]any{"keys": []string{auth.SettingKeyRegistrationPolicy, auth.SettingKeyEmailAllowlist}, "policy": policy},
	})
	c.Status(http.StatusNoContent)
}

// adminInviteCreate creates an invite: optionally restricting the email, role and validity days, and optionally sending mail.
// No mail problem affects the creation result — mail_notice simply reports what happened.
func (s *Server) adminInviteCreate(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	if s.invites == nil {
		adminError(c, http.StatusInternalServerError, "invite_create_failed")
		return
	}
	var req adminInviteCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	role := strings.TrimSpace(req.Role)
	if role != store.RoleAdmin && role != store.RoleUser {
		adminError(c, http.StatusBadRequest, "invite_create_failed")
		return
	}
	inv := &store.Invite{Role: role, CreatedBy: store.Ptr(actor.ID), CreatedAt: time.Now().UTC()}
	if email := strings.ToLower(strings.TrimSpace(req.Email)); email != "" {
		inv.Email = &email
	}
	// Validity days: blank or 0 means no expiry; invalid values are rejected rather than silently treated as no expiry.
	if req.ExpiresDays != nil {
		if *req.ExpiresDays < 0 {
			adminError(c, http.StatusBadRequest, "invite_create_failed")
			return
		}
		if *req.ExpiresDays > 0 {
			exp := time.Now().UTC().AddDate(0, 0, *req.ExpiresDays)
			inv.ExpiresAt = &exp
		}
	}
	ctx := c.Request.Context()
	if err := s.invites.Create(ctx, inv); err != nil {
		s.logger.Error("spa admin: create invite failed", "error", err)
		adminError(c, http.StatusInternalServerError, "invite_create_failed")
		return
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionInviteCreate,
		TargetType: "invite", TargetID: store.Ptr(inv.ID),
		// The audit does not record the token in plaintext (it is equivalent to a credential).
		Detail: map[string]any{"role": inv.Role, "has_email": inv.Email != nil, "expires": inv.ExpiresAt != nil},
	})
	mailNotice := ""
	if req.SendEmail {
		loc, ok := s.localizer(c)
		if !ok {
			return
		}
		if code := s.sendInviteEmail(c, loc, inv); code != "" {
			mailNotice = code
		}
	}
	c.JSON(http.StatusCreated, adminInviteCreateResponse{
		Invite:     s.adminInviteJSON(c, *inv, time.Now().UTC()),
		MailNotice: mailNotice,
	})
}

// adminInviteRevoke revokes an invite (deletes the whole row) and writes an audit row.
func (s *Server) adminInviteRevoke(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	raw := strings.TrimSpace(c.Param("id"))
	if raw == "" || s.invites == nil {
		adminError(c, http.StatusNotFound, "invite_invalid")
		return
	}
	ctx := c.Request.Context()
	inv, err := s.invites.ByPublicID(ctx, raw)
	if err != nil {
		adminError(c, http.StatusNotFound, "invite_invalid")
		return
	}
	if err := s.invites.Revoke(ctx, inv.ID); err != nil {
		if errors.Is(err, store.ErrInviteNotFound) {
			adminError(c, http.StatusNotFound, "invite_invalid")
			return
		}
		s.logger.Error("spa admin: revoke invite failed", "invite_id", inv.ID, "error", err)
		adminError(c, http.StatusInternalServerError, "save_failed")
		return
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionInviteRevoke,
		TargetType: "invite", TargetID: store.Ptr(inv.ID),
	})
	c.Status(http.StatusNoContent)
}
