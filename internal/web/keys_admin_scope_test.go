package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// loginAs 落一行指定角色的用户并登录，返回其 id、会话 cookie 与服务端 CSRF token。
// 与 loginMember 同构，但角色可指定：F11 需要同时覆盖普通用户与管理员两条路径。
func loginAs(t *testing.T, srv *Server, db *gorm.DB, username, role string) (uint64, []*http.Cookie, string) {
	t.Helper()
	hasher := auth.NewPasswordHasher(auth.Params{Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32})
	hash, err := hasher.Hash("Sup3rSecret!")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	u := store.User{
		Username: username, Email: username + "@example.com", DisplayName: username,
		PasswordHash: &hash, Role: role, Status: store.StatusActive,
		Locale: "zh-CN", Timezone: "UTC", DayCutoffHour: store.Ptr(4), CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	login := postForm(t, srv, "/login", url.Values{
		"username": {username}, "password": {"Sup3rSecret!"},
	}, nil)
	if login.Code != http.StatusSeeOther {
		t.Fatalf("POST /login (%s) = %d, want 303", username, login.Code)
	}
	var sess store.Session
	if err := db.Where("user_id = ?", u.ID).Order("created_at desc").First(&sess).Error; err != nil {
		t.Fatalf("load session: %v", err)
	}
	return u.ID, login.Result().Cookies(), sess.CSRFToken
}

// listScopes 读出某用户唯一那把 key 的归一化 scopes 字符串。
func listScopes(t *testing.T, db *gorm.DB, userID uint64) string {
	t.Helper()
	keys, err := store.NewAPIKeyStore(db).ListByUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("keys = %d, want 1", len(keys))
	}
	return keys[0].Scopes
}

// TestSettingsKeysRejectsAdminScopeForMembers 覆盖 F11：普通用户经网页表单提交 admin
// scope 必须被服务端拒绝（4xx、本地化提示、不落库）。表单是否渲染该选项只是辅助。
func TestSettingsKeysRejectsAdminScopeForMembers(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	memberID, cookies, csrf := loginAs(t, srv, db, "scope_member", store.RoleUser)

	before := keyCount(t, db, memberID)
	rec := postForm(t, srv, "/settings/keys", url.Values{
		"name": {"evil"}, "scopes": {store.ScopeAdmin}, "csrf_token": {csrf},
	}, cookies)
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("POST admin scope as member = %d, want 4xx (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "只有管理员") {
		t.Errorf("rejection is not localized; body = %s", snippet(rec.Body.String()))
	}
	if after := keyCount(t, db, memberID); after != before {
		t.Fatalf("rejected admin-scope create still wrote a key: %d -> %d", before, after)
	}
}

// TestSettingsKeysAllowsKeysScopeForMembers 覆盖 F11：普通用户可以创建 keys scope 的
// key——keys 是给普通用户管理自己 key 的独立档位。
func TestSettingsKeysAllowsKeysScopeForMembers(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	memberID, cookies, csrf := loginAs(t, srv, db, "keysscope_member", store.RoleUser)

	rec := postForm(t, srv, "/settings/keys", url.Values{
		"name": {"self manage"}, "scopes": {store.ScopeKeys}, "csrf_token": {csrf},
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST keys scope as member = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if got := listScopes(t, db, memberID); got != store.ScopeKeys {
		t.Errorf("stored scopes = %q, want %q", got, store.ScopeKeys)
	}
}

// TestSettingsKeysAllowsAdminScopeForAdmins 覆盖 F11：管理员不受该限制，仍可创建
// admin scope 的 key。
func TestSettingsKeysAllowsAdminScopeForAdmins(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	adminID, cookies, csrf := loginAs(t, srv, db, "scope_admin", store.RoleAdmin)

	rec := postForm(t, srv, "/settings/keys", url.Values{
		"name": {"ops"}, "scopes": {store.ScopeAdmin}, "csrf_token": {csrf},
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST admin scope as admin = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if got := listScopes(t, db, adminID); got != store.ScopeAdmin {
		t.Errorf("stored scopes = %q, want %q", got, store.ScopeAdmin)
	}
}

// TestSettingsKeysFormHidesAdminOptionForMembers 断言表单层只给普通用户渲染
// read/write/review/keys 四档，admin 选项仅对管理员出现（辅助防线，非唯一防线）。
func TestSettingsKeysFormHidesAdminOptionForMembers(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	// GET /settings/keys 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 回退页（DESIGN.md §8.5）。
	srv.spa = nil

	_, memberCookies, _ := loginAs(t, srv, db, "form_member", store.RoleUser)
	memberBody := getWithCookies(t, srv, "/settings/keys", memberCookies).Body.String()
	if strings.Contains(memberBody, `value="admin"`) {
		t.Errorf("member form still renders the admin scope checkbox; body = %s", snippet(memberBody))
	}
	if !strings.Contains(memberBody, `value="keys"`) {
		t.Errorf("member form is missing the keys scope checkbox; body = %s", snippet(memberBody))
	}

	_, adminCookies, _ := loginAs(t, srv, db, "form_admin", store.RoleAdmin)
	adminBody := getWithCookies(t, srv, "/settings/keys", adminCookies).Body.String()
	if !strings.Contains(adminBody, `value="admin"`) {
		t.Errorf("admin form is missing the admin scope checkbox; body = %s", snippet(adminBody))
	}
}
