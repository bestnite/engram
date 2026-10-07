package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 本文件是三个「个人/卡组设置页」切流到 SPA 的验收：GET /settings/totp、
// GET /settings/notifications、GET /decks/:id/settings 一律返回应用壳（SSR 页面层已删除，
// 不再有降级回退）。
//
// 页面迁移不改动授权判定：未登录一律重定向登录页，卡组设置页的 owner 门禁仍在返回应用壳
// 之前生效，写操作全部走 JSON 端点。

// assertServesSPAShell 断言响应是 SPA 应用壳（入口脚本引用），而不是 SSR 页面。
func assertServesSPAShell(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status = %d, want 200 (body %s)", what, rec.Code, snippet(body))
	}
	if !strings.Contains(body, `id="app"`) {
		t.Errorf("%s did not serve the SPA shell: %s", what, snippet(body))
	}
	// SSR 设置页的标志性结构不得出现（服务端不再渲染它们）。
	for _, marker := range []string{"hx-post", "name=\"csrf_token\""} {
		if strings.Contains(body, marker) {
			t.Errorf("%s still renders the SSR page (%q present)", what, marker)
		}
	}
}

// TestSPATOTPSettingsRouteCutover 覆盖 GET /settings/totp 的切流（页面只返回应用壳，无 SSR 回退）。
func TestSPATOTPSettingsRouteCutover(t *testing.T) {
	srv, db := newAuthServer(t)
	_ = createTOTPAdmin(t, srv, db)
	cookies, _ := spaTOTPLogin(t, srv, db)

	rec := getWithCookies(t, srv, "/settings/totp", cookies)
	assertServesSPAShell(t, rec, "GET /settings/totp")

	// 未登录仍然重定向登录页（授权判定不变）。
	anon := get(t, srv, "/settings/totp", nil)
	if anon.Code != http.StatusSeeOther || anon.Header().Get("Location") != "/login" {
		t.Errorf("anonymous GET /settings/totp = %d %q, want 303 /login", anon.Code, anon.Header().Get("Location"))
	}
}

// TestSPANotificationPrefsRouteCutover 覆盖 GET /settings/notifications 的切流。
func TestSPANotificationPrefsRouteCutover(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)

	rec := getWithCookies(t, srv, "/settings/notifications", cookies)
	assertServesSPAShell(t, rec, "GET /settings/notifications")

	anon := get(t, srv, "/settings/notifications", nil)
	if anon.Code != http.StatusSeeOther || anon.Header().Get("Location") != "/login" {
		t.Errorf("anonymous GET = %d %q, want 303 /login", anon.Code, anon.Header().Get("Location"))
	}
}

// TestSPADeckSettingsRouteCutover 覆盖 GET /decks/:id/settings 的切流，并钉住 owner 门禁
// 仍在应用壳之前生效（无权用户拿不到页面外壳）。
func TestSPADeckSettingsRouteCutover(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Cutover deck")
	path := "/decks/" + u64str(deck.ID) + "/settings"

	assertServesSPAShell(t, getWithCookies(t, srv, path, cookies), "GET "+path)

	// 未登录：重定向登录页，绝不返回外壳。
	anon := get(t, srv, path, nil)
	if anon.Code != http.StatusSeeOther {
		t.Errorf("anonymous GET %s = %d, want 303", path, anon.Code)
	}

	// 非 owner：403，且响应体里没有应用壳。
	_, strangerCookies, _ := createUserAndLogin(t, srv, db, "settings_stranger")
	denied := getWithCookies(t, srv, path, strangerCookies)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("non-owner GET %s = %d, want 403 (body %s)", path, denied.Code, snippet(denied.Body.String()))
	}
	if strings.Contains(denied.Body.String(), `id="app"`) {
		t.Error("a non-owner must not receive the SPA shell")
	}
}
