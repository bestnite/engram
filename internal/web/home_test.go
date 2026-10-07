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

// TestStaticRouteServesEmbeddedAsset 断言 /static/v/<hash>/... 路由已接入并返回内容。
func TestStaticRouteServesEmbeddedAsset(t *testing.T) {
	srv := newRenderServer(t, nil)
	url := srv.assets.URL("js/pwa.js")
	if url == "" {
		t.Fatal("pwa asset is not embedded")
	}
	rec := get(t, srv, url, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", url, rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("served static asset is empty")
	}
}

// TestHomeFirstRunRedirect 断言首启窗口内 GET / 303 到 /setup，窗口由「是否存在活跃管理员」界定（M1-25 验收点）。
// 管理员存在时 GET / 交给 SPA 应用壳（由客户端路由渲染首页）。
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
				if strings.Contains(rec.Body.String(), `<div id="app">`) {
					t.Error("first-run redirect returned the SPA shell; /setup must win")
				}
				return
			}
			if body := rec.Body.String(); !strings.Contains(body, `<div id="app">`) {
				t.Errorf("home after setup is not the SPA shell: %s", snippet(body))
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
