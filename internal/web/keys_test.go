package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// keysPlaintextRe 匹配一次性明文（fcard_ 后至少 20 个 base64url 字符）；展示前缀只有
// 8 个字符，因此不会误命中列表里的前缀。
var keysPlaintextRe = regexp.MustCompile(`fcard_[A-Za-z0-9_-]{20,}`)

// newKeysAPI 用同一份数据库装配真实的 /api/v1 handler 链，用于验证创建出来的 key
// 能通过 bearer 鉴权访问 GET /api/v1/decks。
func newKeysAPI(t *testing.T, db *gorm.DB) http.Handler {
	t.Helper()
	a, err := api.New(api.Deps{
		DB:      db,
		Logger:  discardLogger(),
		Keys:    store.NewAPIKeyStore(db),
		Users:   store.NewUserStore(db),
		Decks:   store.NewDeckStore(db),
		Notes:   store.NewNoteStore(db),
		Presets: store.NewPresetStore(db),
		Cards:   store.NewCardStore(db),
	})
	if err != nil {
		t.Fatalf("api.New() error = %v", err)
	}
	r := gin.New()
	a.Register(r)
	return r
}

// apiGetDecks 用 bearer key 请求 GET /api/v1/decks，返回状态码。
func apiGetDecks(t *testing.T, h http.Handler, plaintext string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/decks", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// loginMember 直接落一行非管理员用户并登录，返回其 id、会话 cookie 与服务端 CSRF token。
// 验收明确要求「非管理员」也能创建 key，因此不复用 newNotesServer 的 admin owner。
func loginMember(t *testing.T, srv *Server, db *gorm.DB, username string) (uint64, []*http.Cookie, string) {
	t.Helper()
	hasher := auth.NewPasswordHasher(auth.Params{Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32})
	hash, err := hasher.Hash("Sup3rSecret!")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	u := store.User{
		Username: username, Email: username + "@example.com", DisplayName: username,
		PasswordHash: &hash, Role: store.RoleUser, Status: store.StatusActive,
		Locale: "zh-CN", Timezone: "UTC", DayCutoffHour: store.Ptr(4), CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create member: %v", err)
	}
	login := postForm(t, srv, "/login", url.Values{
		"username": {username}, "password": {"Sup3rSecret!"},
	}, nil)
	if login.Code != http.StatusSeeOther {
		t.Fatalf("POST /login (%s) = %d, want 303", username, login.Code)
	}
	var sess store.Session
	if err := db.Where("user_id = ?", u.ID).Order("created_at desc").First(&sess).Error; err != nil {
		t.Fatalf("load member session: %v", err)
	}
	return u.ID, login.Result().Cookies(), sess.CSRFToken
}

// keyCount 返回某用户当前的 key 数。
func keyCount(t *testing.T, db *gorm.DB, userID uint64) int {
	t.Helper()
	keys, err := store.NewAPIKeyStore(db).ListByUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	return len(keys)
}

// TestSettingsKeysLifecycle 是 M4-10 的核心验收：
// 非管理员在浏览器里创建 key（明文只出现一次）→ 用它对 GET /api/v1/decks 通过鉴权 →
// 列表页后续不再显示明文 → 撤销后同一 key 立即鉴权失败。
func TestSettingsKeysLifecycle(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	memberID, cookies, csrf := loginMember(t, srv, db, "member")

	// 页面可达，且带创建入口。
	page := getWithCookies(t, srv, "/settings/keys", cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /settings/keys = %d, want 200 (body %s)", page.Code, snippet(page.Body.String()))
	}
	if body := page.Body.String(); !strings.Contains(body, "我的 API Key") || !strings.Contains(body, "创建") {
		t.Errorf("keys page is missing the localized heading/create text; body = %s", snippet(body))
	}

	// 创建：name + read scope。响应体必须携带一次明文。
	create := postForm(t, srv, "/settings/keys", url.Values{
		"name": {"ci key"}, "scopes": {store.ScopeRead}, "csrf_token": {csrf},
	}, cookies)
	if create.Code != http.StatusOK {
		t.Fatalf("POST /settings/keys = %d, want 200 (body %s)", create.Code, snippet(create.Body.String()))
	}
	plaintext := keysPlaintextRe.FindString(create.Body.String())
	if plaintext == "" {
		t.Fatalf("create response did not show the plaintext once; body = %s", snippet(create.Body.String()))
	}

	keys, err := store.NewAPIKeyStore(db).ListByUser(context.Background(), memberID)
	if err != nil || len(keys) != 1 {
		t.Fatalf("after create: keys = %d, err = %v, want 1", len(keys), err)
	}
	keyID := keys[0].ID
	// 库里只存 sha256：既不是明文，也必须等于明文的 sha256。
	if keys[0].KeyHash == plaintext {
		t.Fatalf("stored value is the plaintext key")
	}
	if keys[0].KeyHash != store.HashAPIKey(plaintext) {
		t.Fatalf("stored value is not sha256(plaintext)")
	}

	// 明文只出现一次：后续 GET 列表不再包含它，但显示前缀、名称与状态。
	later := getWithCookies(t, srv, "/settings/keys", cookies)
	if later.Code != http.StatusOK {
		t.Fatalf("GET /settings/keys after create = %d, want 200", later.Code)
	}
	laterBody := later.Body.String()
	if strings.Contains(laterBody, plaintext) {
		t.Fatalf("later GET leaked the plaintext key")
	}
	if !strings.Contains(laterBody, keys[0].Prefix) || !strings.Contains(laterBody, "ci key") {
		t.Errorf("list is missing prefix or name; body = %s", snippet(laterBody))
	}

	// 用明文对真实的 /api/v1 handler 链鉴权：GET /api/v1/decks 必须 200。
	apiHandler := newKeysAPI(t, db)
	if code := apiGetDecks(t, apiHandler, plaintext); code != http.StatusOK {
		t.Fatalf("GET /api/v1/decks with the created key = %d, want 200", code)
	}

	// 撤销：303 回列表，状态变为已撤销。
	revoke := postForm(t, srv, "/settings/keys/"+strconv.FormatUint(keyID, 10)+"/revoke",
		url.Values{"csrf_token": {csrf}}, cookies)
	if revoke.Code != http.StatusSeeOther {
		t.Fatalf("POST revoke = %d, want 303 (body %s)", revoke.Code, snippet(revoke.Body.String()))
	}
	var stored store.APIKey
	if err := db.First(&stored, "id = ?", keyID).Error; err != nil {
		t.Fatalf("reload key: %v", err)
	}
	if stored.RevokedAt == nil {
		t.Fatal("key was not revoked")
	}
	if code := apiGetDecks(t, apiHandler, plaintext); code != http.StatusUnauthorized {
		t.Errorf("revoked key against /api/v1/decks = %d, want 401", code)
	}
	if body := getWithCookies(t, srv, "/settings/keys", cookies).Body.String(); !strings.Contains(body, "已撤销") {
		t.Errorf("list does not show the revoked state; body = %s", snippet(body))
	}

	// 创建与撤销各写一条审计（两条动作常量都来自 store 包）。
	ctx := context.Background()
	if n, err := store.NewAuditStore(db).CountByAction(ctx, store.ActionAPIKeyCreate); err != nil || n != 1 {
		t.Errorf("create audit rows = (%d, %v), want (1, nil)", n, err)
	}
	if n, err := store.NewAuditStore(db).CountByAction(ctx, store.ActionAPIKeyRevoke); err != nil || n != 1 {
		t.Errorf("revoke audit rows = (%d, %v), want (1, nil)", n, err)
	}
}

// TestSettingsKeysIsolation 断言他人的 key 既不列出也不可撤销（撤销映射成 404，不泄露存在性）。
func TestSettingsKeysIsolation(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	ctx := context.Background()

	// 另一个用户（非 owner）的一把 key。
	other := store.User{
		Username: "other", Email: "other@example.com", DisplayName: "other",
		Role: store.RoleUser, Status: store.StatusActive,
		Locale: "zh-CN", Timezone: "UTC", DayCutoffHour: store.Ptr(4), CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatalf("create other user: %v", err)
	}
	created, err := store.NewAPIKeyStore(db).Create(ctx, store.CreateAPIKeyParams{
		UserID: other.ID, Name: "not mine", Scopes: []string{store.ScopeRead},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	body := getWithCookies(t, srv, "/settings/keys", cookies).Body.String()
	if strings.Contains(body, "not mine") || strings.Contains(body, created.Key.Prefix) {
		t.Fatalf("another user's key appears in the list; body = %s", snippet(body))
	}

	rec := postForm(t, srv, "/settings/keys/"+strconv.FormatUint(created.Key.ID, 10)+"/revoke",
		url.Values{"csrf_token": {csrf}}, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("revoking another user's key = %d, want 404", rec.Code)
	}
	var stored store.APIKey
	if err := db.First(&stored, "id = ?", created.Key.ID).Error; err != nil {
		t.Fatalf("reload other key: %v", err)
	}
	if stored.RevokedAt != nil {
		t.Fatal("another user's key was revoked")
	}
}

// TestSettingsKeysValidation 覆盖创建表单的负例：每种非法输入都必须 4xx 且不写库。
func TestSettingsKeysValidation(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	memberID, cookies, csrf := loginMember(t, srv, db, "member2")

	cases := []struct {
		name   string
		form   url.Values
		want   int
		substr string
	}{
		{
			name:   "empty name",
			form:   url.Values{"name": {"   "}, "scopes": {store.ScopeRead}, "csrf_token": {csrf}},
			want:   http.StatusBadRequest,
			substr: "请填写名称",
		},
		{
			name:   "no scope selected",
			form:   url.Values{"name": {"k"}, "csrf_token": {csrf}},
			want:   http.StatusBadRequest,
			substr: "至少选择一项权限",
		},
		{
			name:   "invalid scope",
			form:   url.Values{"name": {"k"}, "scopes": {"superuser"}, "csrf_token": {csrf}},
			want:   http.StatusBadRequest,
			substr: "权限取值非法",
		},
		{
			name:   "invalid expiry",
			form:   url.Values{"name": {"k"}, "scopes": {store.ScopeRead}, "expires_at": {"not-a-date"}, "csrf_token": {csrf}},
			want:   http.StatusBadRequest,
			substr: "过期日期格式不正确",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := keyCount(t, db, memberID)
			rec := postForm(t, srv, "/settings/keys", tc.form, cookies)
			if rec.Code != tc.want {
				t.Fatalf("POST /settings/keys = %d, want %d (body %s)", rec.Code, tc.want, snippet(rec.Body.String()))
			}
			if !strings.Contains(rec.Body.String(), tc.substr) {
				t.Errorf("error is not localized (%q); body = %s", tc.substr, snippet(rec.Body.String()))
			}
			if after := keyCount(t, db, memberID); after != before {
				t.Errorf("key count changed on a rejected create: %d -> %d", before, after)
			}
		})
	}
}

// TestSettingsKeysCSRF 断言两个写路由都强制 CSRF：缺 token 一律 403。
func TestSettingsKeysCSRF(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	memberID, cookies, _ := loginMember(t, srv, db, "member3")

	// 先直接建一把 key，供撤销路由使用。
	created, err := store.NewAPIKeyStore(db).Create(context.Background(), store.CreateAPIKeyParams{
		UserID: memberID, Name: "csrf target", Scopes: []string{store.ScopeRead},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	cases := []struct {
		name string
		path string
		form url.Values
	}{
		{"create", "/settings/keys", url.Values{"name": {"x"}, "scopes": {store.ScopeRead}}},
		{"revoke", "/settings/keys/" + strconv.FormatUint(created.Key.ID, 10) + "/revoke", url.Values{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postForm(t, srv, tc.path, tc.form, cookies)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("POST %s without CSRF = %d, want 403", tc.path, rec.Code)
			}
		})
	}
	if keyCount(t, db, memberID) != 1 {
		t.Error("a CSRF-rejected create still wrote a key")
	}
}
