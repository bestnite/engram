package web

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"example.com/engram/internal/i18n"
	"example.com/engram/internal/mail"
	"example.com/engram/internal/store"
)

// 本文件是 M1-20：邀请邮件投递（B 类协作/授权，DESIGN.md §4.7、§4.1）。
//
// 设计约束（照 DESIGN.md §4.7，逐条落实）：
//   - 绝不在请求路径同步发信：一律经 Outbox.Enqueue 入队，由后台 worker 投递。
//   - 发信失败不得让触发操作失败：建邀请照旧成功，Enqueue 出错只记英文日志并回显提示。
//   - SMTP 未配置时流程禁用并说明原因：邀请页的「寄到邮箱」入口渲染 mail.not_configured，
//     提交时也走同一说明文案，绝不静默什么也不做。
//   - B 类可关：收件人若是本站用户，尊重其对 invite 类型的显式偏好；不是则回落默认（开）。
//   - 一键退订头是 M1-22 的任务，本任务不设置 Message.Headers（保持为空）。
//
// 邀请的存储 / 校验 / 接受逻辑完全不变；这里只在既有创建流程后追加「寄邮件」这一步。

// 邀请邮件回显给管理员的 notice 码（经 regNotice 翻成语言包文案）。
const (
	noticeInviteMailQueued       = "invite_mail_queued"
	noticeInviteMailUnconfigured = "invite_mail_unconfigured"
	noticeInviteMailFailed       = "invite_mail_failed"
	noticeInviteMailOptedOut     = "invite_mail_opted_out"
)

// sendInviteEmail 在一条邀请创建成功后决定是否把它寄给被邀请人，并在需要时入队。
//
// 返回值是供页面回显的 notice 码；返回空串表示调用方沿用 invite_created：
//   - noticeInviteMailQueued：已入队，等待 worker 投递；
//   - noticeInviteMailUnconfigured：请求寄信但 SMTP 未配置（附 mail.not_configured 说明）；
//   - noticeInviteMailFailed：入队失败（邀请仍然创建成功）；
//   - noticeInviteMailOptedOut：收件人已关闭该类邮件（邀请仍然创建成功）。
//
// 关键：无论返回什么，邀请本身都已落库——发信的任何问题都不能让触发操作失败。
func (s *Server) sendInviteEmail(c *gin.Context, loc *i18n.Localizer, inv *store.Invite) string {
	if inv.Email == nil || strings.TrimSpace(*inv.Email) == "" {
		// 没有收件地址就没有可寄的对象；保持原有的手工拷链接路径。
		return ""
	}
	to := strings.TrimSpace(*inv.Email)
	ctx := c.Request.Context()

	// SMTP 未配置：入口在页面上已渲染说明，这里再兜底一次，绝不静默丢弃。
	if s.mail == nil || !s.mail.Configured() {
		return noticeInviteMailUnconfigured
	}

	// 偏好门禁：收件人若是本站用户，尊重其对 B 类（invite）的显式选择；否则回落目录默认（开）。
	recipient := s.inviteRecipient(ctx, to)
	var userID uint64
	if recipient != nil {
		userID = recipient.ID
	}
	choices, err := store.NewEmailPrefStore(s.db).Choices(ctx, userID)
	if err != nil {
		// 读偏好失败不阻断：回落默认（B 类默认开）继续尝试入队，但绝不静默。
		s.logger.Error("invite mail: load email preferences failed", "user_id", userID, "error", err)
		choices = map[string]bool{}
	}
	if !mail.ResolveEnabled(choices, mail.TypeInvite) {
		return noticeInviteMailOptedOut
	}

	// 文案语言：已知收件人用其偏好语言，否则沿用管理员当前请求的语言。
	recipientLoc := loc
	if recipient != nil {
		if code := strings.TrimSpace(recipient.Locale); code != "" {
			recipientLoc = s.i18n.Localizer(s.i18n.Pick(code, "", ""))
		}
	}

	link := s.inviteAbsoluteURL(c, "/register?invite="+url.QueryEscape(inv.Token))
	msg := inviteMessage(recipientLoc, to, link, s.siteName(ctx, recipientLoc), inv.ExpiresAt)

	if err := s.mail.Enqueue(ctx, msg); err != nil {
		if errors.Is(err, mail.ErrNotConfigured) {
			return noticeInviteMailUnconfigured
		}
		// 入队失败只记日志：邀请已经创建，不能因此让整个操作失败。
		s.logger.Error("invite mail: enqueue failed", "error", err)
		return noticeInviteMailFailed
	}
	return noticeInviteMailQueued
}

// inviteRecipient 按邮箱找本站用户；不是本站用户（或查询出错）时返回 nil。
// 邮箱大小写不敏感：用户表里的邮箱在注册时已归一化为小写。
func (s *Server) inviteRecipient(ctx context.Context, email string) *store.User {
	if s.users == nil {
		return nil
	}
	u, err := s.users.ByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil || u == nil {
		return nil
	}
	return u
}

// inviteAbsoluteURL 拼出邮件里可点击的绝对地址：配置了 BASE_URL 就用它，
// 否则按当前请求推导（本地开发与 httptest 依赖后者）。
func (s *Server) inviteAbsoluteURL(c *gin.Context, path string) string {
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

// inviteMailNote 返回邀请页「寄到邮箱」入口旁的说明：可用时说明行为，不可用时
// 直接给出 mail.not_configured 的原因文案，让管理员一眼知道为什么发不出去。
func inviteMailNote(loc *i18n.Localizer, available bool) string {
	if available {
		return loc.T("mail.invite.form_note")
	}
	return loc.T("mail.not_configured")
}

// inviteMessage 组装邀请邮件。Headers 刻意留空：一键退订头属于 M1-22。
func inviteMessage(loc *i18n.Localizer, to, link, site string, expiresAt *time.Time) mail.Message {
	lines := []string{
		loc.T("mail.invite.greeting"),
		"",
		loc.Tf("mail.invite.body", map[string]any{"site": site}),
		"",
		loc.T("mail.invite.link_label"),
		link,
	}
	if expiresAt != nil {
		lines = append(lines, "",
			loc.Tf("mail.invite.expires", map[string]any{"expires": expiresAt.UTC().Format("2006-01-02 15:04 UTC")}))
	}
	return mail.Message{
		To:       to,
		Type:     string(mail.TypeInvite),
		Subject:  loc.Tf("mail.invite.subject", map[string]any{"site": site}),
		TextBody: strings.Join(lines, "\n"),
	}
}
