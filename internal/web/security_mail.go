package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是（A 类事务安全邮件）在 web 层的公共部分：投递助手、链接拼装、
// 五类邮件的文案组装，以及各触发点复用的通知函数。
//
// 三条行为约束在这里统一兑现：
//   - 绝不在请求路径同步发信：一律经 SecurityNotifier.Send → Outbox.Enqueue 入队；
//   - 发信失败不让触发操作失败：所有通知函数不返回 error，失败只记英文日志；
//   - SMTP 未配置时流程禁用并说明原因：页面渲染 mail.not_configured（见各页面 handler）。
//
// A 类邮件不受用户偏好影响、不携带退订头（SecurityNotifier 不写 Headers）。

// securityMailReady 报告 A 类邮件当前是否可投递（SMTP 已配置且 outbox 已装配）。
// 依赖邮件的页面据此禁用并渲染 mail.not_configured，而不是静默什么都不做。
func (s *Server) securityMailReady() bool {
	return s.securityMail != nil && s.securityMail.Configured()
}

// securityAbsoluteURL 拼出邮件里可点击的绝对地址：配置了 BASE_URL 就用它，
// 否则按当前请求推导（本地开发与 httptest 依赖后者）。
func (s *Server) securityAbsoluteURL(c *gin.Context, path string) string {
	if base := strings.TrimRight(strings.TrimSpace(s.baseURL), "/"); base != "" {
		return base + path
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return scheme + "://" + c.Request.Host + path
}

// userLocalizer 返回收件人语言对应的本地化器：邮件文案跟随用户自己的语言设置，
// 而不是触发请求的管理员语言。用户没有设置时回落到站点默认。
func (s *Server) userLocalizer(u *store.User) *i18n.Localizer {
	code := ""
	if u != nil {
		code = strings.TrimSpace(u.Locale)
	}
	return s.i18n.Localizer(s.i18n.Pick(code, "", ""))
}

// sendSecurity 是 A 类邮件的唯一投递出口；刻意不返回 error：
// 登录成功、改密码成功等触发操作绝不能因为入队失败而失败。
func (s *Server) sendSecurity(ctx context.Context, userID uint64, to string, t mail.Type, subject, textBody, htmlBody string) bool {
	if s.securityMail == nil {
		return false
	}
	return s.securityMail.Send(ctx, userID, to, t, subject, textBody, htmlBody)
}

// loginFingerprint 把客户端 IP 与 User-Agent 归一化成稳定指纹（sha256 十六进制）。
// 存哈希而非原文：足够做去重，又不在库里长期堆积用户代理原文（隐私最小化）。
func loginFingerprint(c *gin.Context) string {
	ip := strings.TrimSpace(c.ClientIP())
	ua := strings.TrimSpace(c.GetHeader("User-Agent"))
	sum := sha256.Sum256([]byte(ip + "\n" + ua))
	return hex.EncodeToString(sum[:])
}

// ── 五类邮件的文案组装（默认正文只有纯文本；管理员配了模板才产出 HTML 段）──────────
//
// 每个组装函数做三件事：给出模板变量、给出内置正文（今天的行为）、交给 renderMail 渲染。
// 变量名必须与 internal/mail/vars.go 的表一致，否则必填校验会在发送时拦下（这是有意的
// 失败方向：宁可不发，也不发一封没有重置链接的邮件）。

// passwordResetMessage 组装密码重置邮件。链接明文只出现在这里，绝不入库、绝不进日志。
func (s *Server) passwordResetMessage(ctx context.Context, loc *i18n.Localizer, to, link, site string, expires time.Time) mail.Message {
	expiresAt := expires.UTC().Format("2006-01-02 15:04 UTC")
	vars := mail.Vars{"site": site, "url": link, "expires": expiresAt}
	fbSubject, fbText, _ := mail.DefaultTemplate(mail.TypePasswordReset, mail.VariantDefault, loc.Tf, vars)
	subject, text, htmlBody := s.renderMail(ctx, loc, mail.TypePasswordReset, vars, fbSubject, fbText, "")
	return mail.Message{To: to, Type: string(mail.TypePasswordReset), Subject: subject, TextBody: text, HTMLBody: htmlBody}
}

// emailVerificationMessage 组装邮箱验证邮件。
func (s *Server) emailVerificationMessage(ctx context.Context, loc *i18n.Localizer, to, link, site string, expires time.Time) mail.Message {
	expiresAt := expires.UTC().Format("2006-01-02 15:04 UTC")
	vars := mail.Vars{"site": site, "url": link, "expires": expiresAt}
	fbSubject, fbText, _ := mail.DefaultTemplate(mail.TypeEmailVerification, mail.VariantDefault, loc.Tf, vars)
	subject, text, htmlBody := s.renderMail(ctx, loc, mail.TypeEmailVerification, vars, fbSubject, fbText, "")
	return mail.Message{To: to, Type: string(mail.TypeEmailVerification), Subject: subject, TextBody: text, HTMLBody: htmlBody}
}

// emailChangeMessage 组装改邮箱确认邮件；发给待确认的新地址。
//
// 它复用邮箱验证这个类型（同属「确认一个邮箱地址」），所以管理员为 email_verification
// 配的模板对两类信都生效，变量集也是同一套（site/url/expires）。
func (s *Server) emailChangeMessage(ctx context.Context, loc *i18n.Localizer, to, link, site string, expires time.Time) mail.Message {
	expiresAt := expires.UTC().Format("2006-01-02 15:04 UTC")
	vars := mail.Vars{"site": site, "url": link, "expires": expiresAt}
	fbSubject, fbText, _ := mail.DefaultTemplate(mail.TypeEmailVerification, mail.VariantEmailChange, loc.Tf, vars)
	subject, text, htmlBody := s.renderMail(ctx, loc, mail.TypeEmailVerification, vars, fbSubject, fbText, "")
	return mail.Message{To: to, Type: string(mail.TypeEmailVerification), Subject: subject, TextBody: text, HTMLBody: htmlBody}
}

// newDeviceLoginMessage 组装新设备/新 IP 登录提醒。
func (s *Server) newDeviceLoginMessage(ctx context.Context, loc *i18n.Localizer, to, site, ip, userAgent string, at time.Time) mail.Message {
	atText := at.UTC().Format("2006-01-02 15:04 UTC")
	vars := mail.Vars{"site": site, "ip": ip, "time": atText}
	fbSubject, fbText, _ := mail.DefaultTemplate(mail.TypeNewDeviceLogin, mail.VariantDefault, loc.Tf, vars)
	subject, text, htmlBody := s.renderMail(ctx, loc, mail.TypeNewDeviceLogin, vars, fbSubject, fbText, "")
	return mail.Message{To: to, Type: string(mail.TypeNewDeviceLogin), Subject: subject, TextBody: text, HTMLBody: htmlBody}
}

// credentialChangedMessage 组装凭据变更通知；what 是已本地化的「改了什么」短语。
func (s *Server) credentialChangedMessage(ctx context.Context, loc *i18n.Localizer, to, site, what string) mail.Message {
	vars := mail.Vars{"site": site, "what": what}
	fbSubject, fbText, _ := mail.DefaultTemplate(mail.TypeCredentialChanged, mail.VariantDefault, loc.Tf, vars)
	subject, text, htmlBody := s.renderMail(ctx, loc, mail.TypeCredentialChanged, vars, fbSubject, fbText, "")
	return mail.Message{To: to, Type: string(mail.TypeCredentialChanged), Subject: subject, TextBody: text, HTMLBody: htmlBody}
}

// accountStatusMessage 组装账号状态变更通知；status 是已本地化的状态短语。
func (s *Server) accountStatusMessage(ctx context.Context, loc *i18n.Localizer, to, site, status string) mail.Message {
	vars := mail.Vars{"site": site, "status": status}
	fbSubject, fbText, _ := mail.DefaultTemplate(mail.TypeAccountStatus, mail.VariantDefault, loc.Tf, vars)
	subject, text, htmlBody := s.renderMail(ctx, loc, mail.TypeAccountStatus, vars, fbSubject, fbText, "")
	return mail.Message{To: to, Type: string(mail.TypeAccountStatus), Subject: subject, TextBody: text, HTMLBody: htmlBody}
}

// ── 触发点通知（各调用点只需一行；绝不返回 error）────────────────────────────────

// notifyNewDeviceLogin 记录登录指纹，并在指纹是新的时投递新设备/新 IP 登录提醒。
// 指纹已见过（或并发下刚被其它请求记录）时不发信，避免同一设备反复提醒。
func (s *Server) notifyNewDeviceLogin(c *gin.Context, u *store.User) {
	if s.fingerprints == nil || u == nil {
		return
	}
	ctx := c.Request.Context()
	now := time.Now().UTC()
	known, err := s.fingerprints.Touch(ctx, u.ID, loginFingerprint(c), now)
	if err != nil {
		s.logger.Error("security mail: touch login fingerprint failed", "user_id", u.ID, "error", err)
		return
	}
	if known {
		return
	}
	loc := s.userLocalizer(u)
	msg := s.newDeviceLoginMessage(ctx, loc, u.Email, securitySiteName(loc), strings.TrimSpace(c.ClientIP()),
		strings.TrimSpace(c.GetHeader("User-Agent")), now)
	s.sendSecurity(ctx, u.ID, msg.To, mail.TypeNewDeviceLogin, msg.Subject, msg.TextBody, msg.HTMLBody)
}

// notifyCredentialChanged 在密码/TOTP/恢复码变更后投递通知。
// kind 是 mail.notice.credential.* 的键后缀（password / totp_enabled / totp_disabled / recovery_codes）。
func (s *Server) notifyCredentialChanged(ctx context.Context, u *store.User, kind string) {
	if u == nil {
		return
	}
	loc := s.userLocalizer(u)
	msg := s.credentialChangedMessage(ctx, loc, u.Email, securitySiteName(loc), loc.T("mail.notice.credential."+kind))
	s.sendSecurity(ctx, u.ID, msg.To, mail.TypeCredentialChanged, msg.Subject, msg.TextBody, msg.HTMLBody)
}

// notifyAccountStatus 在账号被禁用或删除后投递通知。
// status 是 mail.notice.account.* 的键后缀（disabled / deleted）。
func (s *Server) notifyAccountStatus(ctx context.Context, u *store.User, status string) {
	if u == nil {
		return
	}
	loc := s.userLocalizer(u)
	msg := s.accountStatusMessage(ctx, loc, u.Email, securitySiteName(loc), loc.T("mail.notice.account."+status))
	s.sendSecurity(ctx, u.ID, msg.To, mail.TypeAccountStatus, msg.Subject, msg.TextBody, msg.HTMLBody)
}

// securitySiteName 取站点品牌名作为邮件里的 {site}。
func securitySiteName(loc *i18n.Localizer) string { return loc.T("app.name") }

// sendEmailVerification 签发验证令牌并投递验证邮件；返回是否成功入队。
// SMTP 未配置或令牌签发失败时返回 false，调用方据页面/回显说明原因，绝不静默。
func (s *Server) sendEmailVerification(c *gin.Context, u *store.User) bool {
	if s.tokens == nil || u == nil || !s.securityMailReady() {
		return false
	}
	ctx := c.Request.Context()
	now := time.Now().UTC()
	token, err := s.tokens.Issue(ctx, u.ID, store.ActionTokenEmailVerify, "", auth.EmailVerifyTTL)
	if err != nil {
		s.logger.Error("security mail: issue email verification token failed", "user_id", u.ID, "error", err)
		return false
	}
	loc := s.userLocalizer(u)
	link := s.securityAbsoluteURL(c, "/verify-email?token="+url.QueryEscape(token))
	msg := s.emailVerificationMessage(ctx, loc, u.Email, link, securitySiteName(loc), now.Add(auth.EmailVerifyTTL))
	return s.sendSecurity(ctx, u.ID, msg.To, mail.TypeEmailVerification, msg.Subject, msg.TextBody, msg.HTMLBody)
}

// sendEmailChangeConfirmation 向待确认的新地址签发改邮箱令牌并投递确认邮件。
func (s *Server) sendEmailChangeConfirmation(c *gin.Context, u *store.User, newEmail string) bool {
	if s.tokens == nil || u == nil || !s.securityMailReady() {
		return false
	}
	ctx := c.Request.Context()
	now := time.Now().UTC()
	token, err := s.tokens.Issue(ctx, u.ID, store.ActionTokenEmailChange, newEmail, auth.EmailChangeTTL)
	if err != nil {
		s.logger.Error("security mail: issue email change token failed", "user_id", u.ID, "error", err)
		return false
	}
	loc := s.userLocalizer(u)
	link := s.securityAbsoluteURL(c, "/confirm-email-change?token="+url.QueryEscape(token))
	msg := s.emailChangeMessage(ctx, loc, newEmail, link, securitySiteName(loc), now.Add(auth.EmailChangeTTL))
	return s.sendSecurity(ctx, u.ID, msg.To, mail.TypeEmailVerification, msg.Subject, msg.TextBody, msg.HTMLBody)
}
