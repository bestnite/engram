package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// seedAdminUser 建出一个活跃管理员，关闭“首个管理员引导”分支，让注册真正走策略路径。
func seedAdminUser(t *testing.T, srv *Server) {
	t.Helper()
	if _, err := srv.accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "admin", Email: "admin@example.com", Password: "Sup3rSecret!", Role: store.RoleAdmin,
	}); err != nil {
		t.Fatalf("create admin: %v", err)
	}
}

// TestRegistrationPolicyMatrix 是 M1-6 的 HTTP 级验收：每一种策略与白名单拒绝都走真实注册请求。
func TestRegistrationPolicyMatrix(t *testing.T) {
	cases := []struct {
		name       string
		policy     string
		allowlist  string
		email      string
		wantStatus int
		wantText   string
	}{
		{
			name: "open without allowlist allows anyone", policy: "open",
			email: "alice@example.org", wantStatus: http.StatusSeeOther,
		},
		{
			name: "open with allowlist match", policy: "open", allowlist: `["example.com"]`,
			email: "alice@example.com", wantStatus: http.StatusSeeOther,
		},
		{
			name: "open with allowlist denial", policy: "open", allowlist: `["example.com"]`,
			email: "alice@example.net", wantStatus: http.StatusForbidden, wantText: "该邮箱域名不在允许注册的范围内",
		},
		{
			name: "invite requires a token", policy: "invite",
			email: "alice@example.com", wantStatus: http.StatusForbidden, wantText: "注册需要邀请链接",
		},
		{
			name: "closed rejects everyone", policy: "closed",
			email: "alice@example.com", wantStatus: http.StatusForbidden, wantText: "自助注册已关闭",
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := newAuthServer(t)
			seedAdminUser(t, srv)
			writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, tc.policy)))
			if tc.allowlist != "" {
				writeSetting(t, db, auth.SettingKeyEmailAllowlist, tc.allowlist)
			}
			rec := postForm(t, srv, "/register", url.Values{
				"username": {string(rune('a'+i)) + "_user"},
				"email":    {tc.email},
				"password": {"Sup3rSecret!"},
			}, nil)
			if rec.Code != tc.wantStatus {
				t.Fatalf("POST /register (%s) status = %d, want %d (body %s)",
					tc.name, rec.Code, tc.wantStatus, snippet(rec.Body.String()))
			}
			if tc.wantText != "" && !strings.Contains(rec.Body.String(), tc.wantText) {
				t.Errorf("POST /register (%s) body missing %q; body = %s", tc.name, tc.wantText, snippet(rec.Body.String()))
			}
		})
	}
}

// TestInviteAcceptCreatesExactlyOneUser 是 M1-7 的 HTTP 级验收：
// 有效邀请放行一次，令牌随即失效，且只产生一个用户。
func TestInviteAcceptCreatesExactlyOneUser(t *testing.T) {
	srv, db := newAuthServer(t)
	seedAdminUser(t, srv)
	writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, "invite")))
	invites := store.NewInviteStore(db)

	inv := &store.Invite{}
	if err := invites.Create(context.Background(), inv); err != nil {
		t.Fatalf("create invite: %v", err)
	}

	form := url.Values{
		"username": {"invitee"},
		"email":    {"invitee@example.com"},
		"password": {"Sup3rSecret!"},
		"invite":   {inv.Token},
	}
	first := postForm(t, srv, "/register", form, nil)
	if first.Code != http.StatusSeeOther {
		t.Fatalf("POST /register with invite status = %d, want 303 (body %s)", first.Code, snippet(first.Body.String()))
	}

	// 再次使用同一 token 必须被拒（一次性）。
	second := postForm(t, srv, "/register", url.Values{
		"username": {"invitee2"},
		"email":    {"invitee2@example.com"},
		"password": {"Sup3rSecret!"},
		"invite":   {inv.Token},
	}, nil)
	if second.Code != http.StatusForbidden {
		t.Fatalf("reused invite status = %d, want 403 (body %s)", second.Code, snippet(second.Body.String()))
	}
	if !strings.Contains(second.Body.String(), "邀请链接无效") {
		t.Errorf("reused-invite page is missing the localized notice; body = %s", snippet(second.Body.String()))
	}

	// 邀请只创建一个用户，且已记录 used_at/used_by。
	var count int64
	if err := db.Model(&store.User{}).Where("email = ?", "invitee@example.com").Count(&count).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Errorf("users created by one invite = %d, want 1", count)
	}
	used, err := invites.ByToken(context.Background(), inv.Token)
	if err != nil {
		t.Fatalf("ByToken() error = %v", err)
	}
	if used.UsedAt == nil || used.UsedBy == nil {
		t.Errorf("invite after acceptance = %+v, want used_at and used_by set", used)
	}
}

// TestInviteRejections 覆盖已过期与邮箱不符两种拒绝路径（HTTP 层）。
func TestInviteRejections(t *testing.T) {
	cases := []struct {
		name  string
		setup func(inv *store.Invite)
		email string
	}{
		{
			name: "expired token",
			setup: func(inv *store.Invite) {
				past := time.Now().UTC().Add(-time.Hour)
				inv.ExpiresAt = &past
			},
			email: "invitee@example.com",
		},
		{
			name: "email restricted to another address",
			setup: func(inv *store.Invite) {
				other := "someone-else@example.com"
				inv.Email = &other
			},
			email: "invitee@example.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := newAuthServer(t)
			seedAdminUser(t, srv)
			invites := store.NewInviteStore(db)
			inv := &store.Invite{}
			tc.setup(inv)
			if err := invites.Create(context.Background(), inv); err != nil {
				t.Fatalf("create invite: %v", err)
			}
			rec := postForm(t, srv, "/register", url.Values{
				"username": {"rejectee"},
				"email":    {tc.email},
				"password": {"Sup3rSecret!"},
				"invite":   {inv.Token},
			}, nil)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("POST /register status = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
			}
			if !strings.Contains(rec.Body.String(), "邀请链接无效") {
				t.Errorf("rejection page missing localized notice; body = %s", snippet(rec.Body.String()))
			}
		})
	}
}

// mustJSON 生成 settings 表要求的 JSON 文本（字符串值）。
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal(%v): %v", v, err)
	}
	return b
}

// writeSetting 写一条 settings 覆盖值，value 已是 JSON 文本。
func writeSetting(t *testing.T, db *gorm.DB, key, value string) {
	t.Helper()
	row := store.Setting{Key: key, Value: value, UpdatedAt: time.Now().UTC()}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("write setting %q: %v", key, err)
	}
}
