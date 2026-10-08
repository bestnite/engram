package web

import (
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// 管理面板的注册与邀请支撑函数（ROADMAP.md M6-3）。
//
// SSR 注册与邀请页删除后，策略与邀请的读写在 /api/v1/admin/registration 与
// /api/v1/admin/invites* 的 JSON 端点上（admin_registration_api.go）；
// 这里只保留仍被复用的邀请状态与时间格式化。

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
