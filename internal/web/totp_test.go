package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 M1-16 三条验收的端到端证据（httptest 走真实路由 + 真实 SQLite）。
//
// 页面已迁移到 SPA：登录两步与设置读写全部走同源 JSON 端点
// （POST /api/v1/auth/login、POST /api/v1/auth/totp、/api/v1/settings/totp*）。
//  ① 启用了 TOTP 的用户仅凭密码无法完成登录；
//  ② 恢复码一次性；
//  ③ 关闭 TOTP 需要密码。

// createTOTPAdmin 走 JSON 引导端点建首个管理员并返回其用户行。
func createTOTPAdmin(t *testing.T, srv *Server, db *gorm.DB) *store.User {
	t.Helper()
	rec := setupFirstAdmin(t, srv, "admin", "admin@example.com", "Sup3rSecret!")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/setup = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var u store.User
	if err := db.First(&u, "username = ?", "admin").Error; err != nil {
		t.Fatalf("load admin: %v", err)
	}
	return &u
}

// enableTOTPFor 直接经服务层绑定并启用（JSON 接口流程另由 spa_totp_test.go 覆盖），
// 返回明文 secret 与恢复码。
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

// startSecondStep 用正确密码经 JSON 登录，返回「等待第二因素」的 cookie；
// 同时断言此时还没有会话。第一因素的 pending cookie 是服务端在响应里下发的 HttpOnly 值。
func startSecondStep(t *testing.T, srv *Server) *http.Cookie {
	t.Helper()
	rec, _, pendingCookie := totpJSONLogin(t, srv, "admin", "Sup3rSecret!")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/login (password ok, TOTP enabled) = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "requires_totp") {
		t.Fatalf("login for a TOTP account must ask for the second factor: %s", snippet(rec.Body.String()))
	}
	if findCookie(rec, srv.sessions.CookieName()) != nil {
		t.Fatal("a session cookie was issued before the second factor")
	}
	if pendingCookie == nil {
		t.Fatal("the challenge response set no pending-second-factor cookie")
	}
	return pendingCookie
}

// TestAcceptance1_PasswordAloneCannotFinishLogin 是验收①。
func TestAcceptance1_PasswordAloneCannotFinishLogin(t *testing.T) {
	srv, db := newAuthServer(t)
	u := createTOTPAdmin(t, srv, db)
	secret, _ := enableTOTPFor(t, srv, u)

	// 密码错误：必须仍是通用凭据错误，绝不透露「该账号启用了 TOTP」。
	bad, _, badPending := totpJSONLogin(t, srv, "admin", "WrongPassword!")
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401", bad.Code)
	}
	if badPending != nil {
		t.Error("a failed first factor issued a second-factor cookie")
	}
	if body := bad.Body.String(); strings.Contains(body, "totp") || strings.Contains(body, "otpauth") {
		t.Errorf("a failed first factor leaked TOTP state; body = %s", snippet(body))
	}

	// 密码正确：进入第二步，没有会话。
	pending := startSecondStep(t, srv)

	// 空验证码：仍拿不到会话。
	empty := postTOTPJSON(t, srv, "", nil, pending, testDoubleSubmitToken)
	if empty.Code != http.StatusUnauthorized {
		t.Fatalf("empty second factor = %d, want 401 (body %s)", empty.Code, snippet(empty.Body.String()))
	}
	if findCookie(empty, srv.sessions.CookieName()) != nil {
		t.Fatal("empty second factor issued a session cookie")
	}

	// 正确验证码：完成登录，拿到会话。
	// 用下一个时间步的码：绑定确认已用掉当前步，同一时间步不可二次通过。
	code, err := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("GenerateCode() error = %v", err)
	}
	ok := postTOTPJSON(t, srv, code, nil, pending, testDoubleSubmitToken)
	if ok.Code != http.StatusOK {
		t.Fatalf("valid second factor = %d, want 200 (body %s)", ok.Code, snippet(ok.Body.String()))
	}
	if !hasSessionCookie(ok, srv) {
		t.Fatal("a valid second factor did not issue a session cookie")
	}
}

// TestAcceptance2_RecoveryCodeIsSingleUse 是验收②（经 JSON 路径）。
func TestAcceptance2_RecoveryCodeIsSingleUse(t *testing.T) {
	srv, db := newAuthServer(t)
	u := createTOTPAdmin(t, srv, db)
	_, codes := enableTOTPFor(t, srv, u)

	// 第一次用恢复码：登录成功。
	first := postTOTPJSON(t, srv, codes[0], nil, startSecondStep(t, srv), testDoubleSubmitToken)
	if first.Code != http.StatusOK || !hasSessionCookie(first, srv) {
		t.Fatalf("first recovery-code login = %d (session=%v), want 200 with a session", first.Code, hasSessionCookie(first, srv))
	}

	// 第二次用同一个恢复码：必须被拒，且没有会话。
	second := postTOTPJSON(t, srv, codes[0], nil, startSecondStep(t, srv), testDoubleSubmitToken)
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("reused recovery code = %d, want 401 (body %s)", second.Code, snippet(second.Body.String()))
	}
	if ck := findCookie(second, srv.sessions.CookieName()); ck != nil && ck.MaxAge >= 0 {
		t.Fatalf("reused recovery code issued a session cookie: %v", ck)
	}
}

// TestAcceptance3_DisableRequiresPassword 是验收③。
func TestAcceptance3_DisableRequiresPassword(t *testing.T) {
	srv, db := newAuthServer(t)
	u := createTOTPAdmin(t, srv, db)
	_, codes := enableTOTPFor(t, srv, u)

	// 先按正常流程完成一次登录，拿到会话 cookie 与会话绑定的 CSRF token。
	loginOK := postTOTPJSON(t, srv, codes[0], nil, startSecondStep(t, srv), testDoubleSubmitToken)
	if loginOK.Code != http.StatusOK {
		t.Fatalf("login = %d, want 200 (body %s)", loginOK.Code, snippet(loginOK.Body.String()))
	}
	cookies := loginOK.Result().Cookies()
	var sess store.Session
	if err := db.Order("created_at desc").First(&sess).Error; err != nil {
		t.Fatalf("load session row: %v", err)
	}

	// 密码错误：关闭被拒，仍然启用。
	wrong := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/disable", map[string]any{"password": "WrongPassword!"}, cookies, sess.CSRFToken)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("disable with a wrong password = %d, want 401 (body %s)", wrong.Code, snippet(wrong.Body.String()))
	}
	if enabled, _ := srv.totp.Enabled(context.Background(), u.ID); !enabled {
		t.Fatal("TOTP was disabled despite a wrong password")
	}

	// 密码正确：关闭成功。
	right := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/disable", map[string]any{"password": "Sup3rSecret!"}, cookies, sess.CSRFToken)
	if right.Code != http.StatusOK {
		t.Fatalf("disable with the correct password = %d, want 200 (body %s)", right.Code, snippet(right.Body.String()))
	}
	if enabled, _ := srv.totp.Enabled(context.Background(), u.ID); enabled {
		t.Fatal("TOTP is still enabled after a confirmed disable")
	}
}

// hasSessionCookie 报告响应是否下发了有效的会话 cookie。
func hasSessionCookie(rec *httptest.ResponseRecorder, srv *Server) bool {
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == srv.sessions.CookieName() && ck.Value != "" && ck.MaxAge >= 0 {
			return true
		}
	}
	return false
}
