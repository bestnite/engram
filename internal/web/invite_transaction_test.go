package web

import (
	"context"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// TestInviteAcceptFailureKeepsTokenUsable 是 HTTP 级反面验收：
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
	cookie, headers := preSessionPair(t, srv, "/register")
	failed := postJSON(srv, "/api/v1/auth/register", map[string]string{
		"username": "admin",
		"email":    "admin@example.com",
		"password": "Sup3rSecret!",
		"invite":   inv.Token,
	}, []*http.Cookie{cookie}, headers)
	if failed.Code != http.StatusConflict {
		t.Fatalf("POST /api/v1/auth/register with a failing create = %d, want 409 (body %s)", failed.Code, snippet(failed.Body.String()))
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
	retry := postJSON(srv, "/api/v1/auth/register", map[string]string{
		"username": "invitee",
		"email":    "invitee@example.com",
		"password": "Sup3rSecret!",
		"invite":   inv.Token,
	}, []*http.Cookie{cookie}, headers)
	if retry.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/register after the rollback = %d, want 200 (body %s)", retry.Code, snippet(retry.Body.String()))
	}
	used, err := invites.ByToken(context.Background(), inv.Token)
	if err != nil {
		t.Fatalf("ByToken() error = %v", err)
	}
	if used.UsedAt == nil || used.UsedBy == nil {
		t.Fatalf("invite after a successful accept = %+v, want used_at and used_by set", used)
	}
}
