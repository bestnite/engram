package web

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

// TestSPAServesAssetsWithImmutableCache 断言已嵌入的 Vite 构建资源（/assets/*）
// 能被正确提供，并带有长效不可变缓存、ETag 与正确的 MIME 类型（DESIGN.md §8.5）。
func TestSPAServesAssetsWithImmutableCache(t *testing.T) {
	srv := newRenderServer(t, nil)
	if srv.spa == nil {
		t.Fatal("srv.spa is nil")
	}

	// 至少包含 index-*.css 与 index-*.js
	var jsPath, cssPath string
	for p := range srv.spa.assets {
		if strings.HasSuffix(p, ".js") && strings.HasPrefix(p, "assets/") {
			jsPath = "/" + p
		}
		if strings.HasSuffix(p, ".css") && strings.HasPrefix(p, "assets/") {
			cssPath = "/" + p
		}
	}
	if jsPath == "" || cssPath == "" {
		t.Fatalf("expected both js and css assets in spa, got js=%q css=%q", jsPath, cssPath)
	}

	for _, target := range []struct {
		url     string
		wantExt string
		wantCT  string
	}{
		{url: jsPath, wantExt: ".js", wantCT: "text/javascript"},
		{url: cssPath, wantExt: ".css", wantCT: "text/css"},
	} {
		rec := get(t, srv, target.url, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", target.url, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, target.wantCT) {
			t.Errorf("GET %s Content-Type = %q, want prefix %q", target.url, ct, target.wantCT)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
			t.Errorf("GET %s Cache-Control = %q, want public, max-age=31536000, immutable", target.url, cc)
		}
		etag := rec.Header().Get("ETag")
		if etag == "" {
			t.Errorf("GET %s ETag is missing", target.url)
		}

		// 304 条件请求
		condRec := get(t, srv, target.url, map[string]string{"If-None-Match": etag})
		if condRec.Code != http.StatusNotModified {
			t.Errorf("conditional GET %s status = %d, want 304", target.url, condRec.Code)
		}

		// 安全响应头断言
		if nosniff := rec.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
			t.Errorf("GET %s X-Content-Type-Options = %q, want nosniff", target.url, nosniff)
		}
		if csp := rec.Header().Get("Content-Security-Policy"); csp == "" {
			t.Errorf("GET %s Content-Security-Policy is missing", target.url)
		}
	}
}

// TestSPAMissingAssetReturns404 断言请求不存在的 /assets/* 静态资源时明确返回 404，
// 严禁错误回退到 SPA index.html 应用壳（DESIGN.md §8.5）。
func TestSPAMissingAssetReturns404(t *testing.T) {
	srv := newRenderServer(t, nil)

	rec := get(t, srv, "/assets/not-found-bundle.js", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /assets/not-found-bundle.js status = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<div id=\"app\">") {
		t.Errorf("missing SPA asset unexpectedly served index.html")
	}
}

// TestSPAFallbackDeepLinks 断言 SPA 客户端深链接访问时回退 index.html（DESIGN.md §8.5）。
func TestSPAFallbackDeepLinks(t *testing.T) {
	srv := newRenderServer(t, nil)

	deepLinks := []string{
		"/decks/42/details",
		"/stats/history",
		"/settings/profile",
	}
	for _, dl := range deepLinks {
		rec := get(t, srv, dl, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", dl, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s Content-Type = %q, want text/html", dl, ct)
		}
		if !strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
			t.Errorf("GET %s missing SPA app container", dl)
		}
	}
}

// TestNewSPAErrorsOnEmptyOrMissingIndex 断言当缺少 index.html 或文件为空时构造失败。
func TestNewSPAErrorsOnEmptyOrMissingIndex(t *testing.T) {
	missingFS := fstest.MapFS{
		"assets/bundle.js": &fstest.MapFile{Data: []byte("console.log('hi');")},
	}
	if _, err := NewSPA(missingFS); err == nil {
		t.Errorf("NewSPA(missingFS) error = nil, want error")
	}

	emptyFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("")},
	}
	if _, err := NewSPA(emptyFS); err == nil {
		t.Errorf("NewSPA(emptyFS) error = nil, want error")
	}
}
