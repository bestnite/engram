package web

import (
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// Support functions for the admin panel's registration and invites.
//
// After the SSR registration and invite pages were removed, policy and invite reads and writes live on the /api/v1/admin/registration and
// /api/v1/admin/invites* JSON endpoints (admin_registration_api.go);
// only the invite status and time formatting that are still reused are kept here.

// inviteStatus classifies an invite into one of three states: active / used / expired.
func inviteStatus(inv store.Invite, now time.Time) string {
	if inv.UsedAt != nil {
		return "used"
	}
	if inv.ExpiresAt != nil && !inv.ExpiresAt.After(now) {
		return "expired"
	}
	return "active"
}

// formatTimePtr formats a nullable time into a display string; returns empty for nil.
func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04")
}
