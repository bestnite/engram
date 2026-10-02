package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"gorm.io/gorm"

	"example.com/flashcard/internal/store"
)

// 本文件是 M1-16 三条验收的端到端证据（httptest 走真实路由 + 真实 SQLite）：
//  ① 启用了 TOTP 的用户仅凭密码无法完成登录；
//  ② 恢复码一次性；
//  ③ 关闭 TOTP 需要密码。

// createTOTPAdmin 走真实 /setup 建首个管理员并返回其用户行。
func createTOTPAdmin(t *testing.T, srv *Server, db *gorm.DB) *store.User {
	t.Helper()
	rec := postForm(t, srv, "/setup", url.Values{
		"username": {"admin"},
		"email":    {"admin@example.com"},
		"password": {"Sup3rSecret!"},
	}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /setup = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var u store.User
	if err := db.First(&u, "username = ?", "admin").Error; err != nil {
		t.Fatalf("load admin: %v", err)
	}
	return &u
}

// enableTOTPFor 直接经服务层绑定并启用（页面流程另由设置页路由覆盖），返回明文 secret 与恢复码。
func enableTOTPFor(t *testing.T, srv *Server, u *store.User) (string, []string) {
	t.Helper()
	ctx := context.Background()
	secret, otpauth, err := srv.totp.Begin(ctx, u.ID, u.Username)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if !strings.Contains(otpauth, "otpauth://totp/") {
		t.Fatalf("Begin() otpauth = %q", otpauth)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error = %v", err)
	}
	codes, err := srv.totp.Confirm(ctx, u.ID, code)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	return secret, codes
}

// startSecondStep 用正确密码登录并返回「等待第二因素」的 cookie；同时断言此时还没有会话。
func startSecondStep(t *testing.T, srv *Server) []*http.Cookie {
	t.Helper()
	rec := postForm(t, srv, "/login", url.Values{
		"username": {"admin"},
		"password": {"Sup3rSecret!"},
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /login (password ok, TOTP enabled) = %d, want 200 challenge (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var pending []*http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == srv.sessions.CookieName() {
			t.Fatalf("a session cookie was issued before the second factor: %v", ck)
		}
		if ck.Name == totpPendingCookieName {
			pending = append(pending, ck)
		}
	}
	if len(pending) == 0 {
		t.Fatal("the challenge response set no pending-second-factor cookie")
	}
	return pending
}

// TestAcceptance1_PasswordAloneCannotFinishLogin 是验收①。
func TestAcceptance1_PasswordAloneCannotFinishLogin(t *testing.T) {
	srv, db := newAuthServer(t)
	u := createTOTPAdmin(t, srv, db)
	secret, _ := enableTOTPFor(t, srv, u)

	// 密码错误：必须仍是通用凭据错误，绝不透露「该账号启用了 TOTP」。
	bad := postForm(t, srv, "/login", url.Values{
		"username": {"admin"},
		"password": {"WrongPassword!"},
	}, nil)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401", bad.Code)
	}
	if strings.Contains(bad.Body.String(), "两步验证") || strings.Contains(bad.Body.String(), "otpauth") {
		t.Errorf("a failed first factor leaked TOTP state; body = %s", snippet(bad.Body.String()))
	}
	if len(bad.Result().Cookies()) != 0 {
		t.Errorf("failed login set cookies: %v", bad.Result().Cookies())
	}

	// 密码正确：进入第二步，没有会话。
	pending := startSecondStep(t, srv)
	challenge := postForm(t, srv, "/login", url.Values{
		"username": {"admin"},
		"password": {"Sup3rSecret!"},
	}, nil)
	if !strings.Contains(challenge.Body.String(), "两步验证") {
		t.Errorf("challenge page is not localized; body = %s", snippet(challenge.Body.String()))
	}

	// 第二步密码缺失/空码：仍拿不到会话。
	empty := postForm(t, srv, "/login/totp", url.Values{"code": {""}}, pending)
	if empty.Code == http.StatusSeeOther {
		t.Fatal("empty second factor completed the login")
	}
	for _, ck := range empty.Result().Cookies() {
		if ck.Name == srv.sessions.CookieName() && ck.MaxAge >= 0 {
			t.Fatalf("empty second factor issued a session cookie: %v", ck)
		}
	}

	// 正确验证码：完成登录，拿到会话。
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error = %v", err)
	}
	ok := postForm(t, srv, "/login/totp", url.Values{"code": {code}}, pending)
	if ok.Code != http.StatusSeeOther {
		t.Fatalf("valid second factor = %d, want 303 (body %s)", ok.Code, snippet(ok.Body.String()))
	}
	if !hasSessionCookie(ok, srv) {
		t.Fatal("a valid second factor did not issue a session cookie")
	}
}

// TestAcceptance2_RecoveryCodeIsSingleUse 是验收②（经 HTTP 路径）。
func TestAcceptance2_RecoveryCodeIsSingleUse(t *testing.T) {
	srv, db := newAuthServer(t)
	u := createTOTPAdmin(t, srv, db)
	_, codes := enableTOTPFor(t, srv, u)

	// 第一次用恢复码：登录成功。
	first := postForm(t, srv, "/login/totp", url.Values{"code": {codes[0]}}, startSecondStep(t, srv))
	if first.Code != http.StatusSeeOther || !hasSessionCookie(first, srv) {
		t.Fatalf("first recovery-code login = %d (session=%v), want 303 with a session", first.Code, hasSessionCookie(first, srv))
	}

	// 第二次用同一个恢复码：必须被拒，且没有会话。
	second := postForm(t, srv, "/login/totp", url.Values{"code": {codes[0]}}, startSecondStep(t, srv))
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("reused recovery code = %d, want 401 (body %s)", second.Code, snippet(second.Body.String()))
	}
	for _, ck := range second.Result().Cookies() {
		if ck.Name == srv.sessions.CookieName() && ck.MaxAge >= 0 {
			t.Fatalf("reused recovery code issued a session cookie: %v", ck)
		}
	}
}

// TestAcceptance3_DisableRequiresPassword 是验收③。
func TestAcceptance3_DisableRequiresPassword(t *testing.T) {
	srv, db := newAuthServer(t)
	u := createTOTPAdmin(t, srv, db)
	_, codes := enableTOTPFor(t, srv, u)

	// 先按正常流程完成一次登录，拿到会话 cookie 与会话绑定的 CSRF token。
	loginOK := postForm(t, srv, "/login/totp", url.Values{"code": {codes[0]}}, startSecondStep(t, srv))
	if loginOK.Code != http.StatusSeeOther {
		t.Fatalf("login = %d, want 303", loginOK.Code)
	}
	cookies := loginOK.Result().Cookies()
	var sess store.Session
	if err := db.Order("created_at desc").First(&sess).Error; err != nil {
		t.Fatalf("load session row: %v", err)
	}

	// 密码错误：关闭被拒，仍然启用。
	wrong := postForm(t, srv, "/settings/totp/disable", url.Values{
		"csrf_token": {sess.CSRFToken},
		"password":   {"WrongPassword!"},
	}, cookies)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("disable with a wrong password = %d, want 401", wrong.Code)
	}
	if enabled, _ := srv.totp.Enabled(context.Background(), u.ID); !enabled {
		t.Fatal("TOTP was disabled despite a wrong password")
	}

	// 密码正确：关闭成功。
	right := postForm(t, srv, "/settings/totp/disable", url.Values{
		"csrf_token": {sess.CSRFToken},
		"password":   {"Sup3rSecret!"},
	}, cookies)
	if right.Code != http.StatusOK {
		t.Fatalf("disable with the correct password = %d, want 200 (body %s)", right.Code, snippet(right.Body.String()))
	}
	if enabled, _ := srv.totp.Enabled(context.Background(), u.ID); enabled {
		t.Fatal("TOTP is still enabled after a confirmed disable")
	}
}

// secretOf 已由 enableTOTPFor 直接返回；此处不再需要读回接口。

// hasSessionCookie 报告响应是否下发了有效的会话 cookie。
func hasSessionCookie(rec *httptest.ResponseRecorder, srv *Server) bool {
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == srv.sessions.CookieName() && ck.Value != "" && ck.MaxAge >= 0 {
			return true
		}
	}
	return false
}
