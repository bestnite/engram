package web

import (
	"net/url"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 M1-19 的密码重置流程（服务端部分）：令牌签发与投递、以及三条页面的应用壳入口。
//
// 安全要点（DESIGN.md §4.7）：
//   - 明文令牌只出现在邮件链接里，绝不入库、绝不进日志（日志只记 user_id 与结果）；
//   - 令牌库中只存摘要，一次性（条件更新），有 1 小时过期；
//   - 请求重置一律回同一结果，不因邮箱是否存在而不同，避免账号枚举；
//   - SMTP 未配置时响应渲染 mail_ready=false，绝不静默。
//
// 页面的读写协议全部走同源 JSON 端点（spa_account.go）；本文件只保留页面外壳与令牌签发。

// registerSecurityMailRoutes 挂载 M1-19 的密码重置、邮箱验证与改邮箱确认路由。
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerSecurityMailRoutes(router *gin.Engine) {
	if s.tokens == nil {
		return
	}
	// 三条可导航的登录前页面只返回 SPA 应用壳；协议走
	// /api/v1/auth/{forgot-password,reset-password}（与 SPA 同一份服务逻辑）。
	router.GET("/forgot-password", s.forgotPasswordRoute)
	router.GET("/reset-password", s.resetPasswordRoute)
	// 邮件里的一键 GET 链接落在规范路径上，返回应用壳；令牌由 SPA 通过
	// /api/v1/auth/{verify-email,confirm-email-change} 消费（一次性、有过期）。
	// 会话前写请求由 DoubleSubmitMiddleware 校验双提交 cookie 与镜像 token。
	router.GET("/verify-email", s.spaVerifyEmailShell)
	router.GET("/confirm-email-change", s.spaConfirmEmailChangeShell)
	// 改邮箱页是登录用户自己的页面：未登录先重定向登录页。
	router.GET("/settings/email", s.emailChangeRoute)
	// SPA 的同源 JSON 传输层与应用壳入口（spa_account.go）。
	s.registerSPAAccountRoutes(router)
}

// renderSecurityForm 与它渲染的 security_mail.templ 已随退订页切到 SPA 删除：
// 安全/事务流程不再有服务端渲染，只剩 JSON 端点与 SPA 应用壳。

// issuePasswordReset 签发密码重置令牌并投递邮件；任何失败只记英文日志（绝不回传）。
// 日志绝不包含令牌明文。
func (s *Server) issuePasswordReset(c *gin.Context, u *store.User) {
	ctx := c.Request.Context()
	now := time.Now().UTC()
	token, err := s.tokens.Issue(ctx, u.ID, store.ActionTokenPasswordReset, "", auth.PasswordResetTTL)
	if err != nil {
		s.logger.Error("security mail: issue password reset token failed", "user_id", u.ID, "error", err)
		return
	}
	loc := s.userLocalizer(u)
	link := s.securityAbsoluteURL(c, "/reset-password?token="+url.QueryEscape(token))
	msg := passwordResetMessage(loc, u.Email, link, securitySiteName(loc), now.Add(auth.PasswordResetTTL))
	s.sendSecurity(ctx, u.ID, msg.To, mail.TypePasswordReset, msg.Subject, msg.TextBody, msg.HTMLBody)
}
