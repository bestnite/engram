package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the web layer: the credential cookie for the login second step, and the app shell entry of the two-step verification management page.
//
// Isolated from the shared hot spots: the routes and handlers all live in this file, and internal/web/auth.go only gets a route-registration insert,
// without reordering existing code. The login second step and the settings read/write protocols are same-origin JSON endpoints (totp_login.go / totp_api.go).

const (
	// totpPendingCookieName is the name of the short-lived credential cookie for "password passed, waiting for the second factor".
	totpPendingCookieName = "engram_totp_pending"
	// totpPendingTTL is the lifetime of the second-step credential: the password is already verified, so the window must be short.
	totpPendingTTL = 5 * time.Minute
)

// registerTOTPRoutes mounts all TOTP routes.
// Skips when the dependencies are not wired, so M0-stage tests can still construct a Server.
func (s *Server) registerTOTPRoutes(router *gin.Engine) {
	if s.totp == nil || s.sessions == nil || s.users == nil || s.accounts == nil {
		return
	}
	// The same-origin JSON protocol for the SPA login second step: GET reports whether a valid second-step
	// credential is held, POST submits the code and issues a session. Mounted with the double-submit cookie middleware (the pre-login flow has no session to bind a token to).
	router.GET("/api/v1/auth/totp", s.apiTOTPPending)
	router.POST("/api/v1/auth/totp", auth.PreSessionCSRFMiddleware(), s.apiTOTPSubmit)
}

// setTOTPPendingCookie issues the second-step credential: the content is unreadable to the client (HttpOnly + HMAC signature).
// No "account has TOTP enabled" marker is recorded here; the cookie only holds user_id and the expiry.
func (s *Server) setTOTPPendingCookie(c *gin.Context, userID uint64) {
	exp := time.Now().Add(totpPendingTTL).Unix()
	payload := fmt.Sprintf("%d.%d", userID, exp)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     totpPendingCookieName,
		Value:    s.sessions.SignValue(payload),
		Path:     "/",
		MaxAge:   int(totpPendingTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
}

// clearTOTPPendingCookie expires the second-step credential immediately (when login completes or the credential is invalid).
func (s *Server) clearTOTPPendingCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     totpPendingCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
}

// pendingTOTPUser validates the second-step credential and returns the user ID; a bad signature, a bad format or expiry all return false.
func (s *Server) pendingTOTPUser(c *gin.Context) (uint64, bool) {
	cookie, err := c.Request.Cookie(totpPendingCookieName)
	if err != nil {
		return 0, false
	}
	payload, ok := s.sessions.VerifyValue(cookie.Value)
	if !ok {
		return 0, false
	}
	idPart, expPart, ok := strings.Cut(payload, ".")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseUint(idPart, 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	exp, err := strconv.ParseInt(expPart, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return 0, false
	}
	return id, true
}

// verifyUserPassword checks the current user's local password; a pure OIDC account (no password) is always refused.
func verifyUserPassword(u *store.User, password string) error {
	if u.PasswordHash == nil {
		return auth.ErrInvalidCredentials
	}
	ok, err := auth.Verify(*u.PasswordHash, password)
	if err != nil {
		return err
	}
	if !ok {
		return auth.ErrInvalidCredentials
	}
	return nil
}
