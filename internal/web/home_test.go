package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/store"
)

// newRenderServer 构造一个可渲染的测试服务；userLocale 模拟 M1 的用户语言设置。
func newRenderServer(t *testing.T, userLocale func(c *gin.Context) string) *Server {
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
		Logger:        discardLogger(),
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		UserLocale:    userLocale,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return srv
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

// snippet 截断页面用于失败信息，避免把整页塞进测试日志。
func snippet(body string) string {
	const limit = 400
	if len(body) <= limit {
		return body
	}
	return body[:limit] + "..."
}
