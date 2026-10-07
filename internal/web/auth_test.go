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
	// TOTP 二次验证（M1-16）：真实 SecretCodec + TOTPStore，登录第二步与设置页因此可用。
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

// preSessionCSRFRoutes 是需要双提交 cookie 的登录前表单路由（B-13）。
// 这些路由的 POST 由 auth.DoubleSubmitMiddleware 校验 cookie 与镜像 token 的一致性。
var preSessionCSRFRoutes = map[string]bool{
	"/login": true, "/register": true, "/setup": true, "/login/totp": true,
	"/forgot-password": true, "/reset-password": true,
}

// testDoubleSubmitToken 是测试里使用的固定会话前 CSRF token；值只需满足长度下限。
const testDoubleSubmitToken = "test-double-submit-token-0123456789"

// postForm 以 application/x-www-form-urlencoded 提交表单。
//
// 登录前表单（/login、/register、/setup）需要双提交 cookie：这里自动补上 cookie 与镜像
// token，让既有测试继续专注各自的断言（策略、邀请、审计……）。拒绝路径（缺 cookie、
// 值不匹配）由 csrf_presession_test.go 用不带该 cookie 的原始请求单独覆盖。
func postForm(t *testing.T, srv *Server, target string, values url.Values, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	if preSessionCSRFRoutes[strings.SplitN(target, "?", 2)[0]] {
		cp := url.Values{}
		for k, v := range values {
			cp[k] = v
		}
		if cp.Get(auth.CSRFFieldName) == "" {
			cp.Set(auth.CSRFFieldName, testDoubleSubmitToken)
			cookies = append(cookies, &http.Cookie{Name: auth.CSRFDoubleSubmitCookieName, Value: testDoubleSubmitToken})
		}
		values = cp
	}
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

// TestRegisterLoginLogoutFlow 是 M1-4 的验收测试：一条 httptest 请求链走通注册→登录→登出。
func TestRegisterLoginLogoutFlow(t *testing.T) {
	srv, db := newAuthServer(t)
	// 断言首页页头的登录/登出入口随会话变化，显式走 SPA 缺失的回退分支。
	srv.spa = nil

	// 尚无管理员：引导页可达，表单标题来自语言包（默认 zh-CN）。
	setup := get(t, srv, "/setup", nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("GET /setup before any admin = %d, want 200", setup.Code)
	}
	if !strings.Contains(setup.Body.String(), "创建首个管理员") {
		t.Errorf("setup page is not localized (zh-CN); body = %s", snippet(setup.Body.String()))
	}

	// 注册首个账号：closed 策略下唯一的放行路径是首个管理员引导。
	rec := postForm(t, srv, "/register", url.Values{
		"username": {"admin"},
		"email":    {"admin@example.com"},
		"password": {"Sup3rSecret!"},
	}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /register status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("POST /register Location = %q, want /login", loc)
	}

	// 已有管理员后再自助注册必须被拒绝，证明不再是"任何人都能注册"。
	closed := postForm(t, srv, "/register", url.Values{
		"username": {"intruder"},
		"email":    {"intruder@example.com"},
		"password": {"Sup3rSecret!"},
	}, nil)
	if closed.Code != http.StatusForbidden {
		t.Fatalf("second POST /register status = %d, want 403", closed.Code)
	}
	if !strings.Contains(closed.Body.String(), "自助注册已关闭") {
		t.Errorf("closed-registration page is missing the localized notice; body = %s", snippet(closed.Body.String()))
	}

	// 登录页英文文案来自语言包，证明 ?lang= 切换与两套文案都在。
	loginEN := get(t, srv, "/login?lang=en", nil)
	if loginEN.Code != http.StatusOK {
		t.Fatalf("GET /login?lang=en status = %d, want 200", loginEN.Code)
	}
	if !strings.Contains(loginEN.Body.String(), "Sign in") {
		t.Errorf("en login page is not English; body = %s", snippet(loginEN.Body.String()))
	}

	// 密码错误：应回填本地化错误且不下发会话。
	bad := postForm(t, srv, "/login", url.Values{
		"username": {"admin"},
		"password": {"WrongPassword!"},
	}, nil)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("login with a wrong password status = %d, want 401", bad.Code)
	}
	if len(bad.Result().Cookies()) != 0 {
		t.Errorf("failed login unexpectedly set a cookie: %v", bad.Result().Cookies())
	}

	// 登录成功：拿到会话 cookie。
	ok := postForm(t, srv, "/login", url.Values{
		"username": {"admin"},
		"password": {"Sup3rSecret!"},
	}, nil)
	if ok.Code != http.StatusSeeOther {
		t.Fatalf("POST /login status = %d, want 303 (body %s)", ok.Code, snippet(ok.Body.String()))
	}
	if loc := ok.Header().Get("Location"); loc != "/" {
		t.Errorf("POST /login Location = %q, want /", loc)
	}
	cookies := ok.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("successful login did not set a session cookie")
	}

	// 带会话访问首页：页头应显示登出入口，证明会话已生效。
	home := getWithCookies(t, srv, "/", cookies)
	if home.Code != http.StatusOK {
		t.Fatalf("GET / with session status = %d, want 200", home.Code)
	}
	if !strings.Contains(home.Body.String(), "登出") {
		t.Errorf("authenticated home page does not show the logout entry; body = %s", snippet(home.Body.String()))
	}

	// 从数据库取会话绑定的 CSRF token（服务端状态，页面外的通道）。
	var sess store.Session
	if err := db.Order("created_at desc").First(&sess).Error; err != nil {
		t.Fatalf("load session row: %v", err)
	}
	if sess.CSRFToken == "" {
		t.Fatal("session row has no CSRF token")
	}

	// 登出必须带 CSRF token；先验证缺失 token 被拒（M1-3 负例），再带 token 成功。
	noCSRF := postForm(t, srv, "/logout", url.Values{}, cookies)
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("POST /logout without a CSRF token status = %d, want 403", noCSRF.Code)
	}
	logout := postForm(t, srv, "/logout", url.Values{auth.CSRFFieldName: {sess.CSRFToken}}, cookies)
	if logout.Code != http.StatusSeeOther {
		t.Fatalf("POST /logout status = %d, want 303", logout.Code)
	}
	if loc := logout.Header().Get("Location"); loc != "/login" {
		t.Errorf("POST /logout Location = %q, want /login", loc)
	}

	// 登出后旧 cookie 必须失效：首页回到匿名状态（显示登录入口，不再显示登出）。
	after := getWithCookies(t, srv, "/", cookies)
	if after.Code != http.StatusOK {
		t.Fatalf("GET / after logout status = %d, want 200", after.Code)
	}
	if strings.Contains(after.Body.String(), "登出") {
		t.Errorf("session still appears active after logout; body = %s", snippet(after.Body.String()))
	}
	if !strings.Contains(after.Body.String(), "登录") {
		t.Errorf("anonymous home page does not show the login entry; body = %s", snippet(after.Body.String()))
	}

	var refreshed store.Session
	if err := db.Order("created_at desc").First(&refreshed).Error; err != nil {
		t.Fatalf("reload session row: %v", err)
	}
	if refreshed.RevokedAt == nil {
		t.Error("session row was not revoked after logout")
	}
}

// TestHomeRedirectsToSetupUntilFirstAdmin 是 M1-25 的验收测试：
// 安装完但还没 setup 时，GET / 303 到 /setup；首个管理员建立后首页交给 SPA 应用壳。
func TestHomeRedirectsToSetupUntilFirstAdmin(t *testing.T) {
	srv, _ := newAuthServer(t)

	rec := get(t, srv, "/", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET / before any admin = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/setup" {
		t.Errorf("Location = %q, want /setup", loc)
	}

	if rec := postForm(t, srv, "/setup", url.Values{
		"username": {"root"},
		"email":    {"root@example.com"},
		"password": {"Sup3rSecret!"},
	}, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /setup status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	after := get(t, srv, "/", nil)
	if after.Code != http.StatusOK {
		t.Fatalf("GET / after setup = %d, want 200", after.Code)
	}
	if body := after.Body.String(); !strings.Contains(body, `<div id="app">`) {
		t.Errorf("home after setup is not the SPA shell; body = %s", snippet(body))
	}
}

// TestSetupUnavailableAfterAdminExists 是 M1-5 的验收测试：有 admin 之前可达，之后 404。
func TestSetupUnavailableAfterAdminExists(t *testing.T) {
	srv, _ := newAuthServer(t)

	if code := get(t, srv, "/setup", nil).Code; code != http.StatusOK {
		t.Fatalf("GET /setup before any admin = %d, want 200", code)
	}

	rec := postForm(t, srv, "/setup", url.Values{
		"username": {"root"},
		"email":    {"root@example.com"},
		"password": {"Sup3rSecret!"},
	}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /setup status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	if code := get(t, srv, "/setup", nil).Code; code != http.StatusNotFound {
		t.Errorf("GET /setup after an admin exists = %d, want 404", code)
	}
	if code := postForm(t, srv, "/setup", url.Values{
		"username": {"second"},
		"email":    {"second@example.com"},
		"password": {"Sup3rSecret!"},
	}, nil).Code; code != http.StatusNotFound {
		t.Errorf("POST /setup after an admin exists = %d, want 404", code)
	}
}

// TestBootstrapAdminEmailPrefillsSetup 断言 BOOTSTRAP_ADMIN_EMAIL 作为引导页邮箱兜底。
func TestBootstrapAdminEmailPrefillsSetup(t *testing.T) {
	srv, _ := newAuthServer(t)
	// 断言 SSR 引导页的邮箱兜底渲染，显式走 SPA 缺失的回退分支。
	srv.spa = nil
	srv.bootstrapEmail = "bootstrap@example.com"
	body := get(t, srv, "/setup", nil).Body.String()
	if !strings.Contains(body, "bootstrap@example.com") {
		t.Errorf("setup page did not prefill the bootstrap admin email; body = %s", snippet(body))
	}
}

// TestRegisterRejectsWeakPassword 断言表单校验错误也走语言包（中文默认语言）。
func TestRegisterRejectsWeakPassword(t *testing.T) {
	srv, _ := newAuthServer(t)
	rec := postForm(t, srv, "/register", url.Values{
		"username": {"weak"},
		"email":    {"weak@example.com"},
		"password": {"password"},
	}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("weak password status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "密码过于常见") {
		t.Errorf("weak-password error is not localized; body = %s", snippet(rec.Body.String()))
	}
}

// 保证 gin 在测试里处于安静模式。
func init() { gin.SetMode(gin.TestMode) }
