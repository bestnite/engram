package web

import (
	"time"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
)

// 管理面板的注册与邀请支撑函数（DESIGN.md §4.2、§8.4；ROADMAP.md M6-3）。
//
// SSR 注册与邀请页删除后，策略与邀请的读写在 /api/v1/admin/registration 与
// /api/v1/admin/invites* 的 JSON 端点上（spa_admin_registration.go）；
// 这里只保留仍被复用的 notice 翻译、邀请状态与时间格式化。

// regNotice 把重定向/JSON 回带的 notice 码翻成文案；未知码不显示。
func (s *Server) regNotice(loc *i18n.Localizer, code string) string {
	switch code {
	case "saved":
		return loc.T("admin.registration.notice.saved")
	case "invalid_policy":
		return loc.T("admin.registration.notice.invalid_policy")
	case "save_failed":
		return loc.T("admin.registration.notice.save_failed")
	case "invite_created":
		return loc.T("admin.registration.notice.invite_created")
	case "invite_revoked":
		return loc.T("admin.registration.notice.invite_revoked")
	case "invite_invalid":
		return loc.T("admin.registration.notice.invite_invalid")
	case "invite_create_failed":
		return loc.T("admin.registration.notice.invite_create_failed")
	case noticeInviteMailQueued:
		return loc.T("mail.invite.notice.queued")
	case noticeInviteMailUnconfigured:
		// 说明原因而不是静默：邀请已创建，但本站未开邮件功能（DESIGN.md §4.7）。
		return loc.T("admin.registration.notice.invite_created") + " " + loc.T("mail.not_configured")
	case noticeInviteMailFailed:
		return loc.T("mail.invite.notice.failed")
	case noticeInviteMailOptedOut:
		return loc.T("mail.invite.notice.opted_out")
	default:
		return ""
	}
}

// inviteStatus 把一条邀请归成 active / used / expired 三态。
func inviteStatus(inv store.Invite, now time.Time) string {
	if inv.UsedAt != nil {
		return "used"
	}
	if inv.ExpiresAt != nil && !inv.ExpiresAt.After(now) {
		return "expired"
	}
	return "active"
}

// formatTimePtr 把可空时间格式化成展示串；nil 返回空。
func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04")
}
