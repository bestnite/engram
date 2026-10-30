package web

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

// TestServesAssetsWithImmutableCache asserts the embedded Vite build assets (/assets/*)
// are served correctly, with long-lived immutable caching, an ETag and the correct MIME type.
func TestServesAssetsWithImmutableCache(t *testing.T) {
	srv := newRenderServer(t, nil)
	if srv.shell == nil {
		t.Fatal("srv.shell is nil")
	}

	// includes at least index-*.css and index-*.js
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

		// 304 conditional request
		condRec := get(t, srv, target.url, map[string]string{"If-None-Match": etag})
		if condRec.Code != http.StatusNotModified {
			t.Errorf("conditional GET %s status = %d, want 304", target.url, condRec.Code)
		}

		// security header assertions
		if nosniff := rec.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
			t.Errorf("GET %s X-Content-Type-Options = %q, want nosniff", target.url, nosniff)
		}
		if csp := rec.Header().Get("Content-Security-Policy"); csp == "" {
			t.Errorf("GET %s Content-Security-Policy is missing", target.url)
		}
		// The sandboxed card frame has an opaque origin, so its module scripts and stylesheets are cross-origin
		// requests; without this header the frame runtime never boots.
		if acao := rec.Header().Get("Access-Control-Allow-Origin"); acao != "*" {
			t.Errorf("GET %s Access-Control-Allow-Origin = %q, want *", target.url, acao)
		}
	}
}

// TestMissingAssetReturns404 asserts that requesting a nonexistent /assets/* static asset returns 404 explicitly,
// and falling back to the SPA index.html app shell is strictly forbidden.
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

// TestFallbackDeepLinks asserts deep links are answered by falling back to index.html.
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

// TestNewErrorsOnEmptyOrMissingIndex asserts construction fails when index.html is missing or the file is empty.
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

// TestIndexInjectsSelfHostedMathJaxURL asserts the SPA entry injects the content-hashed URL of the self-hosted MathJax
// into <head> as <meta name="engram-mathjax">: the frontend loads MathJax from it with a same-origin external script,
// needing no inline script and therefore no CSP widening.
func TestIndexInjectsSelfHostedMathJaxURL(t *testing.T) {
	srv := newRenderServer(t, nil)
	want := srv.assets.URL("js/mathjax/tex-chtml-full.js")
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
	// Self-hosted: no external URL may appear in the entry (no CDN).
	if strings.Contains(body, "http://") || strings.Contains(body, "https://") {
		t.Errorf("SPA index references an external URL; MathJax must stay self-hosted")
	}

	// The entry content has changed, so the ETag must be recomputed from the injected content, otherwise the browser caches a stale reference.
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("SPA index ETag is missing")
	}
	cond := get(t, srv, "/shell/review", map[string]string{"If-None-Match": etag})
	if cond.Code != http.StatusNotModified {
		t.Errorf("conditional GET /shell/review status = %d, want 304", cond.Code)
	}
}

// TestIndexMathJaxMetaFollowsSetURL asserts the injection is driven by SetMathJaxURL: no meta by default,
// it appears before </head> once a URL is passed, and an empty string removes it again (when the resource is missing, MathJax is not referenced, just like SSR).
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

	url := "/static/v/abcd1234/js/mathjax/tex-chtml-full.js"
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

// TestIndexVersionMetaFollowsSetVersion asserts the version injection is driven by SetVersion: no meta by default,
// it appears before </head> once a version is passed, the value is always escaped, and an empty string removes it (a build without an injected version shows no version).
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

	// A version string may contain quotes in the future, so the attribute value must be escaped, otherwise it breaks the document structure.
	shell.SetVersion(`v1" onload="x`)
	if !strings.Contains(string(shell.IndexHTML()), `content="v1&#34; onload=&#34;x"`) {
		t.Errorf("version meta value is not escaped: %s", string(shell.IndexHTML()))
	}

	shell.SetVersion("")
	if strings.Contains(string(shell.IndexHTML()), versionMetaName) {
		t.Errorf("SetVersion(\"\") should remove the version meta")
	}
}

// TestVersionMetaReachesShell asserts web.Deps.Version really reaches the app shell's <head> through New:
// that is the only source for the version number in the frontend footer, so a missed wire leaves the footer silently showing no version.
func TestVersionMetaReachesShell(t *testing.T) {
	srv := newRenderServer(t, nil, func(d *Deps) { d.Version = "v9.9.9" })
	body := fetchShellBody(t, srv, "/", nil)
	want := `<meta name="engram-version" content="v9.9.9" />`
	if !strings.Contains(body, want) {
		t.Errorf("SPA shell is missing %q: %s", want, snippet(body))
	}

	// When Deps.Version is not set (as in test wiring), that meta must not appear.
	plain := newRenderServer(t, nil)
	if strings.Contains(fetchShellBody(t, plain, "/", nil), `name="`+versionMetaName+`"`) {
		t.Error("SPA shell carries a version meta although Deps.Version is empty")
	}
}
