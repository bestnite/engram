package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// userLocale 读出用户当前的语言设置（落库值）。
func userLocale(t *testing.T, db *gorm.DB, id uint64) string {
	t.Helper()
	u, err := store.NewUserStore(db).ByID(t.Context(), id)
	if err != nil {
		t.Fatalf("reload user %d: %v", id, err)
	}
	return u.Locale
}

// postFormHeader 与 postForm 相同，但可附加请求头；htmx 路径需要 HX-Request。
func postFormHeader(t *testing.T, srv *Server, target string, values url.Values, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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

// TestHeaderLocaleSwitchPersistsAndSurvivesReload 是本缺陷的验收测试：
// 页头切换语言必须落进 users.locale；此后不带 ?lang 的请求（刷新、点导航）也保持新语言。
//
// 修复前这条会失败：页头下拉只是 ?lang= 链接，库里仍是旧值，刷新即回退。
func TestHeaderLocaleSwitchPersistsAndSurvivesReload(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)

	before := userLocale(t, db, ownerID)
	if before == "en" {
		t.Fatalf("测试前提不成立：初始语言已是 en")
	}

	rec := postForm(t, srv, "/settings/locale", url.Values{
		"csrf_token": {csrf},
		"lang":       {"en"},
		"next":       {"/settings"},
	}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings/locale status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if got := rec.Header().Get("Location"); got != "/settings" {
		t.Errorf("redirect target = %q, want %q", got, "/settings")
	}
	if got := userLocale(t, db, ownerID); got != "en" {
		t.Fatalf("users.locale after the header switch = %q, want %q", got, "en")
	}

	// 关键断言：不带 lang 参数的页面也用新语言渲染，说明语言来自用户设置而不是 URL 覆盖。
	page := getWithCookies(t, srv, "/settings", cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /settings status = %d, want 200 (body %s)", page.Code, snippet(page.Body.String()))
	}
	if !strings.Contains(page.Body.String(), `<html lang="en">`) {
		t.Errorf("page after the header switch is not English: %s", snippet(page.Body.String()))
	}

	n, err := store.NewAuditStore(db).CountByAction(t.Context(), store.ActionUserProfileUpdate)
	if err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if n != 1 {
		t.Errorf("audit rows for %s = %d, want 1", store.ActionUserProfileUpdate, n)
	}
}

// TestHeaderLocaleSwitchRequiresCSRF 是必测负例：缺 token 的写请求被拒且不改库。
func TestHeaderLocaleSwitchRequiresCSRF(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	before := userLocale(t, db, ownerID)

	rec := postForm(t, srv, "/settings/locale", url.Values{
		"lang": {"en"},
		"next": {"/settings"},
	}, cookies)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /settings/locale without CSRF status = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if got := userLocale(t, db, ownerID); got != before {
		t.Errorf("locale changed by a CSRF-less request: %q -> %q", before, got)
	}
	n, err := store.NewAuditStore(db).CountByAction(t.Context(), store.ActionUserProfileUpdate)
	if err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if n != 0 {
		t.Errorf("audit rows after a rejected request = %d, want 0", n)
	}
}

// TestHeaderLocaleSwitchRejectsUnsupportedLanguage 是必测负例：
// 不受支持的语言码被拒绝、不落库，并回填本地化提示。
func TestHeaderLocaleSwitchRejectsUnsupportedLanguage(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	before := userLocale(t, db, ownerID)

	rec := postForm(t, srv, "/settings/locale", url.Values{
		"csrf_token": {csrf},
		"lang":       {"de"},
		"next":       {"/settings"},
	}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /settings/locale (de) status = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "不支持的语言") {
		t.Errorf("rejection does not carry the localized message: %s", snippet(rec.Body.String()))
	}
	if got := userLocale(t, db, ownerID); got != before {
		t.Errorf("unsupported locale was persisted: %q -> %q", before, got)
	}
	n, err := store.NewAuditStore(db).CountByAction(t.Context(), store.ActionUserProfileUpdate)
	if err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if n != 0 {
		t.Errorf("audit rows after an unsupported locale = %d, want 0", n)
	}
}

// TestHeaderLocaleSwitchKeepsRedirectInternal 是必测负例（开放重定向）：
// next 只接受站内绝对路径，跨站形态一律落回首页。
func TestHeaderLocaleSwitchKeepsRedirectInternal(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)

	tests := []struct {
		next string
		want string
	}{
		{next: "//example.com/path", want: "/"},
		{next: "https://example.com/path", want: "/"},
		{next: "/\\example.com", want: "/"},
		{next: "", want: "/"},
		{next: "/decks?page=2", want: "/decks?page=2"},
	}
	for _, tc := range tests {
		rec := postForm(t, srv, "/settings/locale", url.Values{
			"csrf_token": {csrf},
			"lang":       {"en"},
			"next":       {tc.next},
		}, cookies)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST /settings/locale (next=%q) status = %d, want 303", tc.next, rec.Code)
		}
		if got := rec.Header().Get("Location"); got != tc.want {
			t.Errorf("Location for next=%q = %q, want %q", tc.next, got, tc.want)
		}
	}
}

// TestSafeNextPath 直接覆盖重定向目标的清洗规则（纯函数，边界比集成测试更全）。
func TestSafeNextPath(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{raw: "/settings", want: "/settings"},
		{raw: "/decks?page=2&lang=en", want: "/decks?page=2&lang=en"},
		{raw: "  /stats  ", want: "/stats"},
		{raw: "", want: "/"},
		{raw: "settings", want: "/"},
		{raw: "//example.com", want: "/"},
		{raw: "https://example.com", want: "/"},
		{raw: "/\\example.com", want: "/"},
		{raw: "\\/example.com", want: "/"},
	}
	for _, tc := range tests {
		if got := safeNextPath(tc.raw); got != tc.want {
			t.Errorf("safeNextPath(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// TestLangNextURL 覆盖回跳地址的构造：保留路径与其余查询参数，但去掉 lang，
// 否则旧的 ?lang 会继续覆盖刚写入的用户设置（这正是本缺陷的成因）。
func TestLangNextURL(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{raw: "/", want: "/"},
		{raw: "/settings", want: "/settings"},
		{raw: "/settings?lang=en", want: "/settings"},
		{raw: "/decks?page=2&lang=en", want: "/decks?page=2"},
		{raw: "/decks?lang=en&q=abc&page=2", want: "/decks?page=2&q=abc"},
	}
	for _, tc := range tests {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.raw, err)
		}
		if got := langNextURL(u); got != tc.want {
			t.Errorf("langNextURL(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// TestHeaderLocaleSwitchHtmxRefreshesSwitcher 覆盖 htmx 路径：
// 响应是片段（不是整页），用新语言渲染，并且只在提交带 oob 标记时带上设置页语言控件的带外交换。
func TestHeaderLocaleSwitchHtmxRefreshesSwitcher(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)

	rec := postFormHeader(t, srv, "/settings/locale", url.Values{
		"csrf_token": {csrf},
		"lang":       {"en"},
		"next":       {"/settings"},
		"oob":        {views.SettingsLocaleControlID},
	}, cookies, map[string]string{"HX-Request": "true"})
	if rec.Code != http.StatusOK {
		t.Fatalf("htmx POST /settings/locale status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("htmx response is a whole page, want a fragment: %s", snippet(body))
	}
	if !strings.Contains(body, `id="lang-switcher"`) {
		t.Errorf("fragment does not carry the header switcher: %s", snippet(body))
	}
	if !strings.Contains(body, `hx-swap-oob="outerHTML"`) || !strings.Contains(body, `id="`+views.SettingsLocaleControlID+`"`) {
		t.Errorf("fragment does not carry the out-of-band locale control: %s", snippet(body))
	}
	// 片段必须用新语言渲染：设置页语言标签在新语言下是英文。
	if !strings.Contains(body, "Interface language") {
		t.Errorf("fragment was not rendered in the new language: %s", snippet(body))
	}
	if got := userLocale(t, db, ownerID); got != "en" {
		t.Errorf("users.locale after the htmx switch = %q, want %q", got, "en")
	}

	// 不带 oob 标记（例如在复习页切换）时不应出现带外片段，否则 htmx 找不到目标会报错。
	recNoOOB := postFormHeader(t, srv, "/settings/locale", url.Values{
		"csrf_token": {csrf},
		"lang":       {"zh-CN"},
		"next":       {"/review"},
	}, cookies, map[string]string{"HX-Request": "true"})
	if recNoOOB.Code != http.StatusOK {
		t.Fatalf("htmx POST without oob status = %d, want 200", recNoOOB.Code)
	}
	if strings.Contains(recNoOOB.Body.String(), "hx-swap-oob") {
		t.Errorf("fragment without the oob marker still carries an out-of-band swap: %s", snippet(recNoOOB.Body.String()))
	}
}

// TestSettingsPageMarksLocaleControlForOOB 断言设置页的页头表单带 oob 标记、其它页面不带：
// 标记决定 htmx 响应是否为设置页的语言控件生成带外交换。
func TestSettingsPageMarksLocaleControlForOOB(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	// GET /decks 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 回退列表页（DESIGN.md §8.5）。
	srv.spa = nil

	settings := getWithCookies(t, srv, "/settings", cookies)
	if settings.Code != http.StatusOK {
		t.Fatalf("GET /settings status = %d, want 200", settings.Code)
	}
	if !strings.Contains(settings.Body.String(), `name="oob" value="`+views.SettingsLocaleControlID+`"`) {
		t.Errorf("settings page does not mark the locale control for out-of-band refresh: %s", snippet(settings.Body.String()))
	}
	if !strings.Contains(settings.Body.String(), `id="`+views.SettingsLocaleControlID+`"`) {
		t.Errorf("settings page does not render the locale control anchor: %s", snippet(settings.Body.String()))
	}

	decks := getWithCookies(t, srv, "/decks", cookies)
	if decks.Code != http.StatusOK {
		t.Fatalf("GET /decks status = %d, want 200", decks.Code)
	}
	if strings.Contains(decks.Body.String(), `name="oob"`) {
		t.Errorf("a page without the locale control still carries the oob marker: %s", snippet(decks.Body.String()))
	}
}
