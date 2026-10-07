package main

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/config"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web"
)

// testSessionSecret 是集成测试用的会话签名密钥；仅用于测试，与生产无关。
const testSessionSecret = "integration-test-session-secret-0123456789"

// testEncryptionKey 是集成测试用的主密钥：base64(32 字节)，仅用于测试，与生产无关。
var testEncryptionKey = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

// testDoubleSubmitToken 是集成测试用的会话前 CSRF token；值只需满足长度下限。
const testDoubleSubmitToken = "test-double-submit-token-0123456789"

// newWiredServer 通过与 runServe 相同的装配路径构造服务，验证 M1-14 的接线的确把
// /login、/setup 等认证路由注册进了进程，而不是只在 web 包的单元测试里成立。
func newWiredServer(t *testing.T) (*web.Server, *store.UserStore) {
	t.Helper()
	t.Setenv("DB_DRIVER", "sqlite")
	t.Setenv("DB_DSN", filepath.Join(t.TempDir(), "wiring.db"))
	t.Setenv("SESSION_SECRET", testSessionSecret)
	t.Setenv("ENCRYPTION_KEY", testEncryptionKey)
	t.Setenv("BOOTSTRAP_ADMIN_EMAIL", "bootstrap@example.com")
	t.Setenv("BASE_URL", "http://localhost:8080")

	cfg, err := config.Load(os.LookupEnv, nil)
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	db, err := store.Open(cfg.Get(config.KeyDBDriver).Value, cfg.Get(config.KeyDBDSN).Value)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("store.AutoMigrate() error = %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := newWebServer(cfg, db, logger)
	if err != nil {
		t.Fatalf("newWebServer() error = %v", err)
	}
	return srv, store.NewUserStore(db)
}

// doGet 发起 GET 并返回 recorder。
func doGet(t *testing.T, srv *web.Server, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// doPostForm 以 x-www-form-urlencoded 发起 POST。
//
// 登录前表单（/login、/register、/setup）需要双提交 cookie（B-13）：这里自动补上 cookie 与
// 镜像 token，让本测试继续验证接线本身。拒绝路径（缺镜像 cookie）由
// internal/web 的 csrf_presession_test.go 用原始请求单独覆盖。
func doPostForm(t *testing.T, srv *web.Server, target string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	preSession := false
	switch strings.SplitN(target, "?", 2)[0] {
	case "/login", "/register", "/setup":
		preSession = true
	}
	if preSession && values.Get(auth.CSRFFieldName) == "" {
		values.Set(auth.CSRFFieldName, testDoubleSubmitToken)
	}
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if preSession {
		req.AddCookie(&http.Cookie{Name: auth.CSRFDoubleSubmitCookieName, Value: testDoubleSubmitToken})
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// doPostJSON 以 application/json 发起 POST，并按会话前流程补上双提交 cookie 与镜像头
// （/api/v1/auth/login、/setup 等由 DoubleSubmitMiddleware 保护）。
func doPostJSON(t *testing.T, srv *web.Server, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(auth.CSRFHeaderName, testDoubleSubmitToken)
	req.AddCookie(&http.Cookie{Name: auth.CSRFDoubleSubmitCookieName, Value: testDoubleSubmitToken})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// hasCookie 报告响应是否下发了指定名字的 cookie。
func hasCookie(rec *httptest.ResponseRecorder, name string) bool {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name && c.Value != "" {
			return true
		}
	}
	return false
}

// TestWiredServerExposesAuthRoutes 是 M1-14 的验收测试：真实装配路径下 /login、/setup、
// /healthz 都可达，/setup 在首个管理员出现后按约定变为 404，密码错误按约定返回 401。
func TestWiredServerExposesAuthRoutes(t *testing.T) {
	srv, users := newWiredServer(t)

	// /healthz 仍正常：接线不得破坏 M0 的健康检查。
	if rec := doGet(t, srv, "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// 尚无管理员：引导页可达。GET /setup 已切到 SPA 应用壳（DESIGN.md §8.1 的规范路径），
	// 因此这里断言「可达 + 是应用壳 + 下发了会话前双提交 cookie」；SSR 引导页本身的渲染细节
	// （本地化文案、BOOTSTRAP_ADMIN_EMAIL 预填）由 internal/web 的回退分支用例覆盖。
	setup := doGet(t, srv, "/setup")
	if setup.Code != http.StatusOK {
		t.Fatalf("GET /setup with no admin status = %d, want 200 (body %s)", setup.Code, setup.Body.String())
	}
	if !strings.Contains(setup.Body.String(), `id="app"`) {
		t.Errorf("GET /setup did not serve the SPA shell: %s", snippet(setup.Body.String()))
	}
	if !hasCookie(setup, auth.CSRFDoubleSubmitCookieName) {
		t.Errorf("GET /setup did not issue the %s cookie", auth.CSRFDoubleSubmitCookieName)
	}

	// /login 同样切到 SPA 应用壳。
	login := doGet(t, srv, "/login")
	if login.Code != http.StatusOK {
		t.Fatalf("GET /login status = %d, want 200 (body %s)", login.Code, login.Body.String())
	}
	if !strings.Contains(login.Body.String(), `id="app"`) {
		t.Errorf("GET /login did not serve the SPA shell: %s", snippet(login.Body.String()))
	}
	if !hasCookie(login, auth.CSRFDoubleSubmitCookieName) {
		t.Errorf("GET /login did not issue the %s cookie", auth.CSRFDoubleSubmitCookieName)
	}

	// 密码错误：JSON 登录返回 401，且不下发任何 cookie（SSR 登录表单已随页面层删除）。
	bad := doPostJSON(t, srv, "/api/v1/auth/login", `{"username":"nobody","password":"WrongPassword!"}`)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("POST /api/v1/auth/login with a wrong password status = %d, want 401 (body %s)", bad.Code, snippet(bad.Body.String()))
	}
	if len(bad.Result().Cookies()) != 0 {
		t.Errorf("failed login unexpectedly set a cookie: %v", bad.Result().Cookies())
	}

	// 创建首个管理员，随后引导页必须关闭（404）。
	created := doPostJSON(t, srv, "/api/v1/auth/setup",
		`{"username":"root","email":"root@example.com","password":"Sup3rSecret!"}`)
	if created.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/setup status = %d, want 200 (body %s)", created.Code, snippet(created.Body.String()))
	}
	if n, err := users.CountActiveAdmins(context.Background()); err != nil || n != 1 {
		t.Fatalf("CountActiveAdmins() = %d, %v; want 1, nil", n, err)
	}
	if rec := doGet(t, srv, "/setup"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /setup after an admin exists status = %d, want 404", rec.Code)
	}
}

// snippet 截断响应体，避免失败输出过长。
func snippet(body string) string {
	const max = 300
	if len(body) <= max {
		return body
	}
	return body[:max] + "..."
}

// TestWiredServerStartsWithMailOutbox 验证 M1-17 的接线：真实装配路径下 Server 暴露了
// 邮件 outbox，且默认未配置 SMTP（Configured()=false），worker 可启动并优雅停止。
func TestWiredServerStartsWithMailOutbox(t *testing.T) {
	srv, _ := newWiredServer(t)
	ob := srv.Mail()
	if ob == nil {
		t.Fatal("wired server has no mail outbox; the SMTP worker would never run")
	}
	if ob.Configured() {
		t.Error("mail outbox reports configured with no SMTP settings, want false")
	}
	// 启动/停止必须幂等且不挂死。
	ctx, cancel := context.WithCancel(context.Background())
	ob.Start(ctx)
	ob.Stop()
	cancel()
}
