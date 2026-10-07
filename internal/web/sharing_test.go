package web

import (
	"context"
	"net/http"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// createUserAndLogin 在同一测试库上新建一个普通用户并登录，返回其 id、会话 cookie 与 CSRF token。
func createUserAndLogin(t *testing.T, srv *Server, db *gorm.DB, username string) (uint64, []*http.Cookie, string) {
	t.Helper()
	accounts, err := auth.NewAccountService(store.NewUserStore(db), store.NewSessionStore(db), store.NewAPIKeyStore(db),
		auth.NewPasswordHasher(auth.Params{Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32}))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	u, err := accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: username, Email: username + "@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser(%q) error = %v", username, err)
	}
	// 登录走 SPA 的同源 JSON 端点：失败即 Fatal，成功返回会话 cookie 与会话绑定的 CSRF token。
	cookies, csrf := loginJSON(t, srv, db, username, "Sup3rSecret!")
	return u.ID, cookies, csrf
}

// grantsJSONPath 是共享授权的 JSON 路径。
func grantsJSONPath(deckID uint64) string {
	return "/api/v1/decks/" + u64str(deckID) + "/sharing/grants"
}

// TestSharingGrantGivesImmediateAccessThenRevokeDeniesNextRequest 是 M5-2 的主验收：
// owner 授权后对方马上能访问；撤销后对方下一次请求立即被拒；并各写一行审计。
// 授权写路径走 SPA 的 JSON 端点（SSR 共享表单入口已删除）。
func TestSharingGrantGivesImmediateAccessThenRevokeDeniesNextRequest(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Shared deck")
	deckPath := "/decks/" + u64str(deck.ID)
	user2ID, u2Cookies, _ := createUserAndLogin(t, srv, db, "reader2")

	// 授权前：无访问权，GET 列表页被拒 403。
	if rec := getWithCookies(t, srv, deckPath+"/notes", u2Cookies); rec.Code != http.StatusForbidden {
		t.Fatalf("before grant: GET notes status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}

	// owner 授予 reader。
	rec := jsonRequest(t, srv, http.MethodPost, grantsJSONPath(deck.ID),
		`{"username":"reader2","role":"reader"}`, ownerCookies, ownerCSRF)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST grant status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// 授权后：对方下一个请求立即能访问。
	if rec := getWithCookies(t, srv, deckPath+"/notes", u2Cookies); rec.Code != http.StatusOK {
		t.Fatalf("after grant: GET notes status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// 改角色 reader -> editor：写 deck.role_change，对方仍可访问。
	rec = jsonRequest(t, srv, http.MethodPatch, grantsJSONPath(deck.ID)+"/"+u64str(user2ID),
		`{"role":"editor"}`, ownerCookies, ownerCSRF)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH role status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := getWithCookies(t, srv, deckPath+"/notes", u2Cookies); rec.Code != http.StatusOK {
		t.Fatalf("after role change: GET notes status = %d, want 200", rec.Code)
	}

	// 撤销：对方下一次请求立即被拒。
	rec = jsonRequest(t, srv, http.MethodDelete, grantsJSONPath(deck.ID)+"/"+u64str(user2ID),
		"", ownerCookies, ownerCSRF)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE grant status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := getWithCookies(t, srv, deckPath+"/notes", u2Cookies); rec.Code != http.StatusForbidden {
		t.Fatalf("after revoke: GET notes status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}

	// 审计：授予 1、改角色 1、撤销 1。
	audits := store.NewAuditStore(db)
	for _, c := range []struct {
		action string
		want   int64
	}{
		{store.ActionDeckGrant, 1},
		{store.ActionDeckRoleChange, 1},
		{store.ActionDeckRevoke, 1},
	} {
		n, err := audits.CountByAction(context.Background(), c.action)
		if err != nil {
			t.Fatalf("count audit %s: %v", c.action, err)
		}
		if n != c.want {
			t.Errorf("audit rows for %s = %d, want %d", c.action, n, c.want)
		}
	}
}

// TestSharingPageRejectsNonOwner 覆盖反面用例：非 owner 打不开共享页、写端点也被拒且不落库。
func TestSharingPageRejectsNonOwner(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Owner only")
	deckPath := "/decks/" + u64str(deck.ID)
	// 建一个普通用户并授予 editor：他仍不是 owner，不能管理授权。
	user2ID, u2Cookies, u2CSRF := createUserAndLogin(t, srv, db, "editor2")
	if rec := jsonRequest(t, srv, http.MethodPost, grantsJSONPath(deck.ID),
		`{"username":"editor2","role":"editor"}`, ownerCookies, ownerCSRF); rec.Code != http.StatusOK {
		t.Fatalf("owner grant editor status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// editor 打开共享页 -> 403。
	if rec := getWithCookies(t, srv, deckPath+"/sharing", u2Cookies); rec.Code != http.StatusForbidden {
		t.Fatalf("editor GET sharing status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	// editor 提交授权 -> 403，且不新增授权行。
	before, err := store.NewGrantStore(db).ListByDeck(context.Background(), deck.ID)
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if rec := jsonRequest(t, srv, http.MethodPost, grantsJSONPath(deck.ID),
		`{"username":"editor2","role":"reader"}`, u2Cookies, u2CSRF); rec.Code != http.StatusForbidden {
		t.Fatalf("editor POST grant status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	after, err := store.NewGrantStore(db).ListByDeck(context.Background(), deck.ID)
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("grant rows changed by non-owner: before=%d after=%d", len(before), len(after))
	}
	role, err := store.NewGrantStore(db).Role(context.Background(), deck.ID, user2ID)
	if err != nil {
		t.Fatalf("read editor role: %v", err)
	}
	if role != store.RoleEditor {
		t.Errorf("editor role = %q, want %q (unchanged)", role, store.RoleEditor)
	}
}

// TestSharingGrantRequiresCSRF 是必测负例：缺 CSRF token 的授权写请求被拒。
func TestSharingGrantRequiresCSRF(t *testing.T) {
	srv, db, ownerID, ownerCookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "CSRF deck")
	createUserAndLogin(t, srv, db, "csrf_target")
	rec := jsonRequest(t, srv, http.MethodPost, grantsJSONPath(deck.ID),
		`{"username":"csrf_target","role":"reader"}`, ownerCookies, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST grant without CSRF status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	grants, err := store.NewGrantStore(db).ListByDeck(context.Background(), deck.ID)
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if len(grants) != 0 {
		t.Errorf("grant created despite missing CSRF: %d row(s)", len(grants))
	}
}
