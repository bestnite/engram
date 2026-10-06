package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// postJSON 发起带 Headers 与 Cookies 的 POST JSON 请求。
func postJSON(srv *Server, path string, body any, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// findCookie 从 ResponseRecorder 的 Set-Cookie 响应头解析指定 cookie。
func findCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// createTestUser 在数据库中创建一个测试账号并返回其模型。
func createTestUser(t *testing.T, srv *Server, username, password, role string, active bool) *store.User {
	t.Helper()
	ctx := context.Background()
	u, err := srv.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username:    username,
		Email:       username + "@example.com",
		DisplayName: "Test " + username,
		Password:    password,
		Role:        role,
		Locale:      "zh-CN",
	})
	if err != nil {
		t.Fatalf("CreateLocalUser(%s) error = %v", username, err)
	}
	if !active {
		if err := srv.accounts.DisableUser(ctx, u.ID); err != nil {
			t.Fatalf("DisableUser(%s) error = %v", username, err)
		}
		u.Status = store.StatusDisabled
	}
	return u
}

func TestSPASession_Unauthenticated(t *testing.T) {
	srv, _ := newAuthServer(t)

	rec := get(t, srv, "/api/v1/auth/session", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/session status = %d, want 200", rec.Code)
	}

	var res struct {
		Authenticated bool            `json:"authenticated"`
		User          json.RawMessage `json:"user"`
		CSRFToken     string          `json:"csrf_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal session response: %v", err)
	}

	if res.Authenticated {
		t.Errorf("expected authenticated=false, got true")
	}
	if string(res.User) != "null" {
		t.Errorf("expected user=null, got %s", string(res.User))
	}
	if res.CSRFToken == "" {
		t.Errorf("expected non-empty csrf_token")
	}

	// 验证下发了 HttpOnly、SameSite=Lax 的 csrf_double cookie，且值与返回的 token 一致
	c := findCookie(rec, auth.CSRFDoubleSubmitCookieName)
	if c == nil {
		t.Fatalf("expected Set-Cookie %s to be present", auth.CSRFDoubleSubmitCookieName)
	}
	if !c.HttpOnly {
		t.Errorf("expected csrf_double cookie to be HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("expected csrf_double cookie SameSite=Lax, got %v", c.SameSite)
	}
	if c.Value != res.CSRFToken {
		t.Errorf("cookie value %q does not match response csrf_token %q", c.Value, res.CSRFToken)
	}

	// 验证响应体绝不泄露 session_id 或 secret
	bodyStr := rec.Body.String()
	if strings.Contains(bodyStr, "session_id") || strings.Contains(bodyStr, "secret") {
		t.Errorf("response body leaks session/secret keyword: %s", bodyStr)
	}
}

func TestSPASession_Authenticated(t *testing.T) {
	srv, _ := newAuthServer(t)
	u := createTestUser(t, srv, "alice", "Password123!", store.RoleUser, true)

	ctx := context.Background()
	dummyReq := httptest.NewRequest(http.MethodGet, "/", nil)
	dummyRec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(dummyRec)
	c.Request = dummyReq
	sess, err := srv.sessions.StartSession(ctx, c, u.ID)
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}

	sessCookie := findCookie(dummyRec, srv.sessions.CookieName())
	if sessCookie == nil {
		t.Fatalf("session cookie not found in response")
	}

	rec := getWithCookies(t, srv, "/api/v1/auth/session", []*http.Cookie{sessCookie})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/session status = %d, want 200", rec.Code)
	}

	var res struct {
		Authenticated bool `json:"authenticated"`
		User          struct {
			ID          uint64 `json:"id"`
			Username    string `json:"username"`
			Email       string `json:"email"`
			DisplayName string `json:"display_name"`
			Role        string `json:"role"`
			Locale      string `json:"locale"`
		} `json:"user"`
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal session response: %v", err)
	}

	if !res.Authenticated {
		t.Errorf("expected authenticated=true")
	}
	if res.User.Username != "alice" || res.User.ID != u.ID {
		t.Errorf("unexpected user in response: %+v", res.User)
	}
	if res.CSRFToken != sess.CSRFToken {
		t.Errorf("csrf_token %q does not match session csrf %q", res.CSRFToken, sess.CSRFToken)
	}

	// 验证绝不泄露会话 ID (sess.ID)
	if strings.Contains(rec.Body.String(), sess.ID) {
		t.Errorf("response leaked session ID %q", sess.ID)
	}

	// 测试路径别名 /api/v1/session 输出逐字节一致
	aliasRec := getWithCookies(t, srv, "/api/v1/session", []*http.Cookie{sessCookie})
	if aliasRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/session alias status = %d, want 200", aliasRec.Code)
	}
	if aliasRec.Body.String() != rec.Body.String() {
		t.Errorf("alias body %s differs from canonical body %s", aliasRec.Body.String(), rec.Body.String())
	}
}

func TestSPASession_DisabledUser(t *testing.T) {
	srv, _ := newAuthServer(t)
	u := createTestUser(t, srv, "disabled_user", "Password123!", store.RoleUser, false)

	ctx := context.Background()
	dummyReq := httptest.NewRequest(http.MethodGet, "/", nil)
	dummyRec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(dummyRec)
	c.Request = dummyReq
	_, err := srv.sessions.StartSession(ctx, c, u.ID)
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	sessCookie := findCookie(dummyRec, srv.sessions.CookieName())

	rec := getWithCookies(t, srv, "/api/v1/auth/session", []*http.Cookie{sessCookie})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var res struct {
		Authenticated bool            `json:"authenticated"`
		User          json.RawMessage `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if res.Authenticated {
		t.Errorf("disabled user should be reported as authenticated=false")
	}
	if string(res.User) != "null" {
		t.Errorf("disabled user payload should be null")
	}
}

func TestSPALogin_CSRFRejections(t *testing.T) {
	srv, _ := newAuthServer(t)
	createTestUser(t, srv, "bob", "Password123!", store.RoleUser, true)

	// 1. 无 Cookie 且无 CSRF 头
	rec := postJSON(srv, "/api/v1/auth/login", map[string]string{
		"username": "bob", "password": "Password123!",
	}, nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("missing CSRF status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "csrf_failed") {
		t.Errorf("expected csrf_failed error, got %s", rec.Body.String())
	}

	// 2. 有 Cookie 但无 Header
	cookie := &http.Cookie{Name: auth.CSRFDoubleSubmitCookieName, Value: testDoubleSubmitToken}
	rec2 := postJSON(srv, "/api/v1/auth/login", map[string]string{
		"username": "bob", "password": "Password123!",
	}, []*http.Cookie{cookie}, nil)
	if rec2.Code != http.StatusForbidden {
		t.Errorf("missing header status = %d, want 403", rec2.Code)
	}

	// 3. 有 Cookie 但 Header 值不匹配
	rec3 := postJSON(srv, "/api/v1/auth/login", map[string]string{
		"username": "bob", "password": "Password123!",
	}, []*http.Cookie{cookie}, map[string]string{
		auth.CSRFHeaderName: "mismatched-token-value-123456789",
	})
	if rec3.Code != http.StatusForbidden {
		t.Errorf("mismatched CSRF status = %d, want 403", rec3.Code)
	}
}

func TestSPALogin_SuccessAndFailure(t *testing.T) {
	srv, _ := newAuthServer(t)
	createTestUser(t, srv, "carol", "Password123!", store.RoleUser, true)
	createTestUser(t, srv, "disabled_carol", "Password123!", store.RoleUser, false)

	// 先获取合法的双提交 CSRF token
	initRec := get(t, srv, "/api/v1/auth/session", nil)
	doubleCookie := findCookie(initRec, auth.CSRFDoubleSubmitCookieName)
	if doubleCookie == nil {
		t.Fatalf("double submit cookie not set")
	}
	var initRes struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(initRec.Body.Bytes(), &initRes)
	csrfHeaders := map[string]string{auth.CSRFHeaderName: initRes.CSRFToken}

	// 1. 密码错误
	failRec := postJSON(srv, "/api/v1/auth/login", map[string]string{
		"username": "carol", "password": "WrongPassword123!",
	}, []*http.Cookie{doubleCookie}, csrfHeaders)
	if failRec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401", failRec.Code)
	}
	if !strings.Contains(failRec.Body.String(), "invalid_credentials") {
		t.Errorf("expected invalid_credentials, got %s", failRec.Body.String())
	}

	// 2. 账号禁用
	disRec := postJSON(srv, "/api/v1/auth/login", map[string]string{
		"username": "disabled_carol", "password": "Password123!",
	}, []*http.Cookie{doubleCookie}, csrfHeaders)
	if disRec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled user status = %d, want 401", disRec.Code)
	}
	if !strings.Contains(disRec.Body.String(), "user_disabled") {
		t.Errorf("expected user_disabled, got %s", disRec.Body.String())
	}

	// 3. 登录成功
	okRec := postJSON(srv, "/api/v1/auth/login", map[string]string{
		"username": "carol", "password": "Password123!",
	}, []*http.Cookie{doubleCookie}, csrfHeaders)
	if okRec.Code != http.StatusOK {
		t.Fatalf("login success status = %d, want 200 (body %s)", okRec.Code, okRec.Body.String())
	}

	var okRes struct {
		Authenticated bool `json:"authenticated"`
		User          struct {
			Username string `json:"username"`
		} `json:"user"`
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(okRec.Body.Bytes(), &okRes); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !okRes.Authenticated || okRes.User.Username != "carol" || okRes.CSRFToken == "" {
		t.Errorf("unexpected login response: %+v", okRes)
	}

	// 验证下发了会话 Cookie，且响应不包含会话 ID 内部值
	sessionCookie := findCookie(okRec, srv.sessions.CookieName())
	if sessionCookie == nil {
		t.Fatalf("expected session cookie in login response")
	}
	if !sessionCookie.HttpOnly {
		t.Errorf("expected session cookie to be HttpOnly")
	}

	// 用该会话 Cookie 调用 GET /api/v1/auth/session 验证状态已生效
	sessRec := getWithCookies(t, srv, "/api/v1/auth/session", []*http.Cookie{sessionCookie})
	if sessRec.Code != http.StatusOK {
		t.Fatalf("session check status = %d, want 200", sessRec.Code)
	}
	if !strings.Contains(sessRec.Body.String(), `"authenticated":true`) {
		t.Errorf("session check did not report authenticated: %s", sessRec.Body.String())
	}
}

func TestSPALogin_TOTPChallenge(t *testing.T) {
	srv, _ := newAuthServer(t)
	u := createTestUser(t, srv, "totp_user", "Password123!", store.RoleUser, true)

	// 开启 TOTP
	ctx := context.Background()
	secret, _, err := srv.totp.Begin(ctx, u.ID, u.Username)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	validCode, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error = %v", err)
	}
	_, err = srv.totp.Confirm(ctx, u.ID, validCode)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}

	initRec := get(t, srv, "/api/v1/auth/session", nil)
	doubleCookie := findCookie(initRec, auth.CSRFDoubleSubmitCookieName)
	var initRes struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(initRec.Body.Bytes(), &initRes)

	// 密码正确但开启了 TOTP：必须返回 requires_totp challenge，绝不发放会话 cookie
	rec := postJSON(srv, "/api/v1/auth/login", map[string]string{
		"username": "totp_user", "password": "Password123!",
	}, []*http.Cookie{doubleCookie}, map[string]string{auth.CSRFHeaderName: initRes.CSRFToken})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var res struct {
		RequiresTOTP  bool `json:"requires_totp"`
		Authenticated bool `json:"authenticated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !res.RequiresTOTP {
		t.Errorf("expected requires_totp=true")
	}
	if res.Authenticated {
		t.Errorf("expected authenticated=false before second factor")
	}

	// 验证未下发会话 cookie，但下发了 engram_totp_pending cookie
	if findCookie(rec, srv.sessions.CookieName()) != nil {
		t.Errorf("session cookie must NOT be issued on TOTP first factor")
	}
	pendingCookie := findCookie(rec, totpPendingCookieName)
	if pendingCookie == nil {
		t.Errorf("expected totp pending cookie to be set")
	}
}

func TestSPALogout_Flow(t *testing.T) {
	srv, _ := newAuthServer(t)
	u := createTestUser(t, srv, "dan", "Password123!", store.RoleUser, true)

	ctx := context.Background()
	dummyReq := httptest.NewRequest(http.MethodGet, "/", nil)
	dummyRec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(dummyRec)
	c.Request = dummyReq
	sess, err := srv.sessions.StartSession(ctx, c, u.ID)
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	sessCookie := findCookie(dummyRec, srv.sessions.CookieName())

	// 1. 无会话调用 logout -> 403 csrf_no_session
	recNoSess := postJSON(srv, "/api/v1/auth/logout", nil, nil, map[string]string{
		auth.CSRFHeaderName: sess.CSRFToken,
	})
	if recNoSess.Code != http.StatusForbidden {
		t.Errorf("no session status = %d, want 403", recNoSess.Code)
	}
	if !strings.Contains(recNoSess.Body.String(), "csrf_no_session") {
		t.Errorf("expected csrf_no_session, got %s", recNoSess.Body.String())
	}

	// 2. 有会话但无 CSRF Header -> 403 csrf_failed
	recNoCSRF := postJSON(srv, "/api/v1/auth/logout", nil, []*http.Cookie{sessCookie}, nil)
	if recNoCSRF.Code != http.StatusForbidden {
		t.Errorf("missing CSRF status = %d, want 403", recNoCSRF.Code)
	}
	if !strings.Contains(recNoCSRF.Body.String(), "csrf_failed") {
		t.Errorf("expected csrf_failed, got %s", recNoCSRF.Body.String())
	}

	// 3. 有会话但 CSRF Header 错误 -> 403 csrf_failed
	recBadCSRF := postJSON(srv, "/api/v1/auth/logout", nil, []*http.Cookie{sessCookie}, map[string]string{
		auth.CSRFHeaderName: "wrong-csrf-token",
	})
	if recBadCSRF.Code != http.StatusForbidden {
		t.Errorf("wrong CSRF status = %d, want 403", recBadCSRF.Code)
	}

	// 4. 正确的 CSRF Header 与会话 -> 登出成功
	recOK := postJSON(srv, "/api/v1/auth/logout", nil, []*http.Cookie{sessCookie}, map[string]string{
		auth.CSRFHeaderName: sess.CSRFToken,
	})
	if recOK.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want 200 (body %s)", recOK.Code, recOK.Body.String())
	}

	var logoutRes struct {
		Authenticated bool   `json:"authenticated"`
		CSRFToken     string `json:"csrf_token"`
	}
	if err := json.Unmarshal(recOK.Body.Bytes(), &logoutRes); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if logoutRes.Authenticated {
		t.Errorf("expected authenticated=false after logout")
	}
	if logoutRes.CSRFToken == "" {
		t.Errorf("expected fresh csrf_token returned after logout")
	}

	// 验证会话 Cookie 被清除（MaxAge < 0）
	clearedCookie := findCookie(recOK, srv.sessions.CookieName())
	if clearedCookie == nil || clearedCookie.MaxAge >= 0 {
		t.Errorf("session cookie was not cleared properly: %+v", clearedCookie)
	}

	// 验证用旧会话发起请求已被服务端作废
	recheckRec := getWithCookies(t, srv, "/api/v1/auth/session", []*http.Cookie{sessCookie})
	if !strings.Contains(recheckRec.Body.String(), `"authenticated":false`) {
		t.Errorf("revoked session should be unauthenticated, got %s", recheckRec.Body.String())
	}
}
