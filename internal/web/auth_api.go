package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
)

// apiLoginRequest is the request body for /api/v1/auth/login (accepts JSON and form input).
type apiLoginRequest struct {
	Username string `json:"username" form:"username"`
	Password string `json:"password" form:"password"`
}

// apiSession provides same-origin SPA session bootstrap and CSRF token retrieval.
//
// Whether signed in or not, it never exposes the session ID, signing keys or any internal credential;
// when signed in it returns the current user info and the CSRF token bound to the server-side session;
// when signed out or the user is disabled it returns authenticated=false, user=null, and issues/mirrors the CSRF token matching the double-submit cookie,
// for the SPA to use on pre-session requests such as same-origin login.
func (s *Server) apiSession(c *gin.Context) {
	u, okUser := auth.CurrentUser(c)
	sess, okSess := auth.CurrentSession(c)
	if okUser && okSess && u.Status == store.StatusActive {
		c.JSON(http.StatusOK, gin.H{
			"authenticated": true,
			"user": gin.H{
				"id":           u.PublicID,
				"username":     u.Username,
				"email":        u.Email,
				"display_name": u.DisplayName,
				"role":         u.Role,
				"locale":       u.Locale,
			},
			"csrf_token": sess.CSRFToken,
		})
		return
	}

	// Signed out or account not active: issue/reuse the double-submit CSRF token, with the cookie set HttpOnly
	csrfToken := auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	c.JSON(http.StatusOK, gin.H{
		"authenticated": false,
		"user":          nil,
		"csrf_token":    csrfToken,
	})
}

// apiLogin validates credentials and establishes a server-side session.
//
// auth.PreSessionCSRFMiddleware is mounted to guarantee the request carries both the csrf_double cookie and the X-CSRF-Token header.
// Password validation, rate-limit incremental delay, constant-time dummy hash, audit logging and TOTP two-factor all reuse the existing service semantics.
func (s *Server) apiLogin(c *gin.Context) {
	var req apiLoginRequest
	if err := c.ShouldBind(&req); err != nil && c.Request.ContentLength > 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    api.CodeInvalidRequest,
				"message": "The request is invalid.",
			},
		})
		return
	}

	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = strings.TrimSpace(c.PostForm("username"))
	}
	password := req.Password
	if password == "" {
		password = c.PostForm("password")
	}

	ctx := c.Request.Context()
	ip := c.ClientIP()

	// Before authenticating, delay incrementally by the accumulated failure count and reject outright once it reaches the threshold: anti-brute-force, flattening the rate of brute-force attempts
	if s.loginLimiter != nil {
		if _, err := s.loginLimiter.Wait(ctx, username, ip); err != nil {
			s.writeLoginLimited(c, err, username, ip)
			return
		}
	}

	u, err := s.accounts.Authenticate(ctx, username, password)
	if err != nil {
		if s.loginLimiter != nil {
			s.loginLimiter.RecordFailure(username, ip)
		}
		s.logger.Info("login failed", "username", username, "error", err)
		s.audit(ctx, store.AuditEntry{
			Action: store.ActionUserLoginFailed,
			Detail: map[string]any{"username": username, "ip": ip, "reason": err.Error()},
		})
		code := api.CodeInvalidCredentials
		msg := "Invalid username or password."
		if errors.Is(err, auth.ErrUserDisabled) {
			code = api.CodeUserDisabled
			msg = "This account has been disabled."
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"code":    code,
				"message": msg,
			},
		})
		return
	}

	// TOTP account: after the first factor passes, issue a short-lived pending cookie and return a TOTP challenge, never handing out a session early
	if s.totp != nil {
		enabled, err := s.totp.Enabled(ctx, u.ID)
		if err != nil {
			s.logger.Error("totp enabled check failed", "user_id", u.ID, "error", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{
					"code":    api.CodeInternal,
					"message": "An internal error occurred.",
				},
			})
			return
		}
		if enabled {
			// Passing the first factor counts as neither success nor failure: give the placeholder back, and let the failure count accumulate with the second step.
			if s.loginLimiter != nil {
				s.loginLimiter.Release(username, ip)
			}
			s.setTOTPPendingCookie(c, u.ID)
			c.JSON(http.StatusOK, gin.H{
				"requires_totp": true,
			})
			return
		}
	}

	if s.loginLimiter != nil {
		s.loginLimiter.Reset(username, ip)
	}

	sess, err := s.sessions.StartSession(ctx, c, u.ID)
	if err != nil {
		s.logger.Error("start session failed", "user_id", u.ID, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    api.CodeInternal,
				"message": "An internal error occurred.",
			},
		})
		return
	}

	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID),
		Action: store.ActionUserLoginSucceeded,
		Detail: map[string]any{"ip": ip},
	})
	s.notifyNewDeviceLogin(c, u)

	c.JSON(http.StatusOK, gin.H{
		"authenticated": true,
		"user": gin.H{
			"id":           u.PublicID,
			"username":     u.Username,
			"email":        u.Email,
			"display_name": u.DisplayName,
			"role":         u.Role,
			"locale":       u.Locale,
		},
		"csrf_token": sess.CSRFToken,
	})
}

// loginShell serves GET /login: returns the app shell (the canonical path),
// and the client router renders the login page; the login protocol goes through GET /api/v1/auth/session + POST /api/v1/auth/login,
// with the second step going through GET /login/totp / POST /api/v1/auth/totp.
//
// It first initializes the pre-session double-submit cookie: after mounting, the SPA fetches the same token from GET /api/v1/auth/session and puts it in
// X-CSRF-Token, and PreSessionCSRFMiddleware on POST /api/v1/auth/login compares the cookie with the mirror value accordingly.
// This only issues the cookie and returns the app shell: it renders no form, establishes no session and returns no credential
// (the session cookie is always issued by the server after a successful login).
func (s *Server) loginShell(c *gin.Context) {
	auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	s.shell.ServeIndex(c)
}

// registerShell serves GET /register: returns the app shell (the canonical path),
// and the registration protocol goes through POST /api/v1/auth/register (PreSessionCSRFMiddleware compares the cookie with the mirror token).
// ?invite=<token> is read from the URL by the frontend and filled into the request body. This only issues the cookie and returns the app shell:
// it creates no account, establishes no session and returns no credential.
func (s *Server) registerShell(c *gin.Context) {
	auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	s.shell.ServeIndex(c)
}

// apiRegistrationInfoResponse is the response body for GET /api/v1/auth/registration: only the registration policy.
type apiRegistrationInfoResponse struct {
	Policy string `json:"policy"`
}

// apiRegistrationInfo returns the current self-registration policy (GET /api/v1/auth/registration).
//
// The registration page calls it before rendering the form: when submitting the registration form on this visit would be allowed it shows no notice, and when it would be rejected it states the reason outright,
// rather than letting the visitor fill in the whole form only to be rejected with 403.
// The value comes from the same branch order as attemptRegistration: when there is no active admin, the registration endpoint makes this account
// an admin (bootstrap window), with neither policy nor allowlist involved in the decision, so it answers open in that case — the value still comes from the same
// ParseRegistrationPolicy, just with this exception; otherwise a new instance would show "the page says self-registration is unavailable
// but submitting still creates an admin". A failed policy read returns 500, and the frontend falls back to "show the form as usual" —
// whether to allow is ultimately still decided server-side at submission. The response contains no credential and is callable anonymously.
func (s *Server) apiRegistrationInfo(c *gin.Context) {
	ctx := c.Request.Context()
	admins, err := s.users.CountActiveAdmins(ctx)
	if err != nil {
		s.logger.Error("registration info: count active admins failed", "error", err)
		apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
		return
	}
	if admins == 0 {
		c.JSON(http.StatusOK, apiRegistrationInfoResponse{Policy: auth.PolicyOpen})
		return
	}
	settings, err := store.LoadSettings(ctx, s.db)
	if err != nil {
		s.logger.Error("registration info: load settings failed", "error", err)
		apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
		return
	}
	c.JSON(http.StatusOK, apiRegistrationInfoResponse{
		Policy: auth.ParseRegistrationPolicy(settings[auth.SettingKeyRegistrationPolicy]),
	})
}

// setupShell serves GET /setup: returns the app shell (the canonical path),
// and the setup protocol goes through POST /api/v1/auth/setup. Reachability is exactly as before the migration: 404 when an active admin already exists
// (the one-time admin gate, so it cannot be used as a back door visited over and over). When reachable it issues the pre-session double-submit cookie and returns the app shell;
// this only issues the cookie and returns the app shell, creating no account and no session.
func (s *Server) setupShell(c *gin.Context) {
	if !s.setupAvailable(c) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	s.shell.ServeIndex(c)
}

// apiRegisterRequest is the request body for POST /api/v1/auth/register.
// Invite may be empty: when empty the registration policy decides; when non-empty the invite-acceptance path is taken.
type apiRegisterRequest struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Invite      string `json:"invite"`
}

// apiSetupRequest is the request body for POST /api/v1/auth/setup.
// Email may be empty: when empty and BOOTSTRAP_ADMIN_EMAIL is configured, the env var is used as a fallback (same as SSR).
type apiSetupRequest struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

// apiRegister is the SPA self-registration endpoint (POST /api/v1/auth/register).
//
// Mounts auth.PreSessionCSRFMiddleware: the request must carry both the csrf_double cookie and the X-CSRF-Token header.
// All decisions (anonymous rate limiting, validation, first-admin bootstrap, invite transaction, registration policy/allowlist) go through
// attemptRegistration: invite acceptance and page bootstrap share one implementation, so the policy semantics cannot drift.
// Success establishes no session and only returns created=true.
func (s *Server) apiRegister(c *gin.Context) {
	var req apiRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiAuthError(c, http.StatusBadRequest, api.CodeInvalidRequest, "The request is invalid.")
		return
	}
	ctx := c.Request.Context()
	outcome := s.attemptRegistration(ctx, c.ClientIP(),
		strings.TrimSpace(req.Username),
		strings.ToLower(strings.TrimSpace(req.Email)),
		strings.TrimSpace(req.DisplayName),
		req.Password,
		strings.TrimSpace(req.Invite),
		s.requestLocale(c))
	if outcome.Code != "" {
		s.writeRegistrationError(c, outcome)
		return
	}
	// Sends an email verification mail after registration; when SMTP is unconfigured it sends nothing and does not error (same as SSR).
	s.sendEmailVerification(c, outcome.User)
	c.JSON(http.StatusOK, gin.H{"created": true})
}

// apiSetup is the SPA first-admin bootstrap endpoint (POST /api/v1/auth/setup).
//
// The reachability rule matches the GET /setup app shell: reachable only when there is no active admin, otherwise 404 (the one-time admin gate).
// Account creation and audit reuse attemptSetup, and PreSessionCSRFMiddleware is mounted. Success establishes no session.
func (s *Server) apiSetup(c *gin.Context) {
	if !s.setupAvailable(c) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	var req apiSetupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiAuthError(c, http.StatusBadRequest, api.CodeInvalidRequest, "The request is invalid.")
		return
	}
	ctx := c.Request.Context()
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" && s.bootstrapEmail != "" {
		// Containerized deployments fall back to BOOTSTRAP_ADMIN_EMAIL: when the request leaves the email blank, the env var value is used.
		email = strings.ToLower(strings.TrimSpace(s.bootstrapEmail))
	}
	outcome := s.attemptSetup(ctx,
		strings.TrimSpace(req.Username), email,
		strings.TrimSpace(req.DisplayName), req.Password, s.requestLocale(c))
	if outcome.Status == http.StatusNotFound {
		// An admin already exists is detected inside the gate: same response as the one-time gate at the entry point.
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if outcome.Code != "" {
		apiAuthError(c, outcome.Status, outcome.Code, registrationErrorMessage(outcome.Code))
		return
	}
	c.JSON(http.StatusOK, gin.H{"created": true})
}

// writeRegistrationError writes a registration failure result as a JSON error envelope.
// rate_limited adds Retry-After; internal_error is normalized to api.CodeInternal; the rest follow outcome.Status.
func (s *Server) writeRegistrationError(c *gin.Context, outcome registrationOutcome) {
	if outcome.Code == "rate_limited" {
		c.Header("Retry-After", strconv.Itoa(int(auth.DefaultAnonRateWindow.Seconds())))
	}
	if outcome.Code == "internal_error" {
		apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
		return
	}
	apiAuthError(c, outcome.Status, outcome.Code, registrationErrorMessage(outcome.Code))
}

// registrationErrorMessage returns the stable English fallback copy for a registration failure code.
// The frontend maps localized hints by code and never parses this message.
func registrationErrorMessage(code string) string {
	switch code {
	case "username_required":
		return "Enter a username."
	case "email_required":
		return "Enter an email address."
	case "email_invalid":
		return "Enter a valid email address."
	case "password_required":
		return "Enter a password."
	case "password_too_short":
		return "The password is too short."
	case "password_too_long":
		return "The password is too long."
	case "password_too_common":
		return "The password is too common."
	case "email_domain_not_allowed":
		return "This email domain is not allowed to register."
	case "invite_required":
		return "Registration requires an invite link."
	case "invite_invalid":
		return "The invite link is invalid."
	case "registration_closed":
		return "Self-service registration is closed."
	case "create_failed":
		return "The account could not be created."
	default:
		return "The request is invalid."
	}
}

// requestLocale returns the UI locale code resolved for the request, for writing into a new user's locale; returns an empty string when the localizer is missing.
func (s *Server) requestLocale(c *gin.Context) string {
	if loc := i18n.FromContext(c.Request.Context()); loc != nil {
		return loc.Locale()
	}
	return ""
}

// writeLoginLimited maps a limiter rejection into a response: 429 rate_limited with a Retry-After
// when the failure count reaches the threshold; 408 when the request is cancelled during the wait (client disconnected).
func (s *Server) writeLoginLimited(c *gin.Context, err error, username, ip string) {
	var locked *auth.LockedError
	if errors.As(err, &locked) {
		s.logger.Info("login attempt locked out", "username", username, "ip", ip, "retry_after", locked.RetryAfter)
		secs := int(locked.RetryAfter.Round(time.Second) / time.Second)
		if secs < 1 {
			secs = 1
		}
		c.Header("Retry-After", strconv.Itoa(secs))
		apiAuthError(c, http.StatusTooManyRequests, api.CodeRateLimited, "Too many failed attempts. Try again later.")
		return
	}
	s.logger.Info("login delay aborted", "error", err)
	c.AbortWithStatus(http.StatusRequestTimeout)
}

// apiAuthError writes the error envelope for auth-class SPA interfaces (same shape as totpError).
// The code is stable and English, the message is English fallback copy, and the frontend maps localized hints by code.
func apiAuthError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

// apiLogout invalidates the current session and clears the cookie.
//
// Mounts s.sessions.CSRFMiddleware to require the session-bound CSRF token.
// After logging out it resets the double-submit CSRF cookie and returns the matching token, so subsequent flows such as login work seamlessly.
func (s *Server) apiLogout(c *gin.Context) {
	ctx := c.Request.Context()
	var userID *uint64
	if u, ok := auth.CurrentUser(c); ok {
		userID = store.Ptr(u.ID)
	}
	if err := s.sessions.Logout(ctx, c); err != nil {
		s.logger.Error("logout failed", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    api.CodeInternal,
				"message": "An internal error occurred.",
			},
		})
		return
	}
	s.audit(ctx, store.AuditEntry{UserID: userID, Action: store.ActionUserLogout})
	doubleToken := auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	c.JSON(http.StatusOK, gin.H{
		"authenticated": false,
		"csrf_token":    doubleToken,
	})
}
