package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// listDeckRolesViaHTTP 用 key 调 GET /api/v1/decks，返回 deck_id -> role 映射。
func listDeckRolesViaHTTP(t *testing.T, env *testEnv, key string) map[uint64]string {
	t.Helper()
	status, raw := doJSON(t, env.router(), http.MethodGet, "/api/v1/decks", key, "")
	if status != http.StatusOK {
		t.Fatalf("GET /decks status = %d, want 200 (body %s)", status, raw)
	}
	var body struct {
		Decks []struct {
			ID   uint64 `json:"id"`
			Role string `json:"role"`
		} `json:"decks"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode decks: %v (%s)", err, raw)
	}
	out := make(map[uint64]string, len(body.Decks))
	for _, d := range body.Decks {
		out[d.ID] = d.Role
	}
	return out
}

// leaveDeckViaHTTP 用 key 调 DELETE /api/v1/decks/:id/membership。
func leaveDeckViaHTTP(t *testing.T, env *testEnv, key string, deckID uint64) (int, []byte) {
	t.Helper()
	return doJSON(t, env.router(), http.MethodDelete, fmt.Sprintf("/api/v1/decks/%d/membership", deckID), key, "")
}

// TestListDecksReportsCallerRole 断言列表逐张带出调用者的显式关系：
// 自有 -> owner，被显式授权 -> 授权角色，仅因 public 可见 -> 空串。
//
// 空串必须与「被授予的只读」区分开：若把可见性折算成隐式 reader，界面会给陌生人的
// 公开卡组错误地渲染「退出共享」，而它根本没有可退出的成员身份。
func TestListDecksReportsCallerRole(t *testing.T) {
	env := newTestEnv(t, 600, 600)
	owner := seedUser(t, env.db, "role_owner", store.RoleUser)
	viewer := seedUser(t, env.db, "role_viewer", store.RoleUser)

	ownDeck := seedDeck(t, env.db, viewer.ID)
	grantedDeck := seedDeck(t, env.db, owner.ID)
	publicDeck := seedDeck(t, env.db, owner.ID)
	setDeckVisibility(t, env, publicDeck.ID, store.DeckVisibilityPublic)
	if err := store.NewGrantStore(env.db).Grant(context.Background(), grantedDeck.ID, viewer.ID, store.RoleEditor, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant viewer: %v", err)
	}

	key := seedKey(t, env.keys, viewer.ID, []string{store.ScopeRead}, nil)
	roles := listDeckRolesViaHTTP(t, env, key.Plaintext)

	if roles[ownDeck.ID] != store.RoleOwner {
		t.Errorf("own deck role = %q, want %q", roles[ownDeck.ID], store.RoleOwner)
	}
	if roles[grantedDeck.ID] != store.RoleEditor {
		t.Errorf("granted deck role = %q, want %q", roles[grantedDeck.ID], store.RoleEditor)
	}
	got, ok := roles[publicDeck.ID]
	if !ok {
		t.Fatalf("public deck %d missing from list: %v", publicDeck.ID, roles)
	}
	if got != "" {
		t.Errorf("public deck role = %q, want empty (visible only via public)", got)
	}
}

// TestLeaveDeckRemovesOwnGrant 是核心验收：被共享者能退出——授权行被删除、审计留痕，
// 且紧接的下一次访问立即被拒（撤销立即生效）。
func TestLeaveDeckRemovesOwnGrant(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "leave_owner", store.RoleUser)
	member := seedUser(t, env.db, "leave_member", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	ctx := context.Background()
	grants := store.NewGrantStore(env.db)
	if err := grants.Grant(ctx, deck.ID, member.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant member: %v", err)
	}
	key := seedKey(t, env.keys, member.ID, []string{store.ScopeWrite}, nil)

	status, raw := leaveDeckViaHTTP(t, env, key.Plaintext, deck.ID)
	if status != http.StatusOK {
		t.Fatalf("leave status = %d, want 200 (body %s)", status, raw)
	}
	if role, err := grants.Role(ctx, deck.ID, member.ID); err != nil || role != "" {
		t.Fatalf("grant after leave = (%q,%v), want (\"\",nil)", role, err)
	}
	// 退出立即生效：同一个成员的下一次访问被拒。
	if _, err := env.api.RequireDeckRole(ctx, member.ID, deck.ID, store.RoleReader); err == nil {
		t.Fatalf("member still has access after leaving")
	}
	// 审计留痕：一条 deck.revoke。
	if n, err := store.NewAuditStore(env.db).CountByAction(ctx, store.ActionDeckRevoke); err != nil || n != 1 {
		t.Fatalf("deck.revoke audit rows = %d (err %v), want 1", n, err)
	}
}

// TestLeaveDeckRejectsOwner 断言属主不能「退出」自己的卡组：对自有卡组只有删除，
// 退出必须被拒，避免把不可逆的删除混成可逆的退出。
func TestLeaveDeckRejectsOwner(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "leave_own_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)

	err := env.api.LeaveDeck(context.Background(), owner, deck.ID, nil)
	status, code := serviceCode(t, err)
	if status != http.StatusBadRequest || code != CodeInvalidRequest {
		t.Fatalf("owner LeaveDeck = (%d,%q), want (400,%q)", status, code, CodeInvalidRequest)
	}
}

// TestLeaveDeckRejectsNonMember 断言没有授权行的用户没有可退出的成员身份（陌生人的公开卡组
// 就是这种情况）：返回 not_found，且卡组因 public 仍对他可见。
func TestLeaveDeckRejectsNonMember(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "leave_nm_owner", store.RoleUser)
	stranger := seedUser(t, env.db, "leave_nm_stranger", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	setDeckVisibility(t, env, deck.ID, store.DeckVisibilityPublic)
	ctx := context.Background()

	err := env.api.LeaveDeck(ctx, stranger, deck.ID, nil)
	status, code := serviceCode(t, err)
	if status != http.StatusNotFound || code != CodeNotFound {
		t.Fatalf("stranger LeaveDeck = (%d,%q), want (404,%q)", status, code, CodeNotFound)
	}
	if _, err := env.api.RequireDeckRole(ctx, stranger.ID, deck.ID, store.RoleReader); err != nil {
		t.Fatalf("public deck no longer readable after a rejected leave: %v", err)
	}
}

// TestLeaveDeckRequiresWriteScope 断言只读 key 打退出端点被 scope 门挡下，且授权行原样保留。
func TestLeaveDeckRequiresWriteScope(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "leave_scope_owner", store.RoleUser)
	member := seedUser(t, env.db, "leave_scope_member", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	ctx := context.Background()
	if err := store.NewGrantStore(env.db).Grant(ctx, deck.ID, member.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant member: %v", err)
	}
	key := seedKey(t, env.keys, member.ID, []string{store.ScopeRead}, nil)

	status, raw := leaveDeckViaHTTP(t, env, key.Plaintext, deck.ID)
	if status != http.StatusForbidden {
		t.Fatalf("read-scope leave status = %d, want 403 (body %s)", status, raw)
	}
	if role, err := store.NewGrantStore(env.db).Role(ctx, deck.ID, member.ID); err != nil || role != store.RoleReader {
		t.Fatalf("grant after scope-denied leave = (%q,%v), want (%q,nil)", role, err, store.RoleReader)
	}
}
