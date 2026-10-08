package web

import (
	"context"
	"encoding/json"
	"net/http"
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

// 注册策略矩阵与表单校验的 JSON 验收见 auth_register_test.go（TestSPARegisterAPI_*）。
// 本文件只保留邀请接受与邀请拒绝两条 HTTP 级链路。

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

	cookie, headers := preSessionPair(t, srv, "/register")
	first := postJSON(srv, "/api/v1/auth/register", map[string]string{
		"username": "invitee", "email": "invitee@example.com",
		"password": "Sup3rSecret!", "invite": inv.Token,
	}, []*http.Cookie{cookie}, headers)
	if first.Code != http.StatusOK {
		t.Fatalf("registration with invite = %d, want 200 (body %s)", first.Code, snippet(first.Body.String()))
	}

	// 再次使用同一 token 必须被拒（一次性）。
	second := postJSON(srv, "/api/v1/auth/register", map[string]string{
		"username": "invitee2", "email": "invitee2@example.com",
		"password": "Sup3rSecret!", "invite": inv.Token,
	}, []*http.Cookie{cookie}, headers)
	if second.Code != http.StatusForbidden || apiErrorCode(t, second) != "invite_invalid" {
		t.Fatalf("reused invite = %d %s, want 403 invite_invalid", second.Code, apiErrorCode(t, second))
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

// TestInviteRejections 覆盖已过期与邮箱不符两种拒绝路径（JSON 层）。
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
			cookie, headers := preSessionPair(t, srv, "/register")
			rec := postJSON(srv, "/api/v1/auth/register", map[string]string{
				"username": "rejectee", "email": tc.email,
				"password": "Sup3rSecret!", "invite": inv.Token,
			}, []*http.Cookie{cookie}, headers)
			if rec.Code != http.StatusForbidden || apiErrorCode(t, rec) != "invite_invalid" {
				t.Fatalf("registration = %d %s, want 403 invite_invalid (body %s)", rec.Code, apiErrorCode(t, rec), snippet(rec.Body.String()))
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
