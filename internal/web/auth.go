package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
)

// registerAuthRoutes mounts the auth routes.
//
// Skipped outright when dependencies are not wired up (e.g. M0-stage tests only construct DB/Logger), keeping New usable;
// see cmd/engram for the production wiring, which must explicitly provide Accounts, Sessions and Users.
func (s *Server) registerAuthRoutes(router *gin.Engine) {
	if s.accounts == nil || s.sessions == nil || s.users == nil {
		return
	}
	// The three pages login/register/setup only return the SPA app shell: page decisions and write operations are all handled by
	// the client's same-origin JSON endpoints (/api/v1/auth/{session,login,register,setup}).
	// Pre-session write requests have no server-side session to bind a token to, so PreSessionCSRFMiddleware still validates the double-submit cookie
	// against the mirror token.
	router.GET("/login", s.loginShell)
	router.GET("/register", s.registerShell)
	router.GET("/setup", s.setupShell)
	// OIDC optional login: off by default, and the handler returns 404 when the configuration is incomplete (half-open is not allowed).
	router.GET("/auth/oidc/start", s.oidcStart)
	router.GET("/auth/oidc/callback", s.oidcCallback)
	// The SPA's OIDC entry probe (oidc_api.go): read-only, used by the login view to decide whether to show a second login button.
	s.registerOIDCRoutes(router)
	// Registration policy probe: read-only and callable anonymously, used by the registration view to decide, before rendering the form, whether to state "self-registration is unavailable" outright.
	router.GET("/api/v1/auth/registration", s.apiRegistrationInfo)
	// TOTP two-factor: the login second step and the settings page routes are all in internal/web/totp.go.
	s.registerTOTPRoutes(router)

	// SPA / same-origin JSON session and auth endpoints
	router.GET("/api/v1/auth/session", s.apiSession)
	router.GET("/api/v1/session", s.apiSession)
	router.POST("/api/v1/auth/login", auth.PreSessionCSRFMiddleware(), s.apiLogin)
	// Registration and setup are pre-login flows: they likewise have no server-side session, so POST uses the double-submit cookie.
	router.POST("/api/v1/auth/register", auth.PreSessionCSRFMiddleware(), s.apiRegister)
	router.POST("/api/v1/auth/setup", auth.PreSessionCSRFMiddleware(), s.apiSetup)
	router.POST("/api/v1/auth/logout", s.sessions.CSRFMiddleware(), s.apiLogout)

	// The login second step (TOTP) has no deep-linkable GET page: no route is registered for it, and the canonical path /login/totp
	// falls back to the app shell via NoRoute; the protocol is GET/POST /api/v1/auth/totp, and anonymous CSRF is issued by
	// GET /api/v1/auth/session issuing the double-submit cookie.
}

// localizer takes the localizer from the request context; missing is a wiring defect, so it logs in English and returns 500.
func (s *Server) localizer(c *gin.Context) (*i18n.Localizer, bool) {
	loc := i18n.FromContext(c.Request.Context())
	if loc == nil {
		s.logger.Error("i18n: localizer missing from request context", "path", c.Request.URL.Path)
		c.AbortWithStatus(http.StatusInternalServerError)
		return nil, false
	}
	return loc, true
}

// anonRateLimited reports whether an anonymous entry request should be rejected by the rate limit, and adds the Retry-After response header when rejecting.
// /register and /forgot-password share the same two-dimension count of "IP + target mailbox, 5 times each / 15 minutes"
// When no limiter is wired up everything is allowed, consistent with the wiring convention of loginLimiter.
func (s *Server) anonRateLimited(c *gin.Context, email string) bool {
	if s.anonLimiter == nil {
		return false
	}
	if s.anonLimiter.Allow(c.ClientIP(), email) {
		return false
	}
	c.Header("Retry-After", strconv.Itoa(int(auth.DefaultAnonRateWindow.Seconds())))
	return true
}

// registrationOutcome is the result of the registration and setup decision: an empty Code means success and carries the new user.
// Status is the HTTP status; the JSON transport uses that same status directly.
type registrationOutcome struct {
	Status int
	Code   string // stable English code; empty string means success
	User   *store.User
}

// registrationDenialCode translates a policy or invite denial reason into a stable English code.
// The code is both the code in the JSON error envelope and the source of the stable English message.
func registrationDenialCode(err error) string {
	switch {
	case errors.Is(err, auth.ErrEmailDomainNotAllowed):
		return "email_domain_not_allowed"
	case errors.Is(err, auth.ErrInviteRequired):
		return "invite_required"
	case errors.Is(err, store.ErrInviteNotFound), errors.Is(err, store.ErrInviteUsed),
		errors.Is(err, store.ErrInviteExpired), errors.Is(err, store.ErrInviteEmailMismatch):
		return "invite_invalid"
	default:
		return "registration_closed"
	}
}

// attemptRegistration performs all server-side decisions for self-registration: anonymous rate limiting, form validation, first-admin bootstrap,
// invite acceptance (transactional) and the registration policy/email allowlist. The SPA JSON interface (apiRegister) is the only transport,
// so policy, rate limiting, audit and transaction semantics have only one implementation.
//
// It writes no response and sends no mail: the caller maps the response shape from the outcome.
func (s *Server) attemptRegistration(ctx context.Context, clientIP, username, email, displayName, password, inviteToken, locale string) registrationOutcome {
	// Anonymous entry rate limiting: shares the same IP + target mailbox
	// two-dimension fixed-window count with /api/v1/auth/forgot-password (5 each / 15 minutes), rejected as soon as either exceeds. Placed before form validation and any DB action:
	// a rejected request creates no account, writes no audit row and sends no verification mail.
	if s.anonLimiter != nil && !s.anonLimiter.Allow(clientIP, email) {
		return registrationOutcome{Status: http.StatusTooManyRequests, Code: "rate_limited"}
	}
	if code := auth.AccountInputCode(username, email, password); code != "" {
		return registrationOutcome{Status: http.StatusBadRequest, Code: code}
	}

	now := time.Now().UTC()
	admins, err := s.users.CountActiveAdmins(ctx)
	if err != nil {
		s.logger.Error("count active admins", "error", err)
		return registrationOutcome{Status: http.StatusInternalServerError, Code: "internal_error"}
	}

	role := store.RoleUser
	var invite *store.Invite
	switch {
	case admins == 0:
		// Bootstrap path: with no admins, this account becomes an admin.
		role = store.RoleAdmin
	case inviteToken != "":
		if s.invites == nil {
			return registrationOutcome{Status: http.StatusForbidden, Code: "invite_invalid"}
		}
		inv, verr := s.invites.Validate(ctx, inviteToken, email, now)
		if verr != nil {
			s.logger.Info("invite rejected", "error", verr)
			return registrationOutcome{Status: http.StatusForbidden, Code: registrationDenialCode(verr)}
		}
		invite = inv
		if inv.Role != "" {
			role = inv.Role
		}
	default:
		settings, err := store.LoadSettings(ctx, s.db)
		if err != nil {
			s.logger.Error("load settings", "error", err)
			return registrationOutcome{Status: http.StatusInternalServerError, Code: "internal_error"}
		}
		policy := auth.ParseRegistrationPolicy(settings[auth.SettingKeyRegistrationPolicy])
		allowlist := auth.ParseEmailAllowlist(settings[auth.SettingKeyEmailAllowlist])
		if derr := auth.DecideRegistration(policy, email, allowlist); derr != nil {
			return registrationOutcome{Status: http.StatusForbidden, Code: registrationDenialCode(derr)}
		}
	}

	input := auth.CreateUserInput{
		Username:    username,
		Email:       email,
		DisplayName: displayName,
		Password:    password,
		Role:        role,
		Locale:      locale,
	}

	var u *store.User
	if role == store.RoleAdmin && invite == nil && admins == 0 {
		// Bootstrap window: the count above is outside the gate and may have been beaten by a concurrent setup/registration. Recount and create the account inside the gate transaction,
		// guaranteeing that "registering with no admin makes you an admin" happens at most once.
		created, err := s.createBootstrapAdmin(ctx, input)
		if err != nil {
			if errors.Is(err, store.ErrAdminExists) {
				s.logger.Info("bootstrap registration lost the race to another administrator", "username", username)
				return registrationOutcome{Status: http.StatusConflict, Code: "create_failed"}
			}
			s.logger.Error("create local user failed", "username", username, "error", err)
			return registrationOutcome{Status: http.StatusConflict, Code: "create_failed"}
		}
		u = created
	} else if invite != nil {
		// Invite acceptance is transactional: Accept claims the token, creates the account and backfills used_by in one transaction.
		// If account creation fails the whole thing rolls back and the token stays usable; under concurrency a conditional update guarantees only one request can claim it.
		created, aerr := s.invites.Accept(ctx, invite.Token, now, func(tx *gorm.DB) (*store.User, error) {
			return s.accounts.CreateLocalUserTx(ctx, tx, input)
		})
		if aerr != nil {
			if errors.Is(aerr, store.ErrInviteUsed) {
				return registrationOutcome{Status: http.StatusForbidden, Code: "invite_invalid"}
			}
			s.logger.Error("create local user failed", "username", username, "error", aerr)
			return registrationOutcome{Status: http.StatusConflict, Code: "create_failed"}
		}
		u = created
	} else {
		created, cerr := s.accounts.CreateLocalUser(ctx, input)
		if cerr != nil {
			s.logger.Error("create local user failed", "username", username, "error", cerr)
			return registrationOutcome{Status: http.StatusConflict, Code: "create_failed"}
		}
		u = created
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		Action:     store.ActionUserCreate,
		TargetType: "user",
		TargetID:   store.Ptr(u.ID),
		Detail:     map[string]any{"username": u.Username, "email": u.Email, "role": u.Role},
	})
	return registrationOutcome{Status: http.StatusOK, User: u}
}

// attemptSetup performs first-admin bootstrap account creation and audit. The SPA JSON interface (apiSetup) is the only transport,
// and the one-time admin gate (CountActiveAdmins==0) is checked by the caller at the entry point. Bootstrap does not send an email verification mail.
func (s *Server) attemptSetup(ctx context.Context, username, email, displayName, password, locale string) registrationOutcome {
	if code := auth.AccountInputCode(username, email, password); code != "" {
		return registrationOutcome{Status: http.StatusBadRequest, Code: code}
	}
	u, err := s.createBootstrapAdmin(ctx, auth.CreateUserInput{
		Username:    username,
		Email:       email,
		DisplayName: displayName,
		Password:    password,
		Role:        store.RoleAdmin,
		Locale:      locale,
	})
	if errors.Is(err, store.ErrAdminExists) {
		// After the reachability check at the entry point, another request completed the bootstrap first: handle it the same as "an admin already exists".
		return registrationOutcome{Status: http.StatusNotFound, Code: "not_found"}
	}
	if err != nil {
		s.logger.Error("create bootstrap admin failed", "username", username, "error", err)
		return registrationOutcome{Status: http.StatusConflict, Code: "create_failed"}
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		Action:     store.ActionUserCreate,
		TargetType: "user",
		TargetID:   store.Ptr(u.ID),
		Detail:     map[string]any{"username": u.Username, "email": u.Email, "role": u.Role, "bootstrap": true},
	})
	return registrationOutcome{Status: http.StatusOK, User: u}
}

// createBootstrapAdmin creates the first admin inside the admin gate: recounts active admins within the transaction and, if one exists, returns
// store.ErrAdminExists without creating an account. The setup page and "registration with no admin" share it, so the rule lives in one place.
func (s *Server) createBootstrapAdmin(ctx context.Context, input auth.CreateUserInput) (*store.User, error) {
	input.Role = store.RoleAdmin
	var u *store.User
	err := store.WithAdminGate(ctx, s.db, func(tx *gorm.DB, admins int64) error {
		if admins > 0 {
			return store.ErrAdminExists
		}
		created, err := s.accounts.CreateLocalUserTx(ctx, tx, input)
		u = created
		return err
	})
	return u, err
}

// setupAvailable reports whether the setup page is reachable: reachable only when there is no admin still in use (the one-time admin gate).
func (s *Server) setupAvailable(c *gin.Context) bool {
	n, err := s.users.CountActiveAdmins(c.Request.Context())
	if err != nil {
		s.logger.Error("count active admins", "error", err)
		return false
	}
	return n == 0
}
