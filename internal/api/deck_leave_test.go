package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// listDeckRolesViaHTTP 用 key 调 GET /api/v1/decks，返回 deck public id -> role 映射。
func listDeckRolesViaHTTP(t *testing.T, env *testEnv, key string) map[string]string {
	t.Helper()
	status, raw := doJSON(t, env.router(), http.MethodGet, "/api/v1/decks", key, "")
	if status != http.StatusOK {
		t.Fatalf("GET /decks status = %d, want 200 (body %s)", status, raw)
	}
	var body struct {
		Decks []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"decks"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode decks: %v (%s)", err, raw)
	}
	out := make(map[string]string, len(body.Decks))
	for _, d := range body.Decks {
		out[d.ID] = d.Role
	}
	return out
}

// leaveDeckViaHTTP 用 key 调 DELETE /api/v1/decks/:id/membership（:id 是卡组对外 id）。
func leaveDeckViaHTTP(t *testing.T, env *testEnv, key string, deckPublicID string) (int, []byte) {
	t.Helper()
	return doJSON(t, env.router(), http.MethodDelete, fmt.Sprintf("/api/v1/decks/%s/membership", deckPublicID), key, "")
}

// TestListDecksReportsCallerRole 断言列表逐张带出调用者的显式关系：
// 自有 -> owner，被显式授权 -> 授权角色；别人没授权给我的卡组根本不进列表。
//
// 角色必居其一（owner 或授权角色）：不存在「看得到但没有角色」的卡组，
// 界面因此不必为那种情况准备分支。
func TestListDecksReportsCallerRole(t *testing.T) {
	env := newTestEnv(t, 600, 600)
	owner := seedUser(t, env.db, "role_owner", store.RoleUser)
	viewer := seedUser(t, env.db, "role_viewer", store.RoleUser)

	ownDeck := seedDeck(t, env.db, viewer.ID)
	grantedDeck := seedDeck(t, env.db, owner.ID)
	foreignDeck := seedDeck(t, env.db, owner.ID)
	if err := store.NewGrantStore(env.db).Grant(context.Background(), grantedDeck.ID, viewer.ID, store.RoleEditor, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant viewer: %v", err)
	}

	key := seedKey(t, env.keys, viewer.ID, []string{store.ScopeRead}, nil)
	roles := listDeckRolesViaHTTP(t, env, key.Plaintext)

	if roles[ownDeck.PublicID] != store.RoleOwner {
		t.Errorf("own deck role = %q, want %q", roles[ownDeck.PublicID], store.RoleOwner)
	}
	if roles[grantedDeck.PublicID] != store.RoleEditor {
		t.Errorf("granted deck role = %q, want %q", roles[grantedDeck.PublicID], store.RoleEditor)
	}
	if _, ok := roles[foreignDeck.PublicID]; ok {
		t.Errorf("another user's ungranted deck %s appeared in the list: %v", foreignDeck.PublicID, roles)
	}
	if len(roles) != 2 {
		t.Errorf("listed roles = %v, want exactly the owned and the granted deck", roles)
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

	status, raw := leaveDeckViaHTTP(t, env, key.Plaintext, deck.PublicID)
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

// TestLeaveDeckRejectsNonMember 断言没有授权行的用户没有可退出的成员身份：
// 返回 not_found，而且他本来就打不开这张卡组（403）——「没有成员身份」不等于「能看」。
func TestLeaveDeckRejectsNonMember(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "leave_nm_owner", store.RoleUser)
	stranger := seedUser(t, env.db, "leave_nm_stranger", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	ctx := context.Background()

	err := env.api.LeaveDeck(ctx, stranger, deck.ID, nil)
	status, code := serviceCode(t, err)
	if status != http.StatusNotFound || code != CodeNotFound {
		t.Fatalf("stranger LeaveDeck = (%d,%q), want (404,%q)", status, code, CodeNotFound)
	}
	if _, err := env.api.RequireDeckRole(ctx, stranger.ID, deck.ID, store.RoleReader); err == nil {
		t.Fatalf("stranger could read a deck he was never granted")
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

	status, raw := leaveDeckViaHTTP(t, env, key.Plaintext, deck.PublicID)
	if status != http.StatusForbidden {
		t.Fatalf("read-scope leave status = %d, want 403 (body %s)", status, raw)
	}
	if role, err := store.NewGrantStore(env.db).Role(ctx, deck.ID, member.ID); err != nil || role != store.RoleReader {
		t.Fatalf("grant after scope-denied leave = (%q,%v), want (%q,nil)", role, err, store.RoleReader)
	}
}
