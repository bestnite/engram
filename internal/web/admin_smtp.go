package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"example.com/engram/internal/auth"
	"example.com/engram/internal/i18n"
	"example.com/engram/internal/mail"
	"example.com/engram/internal/store"
	"example.com/engram/internal/web/views"
)

// SMTP 配置页（DESIGN.md §4.7；AGENTS.md M1-17）。
//
// 页面展示：host / port / username / from / TLS 模式，口令只显示「已配置/未配置」（走
// store.SecretCodec 加密存储），以及「测试连接」按钮（把服务端错误文本显示在页面上）。
// 底部是 outbox 读数：是否已配置、待投递/失败计数、最后一次错误与尝试次数。
// 所有写操作过 CSRF、写审计；用户可见文案全部来自语言包。

// adminSMTPPage 渲染 SMTP 配置页。
func (s *Server) adminSMTPPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	data, err := s.smtpAdminData(c, loc, "", false)
	if err != nil {
		s.logger.Error("admin: build smtp page data failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	renderHTML(c, views.AdminPage(data))
}

// adminSMTPTest 执行「测试连接」：用表单里的值（缺省回落到已保存值）做一次连接与认证握手，
// 失败时把服务端的原始错误文本渲染在页面上（M1-17 验收点）。
func (s *Server) adminSMTPTest(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	resolver := mail.NewResolver(s.db, s.secrets)
	cfg, _, err := resolver.Config(ctx)
	if err != nil {
		s.logger.Error("admin: resolve smtp config failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	// 表单值覆盖已保存值；留空表示沿用已保存值（口令不回显，只能沿用）。
	if v := strings.TrimSpace(c.PostForm(mail.SettingKeySMTPHost)); v != "" {
		cfg.Host = v
	}
	if v := strings.TrimSpace(c.PostForm(mail.SettingKeySMTPPort)); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n >= 1 && n <= 65535 {
			cfg.Port = n
		}
	}
	if v := strings.TrimSpace(c.PostForm(mail.SettingKeySMTPUsername)); v != "" {
		cfg.Username = v
	}
	if v := strings.TrimSpace(c.PostForm(mail.SettingKeySMTPFrom)); v != "" {
		cfg.From = v
	}
	if v := strings.TrimSpace(c.PostForm(mail.SettingKeySMTPTLSMode)); v != "" {
		cfg.TLSMode = mail.NormalizeTLSMode(v)
	}
	if pw := c.PostForm(mail.SettingKeySMTPPassword); strings.TrimSpace(pw) != "" {
		cfg.Password = pw
	}

	result := ""
	testOK := false
	if cfg.Host == "" {
		result = loc.T("admin.setting.smtp.test.no_host")
	} else if terr := mail.TestConnection(ctx, cfg); terr != nil {
		// 原样带上服务端/网络层的错误文本，而不是只写日志（DESIGN.md §4.7）。
		s.logger.Info("admin: smtp test connection failed", "host", cfg.Host, "port", cfg.Port, "error", terr)
		result = loc.T("admin.setting.smtp.test.failed_prefix") + " " + terr.Error()
	} else {
		result = loc.T("admin.setting.smtp.test.ok")
		testOK = true
	}
	data, derr := s.smtpAdminData(c, loc, result, testOK)
	if derr != nil {
		s.logger.Error("admin: build smtp page data failed", "error", derr)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	renderHTML(c, views.AdminPage(data))
}

// adminSMTPSave 保存 SMTP 配置：非敏感值走 PutSetting，口令走 PutSecret（加密）。
// 校验端口与 TLS 模式，写审计，再重定向回页面。
func (s *Server) adminSMTPSave(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if _, ok := s.localizer(c); !ok {
		return
	}
	ctx := c.Request.Context()
	now := time.Now().UTC()

	if raw := strings.TrimSpace(c.PostForm(mail.SettingKeySMTPPort)); raw != "" {
		if n, err := strconv.Atoi(raw); err != nil || n < 1 || n > 65535 {
			c.Redirect(http.StatusSeeOther, "/admin/smtp?notice=invalid_port")
			return
		}
	}
	if raw := strings.TrimSpace(c.PostForm(mail.SettingKeySMTPTLSMode)); raw != "" {
		if !mail.ValidTLSMode(raw) {
			c.Redirect(http.StatusSeeOther, "/admin/smtp?notice=invalid_tls_mode")
			return
		}
	}

	changed := make([]string, 0, 6)
	for _, key := range []string{
		mail.SettingKeySMTPHost, mail.SettingKeySMTPPort, mail.SettingKeySMTPUsername,
		mail.SettingKeySMTPFrom, mail.SettingKeySMTPTLSMode,
	} {
		raw := strings.TrimSpace(c.PostForm(key))
		if raw == "" {
			// 留空表示不修改，与系统设置页一致。
			continue
		}
		if err := store.PutSetting(ctx, s.db, key, raw, store.Ptr(u.ID), now); err != nil {
			s.logger.Error("admin: save smtp setting failed", "key", key, "error", err)
			c.Redirect(http.StatusSeeOther, "/admin/smtp?notice=save_failed")
			return
		}
		changed = append(changed, key)
	}
	if s.secrets != nil {
		if pw := c.PostForm(mail.SettingKeySMTPPassword); strings.TrimSpace(pw) != "" {
			if err := store.PutSecret(ctx, s.db, s.secrets, mail.SettingKeySMTPPassword, pw, store.Ptr(u.ID), now); err != nil {
				s.logger.Error("admin: save smtp password failed", "error", err)
				c.Redirect(http.StatusSeeOther, "/admin/smtp?notice=save_failed")
				return
			}
			changed = append(changed, mail.SettingKeySMTPPassword)
		}
	}

	if len(changed) > 0 {
		s.audit(ctx, store.AuditEntry{
			UserID: store.Ptr(u.ID), Action: store.ActionSettingUpdate,
			TargetType: "setting", Detail: map[string]any{"keys": changed, "scope": "smtp"},
		})
	}
	c.Redirect(http.StatusSeeOther, "/admin/smtp?notice=saved")
}

// smtpAdminData 组装 SMTP 配置页的渲染数据；testResult 为空时不渲染测试结论。
func (s *Server) smtpAdminData(c *gin.Context, loc *i18n.Localizer, testResult string, testOK bool) (views.AdminPageData, error) {
	ctx := c.Request.Context()
	resolver := mail.NewResolver(s.db, s.secrets)

	host, hostSrc, err := resolver.Field(ctx, mail.SettingKeySMTPHost)
	if err != nil {
		return views.AdminPageData{}, err
	}
	port, portSrc, err := resolver.Field(ctx, mail.SettingKeySMTPPort)
	if err != nil {
		return views.AdminPageData{}, err
	}
	username, usernameSrc, err := resolver.Field(ctx, mail.SettingKeySMTPUsername)
	if err != nil {
		return views.AdminPageData{}, err
	}
	from, fromSrc, err := resolver.Field(ctx, mail.SettingKeySMTPFrom)
	if err != nil {
		return views.AdminPageData{}, err
	}
	tlsMode, _, err := resolver.Field(ctx, mail.SettingKeySMTPTLSMode)
	if err != nil {
		return views.AdminPageData{}, err
	}
	tlsMode = mail.NormalizeTLSMode(tlsMode)
	pwConfigured, pwSrc, err := resolver.PasswordConfigured(ctx)
	if err != nil {
		return views.AdminPageData{}, err
	}
	_, configured, err := resolver.Config(ctx)
	if err != nil {
		return views.AdminPageData{}, err
	}

	pwStatus := loc.T("admin.sensitive.not_configured")
	if pwConfigured {
		pwStatus = loc.T("admin.sensitive.configured")
	}
	summary, err := store.OutboxSummaryOf(ctx, s.db)
	if err != nil {
		return views.AdminPageData{}, err
	}
	lastError := loc.T("admin.setting.smtp.outbox.no_error")
	attempts := loc.T("admin.setting.smtp.outbox.none")
	if summary.LastError != "" {
		lastError = summary.LastError
		attempts = strconv.Itoa(summary.LastAttempts)
	}
	configuredStatus := loc.T("admin.sensitive.not_configured")
	if configured {
		configuredStatus = loc.T("admin.sensitive.configured")
	}
	// D 类管理员通知的可用状态（M1-24）：未配置时渲染 mail.not_configured，绝不静默。
	adminNotifyStatus := loc.T("mail.not_configured")
	if configured {
		adminNotifyStatus = loc.T("mail.admin.status.ready")
	}

	csrf := ""
	if sess, ok := auth.CurrentSession(c); ok {
		csrf = sess.CSRFToken
	}
	smtp := &views.SMTPPageData{
		FormAction:     "/admin/smtp",
		TestAction:     "/admin/smtp/test",
		CSRF:           csrf,
		HostLabel:      loc.T("admin.setting.smtp.host"),
		HostHint:       loc.T("admin.setting.smtp.host.hint"),
		HostValue:      host,
		HostSource:     sourceLabel(loc, hostSrc),
		PortLabel:      loc.T("admin.setting.smtp.port"),
		PortHint:       loc.T("admin.setting.smtp.port.hint"),
		PortValue:      port,
		PortSource:     sourceLabel(loc, portSrc),
		UsernameLabel:  loc.T("admin.setting.smtp.username"),
		UsernameHint:   loc.T("admin.setting.smtp.username.hint"),
		UsernameValue:  username,
		UsernameSource: sourceLabel(loc, usernameSrc),
		PasswordLabel:  loc.T("admin.setting.smtp.password"),
		PasswordHint:   loc.T("admin.setting.smtp.password.hint"),
		PasswordStatus: pwStatus,
		PasswordSource: sourceLabel(loc, pwSrc),
		FromLabel:      loc.T("admin.setting.smtp.from"),
		FromHint:       loc.T("admin.setting.smtp.from.hint"),
		FromValue:      from,
		FromSource:     sourceLabel(loc, fromSrc),
		TLSModeLabel:   loc.T("admin.setting.smtp.tls_mode"),
		TLSModeHint:    loc.T("admin.setting.smtp.tls_mode.hint"),
		TLSModeValue:   tlsMode,
		TLSModeOptions: []views.AdminOption{
			{Value: mail.TLSModeNone, Label: loc.T("admin.setting.smtp.tls_mode.none"), Selected: tlsMode == mail.TLSModeNone},
			{Value: mail.TLSModeStartTLS, Label: loc.T("admin.setting.smtp.tls_mode.starttls"), Selected: tlsMode == mail.TLSModeStartTLS},
			{Value: mail.TLSModeImplicit, Label: loc.T("admin.setting.smtp.tls_mode.implicit"), Selected: tlsMode == mail.TLSModeImplicit},
		},
		SaveLabel:              loc.T("admin.action.save"),
		TestLabel:              loc.T("admin.setting.smtp.test"),
		TestResult:             testResult,
		TestOK:                 testOK,
		OutboxHeading:          loc.T("admin.setting.smtp.outbox.heading"),
		OutboxIntro:            loc.T("admin.setting.smtp.outbox.intro"),
		OutboxConfiguredLabel:  loc.T("admin.setting.smtp.outbox.configured_label"),
		OutboxConfiguredStatus: configuredStatus,
		OutboxPendingLabel:     loc.T("admin.setting.smtp.outbox.pending"),
		OutboxPending:          strconv.FormatInt(summary.Pending, 10),
		OutboxFailedLabel:      loc.T("admin.setting.smtp.outbox.failed"),
		OutboxFailed:           strconv.FormatInt(summary.Failed, 10),
		OutboxLastErrorLabel:   loc.T("admin.setting.smtp.outbox.last_error"),
		OutboxLastError:        lastError,
		OutboxAttemptsLabel:    loc.T("admin.setting.smtp.outbox.attempts"),
		OutboxAttempts:         attempts,
		// D 类管理员通知状态（M1-24）。
		AdminNotifyLabel:  loc.T("mail.admin.status.label"),
		AdminNotifyStatus: adminNotifyStatus,
	}
	return views.AdminPageData{
		Layout:     s.adminLayout(c, loc, "admin.setting.smtp.title", "/admin/smtp"),
		Heading:    loc.T("admin.setting.smtp.heading"),
		Intro:      loc.T("admin.setting.smtp.intro"),
		NavHeading: loc.T("admin.nav.heading"),
		Nav:        s.adminNav(loc, "/admin/smtp"),
		Notice:     s.smtpNotice(loc, c.Query("notice")),
		SMTPPage:   true,
		SMTP:       smtp,
	}, nil
}

// smtpNotice 把重定向回带的 notice 码翻成文案；未知码不显示。
func (s *Server) smtpNotice(loc *i18n.Localizer, code string) string {
	switch code {
	case "saved":
		return loc.T("admin.setting.smtp.notice.saved")
	case "invalid_port":
		return loc.T("admin.setting.smtp.notice.invalid_port")
	case "invalid_tls_mode":
		return loc.T("admin.setting.smtp.notice.invalid_tls_mode")
	case "save_failed":
		return loc.T("admin.setting.smtp.notice.failed")
	default:
		return ""
	}
}
