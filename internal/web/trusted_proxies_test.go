package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// postLoginFrom 以给定的 RemoteAddr 与额外请求头发起 POST /login，并补齐会话前双提交 CSRF。
// 限流与审计的 IP 都取自 gin 的 ClientIP，而 ClientIP 是否采信 X-Forwarded-For 取决于可信代理配置，
// 因此必须能同时控制 RemoteAddr 与 XFF（DESIGN.md §4.3、§11）。
func postLoginFrom(t *testing.T, srv *Server, username, password, remoteAddr string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	values := url.Values{"username": {username}, "password": {password}, auth.CSRFFieldName: {testDoubleSubmitToken}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.CSRFDoubleSubmitCookieName, Value: testDoubleSubmitToken})
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// latestLoginFailedIP 读回最近一条登录失败审计里的 ip 字段。
func latestLoginFailedIP(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var row store.AuditLog
	if err := db.Where("action = ?", store.ActionUserLoginFailed).Order("id DESC").First(&row).Error; err != nil {
		t.Fatalf("load login_failed audit row: %v", err)
	}
	if row.DetailJSON == nil {
		t.Fatal("login_failed audit row has no detail_json")
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(*row.DetailJSON), &detail); err != nil {
		t.Fatalf("unmarshal audit detail: %v", err)
	}
	ip, _ := detail["ip"].(string)
	return ip
}

// TestLoginAuditAndLimiterIgnoreForwardedForByDefault 是本修复的核心负向断言：
// 未配置 TRUSTED_PROXIES 时不得信任任何代理，伪造的 X-Forwarded-For 既不能改写审计 IP，
// 也不能把登录限流键从真实来源骗走。
//
// 断言方式：审计行的 ip 列（可观察的最终结果）加限流器的 Wait 返回值（账号维度用不同用户名隔离，
// 只剩 IP 维度），避免依赖限流器内部状态。
func TestLoginAuditAndLimiterIgnoreForwardedForByDefault(t *testing.T) {
	srv, db := newAuthServer(t)
	ctx := context.Background()

	const remoteAddr = "192.0.2.1:12345"
	const forged = "1.2.3.4"

	rec := postLoginFrom(t, srv, "attacker", "WrongPassword!", remoteAddr,
		map[string]string{"X-Forwarded-For": forged})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /login status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}

	if got := latestLoginFailedIP(t, db); got != "192.0.2.1" {
		t.Errorf("audit ip = %q, want %q (RemoteAddr) — forged X-Forwarded-For must be ignored", got, "192.0.2.1")
	}

	// 用不存在的用户名查 Wait，隔离出 IP 维度：key 必须落在真实来源 IP 上。
	if d, err := srv.loginLimiter.Wait(ctx, "someone-else", "192.0.2.1"); err != nil || d <= 0 {
		t.Errorf("limiter keyed by RemoteAddr = (%v, %v), want delay > 0", d, err)
	}
	if d, err := srv.loginLimiter.Wait(ctx, "someone-else", forged); err != nil || d != 0 {
		t.Errorf("limiter keyed by forged XFF = (%v, %v), want (0, nil)", d, err)
	}
}

// TestLoginAdoptsForwardedForFromTrustedProxy 断言唯一被采信的路径：来源落在可信代理列表内时，
// X-Forwarded-For 生效，审计 IP 与限流键都改用它。
func TestLoginAdoptsForwardedForFromTrustedProxy(t *testing.T) {
	srv, db := newAuthServerWithProxies(t, []string{"127.0.0.1/32"})
	ctx := context.Background()

	const remoteAddr = "127.0.0.1:54321"
	const forwarded = "198.51.100.7"

	rec := postLoginFrom(t, srv, "attacker", "WrongPassword!", remoteAddr,
		map[string]string{"X-Forwarded-For": forwarded})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /login status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}

	if got := latestLoginFailedIP(t, db); got != forwarded {
		t.Errorf("audit ip = %q, want %q (X-Forwarded-For from a trusted proxy)", got, forwarded)
	}
	if d, err := srv.loginLimiter.Wait(ctx, "someone-else", forwarded); err != nil || d <= 0 {
		t.Errorf("limiter keyed by forwarded IP = (%v, %v), want delay > 0", d, err)
	}
	if d, err := srv.loginLimiter.Wait(ctx, "someone-else", "127.0.0.1"); err != nil || d != 0 {
		t.Errorf("limiter keyed by proxy addr = (%v, %v), want (0, nil)", d, err)
	}
}

// TestForwardedForIgnoredWhenRemoteIsNotATrustedProxy 断言配置了可信代理也不放行任意来源：
// 只有列表内的地址发来的 XFF 才被采信。
func TestForwardedForIgnoredWhenRemoteIsNotATrustedProxy(t *testing.T) {
	srv, db := newAuthServerWithProxies(t, []string{"127.0.0.1/32"})

	rec := postLoginFrom(t, srv, "attacker", "WrongPassword!", "192.0.2.1:12345",
		map[string]string{"X-Forwarded-For": "1.2.3.4"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /login status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
	if got := latestLoginFailedIP(t, db); got != "192.0.2.1" {
		t.Errorf("audit ip = %q, want %q — an untrusted remote must not be able to spoof it", got, "192.0.2.1")
	}
}

// TestNewRejectsInvalidTrustedProxy 断言构造期对非法项失败并点名，不静默忽略。
func TestNewRejectsInvalidTrustedProxy(t *testing.T) {
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "proxy.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	srv, err := New("127.0.0.1:0", Deps{
		DB:             db,
		Logger:         discardLogger(),
		SchemaVersion:  func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		TrustedProxies: []string{"203.0.113.0/33"},
	})
	if err == nil {
		t.Fatalf("New() = %v, want a refusal naming the invalid proxy entry", srv)
	}
	if !strings.Contains(err.Error(), "203.0.113.0/33") {
		t.Errorf("New() error = %q, want it to name %q", err.Error(), "203.0.113.0/33")
	}
}
