package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// newRenderServer 构造一个可渲染的测试服务；userLocale 模拟 M1 的用户语言设置。
// M1-25 之后首页只在「引导已完成」时才渲染，所以这里预置一个活跃管理员。
func newRenderServer(t *testing.T, userLocale func(c *gin.Context) string) *Server {
	t.Helper()
	srv := newFreshServer(t, userLocale)
	seedActiveAdmin(t, srv)
	return srv
}

// newFreshServer 构造一个没有任何用户的测试服务，等价于「安装完但还没 setup」。
func newFreshServer(t *testing.T, userLocale func(c *gin.Context) string) *Server {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "render.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	srv, err := New("127.0.0.1:0", Deps{
		DB:            db,
		Users:         store.NewUserStore(db),
		Logger:        discardLogger(),
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		UserLocale:    userLocale,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return srv
}

// seedActiveAdmin 落一个活跃管理员，让实例越过首启窗口（M1-25）。
func seedActiveAdmin(t *testing.T, srv *Server) {
	t.Helper()
	admin := &store.User{
		Username: "admin", Email: "admin@example.com", DisplayName: "Admin",
		Role: store.RoleAdmin, Status: store.StatusActive, Locale: "zh-CN",
		Timezone: "UTC", CreatedAt: time.Now().UTC(),
	}
	if err := srv.users.Create(context.Background(), admin); err != nil {
		t.Fatalf("seed active admin: %v", err)
	}
}

func get(t *testing.T, srv *Server, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestHomeRendersHashedStaticAssets 断言页面源码引用的是哈希化路径（M0-9 验收点）。
func TestHomeRendersHashedStaticAssets(t *testing.T) {
	srv := newRenderServer(t, nil)
	assets := srv.assets

	rec := get(t, srv, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body := rec.Body.String()

	for _, logical := range []string{"js/htmx.min.js", "js/mathjax/tex-svg.js"} {
		url := assets.URL(logical)
		if url == "" {
			t.Fatalf("asset %q is not embedded", logical)
		}
		if !strings.Contains(body, url) {
			t.Errorf("page source does not reference the hashed URL for %q (%s)", logical, url)
		}
	}

	if !assets.Has("css/tailwind.css") {
		// tailwind.css 是 CI/构建期生成的（AGENTS.md §4）；未生成时跳过而不是误报失败。
		t.Skip("css/tailwind.css not generated; run the Tailwind CLI step from AGENTS.md §4")
	}
	cssURL := assets.URL("css/tailwind.css")
	if !strings.Contains(body, cssURL) {
		t.Errorf("page source does not reference the hashed CSS URL %s", cssURL)
	}
	// 示例页确实用了至少一个 utility 类，供 Tailwind 产物验证其在页面上的存在。
	if !strings.Contains(body, "bg-slate-50") {
		t.Error("sample page does not use the bg-slate-50 utility class")
	}
}

// TestStaticRouteServesEmbeddedAsset 断言 /static/v/<hash>/... 路由已接入并返回内容。
func TestStaticRouteServesEmbeddedAsset(t *testing.T) {
	srv := newRenderServer(t, nil)
	url := srv.assets.URL("js/htmx.min.js")
	if url == "" {
		t.Fatal("htmx asset is not embedded")
	}
	rec := get(t, srv, url, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", url, rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("served static asset is empty")
	}
}

// TestHomeLocalizesByAcceptLanguage 断言相同路径按 Accept-Language 返回不同语言。
func TestHomeLocalizesByAcceptLanguage(t *testing.T) {
	srv := newRenderServer(t, nil)

	zh := get(t, srv, "/", nil).Body.String()
	if !strings.Contains(zh, "今日复习") {
		t.Errorf("default response is not Chinese; body = %s", snippet(zh))
	}
	if !strings.Contains(zh, `lang="zh-CN"`) {
		t.Errorf("default response does not declare lang=\"zh-CN\"; body = %s", snippet(zh))
	}

	en := get(t, srv, "/", map[string]string{"Accept-Language": "en-US,en;q=0.9"}).Body.String()
	if !strings.Contains(en, "Today&#39;s review") && !strings.Contains(en, "Today's review") {
		t.Errorf("Accept-Language en did not switch the page; body = %s", snippet(en))
	}
	if strings.Contains(en, "今日复习") {
		t.Errorf("en response still contains the Chinese heading")
	}
}

// TestHomeExplicitLangBeatsHeader 断言 ?lang 是最高优先级的显式覆盖。
func TestHomeExplicitLangBeatsHeader(t *testing.T) {
	srv := newRenderServer(t, nil)
	body := get(t, srv, "/?lang=en", map[string]string{"Accept-Language": "zh-CN,zh;q=0.9"}).Body.String()
	if !strings.Contains(body, "Today") {
		t.Errorf("?lang=en did not override Accept-Language; body = %s", snippet(body))
	}
}

// TestUserLocaleSettingBeatsAcceptLanguage 断言用户设置优先于 Accept-Language（M0-8 验收点）。
func TestUserLocaleSettingBeatsAcceptLanguage(t *testing.T) {
	srv := newRenderServer(t, func(c *gin.Context) string { return "zh-CN" })
	body := get(t, srv, "/", map[string]string{"Accept-Language": "en-US,en;q=0.9"}).Body.String()
	if !strings.Contains(body, "今日复习") {
		t.Errorf("user locale setting did not beat Accept-Language; body = %s", snippet(body))
	}
}

// TestHomeFirstRunRedirect 断言首启窗口内 GET / 303 到 /setup，窗口由「是否存在活跃管理员」界定（M1-25 验收点）。
func TestHomeFirstRunRedirect(t *testing.T) {
	tests := []struct {
		name     string
		seed     *store.User
		wantCode int
		wantLoc  string
	}{
		{name: "no user at all", wantCode: http.StatusSeeOther, wantLoc: "/setup"},
		{
			name: "admin already active",
			seed: &store.User{
				Username: "admin", Email: "admin@example.com", DisplayName: "Admin",
				Role: store.RoleAdmin, Status: store.StatusActive, Locale: "zh-CN", Timezone: "UTC",
			},
			wantCode: http.StatusOK,
		},
		{
			// 被停用的管理员不算数：引导窗口不该因为一个停用账号而关闭（CountActiveAdmins 的 status 过滤）。
			name: "admin disabled",
			seed: &store.User{
				Username: "old-admin", Email: "old-admin@example.com", DisplayName: "Old Admin",
				Role: store.RoleAdmin, Status: store.StatusDisabled, Locale: "zh-CN", Timezone: "UTC",
			},
			wantCode: http.StatusSeeOther, wantLoc: "/setup",
		},
		{
			// 普通用户同样不关闭窗口：缺的是管理员，不是任何人。
			name: "ordinary user only",
			seed: &store.User{
				Username: "member", Email: "member@example.com", DisplayName: "Member",
				Role: store.RoleUser, Status: store.StatusActive, Locale: "zh-CN", Timezone: "UTC",
			},
			wantCode: http.StatusSeeOther, wantLoc: "/setup",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFreshServer(t, nil)
			if tc.seed != nil {
				tc.seed.CreatedAt = time.Now().UTC()
				if err := srv.users.Create(context.Background(), tc.seed); err != nil {
					t.Fatalf("create seed user: %v", err)
				}
			}
			rec := get(t, srv, "/", nil)
			if rec.Code != tc.wantCode {
				t.Fatalf("GET / status = %d, want %d", rec.Code, tc.wantCode)
			}
			if loc := rec.Header().Get("Location"); loc != tc.wantLoc {
				t.Errorf("Location = %q, want %q", loc, tc.wantLoc)
			}
			if tc.wantLoc != "" {
				// 首启窗口内首页不渲染自身内容，只回一个跳转（gin 会附一段链接文本）。
				if body := rec.Body.String(); strings.Contains(body, "今日复习") {
					t.Errorf("redirect body leaked home content: %s", snippet(body))
				}
				return
			}
			if body := rec.Body.String(); !strings.Contains(body, "今日复习") {
				t.Errorf("home page after setup is missing the heading; body = %s", snippet(body))
			}
		})
	}
}

// TestHomeFirstRunRedirectDropsLang 断言重定向不带 ?lang：语言由引导页按 Accept-Language 与 cookie 自行解析。
func TestHomeFirstRunRedirectDropsLang(t *testing.T) {
	srv := newFreshServer(t, nil)
	rec := get(t, srv, "/?lang=en", map[string]string{"Accept-Language": "zh-CN,zh;q=0.9"})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /?lang=en status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/setup" {
		t.Errorf("Location = %q, want /setup (the query must not be carried over)", loc)
	}
}

// snippet 截断页面用于失败信息，避免把整页塞进测试日志。
func snippet(body string) string {
	const limit = 400
	if len(body) <= limit {
		return body
	}
	return body[:limit] + "..."
}
