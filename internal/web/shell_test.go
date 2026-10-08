package web

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

// TestServesAssetsWithImmutableCache 断言已嵌入的 Vite 构建资源（/assets/*）
// 能被正确提供，并带有长效不可变缓存、ETag 与正确的 MIME 类型。
func TestServesAssetsWithImmutableCache(t *testing.T) {
	srv := newRenderServer(t, nil)
	if srv.shell == nil {
		t.Fatal("srv.shell is nil")
	}

	// 至少包含 index-*.css 与 index-*.js
	var jsPath, cssPath string
	for p := range srv.shell.assets {
		if strings.HasSuffix(p, ".js") && strings.HasPrefix(p, "assets/") {
			jsPath = "/" + p
		}
		if strings.HasSuffix(p, ".css") && strings.HasPrefix(p, "assets/") {
			cssPath = "/" + p
		}
	}
	if jsPath == "" || cssPath == "" {
		t.Fatalf("expected both js and css assets in shell, got js=%q css=%q", jsPath, cssPath)
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

// TestMissingAssetReturns404 断言请求不存在的 /assets/* 静态资源时明确返回 404，
// 严禁错误回退到 SPA index.html 应用壳。
func TestMissingAssetReturns404(t *testing.T) {
	srv := newRenderServer(t, nil)

	rec := get(t, srv, "/assets/not-found-bundle.js", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /assets/not-found-bundle.js status = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<div id=\"app\">") {
		t.Errorf("missing SPA asset unexpectedly served index.html")
	}
}

// TestFallbackDeepLinks 断言 SPA 客户端深链接访问时回退 index.html。
func TestFallbackDeepLinks(t *testing.T) {
	srv := newRenderServer(t, nil)

	deepLinks := []string{
		"/decks/42/details",
		"/shell/review",
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

// TestNewErrorsOnEmptyOrMissingIndex 断言当缺少 index.html 或文件为空时构造失败。
func TestNewErrorsOnEmptyOrMissingIndex(t *testing.T) {
	missingFS := fstest.MapFS{
		"assets/bundle.js": &fstest.MapFile{Data: []byte("console.log('hi');")},
	}
	if _, err := NewShell(missingFS); err == nil {
		t.Errorf("NewShell(missingFS) error = nil, want error")
	}

	emptyFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("")},
	}
	if _, err := NewShell(emptyFS); err == nil {
		t.Errorf("NewShell(emptyFS) error = nil, want error")
	}
}

// TestIndexInjectsSelfHostedMathJaxURL 断言 SPA 入口把自托管 MathJax 的内容哈希 URL
// 以 <meta name="engram-mathjax"> 注入 <head>：前端据此用同源外链脚本加载 MathJax，
// 无需内联脚本，也就无需放宽 CSP。
func TestIndexInjectsSelfHostedMathJaxURL(t *testing.T) {
	srv := newRenderServer(t, nil)
	want := srv.assets.URL("js/mathjax/tex-svg.js")
	if want == "" {
		t.Fatal("mathjax asset is not embedded")
	}
	if !strings.HasPrefix(want, staticPathPrefix) {
		t.Fatalf("mathjax URL = %q, want content-hashed %q prefix", want, staticPathPrefix)
	}

	rec := get(t, srv, "/shell/review", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /shell/review status = %d, want 200", rec.Code)
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

	// 入口内容已变，ETag 必须随注入后的内容重算，否则旧引用会被浏览器缓存。
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("SPA index ETag is missing")
	}
	cond := get(t, srv, "/shell/review", map[string]string{"If-None-Match": etag})
	if cond.Code != http.StatusNotModified {
		t.Errorf("conditional GET /shell/review status = %d, want 304", cond.Code)
	}
}

// TestIndexMathJaxMetaFollowsSetURL 断言注入是 SetMathJaxURL 驱动的：默认不带 meta，
// 传入 URL 后出现在 </head> 之前，再传空串则移除（资源缺失时与 SSR 一样不引用 MathJax）。
func TestIndexMathJaxMetaFollowsSetURL(t *testing.T) {
	shell, err := NewShell(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><head></head><body><div id=\"app\"></div></body></html>")},
	})
	if err != nil {
		t.Fatalf("NewShell() error = %v", err)
	}
	if strings.Contains(string(shell.IndexHTML()), "engram-mathjax") {
		t.Errorf("fresh SPA index should not carry a MathJax meta")
	}

	url := "/static/v/abcd1234/js/mathjax/tex-svg.js"
	shell.SetMathJaxURL(url)
	got := string(shell.IndexHTML())
	meta := `<meta name="engram-mathjax" content="` + url + `" />`
	if !strings.Contains(got, meta) {
		t.Errorf("SetMathJaxURL did not inject %q into the index", meta)
	}
	if strings.Index(got, meta) > strings.Index(got, "</head>") {
		t.Errorf("MathJax meta must be injected inside <head>, before </head>")
	}

	shell.SetMathJaxURL("")
	if strings.Contains(string(shell.IndexHTML()), "engram-mathjax") {
		t.Errorf("SetMathJaxURL(\"\") should remove the MathJax meta")
	}
}

// TestIndexVersionMetaFollowsSetVersion 断言版本注入由 SetVersion 驱动：默认不带 meta，
// 传入版本后出现在 </head> 之前，值一律转义，再传空串则移除（未注入版本的构建不显示版本）。
func TestIndexVersionMetaFollowsSetVersion(t *testing.T) {
	shell, err := NewShell(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<html><head></head><body><div id="app"></div></body></html>`)},
	})
	if err != nil {
		t.Fatalf("NewShell() error = %v", err)
	}
	if strings.Contains(string(shell.IndexHTML()), versionMetaName) {
		t.Errorf("fresh SPA index should not carry a version meta")
	}

	shell.SetVersion("v0.1.4")
	got := string(shell.IndexHTML())
	meta := `<meta name="engram-version" content="v0.1.4" />`
	if !strings.Contains(got, meta) {
		t.Errorf("SetVersion did not inject %q into the index", meta)
	}
	if strings.Index(got, meta) > strings.Index(got, "</head>") {
		t.Errorf("version meta must be injected inside <head>, before </head>")
	}

	// 版本串将来可能含引号，属性值必须转义，否则会破坏文档结构。
	shell.SetVersion(`v1" onload="x`)
	if !strings.Contains(string(shell.IndexHTML()), `content="v1&#34; onload=&#34;x"`) {
		t.Errorf("version meta value is not escaped: %s", string(shell.IndexHTML()))
	}

	shell.SetVersion("")
	if strings.Contains(string(shell.IndexHTML()), versionMetaName) {
		t.Errorf("SetVersion(\"\") should remove the version meta")
	}
}

// TestVersionMetaReachesShell 断言 web.Deps.Version 真的经 New 注入到应用壳的 <head>：
// 前端页脚的版本号只有这一条来源，装配漏传时页脚会静默地不显示版本。
func TestVersionMetaReachesShell(t *testing.T) {
	srv := newRenderServer(t, nil, func(d *Deps) { d.Version = "v9.9.9" })
	body := fetchShellBody(t, srv, "/", nil)
	want := `<meta name="engram-version" content="v9.9.9" />`
	if !strings.Contains(body, want) {
		t.Errorf("SPA shell is missing %q: %s", want, snippet(body))
	}

	// 未设置 Deps.Version（如测试装配）时不得出现该 meta。
	plain := newRenderServer(t, nil)
	if strings.Contains(fetchShellBody(t, plain, "/", nil), `name="`+versionMetaName+`"`) {
		t.Error("SPA shell carries a version meta although Deps.Version is empty")
	}
}
