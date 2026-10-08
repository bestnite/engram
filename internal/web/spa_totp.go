package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 SPA 的 TOTP 管理接口：M1-16的 JSON 版本。
//
// 安全约定与登录第二步完全一致（同一份 auth.TOTPService 与审计/通知语义），只换传输形态：
//   - secret 与 otpauth 链接只在 begin 的响应里出现一次（与恢复码只在生成时出现一次同一约定）；
//   - GET 只报告状态（是否启用 / 是否有待确认绑定 / 剩余恢复码），绝不返回 secret、
//     otpauth 或任何恢复码明文；
//   - 恢复码明文只在 confirm（启用）与 recovery（重新生成）的响应里出现一次；
//   - 关闭与重新生成恢复码都要求重新输入密码。
//
// 全部写操作挂会话 CSRF，复用 auth.TOTPService 与既有审计/通知语义，不新增业务规则。
// 管理页的 GET /settings/totp 由应用壳应答（totpSettingsRoute），读写走 /api/v1/settings/totp*。

// totpStatusResponse 是 GET /api/v1/settings/totp 的响应体；不含任何秘密材料。
type totpStatusResponse struct {
	Enabled           bool  `json:"enabled"`
	Pending           bool  `json:"pending"`
	RecoveryRemaining int64 `json:"recovery_remaining"`
}

// totpBeginResponse 携带待确认绑定的 secret 与 otpauth 链接；只在此刻返回一次。
type totpBeginResponse struct {
	Secret     string `json:"secret"`
	OtpauthURL string `json:"otpauth_url"`
	Pending    bool   `json:"pending"`
}

// totpConfirmRequest 是确认绑定请求体。
type totpConfirmRequest struct {
	Code string `json:"code"`
}

// totpConfirmResponse 在启用成功时一次性返回恢复码明文。
type totpConfirmResponse struct {
	Enabled           bool     `json:"enabled"`
	RecoveryCodes     []string `json:"recovery_codes"`
	RecoveryRemaining int64    `json:"recovery_remaining"`
}

// totpPasswordRequest 是关闭与重新生成恢复码共用的请求体（都要密码确认）。
type totpPasswordRequest struct {
	Password string `json:"password"`
}

// totpDisableResponse 报告关闭后的状态。
type totpDisableResponse struct {
	Enabled bool `json:"enabled"`
}

// totpRecoveryResponse 一次性返回新一批恢复码明文。
type totpRecoveryResponse struct {
	RecoveryCodes     []string `json:"recovery_codes"`
	RecoveryRemaining int64    `json:"recovery_remaining"`
}

// registerTOTPAPIRoutes 挂载 SPA 的 TOTP 管理接口；写操作一律过会话 CSRF。
// 依赖未装配时跳过，保证 M0 阶段与未启用 TOTP 的测试仍能构造 Server。
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

// totpStatus 报告当前用户的 TOTP 状态，仅接受浏览器会话。
// pending 复用 Pending 的判定（未确认的绑定），但丢弃其 secret 与链接，只取布尔量。
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
	// 已启用的账号不可能存在待确认 secret；只在未启用时探测，避免多余的解密。
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

// totpBegin 生成 secret 并进入待确认状态；响应一次性返回 secret 与 otpauth 链接。
// 复用 auth.TOTPService.Begin 与 SSR 相同的 totp.begin 审计。
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
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionTOTPBegin,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
	})
	c.JSON(http.StatusOK, totpBeginResponse{Secret: secret, OtpauthURL: otpauth, Pending: true})
}

// totpConfirm 用一次验证码确认绑定；成功后启用并一次性返回恢复码。
// 复用 auth.TOTPService.Confirm 与 SSR 相同的 totp.enable 审计与凭据变更通知。
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
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionTOTPEnable,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
		Detail:     map[string]any{"recovery_codes": len(codes)},
	})
	// M1-19：凭据变更通知（TOTP 启用）；发信失败不影响启用。
	s.notifyCredentialChanged(ctx, user, "totp_enabled")
	c.JSON(http.StatusOK, totpConfirmResponse{Enabled: true, RecoveryCodes: codes, RecoveryRemaining: int64(len(codes))})
}

// totpDisable 关闭 TOTP：必须先通过密码确认。复用 auth.TOTPService.Disable。
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
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionTOTPDisable,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
	})
	// M1-19：凭据变更通知（TOTP 关闭）。
	s.notifyCredentialChanged(ctx, fresh, "totp_disabled")
	c.JSON(http.StatusOK, totpDisableResponse{Enabled: false})
}

// totpRecovery 重新生成一批恢复码（旧的未使用码立即作废），同样需要密码确认。
// 复用 auth.TOTPService.RegenerateRecoveryCodes 与 SSR 相同的审计与通知语义。
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
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionTOTPRecoveryRegenerate,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
		Detail:     map[string]any{"recovery_codes": len(codes)},
	})
	// M1-19：凭据变更通知（恢复码重新生成）。
	s.notifyCredentialChanged(ctx, fresh, "recovery_codes")
	c.JSON(http.StatusOK, totpRecoveryResponse{RecoveryCodes: codes, RecoveryRemaining: int64(len(codes))})
}

// totpError 写出 SPA TOTP 接口的错误包壳。
// 与 passwordError 同形：code 稳定且英文，message 为英文兜底文案，前端按 code 映射本地化提示。
func totpError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}
