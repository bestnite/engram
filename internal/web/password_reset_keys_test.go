package web

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 F18 的验收测试：口令重置（忘记密码、管理员重置）必须在同一事务里一并吊销
// 该用户的全部 API Key（DESIGN.md §11）；用户主动改密不吊销，保持 key 独立生命周期。
//
// 断言分三层：库里的 revoked_at 已写、旧 key 对真实 /api/v1 链 401、旧会话同时失效。
// 反面对照（TestSelfPasswordChangeKeepsAPIKeys）锁死「主动改密不吊销」的边界。

// seedAPIKey 直接为用户落一把 read scope 的 key，返回创建结果（含一次性明文）。
func seedAPIKey(t *testing.T, db *gorm.DB, userID uint64, name string) *store.CreatedAPIKey {
	t.Helper()
	created, err := store.NewAPIKeyStore(db).Create(context.Background(), store.CreateAPIKeyParams{
		UserID: userID, Name: name, Scopes: []string{store.ScopeRead},
	})
	if err != nil {
		t.Fatalf("Create() api key %q error = %v", name, err)
	}
	return created
}

// assertAllKeysRevoked 断言某用户库里的 key 全部已写 revoked_at。
func assertAllKeysRevoked(t *testing.T, db *gorm.DB, userID uint64) {
	t.Helper()
	keys, err := store.NewAPIKeyStore(db).ListByUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListByUser(%d) error = %v", userID, err)
	}
	if len(keys) == 0 {
		t.Fatalf("user %d has no api keys; nothing to assert", userID)
	}
	for _, k := range keys {
		if k.RevokedAt == nil {
			t.Errorf("api key %d (%s) for user %d is not revoked", k.ID, k.Name, userID)
		}
	}
}

// TestForgotPasswordResetRevokesAPIKeys 覆盖「忘记密码」完整流程：
// 用户先有两把可用 key → 发起重置 + 用真实令牌重置 → 两把 key 立即 401、库里已失效，
// 同时旧会话仍被吊销（回归保护，不能为了 key 丢掉会话行为）。
func TestForgotPasswordResetRevokesAPIKeys(t *testing.T) {
	ts := newSecurityServer(t, true)
	rest := newKeysAPI(t, ts.db)
	k1 := seedAPIKey(t, ts.db, ts.ownerID, "laptop")
	k2 := seedAPIKey(t, ts.db, ts.ownerID, "phone")

	// 重置前两把 key 都能过 REST。
	for _, k := range []*store.CreatedAPIKey{k1, k2} {
		if code := apiGetDecks(t, rest, k.Plaintext); code != http.StatusOK {
			t.Fatalf("pre-reset GET /api/v1/decks with key %q = %d, want 200", k.Key.Name, code)
		}
	}

	// 完整忘记密码流程：请求发信 → 用邮件里的真实令牌设置新密码。
	plain, _ := issueResetToken(t, ts)
	reset := resetPasswordJSON(t, ts, plain, "N3wSup3rSecret!")
	if reset.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/reset-password status = %d, want 200 (body %s)", reset.Code, snippet(reset.Body.String()))
	}

	// 两把旧 key：库里已失效，且对真实 REST 链返回 401（与既有撤销 key 语义一致）。
	assertAllKeysRevoked(t, ts.db, ts.ownerID)
	for _, k := range []*store.CreatedAPIKey{k1, k2} {
		if code := apiGetDecks(t, rest, k.Plaintext); code != http.StatusUnauthorized {
			t.Errorf("post-reset GET /api/v1/decks with key %q = %d, want 401", k.Key.Name, code)
		}
	}

	// 会话吐销行为不被破坏：重置前的会话必须失效，受保护页面重定向到登录。
	if rec := getWithCookies(t, ts.srv, "/admin/users", ts.cookies); rec.Code != http.StatusSeeOther {
		t.Errorf("old session after reset = %d, want 303 to login", rec.Code)
	}
}

// TestAdminPasswordResetRevokesAPIKeys 覆盖管理员重置他人密码：目标用户全部 key 一并吊销。
func TestAdminPasswordResetRevokesAPIKeys(t *testing.T) {
	ts := newSecurityServer(t, true)
	targetID := createTargetUser(t, ts)
	rest := newKeysAPI(t, ts.db)
	k1 := seedAPIKey(t, ts.db, targetID, "target-a")
	k2 := seedAPIKey(t, ts.db, targetID, "target-b")

	if code := apiGetDecks(t, rest, k1.Plaintext); code != http.StatusOK {
		t.Fatalf("pre-reset GET /api/v1/decks = %d, want 200", code)
	}

	rec := jsonRequest(t, ts.srv, http.MethodPost, "/api/v1/admin/users/"+strconv.FormatUint(targetID, 10)+"/password",
		`{"confirm":true}`, ts.cookies, ts.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/admin/users/:id/password status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	assertAllKeysRevoked(t, ts.db, targetID)
	for _, k := range []*store.CreatedAPIKey{k1, k2} {
		if code := apiGetDecks(t, rest, k.Plaintext); code != http.StatusUnauthorized {
			t.Errorf("post-admin-reset GET /api/v1/decks with key %q = %d, want 401", k.Key.Name, code)
		}
	}
}

// TestSelfPasswordChangeKeepsAPIKeys 是反面对照：用户主动改密（/settings/password）
// 绝不吊销自己的 key（DESIGN.md §11）。缩小范围的顺手改动会让这条变红。
func TestSelfPasswordChangeKeepsAPIKeys(t *testing.T) {
	ts := newSecurityServer(t, true)
	member, err := ts.srv.accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "member", Email: "member@example.com", Password: "Sup3rSecret!", Role: store.RoleUser, Locale: "en",
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	memberID := member.ID
	cookies, csrf := loginJSON(t, ts.srv, ts.db, "member", "Sup3rSecret!")
	rest := newKeysAPI(t, ts.db)
	k1 := seedAPIKey(t, ts.db, memberID, "cli")

	if code := apiGetDecks(t, rest, k1.Plaintext); code != http.StatusOK {
		t.Fatalf("pre-change GET /api/v1/decks = %d, want 200", code)
	}

	rec := jsonRequest(t, ts.srv, http.MethodPatch, "/api/v1/settings/password",
		`{"old_password":"Sup3rSecret!","new_password":"N3wSup3rSecret!"}`, cookies, csrf)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PATCH /api/v1/settings/password status = %d, want 204 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	keys, err := store.NewAPIKeyStore(ts.db).ListByUser(context.Background(), memberID)
	if err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	if len(keys) != 1 || keys[0].RevokedAt != nil {
		t.Errorf("self password change revoked the user's own api key; want it kept (DESIGN.md §11)")
	}
	if code := apiGetDecks(t, rest, k1.Plaintext); code != http.StatusOK {
		t.Errorf("old api key after self change = %d, want 200", code)
	}
}
