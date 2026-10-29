package web

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// OIDC login flow.
//
// Only protocol interaction and routing are implemented here; identity linking is entirely auth.IdentityLinkService's job (oauthlink.go):
//   GET /auth/oidc/start     generate state / nonce / PKCE and redirect to the provider authorization endpoint
//   GET /auth/oidc/callback  validate state against the browser cookie (one-time), exchange the token, validate nonce and signature, link and open a session
//
// OIDC is off by default: with incomplete configuration these two routes return 404 and the login page shows no entry (no half-open state allowed).
//
// Failures always 303 back to the SPA login page (GET /login). Now that the SPA has replaced the SSR login page, no HTML is rendered here;
// the failure reason goes to an English log. For the client to show copy it would need a stable error hand-off contract (no consumer today).

// oidcStateCookie is the browser-side half of the one-time pending state. The pending entry lives on the server, so without
// this cookie an attacker could start a login, keep the callback URL and send it to a victim: the victim's browser would
// complete the flow and open a session for the attacker's account (login CSRF). The callback therefore refuses any state
// whose cookie is missing or different, and it does so before the pending entry is consumed so a stolen URL cannot burn it.
const oidcStateCookie = "oidc_state"

// setOIDCStateCookie binds the current pending flow to this browser for ten minutes, matching the pending entry's lifetime.
// Path /auth/oidc keeps it off every other route; Lax still sends it on the provider's top-level redirect back to the callback.
func (s *Server) setOIDCStateCookie(c *gin.Context, state string) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(oidcStateCookie, state, 600, "/auth/oidc", "", s.secureCookies(), true)
}

// clearOIDCStateCookie removes the cookie with the same Path and flags used to write it, so every callback consumes the
// binding even when it fails; a negative MaxAge is what turns the Set-Cookie into a deletion.
func (s *Server) clearOIDCStateCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(oidcStateCookie, "", -1, "/auth/oidc", "", s.secureCookies(), true)
}

// oidcFailToLogin sends one failed OIDC login back to the SPA login page; reason is used only in the log (English).
// 303 rather than rendering a page: the callback is a browser navigation, and the redirect puts the address bar back on /login,
// letting the client router decide what to show. The session cookie is issued only on the success branch, never on failure.
func (s *Server) oidcFailToLogin(c *gin.Context, reason string) {
	s.logger.Info("oidc: login failed", "reason", reason)
	c.Redirect(http.StatusSeeOther, "/login")
}

// oidcProviderError reports the error the provider returned on the authorization request and sends the browser back to the login page.
func (s *Server) oidcProviderError(c *gin.Context) {
	s.logger.Info("oidc: provider returned an error", "error", c.Query("error"), "description", c.Query("error_description"))
	s.oidcFailToLogin(c, "provider_error")
}

// oidcLoadConfig reads the effective OIDC configuration; with no database wired up it counts as unconfigured.
func (s *Server) oidcLoadConfig(c *gin.Context) (*auth.OIDCConfig, error) {
	if s.db == nil {
		return &auth.OIDCConfig{}, nil
	}
	return auth.LoadOIDCConfig(c.Request.Context(), s.db, s.secrets)
}

// oidcRedirectURI returns the callback address: BASE_URL when configured, otherwise derived from the current request
// (local http development and httptest rely on the latter).
func (s *Server) oidcRedirectURI(c *gin.Context) string {
	if base := strings.TrimRight(strings.TrimSpace(s.baseURL), "/"); base != "" {
		return base + "/auth/oidc/callback"
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return scheme + "://" + c.Request.Host + "/auth/oidc/callback"
}

// oidcStart begins an OIDC login: discovery document → state/nonce/PKCE → redirect to the authorization endpoint.
func (s *Server) oidcStart(c *gin.Context) {
	ctx := c.Request.Context()
	cfg, err := s.oidcLoadConfig(c)
	if err != nil {
		s.logger.Error("oidc: load config failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if !cfg.Usable() {
		// Disabled or incompletely configured: the route is unavailable, consistent with the login page not showing the entry.
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	redirectURI := s.oidcRedirectURI(c)
	party, err := s.oidc.Client(ctx, cfg, redirectURI)
	if err != nil {
		s.logger.Error("oidc: discovery failed", "issuer", cfg.Issuer, "error", err)
		s.oidcFailToLogin(c, "discovery_unavailable")
		return
	}
	state, err := auth.NewState()
	if err != nil {
		s.logger.Error("oidc: generate state failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	nonce, err := auth.NewState()
	if err != nil {
		s.logger.Error("oidc: generate nonce failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	pkce, err := auth.NewPKCE()
	if err != nil {
		s.logger.Error("oidc: generate pkce failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if !s.oidc.PutPending(state, auth.PendingAuth{
		Nonce:       nonce,
		Verifier:    pkce.Verifier,
		RedirectURI: redirectURI,
		ExpiresAt:   time.Now().UTC().Add(10 * time.Minute),
		Intent:      auth.PendingIntentLogin,
	}) {
		// The pending-login table is full: refuse new starts, so anonymous requests cannot grow the in-memory table.
		// Log only in English with a generic hint; never echo state / nonce.
		s.logger.Warn("oidc: pending state table is full, rejecting a new login start")
		s.oidcFailToLogin(c, "pending_table_full")
		return
	}
	s.setOIDCStateCookie(c, state)
	authURL := auth.BuildAuthURL(party, state, nonce, pkce.Challenge)
	c.Redirect(http.StatusFound, authURL)
}

// oidcCallback handles the authorization callback: validate state → exchange token → validate ID Token → link → open session.
func (s *Server) oidcCallback(c *gin.Context) {
	ctx := c.Request.Context()
	cfg, err := s.oidcLoadConfig(c)
	if err != nil {
		s.logger.Error("oidc: load config failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if !cfg.Usable() {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if s.identityLink == nil {
		s.logger.Error("oidc: identity link service is not wired")
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// Read the provider error and the state first: a bind that the user cancelled at the provider must still consume its one-time state
	// and go back to the settings page, not to the login page (the user is still signed in). For an unusable state the provider error keeps
	// today's precedence, because no intent can be recovered from it.
	perr := strings.TrimSpace(c.Query("error"))
	state := strings.TrimSpace(c.Query("state"))
	if state == "" {
		if perr != "" {
			s.oidcProviderError(c)
			return
		}
		s.oidcFailToLogin(c, "missing_state")
		return
	}
	// The pending state is server-side but not bound to a browser: whoever holds the callback URL could otherwise finish the
	// flow. Compare the state cookie with the query state before consuming anything, so a forged or replayed callback neither
	// opens a session nor burns the victim's pending entry. Constant-time comparison keeps the state value from leaking through
	// timing. The cookie is one-time: every callback that reaches this point clears it, success or failure.
	cookieState, cookieErr := c.Cookie(oidcStateCookie)
	if cookieErr != nil || subtle.ConstantTimeCompare([]byte(cookieState), []byte(state)) != 1 {
		s.clearOIDCStateCookie(c)
		s.logger.Info("oidc: callback state does not match the state cookie")
		if _, ok := auth.CurrentSession(c); ok {
			// A signed-in browser is answered on the settings page: the missing binding may be a bind whose URL leaked, and a bind
			// failure must keep its existing destination instead of pretending the user was signed out.
			c.Redirect(http.StatusSeeOther, "/settings/security?identity=session")
			return
		}
		s.oidcFailToLogin(c, "state_cookie_mismatch")
		return
	}
	s.clearOIDCStateCookie(c)
	// state is one-time: unknown (forged or replayed) or expired are both refused here (negative case).
	pending, ok := s.oidc.TakePending(state)
	if !ok {
		if perr != "" {
			s.oidcProviderError(c)
			return
		}
		s.logger.Info("oidc: callback with an unknown or expired state")
		s.oidcRecordFailure(ctx, "state")
		s.oidcFailToLogin(c, "state_rejected")
		return
	}

	// A state started from the settings page is a bind, not a login: it must attach the identity to the session that asked for it and
	// must never establish a new session or touch the login policy.
	if pending.Intent == auth.PendingIntentBind {
		s.oidcBindCallback(c, cfg, pending)
		return
	}
	if perr != "" {
		s.oidcProviderError(c)
		return
	}

	code := strings.TrimSpace(c.Query("code"))
	if code == "" {
		s.oidcFailToLogin(c, "missing_code")
		return
	}

	// Recover the redirect_uri / PKCE verifier / nonce saved when the login was started from state.
	party, err := s.oidc.Client(ctx, cfg, pending.RedirectURI)
	if err != nil {
		s.logger.Error("oidc: discovery failed during callback", "issuer", cfg.Issuer, "error", err)
		s.oidcFailToLogin(c, "discovery_unavailable")
		return
	}
	claims, err := s.oidc.ExchangeCode(ctx, party, code, pending.Verifier, pending.Nonce)
	if err != nil {
		s.logger.Error("oidc: token exchange or id_token verification failed", "error", err)
		s.oidcFailToLogin(c, "token_exchange_failed")
		return
	}
	profile := cfg.ProfileFromClaims(party.Issuer(), claims.Claims)

	policy := auth.PolicyClosed
	if settings, err := store.LoadSettings(ctx, s.db); err == nil {
		policy = auth.ParseRegistrationPolicy(settings[auth.SettingKeyRegistrationPolicy])
	}
	u, decision, err := s.identityLink.Resolve(ctx, profile, policy)
	if err != nil {
		if err == auth.ErrIdentityLinkDenied {
			// Under the closed policy there is no existing link and no automatic match either: back to the login page.
			s.logger.Info("oidc: login denied by registration policy", "provider", profile.Provider)
			s.oidcFailToLogin(c, "link_denied")
			return
		}
		s.logger.Error("oidc: identity link failed", "error", err)
		s.oidcFailToLogin(c, "identity_link_failed")
		return
	}
	if _, err := s.sessions.StartSession(ctx, c, u.ID); err != nil {
		s.logger.Error("oidc: start session failed", "user_id", u.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID),
		Action: store.ActionUserLoginSucceeded,
		Detail: map[string]any{"method": "oidc", "provider": profile.Provider, "decision": string(decision.Kind)},
	})
	c.Redirect(http.StatusSeeOther, "/")
}

// oidcBindCallback finishes an identity bind started from the settings page. The caller must be the same account that started the flow:
// a bind attaches credentials to an account, so a state completed under another browser session must be refused before the code is exchanged.
// Every outcome redirects back to the settings page with a stable query value; no session, login audit or login policy is involved.
func (s *Server) oidcBindCallback(c *gin.Context, cfg *auth.OIDCConfig, pending auth.PendingAuth) {
	ctx := c.Request.Context()
	current, ok := auth.CurrentUser(c)
	currentSess, sessOK := auth.CurrentSession(c)
	if !ok || !sessOK || current.ID != pending.UserID || currentSess.ID != pending.SessionID {
		s.logger.Info("oidc: bind callback does not match the pending session")
		c.Redirect(http.StatusSeeOther, "/settings/security?identity=session")
		return
	}
	if perr := strings.TrimSpace(c.Query("error")); perr != "" {
		s.logger.Info("oidc: provider returned an error during bind", "error", perr, "description", c.Query("error_description"))
		c.Redirect(http.StatusSeeOther, "/settings/security?identity=failed")
		return
	}
	code := strings.TrimSpace(c.Query("code"))
	if code == "" {
		s.logger.Info("oidc: bind callback has no authorization code")
		c.Redirect(http.StatusSeeOther, "/settings/security?identity=failed")
		return
	}
	party, err := s.oidc.Client(ctx, cfg, pending.RedirectURI)
	if err != nil {
		s.logger.Error("oidc: discovery failed during bind callback", "issuer", cfg.Issuer, "error", err)
		c.Redirect(http.StatusSeeOther, "/settings/security?identity=failed")
		return
	}
	claims, err := s.oidc.ExchangeCode(ctx, party, code, pending.Verifier, pending.Nonce)
	if err != nil {
		s.logger.Error("oidc: bind token exchange or id_token verification failed", "error", err)
		c.Redirect(http.StatusSeeOther, "/settings/security?identity=failed")
		return
	}
	profile := cfg.ProfileFromClaims(party.Issuer(), claims.Claims)
	switch err := s.identityLink.Bind(ctx, profile, current.ID); {
	case err == nil:
		c.Redirect(http.StatusSeeOther, "/settings/security?identity=linked")
	case errors.Is(err, auth.ErrIdentityAlreadyLinked):
		c.Redirect(http.StatusSeeOther, "/settings/security?identity=taken")
	default:
		s.logger.Error("oidc: bind identity failed", "user_id", current.ID, "error", err)
		c.Redirect(http.StatusSeeOther, "/settings/security?identity=failed")
	}
}

// oidcRecordFailure leaves an audit record for a state validation failure (no user_id: identity is unknown at this point).
func (s *Server) oidcRecordFailure(ctx context.Context, reason string) {
	s.auditor.Record(ctx, store.AuditEntry{
		Action:     store.ActionUserLoginFailed,
		TargetType: "oidc",
		Detail:     map[string]any{"reason": reason},
	})
}
