package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the SPA JSON endpoint for the admin panel's "identity and OIDC" page.
//
// Reads and writes reuse oidcLoadConfig and the s.oidc client, with exactly the same parsing as the SSR page; the client secret only returns
// "configured/not configured" and never echoes plaintext. redirect_uri shares s.oidcRedirectURI with the login flow, so what the page shows
// is the value actually sent. The connection test carries the provider's raw error text into the response (acceptance point).

// adminOIDCIdentity is one linked identity.
type adminOIDCIdentity struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Subject  string `json:"subject"`
	Email    string `json:"email"`
	Username string `json:"username"`
	LinkedAt string `json:"linked_at"`
}

// adminOIDCResponse is the read result for the OIDC config page.
type adminOIDCResponse struct {
	Enabled            bool                `json:"enabled"`
	Issuer             string              `json:"issuer"`
	ClientID           string              `json:"client_id"`
	SecretConfigured   bool                `json:"secret_configured"`
	RedirectURI        string              `json:"redirect_uri"`
	Scopes             string              `json:"scopes"`
	ClaimSubject       string              `json:"claim_subject"`
	ClaimEmail         string              `json:"claim_email"`
	ClaimName          string              `json:"claim_name"`
	ClaimEmailVerified string              `json:"claim_email_verified"`
	Identities         []adminOIDCIdentity `json:"identities"`
}

// adminOIDCRequest is the request body shared by save / test; empty fields mean no change.
type adminOIDCRequest struct {
	Enabled            bool   `json:"enabled"`
	Issuer             string `json:"issuer"`
	ClientID           string `json:"client_id"`
	Scopes             string `json:"scopes"`
	ClaimSubject       string `json:"claim_subject"`
	ClaimEmail         string `json:"claim_email"`
	ClaimName          string `json:"claim_name"`
	ClaimEmailVerified string `json:"claim_email_verified"`
	ClientSecret       string `json:"client_secret"`
}

// adminOIDC returns the OIDC config, the callback URL and the linked identity list.
func (s *Server) adminOIDC(c *gin.Context) {
	ctx := c.Request.Context()
	cfg, err := s.oidcLoadConfig(c)
	if err != nil {
		s.logger.Error("spa admin: load oidc config failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	saved, err := store.LoadSettings(ctx, s.db)
	if err != nil {
		s.logger.Error("spa admin: load settings failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	secretConfigured := false
	if s.db != nil {
		if configured, err := store.SecretConfigured(ctx, s.db, auth.SettingKeyOIDCClientSecret); err == nil && configured {
			secretConfigured = true
		}
	}

	// Show the effective scopes, not the raw row: an unset value falls back to the built-in default at load time, and
	// returning the empty row would make the admin type the default again on every fresh instance.
	resp := adminOIDCResponse{
		Enabled:            cfg.Enabled,
		Issuer:             saved[auth.SettingKeyOIDCIssuer],
		ClientID:           saved[auth.SettingKeyOIDCClientID],
		SecretConfigured:   secretConfigured,
		RedirectURI:        s.oidcRedirectURI(c),
		Scopes:             strings.Join(cfg.Scopes, " "),
		ClaimSubject:       saved[auth.SettingKeyOIDCClaimSubject],
		ClaimEmail:         saved[auth.SettingKeyOIDCClaimEmail],
		ClaimName:          saved[auth.SettingKeyOIDCClaimName],
		ClaimEmailVerified: saved[auth.SettingKeyOIDCClaimEmailVerified],
		Identities:         []adminOIDCIdentity{},
	}
	if s.identities != nil {
		rows, err := s.identities.ListAll(ctx)
		if err != nil {
			s.logger.Error("spa admin: list identities failed", "error", err)
			adminError(c, http.StatusInternalServerError, "internal_error")
			return
		}
		for _, row := range rows {
			username := ""
			if s.users != nil {
				if u, err := s.users.ByID(ctx, row.UserID); err == nil && u != nil {
					username = u.Username
				}
			}
			email := ""
			if row.Email != nil {
				email = *row.Email
			}
			resp.Identities = append(resp.Identities, adminOIDCIdentity{
				ID: row.PublicID, Provider: row.Provider, Subject: row.Subject,
				Email: email, Username: username,
				LinkedAt: row.LinkedAt.Format(time.RFC3339),
			})
		}
	}
	c.JSON(http.StatusOK, resp)
}

// adminOIDCSave saves the OIDC config: non-sensitive values go through PutSetting, the client secret goes through PutSecret (encrypted).
// After saving it invalidates the discovery document cache and writes an audit row.
func (s *Server) adminOIDCSave(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req adminOIDCRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	issuer := strings.TrimRight(strings.TrimSpace(req.Issuer), "/")
	if req.Enabled && issuer != "" {
		if parsed, err := url.Parse(issuer); err != nil || parsed.Scheme == "" || parsed.Host == "" {
			adminError(c, http.StatusBadRequest, "invalid_issuer")
			return
		}
	}
	if req.Enabled && issuer == "" {
		adminError(c, http.StatusBadRequest, "invalid_issuer")
		return
	}

	ctx := c.Request.Context()
	// Record the old issuer first so the cache can be invalidated.
	old, err := s.oidcLoadConfig(c)
	if err != nil {
		s.logger.Error("spa admin: load oidc config failed", "error", err)
		adminError(c, http.StatusInternalServerError, "save_failed")
		return
	}

	now := time.Now().UTC()
	enabledValue := "false"
	if req.Enabled {
		enabledValue = "true"
	}
	writes := []struct{ key, value string }{
		{auth.SettingKeyOIDCEnabled, enabledValue},
	}
	for _, kv := range []struct{ key, value string }{
		{auth.SettingKeyOIDCIssuer, issuer},
		{auth.SettingKeyOIDCClientID, strings.TrimSpace(req.ClientID)},
		{auth.SettingKeyOIDCScopes, strings.TrimSpace(req.Scopes)},
		{auth.SettingKeyOIDCClaimSubject, strings.TrimSpace(req.ClaimSubject)},
		{auth.SettingKeyOIDCClaimEmail, strings.TrimSpace(req.ClaimEmail)},
		{auth.SettingKeyOIDCClaimName, strings.TrimSpace(req.ClaimName)},
		{auth.SettingKeyOIDCClaimEmailVerified, strings.TrimSpace(req.ClaimEmailVerified)},
	} {
		if kv.value != "" {
			writes = append(writes, kv)
		}
	}
	changed := make([]string, 0, len(writes)+1)
	for _, w := range writes {
		if err := store.PutSetting(ctx, s.db, w.key, w.value, store.Ptr(u.ID), now); err != nil {
			s.logger.Error("spa admin: save oidc setting failed", "key", w.key, "error", err)
			adminError(c, http.StatusInternalServerError, "save_failed")
			return
		}
		changed = append(changed, w.key)
	}
	if s.secrets != nil {
		if secret := req.ClientSecret; strings.TrimSpace(secret) != "" {
			if err := store.PutSecret(ctx, s.db, s.secrets, auth.SettingKeyOIDCClientSecret, secret, store.Ptr(u.ID), now); err != nil {
				s.logger.Error("spa admin: save oidc client secret failed", "error", err)
				adminError(c, http.StatusInternalServerError, "save_failed")
				return
			}
			changed = append(changed, auth.SettingKeyOIDCClientSecret)
		}
	}

	// Invalidate the discovery document cache after a config change: clear both the old and the new issuer.
	s.oidc.Invalidate(old.Issuer)
	s.oidc.Invalidate(issuer)
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionSettingUpdate,
		TargetType: "setting", Detail: map[string]any{"keys": changed, "scope": "oidc", "via": "spa"},
	})
	c.Status(http.StatusNoContent)
}

// adminOIDCTest fetches the discovery document for the issuer in the form (defaulting to the saved value),
// and on failure carries the provider's raw error text into the response (acceptance point).
func (s *Server) adminOIDCTest(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req adminOIDCRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	ctx := c.Request.Context()
	cfg, err := s.oidcLoadConfig(c)
	if err != nil {
		s.logger.Error("spa admin: load oidc config failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	issuer := strings.TrimRight(strings.TrimSpace(req.Issuer), "/")
	if issuer == "" {
		issuer = cfg.Issuer
	}
	result := adminTestResult{}
	if issuer == "" {
		result.Code = "no_issuer"
	} else if _, derr := s.oidc.Discover(ctx, issuer); derr != nil {
		// Carry the provider's error text through verbatim instead of only logging it.
		s.logger.Info("spa admin: oidc test connection failed", "issuer", issuer, "error", derr)
		result.Code = "failed"
		result.Message = derr.Error()
	} else {
		result.OK = true
	}
	// "Test connection" makes an outbound connection to an admin-chosen target: both success and failure leave a trace, recording only the issuer and the result.
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionAdminOIDCTest,
		TargetType: "oidc",
		Detail:     map[string]any{"issuer": issuer, "ok": result.OK, "via": "spa"},
	})
	c.JSON(http.StatusOK, result)
}

// adminOIDCUnlink unlinks one external identity and writes an audit row. After unlinking, that identity can no longer sign in with it.
func (s *Server) adminOIDCUnlink(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	raw := strings.TrimSpace(c.Param("id"))
	if raw == "" {
		adminError(c, http.StatusNotFound, "unlink_failed")
		return
	}
	ctx := c.Request.Context()
	ident, err := s.identities.ByPublicID(ctx, raw)
	if err != nil {
		s.logger.Error("spa admin: load identity failed", "id", raw, "error", err)
		adminError(c, http.StatusNotFound, "unlink_failed")
		return
	}
	// DeleteForUser rather than Delete: an admin unlink is still an unlink, and removing the last identity of a passwordless
	// account would leave that account with no way to sign in. The store holds the guard and the delete in one transaction.
	if err := s.identities.DeleteForUser(ctx, ident.ID, ident.UserID); err != nil {
		switch {
		case errors.Is(err, store.ErrLastCredential):
			adminError(c, http.StatusConflict, "last_credential")
		case errors.Is(err, store.ErrIdentityNotFound):
			adminError(c, http.StatusNotFound, "unlink_failed")
		default:
			s.logger.Error("spa admin: unlink identity failed", "id", ident.ID, "error", err)
			adminError(c, http.StatusInternalServerError, "unlink_failed")
		}
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionIdentityUnlink,
		TargetType: "identity", TargetID: store.Ptr(ident.ID),
		Detail: map[string]any{"provider": ident.Provider, "subject": ident.Subject, "user_id": s.userPublicID(ctx, ident.UserID), "via": "spa"},
	})
	c.Status(http.StatusNoContent)
}
