package web

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// TestInviteAcceptFailureKeepsTokenUsable 是 B-12 的 HTTP 级反面验收：
// 邀请建号失败时 token 必须仍然可用，且不暴露“已使用→又变可用”的中间态。
// 失败用用户名冲突触发（与既有 admin 同名），这是真实的建号失败，而非伪造。
func TestInviteAcceptFailureKeepsTokenUsable(t *testing.T) {
	srv, db := newAuthServer(t)
	seedAdminUser(t, srv) // 建出 admin / admin@example.com，令同名注册必然冲突
	writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, "invite")))
	invites := store.NewInviteStore(db)

	inv := &store.Invite{}
	if err := invites.Create(context.Background(), inv); err != nil {
		t.Fatalf("create invite: %v", err)
	}

	// 用户名与既有 admin 冲突：建号失败，整个接受事务回滚。
	failed := postForm(t, srv, "/register", url.Values{
		"username": {"admin"},
		"email":    {"admin@example.com"},
		"password": {"Sup3rSecret!"},
		"invite":   {inv.Token},
	}, nil)
	if failed.Code != http.StatusConflict {
		t.Fatalf("POST /register with a failing create = %d, want 409 (body %s)", failed.Code, snippet(failed.Body.String()))
	}

	// 回滚后 token 未被消费：used_at 仍为空，管理员可再次使用它。
	after, err := invites.ByToken(context.Background(), inv.Token)
	if err != nil {
		t.Fatalf("ByToken() error = %v", err)
	}
	if after.UsedAt != nil || after.UsedBy != nil {
		t.Fatalf("invite after a failed accept = %+v, want used_at and used_by still nil", after)
	}

	// 同一 token 换一个用户名可以正常接受，且 used_by 指向新用户。
	retry := postForm(t, srv, "/register", url.Values{
		"username": {"invitee"},
		"email":    {"invitee@example.com"},
		"password": {"Sup3rSecret!"},
		"invite":   {inv.Token},
	}, nil)
	if retry.Code != http.StatusSeeOther {
		t.Fatalf("POST /register after the rollback = %d, want 303 (body %s)", retry.Code, snippet(retry.Body.String()))
	}
	used, err := invites.ByToken(context.Background(), inv.Token)
	if err != nil {
		t.Fatalf("ByToken() error = %v", err)
	}
	if used.UsedAt == nil || used.UsedBy == nil {
		t.Fatalf("invite after a successful accept = %+v, want used_at and used_by set", used)
	}
}
