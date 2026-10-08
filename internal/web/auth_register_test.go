package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件覆盖 SPA 注册与首个管理员引导：GET /register、GET /setup 应用壳，
// 以及 POST /api/v1/auth/register、POST /api/v1/auth/setup 两个 JSON 端点。
// 安全语义与 SSR 表单一致：会话前双提交 CSRF、注册策略/邮箱白名单/邀请事务、引导一次性
// 管理员门、成功不建立会话。全部走真实路由与 SQLite，不 mock 数据库。

// apiErrorCode 从 JSON 错误包壳里取稳定英文 code。
func apiErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal error envelope: %v (body %s)", err, snippet(rec.Body.String()))
	}
	return env.Error.Code
}

// preSessionPair 走一次 GET 拿到会话前双提交 cookie 与镜像 token，供 JSON 写请求使用。
func preSessionPair(t *testing.T, srv *Server, path string) (*http.Cookie, map[string]string) {
	t.Helper()
	rec := get(t, srv, path, nil)
	cookie := findCookie(rec, auth.CSRFDoubleSubmitCookieName)
	if cookie == nil {
		t.Fatalf("GET %s did not set %s", path, auth.CSRFDoubleSubmitCookieName)
	}
	return cookie, map[string]string{auth.CSRFHeaderName: cookie.Value}
}

// countUsersByEmail 统计指定邮箱的账号数，用于断言建号是否真的发生。
func countUsersByEmail(t *testing.T, srv *Server, email string) int64 {
	t.Helper()
	var n int64
	if err := srv.db.Model(&store.User{}).Where("email = ?", email).Count(&n).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	return n
}

// TestRegisterShellServesAppAndInitializesDoubleSubmitCookie 断言 GET /register
// 返回应用壳并下发会话前双提交 cookie，但不建立会话、不渲染 SSR 表单。
func TestRegisterShellServesAppAndInitializesDoubleSubmitCookie(t *testing.T) {
	srv, _ := newAuthServer(t)

	rec := get(t, srv, "/register", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /register status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET /register Content-Type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<div id="app"></div>`) {
		t.Errorf("GET /register did not serve the SPA shell: %s", snippet(body))
	}
	if strings.Contains(body, `name="password"`) || strings.Contains(body, `action="/register"`) {
		t.Errorf("GET /register must not render the SSR register form: %s", snippet(body))
	}
	c := findCookie(rec, auth.CSRFDoubleSubmitCookieName)
	if c == nil {
		t.Fatalf("GET /register did not set %s", auth.CSRFDoubleSubmitCookieName)
	}
	if !c.HttpOnly {
		t.Errorf("csrf_double cookie must be HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("csrf_double cookie SameSite = %v, want Lax", c.SameSite)
	}
	if findCookie(rec, srv.sessions.CookieName()) != nil {
		t.Errorf("GET /register must not issue a session cookie")
	}
}

// TestSetupShellAvailability 断言 GET /setup 只在没有活跃管理员时返回应用壳，
// 已存在管理员时返回 404（与 SSR 的一次性管理员门一致）。
func TestSetupShellAvailability(t *testing.T) {
	// 首启窗口：没有管理员，可达，下发双提交 cookie。
	fresh, _ := newAuthServer(t)
	rec := get(t, fresh, "/setup", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /setup (no admin) status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
		t.Errorf("GET /setup did not serve the SPA shell: %s", snippet(rec.Body.String()))
	}
	if findCookie(rec, auth.CSRFDoubleSubmitCookieName) == nil {
		t.Errorf("GET /setup must initialize the double-submit cookie")
	}
	if findCookie(rec, fresh.sessions.CookieName()) != nil {
		t.Errorf("GET /setup must not issue a session cookie")
	}

	// 已有管理员：引导窗口关闭，必须 404。
	seeded, _ := newAuthServer(t)
	seedAdminUser(t, seeded)
	closed := get(t, seeded, "/setup", nil)
	if closed.Code != http.StatusNotFound {
		t.Fatalf("GET /setup (admin exists) status = %d, want 404", closed.Code)
	}
}

// TestRegisterAPI_CSRFRejections 断言注册 JSON 端点复用会话前双提交中间件：
// 缺 cookie、缺镜像 token、值不匹配一律 403 csrf_failed，且不建号。
func TestRegisterAPI_CSRFRejections(t *testing.T) {
	srv, _ := newAuthServer(t)
	seedAdminUser(t, srv)
	writeSetting(t, srv.db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, "open")))

	body := map[string]string{
		"username": "intruder", "email": "intruder@example.com", "password": "Sup3rSecret!",
	}

	// 1. 无 cookie 且无镜像 token。
	none := postJSON(srv, "/api/v1/auth/register", body, nil, nil)
	if none.Code != http.StatusForbidden || apiErrorCode(t, none) != "csrf_failed" {
		t.Errorf("missing CSRF = %d %s, want 403 csrf_failed", none.Code, apiErrorCode(t, none))
	}

	// 2. 有 cookie 但无镜像 token。
	cookie := &http.Cookie{Name: auth.CSRFDoubleSubmitCookieName, Value: testDoubleSubmitToken}
	noHeader := postJSON(srv, "/api/v1/auth/register", body, []*http.Cookie{cookie}, nil)
	if noHeader.Code != http.StatusForbidden || apiErrorCode(t, noHeader) != "csrf_failed" {
		t.Errorf("cookie without header = %d %s, want 403 csrf_failed", noHeader.Code, apiErrorCode(t, noHeader))
	}

	// 3. 有 cookie 但镜像值不匹配。
	mismatch := postJSON(srv, "/api/v1/auth/register", body, []*http.Cookie{cookie},
		map[string]string{auth.CSRFHeaderName: "mismatched-token-value-123456789"})
	if mismatch.Code != http.StatusForbidden || apiErrorCode(t, mismatch) != "csrf_failed" {
		t.Errorf("mismatched CSRF = %d %s, want 403 csrf_failed", mismatch.Code, apiErrorCode(t, mismatch))
	}

	if n := countUsersByEmail(t, srv, "intruder@example.com"); n != 0 {
		t.Errorf("CSRF-rejected registrations created %d users, want 0", n)
	}
}

// TestRegisterAPI_PolicyMatrix 断言 JSON 注册端点与 SSR 走同一套策略判定：
// open 放行、closed 拒绝、invite 无令牌拒绝、白名单不匹配拒绝。
func TestRegisterAPI_PolicyMatrix(t *testing.T) {
	cases := []struct {
		name       string
		policy     string
		allowlist  string
		email      string
		wantStatus int
		wantCode   string
	}{
		{name: "open allows anyone", policy: "open", email: "alice@example.org", wantStatus: http.StatusOK},
		{name: "open allowlist denial", policy: "open", allowlist: `["example.com"]`,
			email: "alice@example.net", wantStatus: http.StatusForbidden, wantCode: "email_domain_not_allowed"},
		{name: "invite requires a token", policy: "invite",
			email: "alice@example.com", wantStatus: http.StatusForbidden, wantCode: "invite_required"},
		{name: "closed rejects everyone", policy: "closed",
			email: "alice@example.com", wantStatus: http.StatusForbidden, wantCode: "registration_closed"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := newAuthServer(t)
			seedAdminUser(t, srv)
			writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, tc.policy)))
			if tc.allowlist != "" {
				writeSetting(t, db, auth.SettingKeyEmailAllowlist, tc.allowlist)
			}
			cookie, headers := preSessionPair(t, srv, "/register")
			rec := postJSON(srv, "/api/v1/auth/register", map[string]string{
				"username": string(rune('a'+i)) + "_user",
				"email":    tc.email,
				"password": "Sup3rSecret!",
			}, []*http.Cookie{cookie}, headers)

			if rec.Code != tc.wantStatus {
				t.Fatalf("POST /api/v1/auth/register (%s) status = %d, want %d (body %s)",
					tc.name, rec.Code, tc.wantStatus, snippet(rec.Body.String()))
			}
			if tc.wantCode != "" && apiErrorCode(t, rec) != tc.wantCode {
				t.Errorf("code = %q, want %q", apiErrorCode(t, rec), tc.wantCode)
			}
			wantUsers := int64(0)
			if tc.wantStatus == http.StatusOK {
				wantUsers = 1
			}
			if n := countUsersByEmail(t, srv, tc.email); n != wantUsers {
				t.Errorf("users for %s = %d, want %d", tc.email, n, wantUsers)
			}
		})
	}
}

// TestRegisterAPI_SuccessIssuesNoSession 断言注册成功只返回 created=true，
// 不签发会话 cookie（与 SSR 注册后跳转登录一致）。
func TestRegisterAPI_SuccessIssuesNoSession(t *testing.T) {
	srv, db := newAuthServer(t)
	seedAdminUser(t, srv)
	writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, "open")))

	cookie, headers := preSessionPair(t, srv, "/register")
	rec := postJSON(srv, "/api/v1/auth/register", map[string]string{
		"username": "newcomer", "email": "newcomer@example.com", "password": "Sup3rSecret!",
	}, []*http.Cookie{cookie}, headers)

	if rec.Code != http.StatusOK {
		t.Fatalf("register status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var res struct {
		Created bool `json:"created"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal register response: %v", err)
	}
	if !res.Created {
		t.Errorf("expected created=true, got %s", snippet(rec.Body.String()))
	}
	if findCookie(rec, srv.sessions.CookieName()) != nil {
		t.Errorf("registration must not issue a session cookie")
	}
	if n := countUsersByEmail(t, srv, "newcomer@example.com"); n != 1 {
		t.Errorf("users created = %d, want 1", n)
	}
}

// TestRegisterAPI_Invite 断言邀请路径与 SSR 一致：有效邀请建号一次并占用 token，
// 复用同一 token 返回 403 invite_invalid，且不产生第二个用户。
func TestRegisterAPI_Invite(t *testing.T) {
	srv, db := newAuthServer(t)
	seedAdminUser(t, srv)
	writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, "invite")))
	invites := store.NewInviteStore(db)
	inv := &store.Invite{}
	if err := invites.Create(context.Background(), inv); err != nil {
		t.Fatalf("create invite: %v", err)
	}

	cookie, headers := preSessionPair(t, srv, "/register")
	first := postJSON(srv, "/api/v1/auth/register", map[string]string{
		"username": "invitee", "email": "invitee@example.com",
		"password": "Sup3rSecret!", "invite": inv.Token,
	}, []*http.Cookie{cookie}, headers)
	if first.Code != http.StatusOK {
		t.Fatalf("invite registration status = %d, want 200 (body %s)", first.Code, snippet(first.Body.String()))
	}

	second := postJSON(srv, "/api/v1/auth/register", map[string]string{
		"username": "invitee2", "email": "invitee2@example.com",
		"password": "Sup3rSecret!", "invite": inv.Token,
	}, []*http.Cookie{cookie}, headers)
	if second.Code != http.StatusForbidden || apiErrorCode(t, second) != "invite_invalid" {
		t.Fatalf("reused invite = %d %s, want 403 invite_invalid", second.Code, apiErrorCode(t, second))
	}

	if n := countUsersByEmail(t, srv, "invitee@example.com"); n != 1 {
		t.Errorf("users from one invite = %d, want 1", n)
	}
	used, err := invites.ByToken(context.Background(), inv.Token)
	if err != nil {
		t.Fatalf("ByToken() error = %v", err)
	}
	if used.UsedAt == nil || used.UsedBy == nil {
		t.Errorf("invite after acceptance = %+v, want used_at and used_by set", used)
	}
}

// TestRegisterAPI_Validation 断言表单级校验返回稳定 code 且不建号。
func TestRegisterAPI_Validation(t *testing.T) {
	cases := []struct {
		name     string
		body     map[string]string
		wantCode string
	}{
		{name: "missing username",
			body:     map[string]string{"email": "a@example.com", "password": "Sup3rSecret!"},
			wantCode: "username_required"},
		{name: "invalid email",
			body:     map[string]string{"username": "a", "email": "not-an-email", "password": "Sup3rSecret!"},
			wantCode: "email_invalid"},
		{name: "short password",
			body:     map[string]string{"username": "a", "email": "a@example.com", "password": "short"},
			wantCode: "password_too_short"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := newAuthServer(t)
			seedAdminUser(t, srv)
			writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, "open")))
			cookie, headers := preSessionPair(t, srv, "/register")
			rec := postJSON(srv, "/api/v1/auth/register", tc.body, []*http.Cookie{cookie}, headers)
			if rec.Code != http.StatusBadRequest || apiErrorCode(t, rec) != tc.wantCode {
				t.Fatalf("status/code = %d %s, want 400 %s", rec.Code, apiErrorCode(t, rec), tc.wantCode)
			}
		})
	}
}

// TestSetupAPI_CreatesFirstAdminAndCloses 断言引导 JSON 端点在没有管理员时创建管理员，
// 之后同一端点 404（一次性管理员门），且成功不签发会话 cookie。
func TestSetupAPI_CreatesFirstAdminAndCloses(t *testing.T) {
	srv, _ := newAuthServer(t)
	cookie, headers := preSessionPair(t, srv, "/setup")

	rec := postJSON(srv, "/api/v1/auth/setup", map[string]string{
		"username": "root", "email": "root@example.com", "password": "Sup3rSecret!",
	}, []*http.Cookie{cookie}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if findCookie(rec, srv.sessions.CookieName()) != nil {
		t.Errorf("setup must not issue a session cookie")
	}
	u, err := srv.users.ByUsername(context.Background(), "root")
	if err != nil {
		t.Fatalf("ByUsername(root) error = %v", err)
	}
	if u.Role != store.RoleAdmin {
		t.Errorf("bootstrap user role = %q, want admin", u.Role)
	}

	// 已存在管理员：同一端点必须 404，且不创建第二个账号。
	again := postJSON(srv, "/api/v1/auth/setup", map[string]string{
		"username": "root2", "email": "root2@example.com", "password": "Sup3rSecret!",
	}, []*http.Cookie{cookie}, headers)
	if again.Code != http.StatusNotFound {
		t.Fatalf("second setup status = %d, want 404", again.Code)
	}
	if n := countUsersByEmail(t, srv, "root2@example.com"); n != 0 {
		t.Errorf("closed setup created %d users, want 0", n)
	}
}

// TestSetupAPI_BootstrapEmailFallback 断言请求未填邮箱时采用 BOOTSTRAP_ADMIN_EMAIL，
// 兜底行为与引导页的应用壳入口一致。
func TestSetupAPI_BootstrapEmailFallback(t *testing.T) {
	srv, _ := newAuthServer(t)
	srv.bootstrapEmail = "boot@example.com"
	cookie, headers := preSessionPair(t, srv, "/setup")

	rec := postJSON(srv, "/api/v1/auth/setup", map[string]string{
		"username": "bootroot", "password": "Sup3rSecret!",
	}, []*http.Cookie{cookie}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup with bootstrap email status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if n := countUsersByEmail(t, srv, "boot@example.com"); n != 1 {
		t.Errorf("users with bootstrap email = %d, want 1", n)
	}
}

// TestSetupAPI_CSRFRejection 断言引导 JSON 端点同样强制会话前双提交 CSRF。
func TestSetupAPI_CSRFRejection(t *testing.T) {
	srv, _ := newAuthServer(t)
	rec := postJSON(srv, "/api/v1/auth/setup", map[string]string{
		"username": "root", "email": "root@example.com", "password": "Sup3rSecret!",
	}, nil, nil)
	if rec.Code != http.StatusForbidden || apiErrorCode(t, rec) != "csrf_failed" {
		t.Fatalf("setup without CSRF = %d %s, want 403 csrf_failed", rec.Code, apiErrorCode(t, rec))
	}
	if n := countUsersByEmail(t, srv, "root@example.com"); n != 0 {
		t.Errorf("CSRF-rejected setup created %d users, want 0", n)
	}
}

// TestRegistrationInfoEndpoint 断言 GET /api/v1/auth/registration 与注册端点同源地报告策略：
// open / invite / closed 原样返回，未设置或未知取值归一为 closed（安全默认）。
// 该端点是登录前的只读探测：匿名（无 cookie）即可调用，不签发会话 cookie，也不建立会话。
func TestRegistrationInfoEndpoint(t *testing.T) {
	cases := []struct {
		name    string
		setting string // 空串表示不写入设置，走「未设置」路径
		want    string
	}{
		{name: "unset falls back to closed", want: auth.PolicyClosed},
		{name: "unknown value falls back to closed", setting: "nonsense", want: auth.PolicyClosed},
		{name: "open reported as-is", setting: auth.PolicyOpen, want: auth.PolicyOpen},
		{name: "invite reported as-is", setting: auth.PolicyInvite, want: auth.PolicyInvite},
		{name: "closed reported as-is", setting: auth.PolicyClosed, want: auth.PolicyClosed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := newAuthServer(t)
			seedAdminUser(t, srv)
			if tc.setting != "" {
				writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, tc.setting)))
			}

			// 不带任何 cookie：探测必须对匿名访问者可用。
			rec := get(t, srv, "/api/v1/auth/registration", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /api/v1/auth/registration status = %d, want 200 (body %s)",
					rec.Code, snippet(rec.Body.String()))
			}
			var resp struct {
				Policy string `json:"policy"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal registration info: %v (body %s)", err, snippet(rec.Body.String()))
			}
			if resp.Policy != tc.want {
				t.Errorf("policy = %q, want %q", resp.Policy, tc.want)
			}
			if findCookie(rec, srv.sessions.CookieName()) != nil {
				t.Errorf("GET /api/v1/auth/registration must not issue a session cookie")
			}
		})
	}
}
