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
		"/spa/review",
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

// TestSPAIndexInjectsSelfHostedMathJaxURL 断言 SPA 入口把自托管 MathJax 的内容哈希 URL
// 以 <meta name="engram-mathjax"> 注入 <head>：前端据此用同源外链脚本加载 MathJax，
// 无需内联脚本，也就无需放宽 CSP（DESIGN.md §6.1、§8.5、§11）。
func TestSPAIndexInjectsSelfHostedMathJaxURL(t *testing.T) {
	srv := newRenderServer(t, nil)
	want := srv.assets.URL("js/mathjax/tex-svg.js")
	if want == "" {
		t.Fatal("mathjax asset is not embedded")
	}
	if !strings.HasPrefix(want, staticPathPrefix) {
		t.Fatalf("mathjax URL = %q, want content-hashed %q prefix", want, staticPathPrefix)
	}

	rec := get(t, srv, "/spa/review", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /spa/review status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	meta := `<meta name="engram-mathjax" content="` + want + `" />`
	if !strings.Contains(body, meta) {
		t.Errorf("SPA index is missing injected MathJax meta %q", meta)
	}
	// 自托管：入口不得出现任何外部 URL（无 CDN）。
	if strings.Contains(body, "http://") || strings.Contains(body, "https://") {
		t.Errorf("SPA index references an external URL; MathJax must stay self-hosted")
	}

	// 入口内容已变，ETag 必须随注入后的内容重算，否则旧引用会被浏览器缓存（DESIGN.md §8.5）。
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("SPA index ETag is missing")
	}
	cond := get(t, srv, "/spa/review", map[string]string{"If-None-Match": etag})
	if cond.Code != http.StatusNotModified {
		t.Errorf("conditional GET /spa/review status = %d, want 304", cond.Code)
	}
}

// TestSPAIndexMathJaxMetaFollowsSetURL 断言注入是 SetMathJaxURL 驱动的：默认不带 meta，
// 传入 URL 后出现在 </head> 之前，再传空串则移除（资源缺失时与 SSR 一样不引用 MathJax）。
func TestSPAIndexMathJaxMetaFollowsSetURL(t *testing.T) {
	spa, err := NewSPA(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><head></head><body><div id=\"app\"></div></body></html>")},
	})
	if err != nil {
		t.Fatalf("NewSPA() error = %v", err)
	}
	if strings.Contains(string(spa.IndexHTML()), "engram-mathjax") {
		t.Errorf("fresh SPA index should not carry a MathJax meta")
	}

	url := "/static/v/abcd1234/js/mathjax/tex-svg.js"
	spa.SetMathJaxURL(url)
	got := string(spa.IndexHTML())
	meta := `<meta name="engram-mathjax" content="` + url + `" />`
	if !strings.Contains(got, meta) {
		t.Errorf("SetMathJaxURL did not inject %q into the index", meta)
	}
	if strings.Index(got, meta) > strings.Index(got, "</head>") {
		t.Errorf("MathJax meta must be injected inside <head>, before </head>")
	}

	spa.SetMathJaxURL("")
	if strings.Contains(string(spa.IndexHTML()), "engram-mathjax") {
		t.Errorf("SetMathJaxURL(\"\") should remove the MathJax meta")
	}
}
