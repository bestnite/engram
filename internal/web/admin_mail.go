package web

import (
	"context"
	"strconv"
	"strings"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是：D 类管理员通知邮件。
//
// 三个触发点里，本仓库真实存在的是两个：
//   - 作业失败：internal/jobs/jobs.go 的 fail（Runner 通过 OnFailure 钩子回调本文件的
//     NotifyJobFailed，装配在 cmd/engram/main.go）；
//   - 媒体配额：internal/web/media.go 的 checkMediaQuota（用户上传被配额拦下时）。
//
// 三条硬约束与 A/B 类邮件一致，逐条落实：
//   - 绝不在请求路径同步发信：一律经 mail.AdminNotifier.Send → Outbox.Enqueue 入队；
//   - 发信失败不让触发操作失败：所有触发函数不返回 error，失败只记英文日志；
//   - SMTP 未配置时不得静默：触发点记 Warn，且 SMTP 配置页渲染 mail.not_configured 说明。
//
// 收件地址：D 类发给管理员账号的登录邮箱（store.UserStore.ListAdmins）。
// 偏好：D 类默认开、可关闭，按**收件管理员自己**的偏好判定（mail.ResolveEnabled）。

// adminRecipients 返回所有仍可登录的管理员；查询失败时记日志并返回空列表。
// 这里只做地址解析，绝不因查询失败让触发操作失败。
func (s *Server) adminRecipients(ctx context.Context) []store.User {
	if s.users == nil {
		return nil
	}
	admins, err := s.users.ListAdmins(ctx)
	if err != nil {
		s.logger.Error("admin mail: list admins failed", "error", err)
		return nil
	}
	return admins
}

// notifyAdmins 把一封 D 类通知发给每个管理员：按各自语言本地化、按各自偏好判定、逐封入队。
// 它不返回 error——发信的任何问题都不能让触发操作失败；返回值仅为成功入队的封数（供测试与
// 调用方观测，调用方不必处理）。
//
// build 接收收件管理员的本地化器，返回该管理员语言下的邮件主题与正文。
func (s *Server) notifyAdmins(ctx context.Context, t mail.Type, build func(loc *i18n.Localizer) mail.Message) int {
	if s.mail == nil || !s.mail.Configured() {
		// SMTP 未配置：不得静默——记 Warn，触发操作照常。相关页面渲染 mail.not_configured。
		s.logger.Warn("admin mail: smtp is not configured; notification not sent", "type", string(t))
		return 0
	}
	admins := s.adminRecipients(ctx)
	notifier := mail.NewAdminNotifier(s.mail, store.NewEmailPrefStore(s.db), s.logger)
	sent := 0
	for i := range admins {
		admin := &admins[i]
		to := strings.TrimSpace(admin.Email)
		if to == "" {
			continue
		}
		msg := build(s.userLocalizer(admin))
		if notifier.Send(ctx, admin.ID, to, t, msg.Subject, msg.TextBody, msg.HTMLBody) {
			sent++
		}
	}
	return sent
}

// NotifyJobFailed 在作业失败落库后通知管理员（D 类，job_failed）。
// 签名与 jobs.FailureFunc 对齐；由 cmd/engram 把它接到 Runner.OnFailure 上。
// 它运行在作业 worker 的上下文里，必须快速返回且绝不 panic（jobs 侧已包 recover 兜底）。
func (s *Server) NotifyJobFailed(ctx context.Context, job store.Job, reason string) {
	s.notifyAdmins(ctx, mail.TypeJobFailed, func(loc *i18n.Localizer) mail.Message {
		return s.adminJobFailedMessage(ctx, loc, securitySiteName(loc), job, reason)
	})
}

// NotifyMediaAlert 在用户媒体配额被触及时通知管理员（D 类，media_disk_alert）。
// 由 checkMediaQuota 在写出 413 之后调用；刻意不返回 error，绝不影响上传的错误响应。
func (s *Server) NotifyMediaAlert(ctx context.Context, u *store.User, used, quota int64) {
	if u == nil {
		return
	}
	s.notifyAdmins(ctx, mail.TypeMediaDiskAlert, func(loc *i18n.Localizer) mail.Message {
		return s.adminMediaAlertMessage(ctx, loc, securitySiteName(loc), *u, used, quota)
	})
}

// ── 两类邮件的文案组装（默认正文只有纯文本；管理员配了模板才产出 HTML 段）──────────

// adminJobFailedMessage 组装「作业失败」通知；reason 是失败原因（英文，来自 jobs.error）。
func (s *Server) adminJobFailedMessage(ctx context.Context, loc *i18n.Localizer, site string, job store.Job, reason string) mail.Message {
	vars := mail.Vars{
		"site": site, "job_id": strconv.FormatUint(job.ID, 10), "kind": job.Kind, "reason": reason,
	}
	fbSubject, fbText, _ := mail.DefaultTemplate(mail.TypeJobFailed, mail.VariantDefault, loc.Tf, vars)
	subject, text, htmlBody := s.renderMail(ctx, loc, mail.TypeJobFailed, vars, fbSubject, fbText, "")
	return mail.Message{Subject: subject, TextBody: text, HTMLBody: htmlBody}
}

// adminMediaAlertMessage 组装「媒体配额」告警；used/quota 是字节数（展示时人类可读）。
func (s *Server) adminMediaAlertMessage(ctx context.Context, loc *i18n.Localizer, site string, u store.User, used, quota int64) mail.Message {
	usedText, limitText := humanBytes(used), humanBytes(quota)
	vars := mail.Vars{"site": site, "username": u.Username, "used": usedText, "limit": limitText}
	fbSubject, fbText, _ := mail.DefaultTemplate(mail.TypeMediaDiskAlert, mail.VariantDefault, loc.Tf, vars)
	subject, text, htmlBody := s.renderMail(ctx, loc, mail.TypeMediaDiskAlert, vars, fbSubject, fbText, "")
	return mail.Message{Subject: subject, TextBody: text, HTMLBody: htmlBody}
}
