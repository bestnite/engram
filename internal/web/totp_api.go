package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the SPA's TOTP management API: the JSON version of M1-16.
//
// The security contract is identical to the login second step (the same auth.TOTPService and audit/notification semantics); only the transport shape changes:
//   - the secret and otpauth link appear only once, in the begin response (the same rule as recovery codes appearing only once, when generated);
//   - GET only reports status (enabled or not / whether a binding is pending / remaining recovery codes) and never returns the secret,
//     otpauth, or any plaintext recovery code;
//   - plaintext recovery codes appear only once, in the confirm (enable) and recovery (regenerate) responses;
//   - disabling and regenerating recovery codes both require re-entering the password.
//
// Every write carries session CSRF and reuses auth.TOTPService and the existing audit/notification semantics, adding no business rules.
// The management page is /settings/security (app shell, the same page as the password change), with reads and writes going through /api/v1/settings/totp*.

// totpStatusResponse is the body of GET /api/v1/settings/totp; it holds no secret material.
type totpStatusResponse struct {
	Enabled           bool  `json:"enabled"`
	Pending           bool  `json:"pending"`
	RecoveryRemaining int64 `json:"recovery_remaining"`
}

// totpBeginResponse carries the secret and otpauth link of the pending binding; returned only once, at this moment.
type totpBeginResponse struct {
	Secret     string `json:"secret"`
	OtpauthURL string `json:"otpauth_url"`
	Pending    bool   `json:"pending"`
}

// totpConfirmRequest is the confirm-binding request body.
type totpConfirmRequest struct {
	Code string `json:"code"`
}

// totpConfirmResponse returns the plaintext recovery codes once, on a successful enable.
type totpConfirmResponse struct {
	Enabled           bool     `json:"enabled"`
	RecoveryCodes     []string `json:"recovery_codes"`
	RecoveryRemaining int64    `json:"recovery_remaining"`
}

// totpPasswordRequest is the request body shared by disable and recovery-code regeneration (both need password confirmation).
type totpPasswordRequest struct {
	Password string `json:"password"`
}

// totpDisableResponse reports the state after disabling.
type totpDisableResponse struct {
	Enabled bool `json:"enabled"`
}

// totpRecoveryResponse returns the new batch of plaintext recovery codes once.
type totpRecoveryResponse struct {
	RecoveryCodes     []string `json:"recovery_codes"`
	RecoveryRemaining int64    `json:"recovery_remaining"`
}

// registerTOTPAPIRoutes mounts the SPA's TOTP management API; every write goes through session CSRF.
// Skips when the dependencies are not wired, so M0-stage tests and tests with TOTP disabled can still construct a Server.
func (s *Server) registerTOTPAPIRoutes(router *gin.Engine) {
	if s.sessions == nil || s.users == nil || s.totp == nil {
		return
	}
	router.GET("/api/v1/settings/totp", s.totpStatus)
	router.POST("/api/v1/settings/totp/begin", s.sessions.CSRFMiddleware(), s.totpBegin)
	router.POST("/api/v1/settings/totp/confirm", s.sessions.CSRFMiddleware(), s.totpConfirm)
	router.POST("/api/v1/settings/totp/disable", s.sessions.CSRFMiddleware(), s.totpDisable)
	router.POST("/api/v1/settings/totp/recovery", s.sessions.CSRFMiddleware(), s.totpRecovery)
}

// totpStatus reports the current user's TOTP state and accepts browser sessions only.
// pending reuses Pending's decision (an unconfirmed binding) but discards its secret and link, keeping only the boolean.
func (s *Server) totpStatus(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	enabled, err := s.totp.Enabled(ctx, user.ID)
	if err != nil {
		s.logger.Error("load SPA totp status failed", "user_id", user.ID, "error", err)
		totpError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	resp := totpStatusResponse{Enabled: enabled}
	// An enabled account cannot have a pending secret; this only probes when not enabled, avoiding a needless decryption.
	if !enabled {
		_, _, pending, err := s.totp.Pending(ctx, user.ID, user.Username)
		if err != nil {
			s.logger.Error("load SPA totp pending state failed", "user_id", user.ID, "error", err)
			totpError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
			return
		}
		resp.Pending = pending
	}
	if enabled {
		n, err := s.totp.UnusedRecoveryCodeCount(ctx, user.ID)
		if err != nil {
			s.logger.Error("count SPA totp recovery codes failed", "user_id", user.ID, "error", err)
			totpError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
			return
		}
		resp.RecoveryRemaining = n
	}
	c.JSON(http.StatusOK, resp)
}

// totpBegin generates a secret and enters the pending state; the response returns the secret and otpauth link once.
// Reuses auth.TOTPService.Begin and the same totp.begin audit as SSR.
func (s *Server) totpBegin(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	secret, otpauth, err := s.totp.Begin(ctx, user.ID, user.Username)
	if err != nil {
		if errors.Is(err, auth.ErrTOTPAlreadyEnabled) {
			totpError(c, http.StatusConflict, "already_enabled", "Two-factor authentication is already enabled.")
			return
		}
		s.logger.Error("totp begin failed", "user_id", user.ID, "error", err)
		totpError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionTOTPBegin,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
	})
	c.JSON(http.StatusOK, totpBeginResponse{Secret: secret, OtpauthURL: otpauth, Pending: true})
}

// totpConfirm confirms the binding with one code; on success it enables and returns the recovery codes once.
// Reuses auth.TOTPService.Confirm and the same totp.enable audit and credential-change notification as SSR.
func (s *Server) totpConfirm(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	var req totpConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		totpError(c, http.StatusBadRequest, "invalid_request", "Enter the code from your authenticator app.")
		return
	}
	ctx := c.Request.Context()
	codes, err := s.totp.Confirm(ctx, user.ID, req.Code)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrTOTPInvalidCode):
			totpError(c, http.StatusBadRequest, "invalid_code", "The code is not valid.")
		case errors.Is(err, auth.ErrTOTPAlreadyEnabled):
			totpError(c, http.StatusConflict, "already_enabled", "Two-factor authentication is already enabled.")
		case errors.Is(err, auth.ErrTOTPNoPendingSetup), errors.Is(err, store.ErrTOTPRecordNotFound):
			totpError(c, http.StatusBadRequest, "no_pending_setup", "There is no pending setup; start again.")
		default:
			s.logger.Error("totp confirm failed", "user_id", user.ID, "error", err)
			totpError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		}
		return
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionTOTPEnable,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
		Detail:     map[string]any{"recovery_codes": len(codes)},
	})
	// Credential-change notification (TOTP enabled); a failed send does not affect the enable.
	s.notifyCredentialChanged(ctx, user, "totp_enabled")
	c.JSON(http.StatusOK, totpConfirmResponse{Enabled: true, RecoveryCodes: codes, RecoveryRemaining: int64(len(codes))})
}

// totpDisable disables TOTP: the password must be confirmed first. Reuses auth.TOTPService.Disable.
func (s *Server) totpDisable(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	var req totpPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Password == "" {
		totpError(c, http.StatusBadRequest, "invalid_request", "Enter your password.")
		return
	}
	ctx := c.Request.Context()
	fresh, err := s.users.ByID(ctx, user.ID)
	if err != nil {
		s.logger.Error("load user for SPA totp disable failed", "user_id", user.ID, "error", err)
		totpError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	if err := verifyUserPassword(fresh, req.Password); err != nil {
		totpError(c, http.StatusUnauthorized, "invalid_password", "The password is not correct.")
		return
	}
	if err := s.totp.Disable(ctx, user.ID); err != nil {
		if errors.Is(err, auth.ErrTOTPNotEnabled) {
			totpError(c, http.StatusConflict, "not_enabled", "Two-factor authentication is not enabled for this account.")
			return
		}
		s.logger.Error("totp disable failed", "user_id", user.ID, "error", err)
		totpError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionTOTPDisable,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
	})
	// Credential-change notification (TOTP disabled).
	s.notifyCredentialChanged(ctx, fresh, "totp_disabled")
	c.JSON(http.StatusOK, totpDisableResponse{Enabled: false})
}

// totpRecovery regenerates a batch of recovery codes (unused old codes are void at once) and likewise needs password confirmation.
// Reuses auth.TOTPService.RegenerateRecoveryCodes and the same audit and notification semantics as SSR.
func (s *Server) totpRecovery(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	var req totpPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Password == "" {
		totpError(c, http.StatusBadRequest, "invalid_request", "Enter your password.")
		return
	}
	ctx := c.Request.Context()
	fresh, err := s.users.ByID(ctx, user.ID)
	if err != nil {
		s.logger.Error("load user for SPA recovery regeneration failed", "user_id", user.ID, "error", err)
		totpError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	if err := verifyUserPassword(fresh, req.Password); err != nil {
		totpError(c, http.StatusUnauthorized, "invalid_password", "The password is not correct.")
		return
	}
	enabled, err := s.totp.Enabled(ctx, user.ID)
	if err != nil {
		s.logger.Error("totp enabled check failed", "user_id", user.ID, "error", err)
		totpError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	if !enabled {
		totpError(c, http.StatusConflict, "not_enabled", "Two-factor authentication is not enabled for this account.")
		return
	}
	codes, err := s.totp.RegenerateRecoveryCodes(ctx, user.ID)
	if err != nil {
		s.logger.Error("regenerate recovery codes failed", "user_id", user.ID, "error", err)
		totpError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionTOTPRecoveryRegenerate,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
		Detail:     map[string]any{"recovery_codes": len(codes)},
	})
	// Credential-change notification (recovery codes regenerated).
	s.notifyCredentialChanged(ctx, fresh, "recovery_codes")
	c.JSON(http.StatusOK, totpRecoveryResponse{RecoveryCodes: codes, RecoveryRemaining: int64(len(codes))})
}

// totpError writes the error envelope of the SPA TOTP API.
// Same shape as passwordError: code is stable and English, message is the English fallback text, and the frontend maps code to a localized hint.
func totpError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}
