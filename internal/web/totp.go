package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"example.com/engram/internal/auth"
	"example.com/engram/internal/i18n"
	"example.com/engram/internal/store"
	"example.com/engram/internal/web/views"
)

// 本文件是 M1-16 的 Web 层：登录第二步、启用/关闭/恢复码的设置页。
//
// 与共享热点隔离：路由与 handler 全部落在本文件，对 internal/web/auth.go 只做两处最小插入
// （第二步路由注册、登录成功后进入第二步的钩子），不重排既有代码。

const (
	// totpPendingCookieName 是「密码已通过、等待第二因素」的短期凭据 cookie 名。
	totpPendingCookieName = "engram_totp_pending"
	// totpPendingTTL 是第二步凭据的有效期：密码已验证，窗口必须短。
	totpPendingTTL = 5 * time.Minute
)

// registerTOTPRoutes 挂载 TOTP 的全部路由（M1-16）。
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerTOTPRoutes(router *gin.Engine) {
	if s.totp == nil || s.sessions == nil || s.users == nil || s.accounts == nil {
		return
	}
	// 第二步与 /login 同属登录前流程：没有会话可绑 CSRF token，用双提交 cookie（B-13）。
	router.POST("/login/totp", auth.DoubleSubmitMiddleware(), s.totpSubmit)
	router.GET("/settings/totp", s.totpSettingsPage)
	router.POST("/settings/totp/begin", s.sessions.CSRFMiddleware(), s.totpBeginSubmit)
	router.POST("/settings/totp/confirm", s.sessions.CSRFMiddleware(), s.totpConfirmSubmit)
	router.POST("/settings/totp/disable", s.sessions.CSRFMiddleware(), s.totpDisableSubmit)
	router.POST("/settings/totp/recovery", s.sessions.CSRFMiddleware(), s.totpRecoverySubmit)
}

// beginTOTPChallengeIfEnabled 在密码校验通过后判断是否需要第二因素。
//
// 返回值报告「是否已接管响应」：true 表示已渲染第二步（或已中止请求），调用方必须直接返回。
// 只有密码正确的请求才会走到这里，因此不会向未通过第一因素的人泄露「该账号是否启用 TOTP」。
func (s *Server) beginTOTPChallengeIfEnabled(c *gin.Context, loc *i18n.Localizer, u *store.User) bool {
	if s.totp == nil {
		return false
	}
	enabled, err := s.totp.Enabled(c.Request.Context(), u.ID)
	if err != nil {
		s.logger.Error("totp enabled check failed", "user_id", u.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return true
	}
	if !enabled {
		return false
	}
	s.setTOTPPendingCookie(c, u.ID)
	s.renderTOTPChallenge(c, loc, http.StatusOK, "")
	return true
}

// totpSubmit 处理登录第二步：校验验证码或一次性恢复码，通过后才建立会话。
//
// 失败复用登录限速（M1-9）：与第一步共用同一账号/IP 维度，第二步不能成为绕过限速的缺口。
func (s *Server) totpSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	userID, ok := s.pendingTOTPUser(c)
	if !ok {
		// 没有（或已过期的）第二步凭据：回到登录页从头开始，不暴露任何状态。
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	u, err := s.users.ByID(ctx, userID)
	if err != nil {
		s.logger.Info("totp second step for unknown user", "user_id", userID, "error", err)
		s.clearTOTPPendingCookie(c)
		c.Redirect(http.StatusSeeOther, "/login")
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
	usedRecovery, verified, err := s.totp.VerifySecondFactor(ctx, userID, c.PostForm("code"))
	if err != nil {
		s.logger.Error("totp verification failed", "user_id", userID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
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
		s.renderTOTPChallenge(c, loc, http.StatusUnauthorized, loc.T("totp.error.invalid_code"))
		return
	}
	if usedRecovery {
		// 恢复码被消费：这是安全相关事件，必须留痕（M1-16 要求用掉/生成新的都有审计）。
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
	if _, err := s.sessions.StartSession(ctx, c, u.ID); err != nil {
		s.logger.Error("start session failed", "user_id", u.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID),
		Action: store.ActionUserLoginSucceeded,
		Detail: map[string]any{"ip": c.ClientIP(), "second_factor": "totp"},
	})
	c.Redirect(http.StatusSeeOther, "/")
}

// setTOTPPendingCookie 下发第二步凭据：内容对客户端不可读（HttpOnly + HMAC 签名）。
// 不在这里记录任何「账号已启用 TOTP」的标记；cookie 里只有 user_id 与过期时间。
func (s *Server) setTOTPPendingCookie(c *gin.Context, userID uint64) {
	exp := time.Now().Add(totpPendingTTL).Unix()
	payload := fmt.Sprintf("%d.%d", userID, exp)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     totpPendingCookieName,
		Value:    s.sessions.SignValue(payload),
		Path:     "/",
		MaxAge:   int(totpPendingTTL.Seconds()),
		HttpOnly: true,
		Secure:   strings.HasPrefix(s.baseURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	})
}

// clearTOTPPendingCookie 立即过期第二步凭据（登录完成或凭据失效时）。
func (s *Server) clearTOTPPendingCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     totpPendingCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   strings.HasPrefix(s.baseURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	})
}

// pendingTOTPUser 校验第二步凭据并返回用户 ID；签名不对、格式不对或已过期都返回 false。
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

// renderTOTPChallenge 写出登录第二步页面；status 用于把校验失败渲染成 4xx。
func (s *Server) renderTOTPChallenge(c *gin.Context, loc *i18n.Localizer, status int, errMsg string) {
	data := views.TOTPChallengeData{
		Layout:       s.authLayout(loc, "totp.login.title"),
		Heading:      loc.T("totp.login.heading"),
		Intro:        loc.T("totp.login.intro"),
		CodeLabel:    loc.T("totp.login.code_label"),
		RecoveryHint: loc.T("totp.login.recovery_hint"),
		SubmitLabel:  loc.T("totp.login.submit"),
		ErrorMessage: errMsg,
		CSRF:         auth.EnsureDoubleSubmitToken(c),
		LangOptions:  s.languageOptionsFor(loc, c.Request.URL.Path),
	}
	renderHTMLStatus(c, status, views.TOTPChallengePage(data))
}

// totpSettingsPage 渲染个人设置页里的 TOTP 区块。
func (s *Server) totpSettingsPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	s.renderTOTPSettings(c, loc, user, http.StatusOK, "", "", nil)
}

// totpBeginSubmit 生成 secret 并进入待确认状态；页面展示 otpauth 链接与明文 secret（仅此一次）。
func (s *Server) totpBeginSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if _, _, err := s.totp.Begin(ctx, user.ID, user.Username); err != nil {
		if errors.Is(err, auth.ErrTOTPAlreadyEnabled) {
			s.renderTOTPSettings(c, loc, user, http.StatusForbidden, loc.T("totp.error.already_enabled"), "", nil)
			return
		}
		s.logger.Error("totp begin failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionTOTPBegin,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
	})
	s.renderTOTPSettings(c, loc, user, http.StatusOK, "", "", nil)
}

// totpConfirmSubmit 用一次验证码确认绑定；成功后启用并一次性展示恢复码。
func (s *Server) totpConfirmSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	codes, err := s.totp.Confirm(ctx, user.ID, c.PostForm("code"))
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrTOTPInvalidCode):
			s.renderTOTPSettings(c, loc, user, http.StatusBadRequest, loc.T("totp.error.invalid_code"), "", nil)
		case errors.Is(err, auth.ErrTOTPAlreadyEnabled):
			s.renderTOTPSettings(c, loc, user, http.StatusForbidden, loc.T("totp.error.already_enabled"), "", nil)
		case errors.Is(err, auth.ErrTOTPNoPendingSetup), errors.Is(err, store.ErrTOTPRecordNotFound):
			s.renderTOTPSettings(c, loc, user, http.StatusBadRequest, loc.T("totp.error.no_pending"), "", nil)
		default:
			s.logger.Error("totp confirm failed", "user_id", user.ID, "error", err)
			c.AbortWithStatus(http.StatusInternalServerError)
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
	s.renderTOTPSettings(c, loc, user, http.StatusOK, "", loc.T("totp.saved.enabled"), codes)
}

// totpDisableSubmit 关闭 TOTP：必须先通过密码（或现有一因素）确认。
func (s *Server) totpDisableSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	fresh, err := s.users.ByID(ctx, user.ID)
	if err != nil {
		s.logger.Error("load user for totp disable failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if err := verifyUserPassword(fresh, c.PostForm("password")); err != nil {
		s.renderTOTPSettings(c, loc, fresh, http.StatusUnauthorized, loc.T(passwordConfirmErrorKey(err)), "", nil)
		return
	}
	if err := s.totp.Disable(ctx, user.ID); err != nil {
		if errors.Is(err, auth.ErrTOTPNotEnabled) {
			s.renderTOTPSettings(c, loc, fresh, http.StatusBadRequest, loc.T("totp.error.not_enabled"), "", nil)
			return
		}
		s.logger.Error("totp disable failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionTOTPDisable,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
	})
	s.renderTOTPSettings(c, loc, fresh, http.StatusOK, "", loc.T("totp.saved.disabled"), nil)
}

// totpRecoverySubmit 重新生成一批恢复码（旧的未使用码立即作废），同样需要密码确认。
func (s *Server) totpRecoverySubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	fresh, err := s.users.ByID(ctx, user.ID)
	if err != nil {
		s.logger.Error("load user for recovery regeneration failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if err := verifyUserPassword(fresh, c.PostForm("password")); err != nil {
		s.renderTOTPSettings(c, loc, fresh, http.StatusUnauthorized, loc.T(passwordConfirmErrorKey(err)), "", nil)
		return
	}
	enabled, err := s.totp.Enabled(ctx, user.ID)
	if err != nil {
		s.logger.Error("totp enabled check failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if !enabled {
		s.renderTOTPSettings(c, loc, fresh, http.StatusBadRequest, loc.T("totp.error.not_enabled"), "", nil)
		return
	}
	codes, err := s.totp.RegenerateRecoveryCodes(ctx, user.ID)
	if err != nil {
		s.logger.Error("regenerate recovery codes failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionTOTPRecoveryRegenerate,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
		Detail:     map[string]any{"recovery_codes": len(codes)},
	})
	s.renderTOTPSettings(c, loc, fresh, http.StatusOK, "", loc.T("totp.saved.recovery"), codes)
}

// renderTOTPSettings 组装并写出 TOTP 设置区块；codes 非空时一次性展示恢复码。
func (s *Server) renderTOTPSettings(c *gin.Context, loc *i18n.Localizer, user *store.User, status int, errMsg, savedMsg string, codes []string) {
	data, err := s.totpSettingsData(c, loc, user, errMsg, savedMsg)
	if err != nil {
		s.logger.Error("build totp settings data failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	data.RecoveryCodes = codes
	renderHTMLStatus(c, status, views.TOTPSettingsPage(data))
}

// totpSettingsData 组装 TOTP 设置页数据；文案全部取自语言包。
func (s *Server) totpSettingsData(c *gin.Context, loc *i18n.Localizer, user *store.User, errMsg, savedMsg string) (views.TOTPSettingsData, error) {
	ctx := c.Request.Context()
	enabled, err := s.totp.Enabled(ctx, user.ID)
	if err != nil {
		return views.TOTPSettingsData{}, err
	}
	data := views.TOTPSettingsData{
		Layout:              s.pageLayout(c, loc, "totp.settings.title"),
		Heading:             loc.T("totp.settings.heading"),
		Intro:               loc.T("totp.settings.intro"),
		ErrorMessage:        errMsg,
		SavedMessage:        savedMsg,
		Enabled:             enabled,
		StatusEnabledLabel:  loc.T("totp.status.enabled"),
		StatusDisabledLabel: loc.T("totp.status.disabled"),
		BeginHint:           loc.T("totp.begin.hint"),
		BeginSubmit:         loc.T("totp.begin.submit"),
		PendingHeading:      loc.T("totp.pending.heading"),
		SecretLabel:         loc.T("totp.pending.secret_label"),
		OtpauthLabel:        loc.T("totp.pending.otpauth_label"),
		ConfirmHint:         loc.T("totp.pending.confirm_hint"),
		ConfirmSubmit:       loc.T("totp.pending.confirm_submit"),
		DisableHeading:      loc.T("totp.disable.heading"),
		DisableHint:         loc.T("totp.disable.hint"),
		PasswordLabel:       loc.T("totp.disable.password_label"),
		DisableSubmit:       loc.T("totp.disable.submit"),
		RecoveryHeading:     loc.T("totp.recovery.heading"),
		RecoveryWarning:     loc.T("totp.recovery.warning"),
		RecoveryCodesLabel:  loc.T("totp.recovery.list_label"),
		RecoveryRegenSubmit: loc.T("totp.recovery.regenerate_submit"),
		CSRF:                sessionCSRF(c),
	}
	// 待确认的绑定：刷新设置页仍能看到 secret 与链接，不必重新生成。
	if !enabled {
		secret, otpauth, pending, err := s.totp.Pending(ctx, user.ID, user.Username)
		if err != nil {
			return views.TOTPSettingsData{}, err
		}
		data.Pending = pending
		data.SecretValue = secret
		data.OtpauthURL = otpauth
	}
	if enabled {
		n, err := s.totp.UnusedRecoveryCodeCount(ctx, user.ID)
		if err != nil {
			return views.TOTPSettingsData{}, err
		}
		data.RecoveryRemaining = loc.T("totp.recovery.remaining") + " " + strconv.FormatInt(n, 10)
	}
	return data, nil
}

// verifyUserPassword 校验当前用户的本地密码；纯 OIDC 账号（无密码）一律拒绝。
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

// passwordConfirmErrorKey 把密码确认失败映射到语言包 key。
func passwordConfirmErrorKey(err error) string {
	if errors.Is(err, auth.ErrInvalidCredentials) {
		return "totp.error.password_wrong"
	}
	return "totp.error.password_required"
}
