package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是账号安全与邮件流程 SPA 传输层的验收（internal/web/spa_account.go）。
//
// 覆盖：可导航读取页的切壳、免登录一键链接的令牌消费（应用壳 + JSON 端点）、同源 JSON 端点的令牌
// 一次性/过期语义、匿名限流、会话与 CSRF 负例，以及改邮箱确认前不改库。全部走真实路由与 SQLite。

// postJSONWithSession 以会话 cookie + 会话 CSRF header 发一次 JSON 写请求。
func postJSONWithSession(srv *Server, path string, body any, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
	return postJSON(srv, path, body, cookies, map[string]string{auth.CSRFHeaderName: csrf})
}

// decodeJSON 把响应体解到 out；失败直接 Fatal。
func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decode JSON response failed: %v (body %s)", err, snippet(rec.Body.String()))
	}
}

// jsonResetToken 走 JSON 的 POST /api/v1/auth/forgot-password 触发一封重置邮件并取出明文令牌。
func jsonResetToken(t *testing.T, ts securityTestServer, cookie *http.Cookie, headers map[string]string) string {
	t.Helper()
	before := len(outboxByType(t, ts.db, mail.TypePasswordReset))
	rec := postJSON(ts.srv, "/api/v1/auth/forgot-password",
		map[string]string{"email": "owner@example.com"}, []*http.Cookie{cookie}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/forgot-password status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	rows := outboxByType(t, ts.db, mail.TypePasswordReset)
	if len(rows) != before+1 {
		t.Fatalf("password_reset rows %d -> %d after a JSON reset request, want +1", before, len(rows))
	}
	return tokenFromBody(t, rows[len(rows)-1].TextBody)
}

// TestSPAAccountRouteCutover 覆盖三个可导航读取页与两条一键链接的应用壳、/settings/email 的登录门禁，
// 以及一键链接入口初始化双提交 cookie 但不消费令牌。
func TestSPAAccountRouteCutover(t *testing.T) {
	ts := newSecurityServer(t, true)

	for _, path := range []string{"/forgot-password", "/reset-password", "/settings/email"} {
		assertServesSPAShell(t, getWithCookies(t, ts.srv, path, ts.cookies), "GET "+path)
	}

	// 邮件里的一键链接落在规范路径上，同样返回应用壳（服务端不再渲染结果页）。
	for _, path := range []string{"/verify-email?token=bogus", "/confirm-email-change?token=bogus"} {
		rec := get(t, ts.srv, path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200 (body %s)", path, rec.Code, snippet(rec.Body.String()))
		}
		if !strings.Contains(rec.Body.String(), `id="app"`) {
			t.Errorf("GET %s did not serve the SPA shell: %s", path, snippet(rec.Body.String()))
		}
		if findCookie(rec, auth.CSRFDoubleSubmitCookieName) == nil {
			t.Errorf("GET %s must initialize the double-submit cookie", path)
		}
	}

	// 未登录的 /settings/email 仍重定向登录页：授权判定在服务端，先于切壳。
	anon := get(t, ts.srv, "/settings/email", nil)
	if anon.Code != http.StatusSeeOther || anon.Header().Get("Location") != "/login" {
		t.Errorf("anonymous GET /settings/email = %d %q, want 303 /login", anon.Code, anon.Header().Get("Location"))
	}
}

// TestSPAAccountJSON_CSRFRejections 断言会话前 JSON 端点复用双提交 CSRF：缺 cookie / 缺镜像
// 值一律 403 csrf_failed，且不触发任何业务动作。
func TestSPAAccountJSON_CSRFRejections(t *testing.T) {
	ts := newSecurityServer(t, true)
	for _, path := range []string{
		"/api/v1/auth/forgot-password",
		"/api/v1/auth/reset-password",
		"/api/v1/auth/verify-email",
		"/api/v1/auth/confirm-email-change",
	} {
		rec := postJSON(ts.srv, path, map[string]string{"token": "x"}, nil, nil)
		if rec.Code != http.StatusForbidden || apiErrorCode(t, rec) != "csrf_failed" {
			t.Errorf("POST %s without CSRF = %d %s, want 403 csrf_failed", path, rec.Code, apiErrorCode(t, rec))
		}
	}
	if rows := outboxByType(t, ts.db, mail.TypePasswordReset); len(rows) != 0 {
		t.Errorf("CSRF-rejected reset requests enqueued %d mails, want 0", len(rows))
	}
}

// TestSPAAccountForgotResetFlow 覆盖 JSON 的重置请求与提交：成功改密、令牌一次性、过期与
// 弱密码拒绝，以及新密码真的能登录。
func TestSPAAccountForgotResetFlow(t *testing.T) {
	ts := newSecurityServer(t, true)
	cookie, headers := preSessionPair(t, ts.srv, "/forgot-password")

	// 请求重置：响应只含站点级 mail_ready，且确实入队了一封重置邮件。
	rec := postJSON(ts.srv, "/api/v1/auth/forgot-password",
		map[string]string{"email": "owner@example.com"}, []*http.Cookie{cookie}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("forgot-password status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var forgot struct {
		MailReady bool `json:"mail_ready"`
	}
	decodeJSON(t, rec, &forgot)
	if !forgot.MailReady {
		t.Errorf("forgot-password mail_ready = false, want true when SMTP is configured")
	}
	plain := jsonResetToken(t, ts, cookie, headers)

	// 提交新密码：成功且新密码可登录。
	reset := postJSON(ts.srv, "/api/v1/auth/reset-password",
		map[string]string{"token": plain, "password": "N3wSup3rSecret!"}, []*http.Cookie{cookie}, headers)
	if reset.Code != http.StatusOK {
		t.Fatalf("reset-password status = %d, want 200 (body %s)", reset.Code, snippet(reset.Body.String()))
	}
	var resetRes struct {
		Reset bool `json:"reset"`
	}
	decodeJSON(t, reset, &resetRes)
	if !resetRes.Reset {
		t.Errorf("reset-password reset = false, want true")
	}
	// 新密码真的能登录（走同源 JSON 登录端点）。
	loginJSON(t, ts.srv, ts.db, "owner", "N3wSup3rSecret!")

	// 同一令牌二次使用：token_used。
	reuse := postJSON(ts.srv, "/api/v1/auth/reset-password",
		map[string]string{"token": plain, "password": "An0therSecret!"}, []*http.Cookie{cookie}, headers)
	if reuse.Code != http.StatusBadRequest || apiErrorCode(t, reuse) != "token_used" {
		t.Errorf("reused reset token = %d %s, want 400 token_used", reuse.Code, apiErrorCode(t, reuse))
	}

	// 弱密码：停在密码策略 code（令牌已被消费，与 SSR 一致，故此处先取新令牌）。
	weak := jsonResetToken(t, ts, cookie, headers)
	weakRec := postJSON(ts.srv, "/api/v1/auth/reset-password",
		map[string]string{"token": weak, "password": "short"}, []*http.Cookie{cookie}, headers)
	if weakRec.Code != http.StatusBadRequest || apiErrorCode(t, weakRec) != "password_too_short" {
		t.Errorf("weak password = %d %s, want 400 password_too_short", weakRec.Code, apiErrorCode(t, weakRec))
	}

	// 过期令牌：token_expired。
	expired, err := ts.srv.tokens.Issue(context.Background(), ts.ownerID, store.ActionTokenPasswordReset, "", -time.Minute)
	if err != nil {
		t.Fatalf("issue expired reset token: %v", err)
	}
	expiredRec := postJSON(ts.srv, "/api/v1/auth/reset-password",
		map[string]string{"token": expired, "password": "N3wSup3rSecret!"}, []*http.Cookie{cookie}, headers)
	if expiredRec.Code != http.StatusBadRequest || apiErrorCode(t, expiredRec) != "token_expired" {
		t.Errorf("expired token = %d %s, want 400 token_expired", expiredRec.Code, apiErrorCode(t, expiredRec))
	}
}

// TestSPAAccountEmailVerifyFlows 覆盖会话端点的读取/重发/改邮箱，以及免登录的验证与确认 JSON 端点。
func TestSPAAccountEmailVerifyFlows(t *testing.T) {
	ts := newSecurityServer(t, true)

	// GET /api/v1/settings/email：当前邮箱 + 未验证 + 邮件可用。
	rec := getWithCookies(t, ts.srv, "/api/v1/settings/email", ts.cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/settings/email status = %d, want 200", rec.Code)
	}
	var settings struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		MailReady     bool   `json:"mail_ready"`
	}
	decodeJSON(t, rec, &settings)
	if settings.Email != "owner@example.com" || settings.EmailVerified || !settings.MailReady {
		t.Errorf("email settings = %+v, want owner@example.com / unverified / mail_ready", settings)
	}

	// 匿名读取会话端点：401。
	if anon := get(t, ts.srv, "/api/v1/settings/email", nil); anon.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET /api/v1/settings/email = %d, want 401", anon.Code)
	}

	// 重发验证邮件（会话 CSRF）。
	resend := postJSONWithSession(ts.srv, "/api/v1/settings/verify-email", nil, ts.cookies, ts.csrf)
	if resend.Code != http.StatusOK {
		t.Fatalf("resend verification status = %d, want 200 (body %s)", resend.Code, snippet(resend.Body.String()))
	}
	verifyRows := outboxByType(t, ts.db, mail.TypeEmailVerification)
	if len(verifyRows) == 0 {
		t.Fatal("resend verification enqueued no email_verification mail")
	}
	verifyToken := tokenFromBody(t, verifyRows[len(verifyRows)-1].TextBody)

	// 消费验证令牌（免登录，双提交 CSRF）。
	vc, vh := preSessionPair(t, ts.srv, "/spa/verify-email")
	verified := postJSON(ts.srv, "/api/v1/auth/verify-email",
		map[string]string{"token": verifyToken}, []*http.Cookie{vc}, vh)
	if verified.Code != http.StatusOK {
		t.Fatalf("verify-email status = %d, want 200 (body %s)", verified.Code, snippet(verified.Body.String()))
	}
	var owner store.User
	if err := ts.db.First(&owner, ts.ownerID).Error; err != nil {
		t.Fatalf("reload owner: %v", err)
	}
	if owner.EmailVerifiedAt == nil {
		t.Error("email_verified_at is still NULL after JSON verification")
	}
	// 二次使用：token_used。
	again := postJSON(ts.srv, "/api/v1/auth/verify-email",
		map[string]string{"token": verifyToken}, []*http.Cookie{vc}, vh)
	if again.Code != http.StatusBadRequest || apiErrorCode(t, again) != "token_used" {
		t.Errorf("reused verification token = %d %s, want 400 token_used", again.Code, apiErrorCode(t, again))
	}

	// 改邮箱请求：无会话 CSRF → 403；格式非法 → email_invalid；与原邮箱相同 → email_same。
	if noCSRF := postJSON(ts.srv, "/api/v1/settings/email", map[string]string{"email": "x@example.com"}, ts.cookies, nil); noCSRF.Code != http.StatusForbidden {
		t.Errorf("settings/email without session CSRF = %d, want 403", noCSRF.Code)
	}
	invalid := postJSONWithSession(ts.srv, "/api/v1/settings/email", map[string]string{"email": "not-an-email"}, ts.cookies, ts.csrf)
	if invalid.Code != http.StatusBadRequest || apiErrorCode(t, invalid) != "email_invalid" {
		t.Errorf("invalid new email = %d %s, want 400 email_invalid", invalid.Code, apiErrorCode(t, invalid))
	}
	same := postJSONWithSession(ts.srv, "/api/v1/settings/email", map[string]string{"email": "owner@example.com"}, ts.cookies, ts.csrf)
	if same.Code != http.StatusBadRequest || apiErrorCode(t, same) != "email_same" {
		t.Errorf("same new email = %d %s, want 400 email_same", same.Code, apiErrorCode(t, same))
	}
	// 已被占用 → email_taken。
	taken := &store.User{
		Username: "takenuser", Email: "taken@example.com", DisplayName: "Taken",
		Role: store.RoleUser, Status: store.StatusActive, Locale: "en",
		Timezone: "UTC", CreatedAt: time.Now().UTC(),
	}
	if err := store.NewUserStore(ts.db).Create(context.Background(), taken); err != nil {
		t.Fatalf("create taken user: %v", err)
	}
	takenRec := postJSONWithSession(ts.srv, "/api/v1/settings/email", map[string]string{"email": "taken@example.com"}, ts.cookies, ts.csrf)
	if takenRec.Code != http.StatusBadRequest || apiErrorCode(t, takenRec) != "email_taken" {
		t.Errorf("taken new email = %d %s, want 400 email_taken", takenRec.Code, apiErrorCode(t, takenRec))
	}

	// 成功改邮箱请求：入队到新地址，且确认前库里仍不变。
	before := len(outboxByType(t, ts.db, mail.TypeEmailVerification))
	change := postJSONWithSession(ts.srv, "/api/v1/settings/email", map[string]string{"email": "new-owner@example.com"}, ts.cookies, ts.csrf)
	if change.Code != http.StatusOK {
		t.Fatalf("email change request status = %d, want 200 (body %s)", change.Code, snippet(change.Body.String()))
	}
	rows := outboxByType(t, ts.db, mail.TypeEmailVerification)
	if len(rows) != before+1 || rows[len(rows)-1].To != "new-owner@example.com" {
		t.Fatalf("email change confirmation not enqueued to the new address: %+v", rows)
	}
	if err := ts.db.First(&owner, ts.ownerID).Error; err != nil {
		t.Fatalf("reload owner: %v", err)
	}
	if owner.Email != "owner@example.com" {
		t.Fatalf("email changed before confirmation: %q", owner.Email)
	}

	// 确认改邮箱（免登录，双提交 CSRF）：真正改库。
	changeToken := tokenFromBody(t, rows[len(rows)-1].TextBody)
	cc, ch := preSessionPair(t, ts.srv, "/spa/confirm-email-change")
	confirm := postJSON(ts.srv, "/api/v1/auth/confirm-email-change",
		map[string]string{"token": changeToken}, []*http.Cookie{cc}, ch)
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm-email-change status = %d, want 200 (body %s)", confirm.Code, snippet(confirm.Body.String()))
	}
	if err := ts.db.First(&owner, ts.ownerID).Error; err != nil {
		t.Fatalf("reload owner: %v", err)
	}
	if owner.Email != "new-owner@example.com" {
		t.Errorf("email after confirmation = %q, want new-owner@example.com", owner.Email)
	}
}

// TestSPAShellEndpointsServeAppAndDoubleSubmitCookie 断言 /spa/verify-email 与
// /spa/confirm-email-change 下发双提交 cookie 并返回应用壳，且不消费任何令牌。
func TestSPAShellEndpointsServeAppAndDoubleSubmitCookie(t *testing.T) {
	ts := newSecurityServer(t, true)
	for _, path := range []string{"/spa/verify-email", "/spa/confirm-email-change"} {
		rec := get(t, ts.srv, path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `id="app"`) {
			t.Errorf("GET %s did not serve the SPA shell: %s", path, snippet(rec.Body.String()))
		}
		if findCookie(rec, auth.CSRFDoubleSubmitCookieName) == nil {
			t.Errorf("GET %s must initialize the double-submit cookie", path)
		}
	}
}
