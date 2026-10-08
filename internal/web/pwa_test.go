package web

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// swAssetList 从生成的 service worker 脚本里解析 STATIC_ASSETS 数组。
func swAssetList(t *testing.T, body string) []string {
	t.Helper()
	re := regexp.MustCompile(`(?s)var STATIC_ASSETS = \[(.*?)\];`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("sw.js has no STATIC_ASSETS array:\n%s", body)
	}
	entry := regexp.MustCompile(`"([^"]*)"`)
	var out []string
	for _, e := range entry.FindAllStringSubmatch(m[1], -1) {
		out = append(out, e[1])
	}
	if len(out) == 0 {
		t.Fatalf("STATIC_ASSETS array is empty:\n%s", body)
	}
	return out
}

// TestServiceWorkerCachesOnlyStaticAssets 是核心验收：缓存清单里只有静态资源
// 路径，没有 API 端点、没有复习/答题路由。
func TestServiceWorkerCachesOnlyStaticAssets(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, serviceWorkerPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", serviceWorkerPath, rec.Code)
	}
	body := rec.Body.String()

	for _, asset := range swAssetList(t, body) {
		if asset != manifestPath && !strings.HasPrefix(asset, staticPathPrefix) && !strings.HasPrefix(asset, "/assets/") {
			t.Errorf("cache list entry %q is not a static asset path", asset)
		}
		if strings.HasPrefix(asset, "/api/") {
			t.Errorf("cache list must not contain API endpoints: %q", asset)
		}
		if strings.HasPrefix(asset, "/review") {
			t.Errorf("cache list must not contain review routes: %q", asset)
		}
	}

	if !strings.Contains(body, "\""+manifestPath+"\"") {
		t.Errorf("cache list should include the manifest, got:\n%s", body)
	}
	if !strings.Contains(body, "var SW_VERSION = \"") {
		t.Errorf("service worker is missing a version marker")
	}
	if !strings.Contains(body, "caches.delete(") {
		t.Errorf("service worker is missing old-cache cleanup")
	}
	if !strings.Contains(body, `if (req.method !== "GET")`) {
		t.Errorf("service worker must ignore non-GET requests (no caching of POSTs)")
	}
	for _, path := range []string{"/", "/review", "/shell/review", "/api/v1/reviews"} {
		if strings.Contains(strings.Join(swAssetList(t, body), "\n"), `"`+path+`"`) {
			t.Errorf("cache list must not contain document or data route %q", path)
		}
	}
	if !strings.Contains(body, `path.indexOf("/assets/") === 0`) {
		t.Errorf("fetch strategy must allow Vite assets under /assets/")
	}
	if !strings.Contains(body, `path.endsWith(".css")`) || !strings.Contains(body, `path.endsWith(".js")`) {
		t.Errorf("fetch strategy must restrict Vite caching to JS and CSS")
	}
	if strings.Contains(body, `url.pathname.indexOf("/review")`) || strings.Contains(body, `url.pathname.indexOf("/api/")`) {
		t.Errorf("fetch strategy must not allow review or API routes")
	}
}

func TestServiceWorkerIncludesEmbeddedViteAssets(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	shell, err := NewShell(fstest.MapFS{
		"index.html":                 &fstest.MapFile{Data: []byte("<html>shell</html>")},
		"assets/index-abc123.js":     &fstest.MapFile{Data: []byte("console.log('app')")},
		"assets/index-def456.css":    &fstest.MapFile{Data: []byte("body { color: red }")},
		"assets/data-ghi789.json":    &fstest.MapFile{Data: []byte(`{"not":"static shell code"}`)},
		"assets/nested/chunk-jkl.js": &fstest.MapFile{Data: []byte("export {}")},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.shell = shell

	body := getWithCookies(t, srv, serviceWorkerPath, nil).Body.String()
	assets := swAssetList(t, body)
	for _, want := range []string{"/assets/index-abc123.js", "/assets/index-def456.css", "/assets/nested/chunk-jkl.js"} {
		if !containsString(assets, want) {
			t.Errorf("STATIC_ASSETS does not include embedded Vite asset %q: %v", want, assets)
		}
	}
	for _, forbidden := range []string{"/assets/data-ghi789.json", "/index.html", "/review", "/api/v1/reviews"} {
		if containsString(assets, forbidden) {
			t.Errorf("STATIC_ASSETS contains non-shell path %q", forbidden)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestServiceWorkerVersionTracksAssetContent 断言版本号随缓存内容变化，保证旧缓存被清理。
func TestServiceWorkerVersionTracksAssetContent(t *testing.T) {
	if shellVersion([]string{"/static/v/aaaa/js/a.js"}) == shellVersion([]string{"/static/v/bbbb/js/a.js"}) {
		t.Errorf("shellVersion did not change when the cache list changed")
	}
	if shellVersion([]string{"/a", "/b"}) != shellVersion([]string{"/a", "/b"}) {
		t.Errorf("shellVersion is not deterministic")
	}
}

// TestManifestIsStandaloneAndUsesHashedIcon 断言 manifest 可被取到、为 standalone，
// 且图标指向内容哈希资源。
func TestManifestIsStandaloneAndUsesHashedIcon(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, manifestPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", manifestPath, rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/manifest+json") {
		t.Errorf("Content-Type = %q, want application/manifest+json", ct)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("manifest is not valid JSON: %v\n%s", err, rec.Body.String())
	}
	if body["display"] != "standalone" {
		t.Errorf(`manifest display = %v, want "standalone"`, body["display"])
	}
	if body["start_url"] != "/" {
		t.Errorf(`manifest start_url = %v, want "/"`, body["start_url"])
	}
	if name, _ := body["name"].(string); strings.TrimSpace(name) == "" {
		t.Errorf("manifest name is empty")
	}
	icons, ok := body["icons"].([]any)
	if !ok || len(icons) == 0 {
		t.Fatalf("manifest has no icons: %s", rec.Body.String())
	}
	icon, _ := icons[0].(map[string]any)
	src, _ := icon["src"].(string)
	if !strings.HasPrefix(src, staticPathPrefix) {
		t.Errorf("icon src %q is not a content-hashed static path", src)
	}
}

// TestManifestNamePrefersSettings 断言应用名优先取 settings 表的 site.name，缺省回退站点名 key。
func TestManifestNamePrefersSettings(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)

	fallback := manifestName(t, srv)
	if strings.TrimSpace(fallback) == "" {
		t.Fatalf("default manifest name is empty")
	}

	row := store.Setting{Key: "site.name", Value: `"Custom Engram"`, UpdatedAt: time.Now()}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed setting: %v", err)
	}
	if got := manifestName(t, srv); got != "Custom Engram" {
		t.Errorf("manifest name = %q, want settings value %q", got, "Custom Engram")
	}
}

func manifestName(t *testing.T, srv *Server) string {
	t.Helper()
	rec := getWithCookies(t, srv, manifestPath, nil)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	name, _ := body["name"].(string)
	return name
}

// TestPWAScriptIsServable 断言稳定 URL 的注册脚本可取到，并注册 /sw.js。
func TestPWAScriptIsServable(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, pwaScriptPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", pwaScriptPath, rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `register("/sw.js")`) {
		t.Errorf("%s does not register /sw.js:\n%s", pwaScriptPath, body)
	}
}

// TestManifestAndServiceWorkerAreReachableWithoutSession 断言 PWA 外壳是公开的：
// 首页（未登录）也要能取到 manifest，否则无法“添加到主屏幕”。
func TestManifestAndServiceWorkerAreReachableWithoutSession(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	for _, path := range []string{manifestPath, serviceWorkerPath, pwaScriptPath} {
		rec := getWithCookies(t, srv, path, nil) // 不带任何 cookie
		if rec.Code != http.StatusOK {
			t.Errorf("anonymous GET %s status = %d, want 200", path, rec.Code)
		}
	}
}
