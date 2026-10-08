package web

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// testEncryptionKey 是测试用 AES-GCM 主密钥（base64 的 32 字节），与生产无关。
func testEncryptionKey() string {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = 0x2b
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// testSessionSecret 是测试用会话签名密钥；长度足够且与生产无关。
var testSessionSecret = []byte("test-session-secret-0123456789abcdef")

// newAuthServer 构造一个装配了账号、会话与用户存储的测试服务，并返回底层 DB。
// 不信任任何代理（默认），请求 IP 一律取 RemoteAddr。
func newAuthServer(t *testing.T) (*Server, *gorm.DB) {
	t.Helper()
	return newAuthServerWithProxies(t, nil)
}

// newAuthServerWithProxies 与 newAuthServer 相同，但显式注入可信代理列表，
// 用于验证 X-Forwarded-For 只在来自可信代理时才被采信。
func newAuthServerWithProxies(t *testing.T, trustedProxies []string) (*Server, *gorm.DB) {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	users := store.NewUserStore(db)
	sessions := store.NewSessionStore(db)
	invites := store.NewInviteStore(db)
	accounts, err := auth.NewAccountService(users, sessions, store.NewAPIKeyStore(db), auth.NewPasswordHasher(auth.Params{
		// 测试用弱参数，避免每次哈希耗时过长。
		Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32,
	}))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	mgr, err := auth.NewSessionManager(users, sessions, auth.SessionConfig{Secret: testSessionSecret})
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}
	auditor, err := auth.NewAuditor(store.NewAuditStore(db))
	if err != nil {
		t.Fatalf("NewAuditor() error = %v", err)
	}
	// 限流器注入空等待：测试断言的是延迟数值（见 internal/auth 的单测），这里不需要真的睡。
	limiter := auth.NewLoginLimiter(auth.LimiterConfig{
		BaseDelay: 5 * time.Millisecond,
		MaxDelay:  50 * time.Millisecond,
		Sleep:     func(context.Context, time.Duration) error { return nil },
	})
	// TOTP 二次验证：真实 SecretCodec + TOTPStore，登录第二步与设置接口因此可用。
	codec, err := store.NewSecretCodec(testEncryptionKey())
	if err != nil {
		t.Fatalf("NewSecretCodec() error = %v", err)
	}
	totpSvc, err := auth.NewTOTPService(store.NewTOTPStore(db), codec, auth.DefaultTOTPIssuer)
	if err != nil {
		t.Fatalf("NewTOTPService() error = %v", err)
	}
	srv, err := New("127.0.0.1:0", Deps{
		DB:            db,
		Logger:        discardLogger(),
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		Accounts:      accounts,
		Sessions:      mgr,
		Users:         users,
		Invites:       invites,
		Auditor:       auditor,
		LoginLimiter:  limiter,
		TOTP:          totpSvc,

		TrustedProxies: trustedProxies,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return srv, db
}

// testDoubleSubmitToken 是测试里使用的固定会话前 CSRF token；值只需满足长度下限。
const testDoubleSubmitToken = "test-double-submit-token-0123456789"

// postForm 以 application/x-www-form-urlencoded 提交表单。
//
// 移除 SSR 会话前表单后，页面写请求全部改走同源 JSON 端点（postJSON + 双提交头），
// 这个助手只服务仍存在的表单写端点（例如 /settings/password、/admin/*、/unsubscribe）。
func postForm(t *testing.T, srv *Server, target string, values url.Values, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// getWithCookies 带 cookie 发起 GET。
func getWithCookies(t *testing.T, srv *Server, target string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// loginJSON 走 SPA 的同源 JSON 登录（POST /api/v1/auth/login），返回会话 cookie 与会话绑定的 CSRF token。
// 会话前双提交 cookie/镜像 token 由 GET /login 应用壳下发。登录失败直接 Fatal。
func loginJSON(t *testing.T, srv *Server, db *gorm.DB, username, password string) ([]*http.Cookie, string) {
	t.Helper()
	cookie, headers := preSessionPair(t, srv, "/login")
	rec := postJSON(srv, "/api/v1/auth/login", map[string]string{
		"username": username, "password": password,
	}, []*http.Cookie{cookie}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/login (%s) = %d, want 200 (body %s)", username, rec.Code, snippet(rec.Body.String()))
	}
	var sess store.Session
	if err := db.Order("created_at desc").First(&sess).Error; err != nil {
		t.Fatalf("load session row: %v", err)
	}
	if sess.CSRFToken == "" {
		t.Fatal("session row has no CSRF token")
	}
	return rec.Result().Cookies(), sess.CSRFToken
}

// setupFirstAdmin 走 JSON 引导端点建出首个管理员；双提交 cookie 取自已打开的引导窗口。
// 调用方断言响应码。
func setupFirstAdmin(t *testing.T, srv *Server, username, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	cookie, headers := preSessionPair(t, srv, "/setup")
	body := map[string]string{"username": username, "password": password}
	if email != "" {
		body["email"] = email
	}
	return postJSON(srv, "/api/v1/auth/setup", body, []*http.Cookie{cookie}, headers)
}

// TestAuthFlowThroughJSONEndpoints 是验收：引导建管理员 → JSON 登录 → JSON 登出。
// 页面已迁移到 SPA，这条链路的唯一传输是 /api/v1/auth/{setup,login,session,logout}。
func TestAuthFlowThroughJSONEndpoints(t *testing.T) {
	srv, db := newAuthServer(t)

	// 尚无管理员：引导端点建出首个管理员，且不签发会话。
	setup := setupFirstAdmin(t, srv, "admin", "admin@example.com", "Sup3rSecret!")
	if setup.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/setup = %d, want 200 (body %s)", setup.Code, snippet(setup.Body.String()))
	}
	if findCookie(setup, srv.sessions.CookieName()) != nil {
		t.Error("setup must not issue a session cookie")
	}

	// 密码错误：401，不下发会话 cookie。
	cookie, headers := preSessionPair(t, srv, "/login")
	bad := postJSON(srv, "/api/v1/auth/login", map[string]string{
		"username": "admin", "password": "WrongPassword!",
	}, []*http.Cookie{cookie}, headers)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("login with a wrong password = %d, want 401", bad.Code)
	}
	if findCookie(bad, srv.sessions.CookieName()) != nil {
		t.Error("failed login issued a session cookie")
	}

	// 正确凭据：200 + 会话 cookie + 会话绑定的 CSRF token。
	cookies, csrf := loginJSON(t, srv, db, "admin", "Sup3rSecret!")

	// 会话可用：GET /api/v1/auth/session 报告已登录。
	me := getWithCookies(t, srv, "/api/v1/auth/session", cookies)
	if !strings.Contains(me.Body.String(), `"authenticated":true`) {
		t.Fatalf("session after login is not authenticated: %s", snippet(me.Body.String()))
	}

	// 登出必须带会话绑定的 CSRF：缺 token 403，带 token 200 并作废会话。
	noCSRF := postJSON(srv, "/api/v1/auth/logout", nil, cookies, nil)
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("logout without a CSRF token = %d, want 403", noCSRF.Code)
	}
	logout := postJSON(srv, "/api/v1/auth/logout", nil, cookies, map[string]string{auth.CSRFHeaderName: csrf})
	if logout.Code != http.StatusOK {
		t.Fatalf("logout = %d, want 200 (body %s)", logout.Code, snippet(logout.Body.String()))
	}
	after := getWithCookies(t, srv, "/api/v1/auth/session", cookies)
	if !strings.Contains(after.Body.String(), `"authenticated":false`) {
		t.Errorf("session is still active after logout: %s", snippet(after.Body.String()))
	}

	var refreshed store.Session
	if err := db.Order("created_at desc").First(&refreshed).Error; err != nil {
		t.Fatalf("reload session row: %v", err)
	}
	if refreshed.RevokedAt == nil {
		t.Error("session row was not revoked after logout")
	}
}

// TestHomeRedirectsToSetupUntilFirstAdmin 是验收：安装完但还没 setup 时，
// GET / 303 到 /setup（引导窗口）；首个管理员建立后首页返回应用壳。
func TestHomeRedirectsToSetupUntilFirstAdmin(t *testing.T) {
	srv, _ := newAuthServer(t)

	rec := get(t, srv, "/", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET / before any admin = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/setup" {
		t.Errorf("Location = %q, want /setup", loc)
	}

	if setup := setupFirstAdmin(t, srv, "root", "root@example.com", "Sup3rSecret!"); setup.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/setup = %d, want 200 (body %s)", setup.Code, snippet(setup.Body.String()))
	}

	after := get(t, srv, "/", nil)
	if after.Code != http.StatusOK {
		t.Fatalf("GET / after setup = %d, want 200 (body %s)", after.Code, snippet(after.Body.String()))
	}
	if !strings.Contains(after.Body.String(), `id="app"`) {
		t.Errorf("home after setup did not serve the SPA shell: %s", snippet(after.Body.String()))
	}
}

// TestSetupUnavailableAfterAdminExists 是验收点：一次性管理员门。
// 有管理员之前 GET /setup 可达，之后 GET /setup 与 POST /api/v1/auth/setup 都必须 404。
func TestSetupUnavailableAfterAdminExists(t *testing.T) {
	srv, _ := newAuthServer(t)

	if code := get(t, srv, "/setup", nil).Code; code != http.StatusOK {
		t.Fatalf("GET /setup before any admin = %d, want 200", code)
	}
	if setup := setupFirstAdmin(t, srv, "root", "root@example.com", "Sup3rSecret!"); setup.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/setup = %d, want 200 (body %s)", setup.Code, snippet(setup.Body.String()))
	}

	if code := get(t, srv, "/setup", nil).Code; code != http.StatusNotFound {
		t.Errorf("GET /setup after an admin exists = %d, want 404", code)
	}
	// 双提交 cookie 从仍然可达的 /login 取：已关闭的引导窗口不再下发它。
	cookie, headers := preSessionPair(t, srv, "/login")
	closed := postJSON(srv, "/api/v1/auth/setup", map[string]string{
		"username": "second", "email": "second@example.com", "password": "Sup3rSecret!",
	}, []*http.Cookie{cookie}, headers)
	if closed.Code != http.StatusNotFound {
		t.Errorf("POST /api/v1/auth/setup after an admin exists = %d, want 404", closed.Code)
	}
}

// 保证 gin 在测试里处于安静模式。
func init() { gin.SetMode(gin.TestMode) }
