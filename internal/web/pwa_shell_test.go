package web

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

// fetchShellBody fetches an SPA app shell response body (expects 200).
func fetchShellBody(t *testing.T, srv *Server, path string, headers map[string]string) string {
	t.Helper()
	rec := get(t, srv, path, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200; body %s", path, rec.Code, snippet(rec.Body.String()))
	}
	return rec.Body.String()
}

// TestShellDeclaresPWAHead asserts the SPA app shell's <head> matches the SSR shell: manifest,
// theme-color, content-hashed icons, the /pwa.js registration script, and the inline theme bootstrap.
func TestShellDeclaresPWAHead(t *testing.T) {
	srv := newRenderServer(t, nil)
	icon := srv.assets.URL("icons/icon.svg")
	apple := srv.assets.URL("icons/apple-touch-icon.png")
	if icon == "" || apple == "" {
		t.Fatalf("icon assets are not embedded: icon=%q apple=%q", icon, apple)
	}
	if !strings.HasPrefix(icon, staticPathPrefix) || !strings.HasPrefix(apple, staticPathPrefix) {
		t.Fatalf("icons must be content-hashed: icon=%q apple=%q", icon, apple)
	}

	body := fetchShellBody(t, srv, "/", nil)
	for _, want := range []string{
		`<link rel="manifest" href="` + manifestPath + `"/>`,
		`<meta name="theme-color" content="#18181b"/>`,
		`<link rel="icon" type="image/svg+xml" href="` + icon + `"/>`,
		`<link rel="apple-touch-icon" href="` + apple + `"/>`,
		`<script src="` + pwaScriptPath + `"></script>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SPA shell is missing %q: %s", want, snippet(body))
		}
	}

	// The theme bootstrap must be inline inside <head>, before </head>: an external script is a separate request, so dark mode would first paint a frame of white.
	head := strings.Index(body, "<head>")
	headEnd := strings.Index(body, "</head>")
	script := strings.Index(body, `localStorage.getItem("engram-theme")`)
	if head < 0 || headEnd < 0 || script < 0 || !(head < script && script < headEnd) {
		t.Errorf("inline theme bootstrap must sit in <head>: head=%d script=%d headEnd=%d", head, script, headEnd)
	}

	// Self-hosted: the entry point must not reference any external URL.
	if strings.Contains(body, "http://") || strings.Contains(body, "https://") {
		t.Errorf("SPA shell references an external URL: %s", snippet(body))
	}
}

// TestShellThemeBootstrapMatchesSSR asserts the inline bootstrap injected into the SPA shell uses the same
// compile-time constant as SSR: the content is identical, so the CSP script-src hash allowlist needs no widening.
func TestShellThemeBootstrapMatchesSSR(t *testing.T) {
	srv := newRenderServer(t, nil)
	body := fetchShellBody(t, srv, "/", nil)

	start := strings.Index(body, "<script>(function(){var s;try{s=localStorage.getItem(\"engram-theme\")")
	if start < 0 {
		t.Fatal("SPA shell has no inline theme bootstrap <script>")
	}
	start += len("<script>")
	end := strings.Index(body[start:], "</script>")
	if end < 0 {
		t.Fatal("inline theme bootstrap has no closing </script>")
	}
	if inline := body[start : start+end]; inline != themeBootstrapJS {
		t.Errorf("SPA inline bootstrap differs from the SSR constant:\n got %q\nwant %q", inline, themeBootstrapJS)
	}
}

// TestShellThemeBootstrapHashWhitelisted takes the inline bootstrap body out of the rendered SPA shell, computes
// SHA-256 on its own, and asserts the CSP carries exactly that. When the hash does not match, the browser blocks this
// sole inline <script> as an unauthorized script and the first-paint theme bootstrap stops working (same criterion as SSR's TestSecurityHeadersCSPHashMatchesRenderedThemeBootstrap).
func TestShellThemeBootstrapHashWhitelisted(t *testing.T) {
	srv := newRenderServer(t, nil)
	rec := get(t, srv, "/", nil)
	csp := rec.Header().Get(wantCSPHeader)
	if csp == "" {
		t.Fatalf("%s is missing on the SPA shell", wantCSPHeader)
	}
	body := rec.Body.String()

	start := strings.Index(body, "<script>(function(){var s;try{s=localStorage.getItem(\"engram-theme\")")
	if start < 0 {
		t.Fatal("SPA shell has no inline theme bootstrap <script>")
	}
	start += len("<script>")
	end := strings.Index(body[start:], "</script>")
	if end < 0 {
		t.Fatal("inline theme bootstrap has no closing </script>")
	}
	inline := body[start : start+end]

	sum := sha256.Sum256([]byte(inline))
	want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	if !strings.Contains(csp, want) {
		t.Errorf("CSP %q does not carry %q, the hash of the SPA inline theme bootstrap", csp, want)
	}
}

// TestShellLanguageFollowsI18nRules asserts the SPA shell's <html lang> resolves by the i18n precedence
// (?lang > user setting > Accept-Language > site default), consistent with SSR.
func TestShellLanguageFollowsI18nRules(t *testing.T) {
	srv := newRenderServer(t, nil)

	tests := []struct {
		name    string
		path    string
		headers map[string]string
		want    string
	}{
		{name: "site default", path: "/", want: `lang="zh-CN"`},
		{name: "lang query override", path: "/?lang=en", want: `lang="en"`},
		{name: "accept-language fallback", path: "/", headers: map[string]string{"Accept-Language": "en-US,en;q=0.9"}, want: `lang="en"`},
		{name: "unsupported lang falls back", path: "/?lang=de", want: `lang="zh-CN"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := fetchShellBody(t, srv, tc.path, tc.headers)
			if !strings.Contains(body, `<html `+tc.want+`>`) {
				t.Errorf("GET %s shell does not declare %s: %s", tc.path, tc.want, snippet(body))
			}
		})
	}
}

// TestShellETagTracksLanguage asserts the entry ETag varies with the language: different languages must get different ETags,
// otherwise a conditional request after switching language hits the old shell (no-cache semantics).
func TestShellETagTracksLanguage(t *testing.T) {
	srv := newRenderServer(t, nil)

	zh := get(t, srv, "/", nil)
	en := get(t, srv, "/?lang=en", nil)
	if zh.Header().Get("ETag") == "" || en.Header().Get("ETag") == "" {
		t.Fatal("SPA shell ETag is missing")
	}
	if zh.Header().Get("ETag") == en.Header().Get("ETag") {
		t.Errorf("SPA shell ETag does not change with language: %q", zh.Header().Get("ETag"))
	}

	// A conditional request in the same language hits 304.
	cond := get(t, srv, "/", map[string]string{"If-None-Match": zh.Header().Get("ETag")})
	if cond.Code != http.StatusNotModified {
		t.Errorf("conditional GET / status = %d, want 304", cond.Code)
	}
	// A conditional request in a different language must not hit 304, otherwise it gets the old shell in the other language.
	cross := get(t, srv, "/?lang=en", map[string]string{"If-None-Match": zh.Header().Get("ETag")})
	if cross.Code != http.StatusOK {
		t.Errorf("cross-language conditional GET status = %d, want 200", cross.Code)
	}
}

// TestSetHTMLLangRewritesOpenTag covers the edges of language rewriting: replacing an existing lang, adding a missing attribute,
// keeping other attributes, returning as-is when there is no <html>, and escaping the language code.
func TestSetHTMLLangRewritesOpenTag(t *testing.T) {
	tests := []struct {
		name string
		in   string
		lang string
		want string
	}{
		{name: "replace existing", in: `<html lang="zh-CN"><body></body></html>`, lang: "en", want: `<html lang="en"><body></body></html>`},
		{name: "insert missing", in: `<html><body></body></html>`, lang: "en", want: `<html lang="en"><body></body></html>`},
		{name: "preserve other attrs", in: `<html class="x" lang="zh-CN">`, lang: "en", want: `<html class="x" lang="en">`},
		{name: "no html tag", in: `<div>no root</div>`, lang: "en", want: `<div>no root</div>`},
		{name: "escapes lang", in: `<html lang="zh-CN">`, lang: `en"onload`, want: `<html lang="en&#34;onload">`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(setHTMLLang([]byte(tc.in), tc.lang)); got != tc.want {
				t.Errorf("setHTMLLang(%q, %q) = %q, want %q", tc.in, tc.lang, got, tc.want)
			}
		})
	}
}

// TestBuildShellBlockSkipsEmptyAndEscapes asserts empty fields produce no markup and attribute values are always escaped.
func TestBuildShellBlockSkipsEmptyAndEscapes(t *testing.T) {
	if block := buildShellBlock(ShellHTML{}); len(block) != 0 {
		t.Errorf("empty ShellHTML should inject nothing, got %q", block)
	}
	block := string(buildShellBlock(ShellHTML{ManifestURL: `/m"x`}))
	if !strings.Contains(block, `href="/m&#34;x"`) {
		t.Errorf("shell attribute value is not escaped: %q", block)
	}
}

// TestShellComposesWithMathJax asserts shell injection, MathJax injection and version injection do not overwrite each other: all three appear
// before </head>, and an injection set later does not drop the one set earlier.
func TestShellComposesWithMathJax(t *testing.T) {
	shell, err := NewShell(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<html lang="zh-CN"><head></head><body><div id="app"></div></body></html>`)},
	})
	if err != nil {
		t.Fatalf("NewShell() error = %v", err)
	}

	shell.SetMathJaxURL("/static/v/abcd1234/js/mathjax/tex-svg.js")
	shell.SetShell(ShellHTML{
		ManifestURL:    manifestPath,
		ThemeColor:     "#18181b",
		ScriptURL:      pwaScriptPath,
		ThemeBootstrap: themeBootstrap,
	})
	shell.SetVersion("v0.1.4")

	got := string(shell.IndexHTML())
	headEnd := strings.Index(got, "</head>")
	for _, want := range []string{
		`<meta name="` + mathjaxMetaName + `" content="/static/v/abcd1234/js/mathjax/tex-svg.js" />`,
		`<meta name="` + versionMetaName + `" content="v0.1.4" />`,
		`<link rel="manifest" href="` + manifestPath + `"/>`,
		`<script src="` + pwaScriptPath + `"></script>`,
		themeBootstrap,
	} {
		at := strings.Index(got, want)
		if at < 0 {
			t.Errorf("index is missing %q:\n%s", want, got)
			continue
		}
		if headEnd < 0 || at > headEnd {
			t.Errorf("%q was not injected inside <head>:\n%s", want, got)
		}
	}
}
