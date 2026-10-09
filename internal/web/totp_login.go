package web

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 SPA 登录第二步（TOTP）的同源 JSON 端点。
//
// 第二步没有可深链的 GET 页面：SPA 需要的是「先问状态、再提交」的无状态协议，因此这里
// 提供两条端点：
//
//   - GET  /api/v1/auth/totp 报告当前请求是否持有有效的第二步凭据（pendingTOTPUser）；
//   - POST /api/v1/auth/totp 提交验证码或一次性恢复码，通过后签发会话并返回当前用户。
//
// 两条端点共用 setTOTPPendingCookie / pendingTOTPUser / VerifySecondFactor 与同一个登录限速器，
// 因此限速、恒定耗时、审计、恢复码消费与新设备提醒的语义只有一份实现。
//
// 状态泄露边界：pending 凭据只在第一因素（密码）通过后才下发，且它是 HttpOnly 的 HMAC 签名值，
// 因此 GET 的 pending=true 只可能来自刚通过密码的调用方；未通过第一因素的人无法据此探测
// 「某账号是否启用 TOTP」。凭据缺失或过期时 POST 一律返回同一个错误 code，不区分原因。

// apiTOTPSubmitRequest 是 POST /api/v1/auth/totp 的请求体。
// Code 同时接受 6 位动态验证码与一次性恢复码。
type apiTOTPSubmitRequest struct {
	Code string `json:"code"`
}

// apiTOTPPending 报告第二步是否处于可提交状态（GET /api/v1/auth/totp）。
//
// 只读、无副作用：不刷新凭据有效期、不写审计、不建立会话。凭据无效（未登录第二步、
// 已过期、签名不符）时返回 pending=false，前端据此把用户送回第一步。
func (s *Server) apiTOTPPending(c *gin.Context) {
	pending := false
	if s.totp != nil {
		if _, ok := s.pendingTOTPUser(c); ok {
			pending = true
		}
	}
	c.JSON(http.StatusOK, gin.H{"pending": pending})
}

// apiTOTPSubmit 校验第二因素并建立会话（POST /api/v1/auth/totp）。
//
// 挂载 auth.PreSessionCSRFMiddleware：请求必须同时携带 csrf_double cookie 与 X-CSRF-Token 头。
// 失败复用登录限速：与第一步共用同一
// 账号/IP 维度，第二步不能成为绕过限速的缺口。
func (s *Server) apiTOTPSubmit(c *gin.Context) {
	if s.totp == nil {
		// TOTP 未装配时端点不可用；按「不存在」处理，不暴露装配细节。
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
		// 没有（或已过期的）第二步凭据：清掉残留 cookie 并回到第一步，不暴露任何账号状态。
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

	// 第二步与第一步共用同一限速器：失败同样递增延迟。
	if s.loginLimiter != nil {
		if _, err := s.loginLimiter.Wait(ctx, u.Username, c.ClientIP()); err != nil {
			s.logger.Info("totp second step delay aborted", "error", err)
			c.AbortWithStatus(http.StatusRequestTimeout)
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
		// 恢复码被消费：这是安全相关事件，必须留痕（要求用掉/生成新的都有审计）。
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
	// 第二因素通过后的登录同样记录指纹并在新设备/新 IP 时提醒。
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
