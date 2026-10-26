package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the SPA settings API for a user's own external identities.
//
// Listing, binding and unlinking are all browser-session only (profileSessionOnly rejects bearer tokens and API keys):
// an identity is a sign-in credential, and a credential change must come from the account owner's browser session.
// Binding starts the same authorization-code + PKCE flow as login, but the pending entry records Intent=bind and the caller's user id,
// so the callback attaches the identity instead of opening a session.

// identityPayload is the public shape of one linked identity.
type identityPayload struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Email    string `json:"email"`
	LinkedAt string `json:"linked_at"`
}

// identityListResponse is the read result for the settings page.
type identityListResponse struct {
	OIDCEnabled bool              `json:"oidc_enabled"`
	Identities  []identityPayload `json:"identities"`
}

// identityList returns the caller's own linked identities and whether OIDC is usable right now.
// A configuration read failure is a server error, never a silent "disabled": the page must not claim the feature is off when it is merely broken.
func (s *Server) identityList(c *gin.Context) {
	u, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	cfg, err := s.oidcLoadConfig(c)
	if err != nil {
		s.logger.Error("identity list: load oidc config failed", "error", err)
		apiAuthError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	if s.identities == nil {
		s.logger.Error("identity list: identity store is not wired")
		apiAuthError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	rows, err := s.identities.ListForUser(c.Request.Context(), u.ID)
	if err != nil {
		s.logger.Error("identity list: list identities failed", "user_id", u.ID, "error", err)
		apiAuthError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	resp := identityListResponse{OIDCEnabled: cfg.Usable(), Identities: make([]identityPayload, 0, len(rows))}
	for _, row := range rows {
		email := ""
		if row.Email != nil {
			email = *row.Email
		}
		resp.Identities = append(resp.Identities, identityPayload{
			ID: row.PublicID, Provider: row.Provider, Email: email,
			LinkedAt: row.LinkedAt.Format(time.RFC3339),
		})
	}
	c.JSON(http.StatusOK, resp)
}

// identityBindStart starts binding one new external identity to the caller's account and returns the provider authorization URL.
// Nothing is written to the database or the audit log here: the link exists only after the callback validates the provider's response.
func (s *Server) identityBindStart(c *gin.Context) {
	u, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	cfg, err := s.oidcLoadConfig(c)
	if err != nil {
		s.logger.Error("identity bind: load oidc config failed", "error", err)
		apiAuthError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	if !cfg.Usable() {
		apiAuthError(c, http.StatusConflict, "oidc_unavailable", "OIDC is not available.")
		return
	}
	if s.identityLink == nil {
		s.logger.Error("identity bind: identity link service is not wired")
		apiAuthError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	redirectURI := s.oidcRedirectURI(c)
	party, err := s.oidc.Client(ctx, cfg, redirectURI)
	if err != nil {
		s.logger.Error("identity bind: discovery failed", "issuer", cfg.Issuer, "error", err)
		apiAuthError(c, http.StatusBadGateway, "oidc_unavailable", "The identity provider is unavailable.")
		return
	}
	state, err := auth.NewState()
	if err != nil {
		s.logger.Error("identity bind: generate state failed", "error", err)
		apiAuthError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	nonce, err := auth.NewState()
	if err != nil {
		s.logger.Error("identity bind: generate nonce failed", "error", err)
		apiAuthError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	pkce, err := auth.NewPKCE()
	if err != nil {
		s.logger.Error("identity bind: generate pkce failed", "error", err)
		apiAuthError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	if !s.oidc.PutPending(state, auth.PendingAuth{
		Nonce:       nonce,
		Verifier:    pkce.Verifier,
		RedirectURI: redirectURI,
		ExpiresAt:   time.Now().UTC().Add(10 * time.Minute),
		Intent:      auth.PendingIntentBind,
		UserID:      u.ID,
	}) {
		s.logger.Warn("identity bind: pending state table is full, rejecting a new bind start")
		apiAuthError(c, http.StatusServiceUnavailable, "busy", "Too many pending sign-in requests. Try again later.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"authorize_url": auth.BuildAuthURL(party, state, nonce, pkce.Challenge)})
}

// identityUnlink removes one identity from the caller's account.
// A row that does not exist and a row owned by another user answer identically, so the endpoint cannot be used to probe which public ids exist.
// The last credential may not be removed: an account with no password and no remaining identity could never sign in again.
func (s *Server) identityUnlink(c *gin.Context) {
	u, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if s.identities == nil || s.users == nil {
		s.logger.Error("identity unlink: identity or user store is not wired")
		apiAuthError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	raw := c.Param("id")
	ident, err := s.identities.ByPublicID(ctx, raw)
	if err != nil || ident == nil || ident.UserID != u.ID {
		apiAuthError(c, http.StatusNotFound, "identity_not_found", "Identity not found.")
		return
	}
	fresh, err := s.users.ByID(ctx, u.ID)
	if err != nil {
		s.logger.Error("identity unlink: load user failed", "user_id", u.ID, "error", err)
		apiAuthError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	if fresh.PasswordHash == nil {
		rows, err := s.identities.ListForUser(ctx, u.ID)
		if err != nil {
			s.logger.Error("identity unlink: list identities failed", "user_id", u.ID, "error", err)
			apiAuthError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
			return
		}
		if len(rows) <= 1 {
			apiAuthError(c, http.StatusConflict, "last_credential", "This is the last way to sign in; set a password before unlinking.")
			return
		}
	}
	if err := s.identities.Delete(ctx, ident.ID); err != nil {
		if errors.Is(err, store.ErrIdentityNotFound) {
			apiAuthError(c, http.StatusNotFound, "identity_not_found", "Identity not found.")
			return
		}
		s.logger.Error("identity unlink: delete identity failed", "user_id", u.ID, "identity_id", ident.ID, "error", err)
		apiAuthError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionIdentityUnlink,
		TargetType: "identity", TargetID: store.Ptr(ident.ID),
		Detail: map[string]any{"provider": ident.Provider, "subject": ident.Subject, "via": "self"},
	})
	c.Status(http.StatusNoContent)
}
