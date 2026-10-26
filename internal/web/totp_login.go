package web

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// This file holds the same-origin JSON endpoints for the SPA login second step (TOTP).
//
// The second step has no deep-linkable GET page: what the SPA needs is a stateless "ask the status, then submit" protocol, so this
// provides two endpoints:
//
//   - GET  /api/v1/auth/totp reports whether the current request holds a valid second-step credential (pendingTOTPUser);
//   - POST /api/v1/auth/totp submits a code or a one-time recovery code; on success it issues a session and returns the current user.
//
// Both endpoints share setTOTPPendingCookie / pendingTOTPUser / VerifySecondFactor and the same login rate limiter,
// so rate limiting, constant-time comparison, auditing, recovery-code consumption and new-device alerts have exactly one implementation.
//
// The state-leak boundary: the pending credential is only issued after the first factor (password) passes, and it is an HttpOnly HMAC-signed value,
// so a GET with pending=true can only come from a caller that just passed the password; someone who has not passed the first factor cannot use it to probe
// "whether some account has TOTP enabled". When the credential is missing or expired, POST always returns the same error code, without distinguishing the reason.

// apiTOTPSubmitRequest is the request body of POST /api/v1/auth/totp.
// Code accepts both a 6-digit TOTP code and a one-time recovery code.
type apiTOTPSubmitRequest struct {
	Code string `json:"code"`
}

// apiTOTPPending reports whether the second step is in a submittable state (GET /api/v1/auth/totp).
//
// Read-only, no side effects: it does not refresh the credential lifetime, write an audit, or create a session. When the credential is invalid (not in the second step,
// expired, bad signature) it returns pending=false, and the frontend sends the user back to the first step.
func (s *Server) apiTOTPPending(c *gin.Context) {
	pending := false
	if s.totp != nil {
		if _, ok := s.pendingTOTPUser(c); ok {
			pending = true
		}
	}
	c.JSON(http.StatusOK, gin.H{"pending": pending})
}

// apiTOTPSubmit verifies the second factor and creates a session (POST /api/v1/auth/totp).
//
// Mounted with auth.PreSessionCSRFMiddleware: the request must carry both the csrf_double cookie and the X-CSRF-Token header.
// Failures reuse the login rate limiter: it shares the same
// account/IP dimensions as the first step, so the second step cannot become a gap that bypasses rate limiting.
func (s *Server) apiTOTPSubmit(c *gin.Context) {
	if s.totp == nil {
		// When TOTP is not wired the endpoint is unavailable; it is handled as "nonexistent", exposing no wiring details.
		apiAuthError(c, http.StatusNotFound, api.CodeNotFound, "The requested resource was not found.")
		return
	}

	var req apiTOTPSubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiAuthError(c, http.StatusBadRequest, api.CodeInvalidRequest, "The request is invalid.")
		return
	}

	ctx := c.Request.Context()
	userID, ok := s.pendingTOTPUser(c)
	if !ok {
		// No (or an expired) second-step credential: clear the leftover cookie and return to the first step, exposing no account state.
		s.clearTOTPPendingCookie(c)
		apiAuthError(c, http.StatusUnauthorized, api.CodeTOTPChallengeExpired,
			"The two-factor challenge has expired. Sign in again.")
		return
	}
	u, err := s.users.ByID(ctx, userID)
	if err != nil {
		s.logger.Info("totp second step for unknown user", "user_id", userID, "error", err)
		s.clearTOTPPendingCookie(c)
		apiAuthError(c, http.StatusUnauthorized, api.CodeTOTPChallengeExpired,
			"The two-factor challenge has expired. Sign in again.")
		return
	}

	// The second step shares the same rate limiter as the first: a failure escalates the delay too.
	if s.loginLimiter != nil {
		if _, err := s.loginLimiter.Wait(ctx, u.Username, c.ClientIP()); err != nil {
			s.writeLoginLimited(c, err, u.Username, c.ClientIP())
			return
		}
	}

	usedRecovery, verified, err := s.totp.VerifySecondFactor(ctx, userID, strings.TrimSpace(req.Code))
	if err != nil {
		s.logger.Error("totp verification failed", "user_id", userID, "error", err)
		apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
		return
	}
	if !verified {
		if s.loginLimiter != nil {
			s.loginLimiter.RecordFailure(u.Username, c.ClientIP())
		}
		s.logger.Info("totp second step rejected", "user_id", userID)
		s.audit(ctx, store.AuditEntry{
			UserID: store.Ptr(userID),
			Action: store.ActionTOTPVerifyFailed,
			Detail: map[string]any{"ip": c.ClientIP()},
		})
		apiAuthError(c, http.StatusUnauthorized, api.CodeTOTPInvalid, "The two-factor code is not valid.")
		return
	}
	if usedRecovery {
		// A recovery code was consumed: this is a security-relevant event and must leave a trace (both using one up and generating new ones are audited).
		s.audit(ctx, store.AuditEntry{
			UserID: store.Ptr(userID),
			Action: store.ActionTOTPRecoveryUsed,
			Detail: map[string]any{"ip": c.ClientIP()},
		})
	}
	if s.loginLimiter != nil {
		s.loginLimiter.Reset(u.Username, c.ClientIP())
	}
	s.clearTOTPPendingCookie(c)

	sess, err := s.sessions.StartSession(ctx, c, u.ID)
	if err != nil {
		s.logger.Error("start session failed", "user_id", u.ID, "error", err)
		apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID),
		Action: store.ActionUserLoginSucceeded,
		Detail: map[string]any{"ip": c.ClientIP(), "second_factor": "totp"},
	})
	// A login after the second factor passes also records the fingerprint and alerts on a new device / new IP.
	s.notifyNewDeviceLogin(c, u)

	c.JSON(http.StatusOK, gin.H{
		"authenticated": true,
		"user":          sessionUserPayload(u),
		"csrf_token":    sess.CSRFToken,
	})
}
