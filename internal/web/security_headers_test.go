package web

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/frontend"
)

// This file holds the acceptance tests: one fixed set of security headers is mounted on every route, and the CSP has been switched from Report-Only to
// enforcing mode. Assertions always use literals, never constants from the package under test, so the test cannot simply follow a changed constant.
const (
	wantCSPHeader      = "Content-Security-Policy"
	wantCSPReportOnly  = "Content-Security-Policy-Report-Only"
	wantReferrerPolicy = "strict-origin-when-cross-origin"
)

// assertSecurityHeaders asserts all five header groups are present and the CSP is an enforcing header (blocking mode).
func assertSecurityHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	h := rec.Header()

	csp := h.Get(wantCSPHeader)
	if csp == "" {
		t.Errorf("%s is missing", wantCSPHeader)
	}
	// Enforcing mode: Report-Only must not be sent alongside — the two CSP headers override each other and the browser honours only the last one,
	// so mixing them makes the policy unpredictable.
	if got := h.Get(wantCSPReportOnly); got != "" {
		t.Errorf("%s must not be set once the policy is enforcing; got %q", wantCSPReportOnly, got)
	}
	for _, d := range []string{"default-src", "frame-ancestors"} {
		if !strings.Contains(csp, d) {
			t.Errorf("%s = %q, missing directive %q", wantCSPHeader, csp, d)
		}
	}
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := h.Get("Referrer-Policy"); got != wantReferrerPolicy {
		t.Errorf("Referrer-Policy = %q, want %q", got, wantReferrerPolicy)
	}
	if got := h.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := h.Get("Permissions-Policy"); got == "" {
		t.Error("Permissions-Policy is missing")
	}
}

// TestSecurityHeadersCoverRepresentativeRoutes covers the login page, the review page, and static assets,
// the health check, the /api/v1 and /mcp prefixes, and the 404 fallback. The middleware is global, so none of these exits may be missed.
func TestSecurityHeadersCoverRepresentativeRoutes(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "CSP deck")
	seedBasic(t, db, deck.ID, "FrontCSP", "BackCSP")

	asset := srv.assets.URL("js/pwa.js")
	if asset == "" {
		t.Fatal("pwa asset is not embedded")
	}

	tests := []struct {
		name    string
		path    string
		cookies []*http.Cookie
	}{
		{name: "login page", path: "/login"},
		{name: "review page", path: "/review?deck=" + deck.PublicID, cookies: cookies},
		{name: "home page", path: "/"},
		{name: "static asset", path: asset},
		{name: "health", path: "/healthz"},
		{name: "api prefix", path: "/api/v1/ping"},
		{name: "mcp endpoint", path: "/mcp"},
		{name: "unknown path", path: "/no-such-page"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := getWithCookies(t, srv, tc.path, tc.cookies)
			assertSecurityHeaders(t, rec)
		})
	}
}

// TestSecurityHeadersDoNotAlterResponseBody asserts the middleware only adds headers and changes neither the status code nor the body:
// the login and review pages are still 200 and both return the real SPA app shell (not truncated by the middleware).
func TestSecurityHeadersDoNotAlterResponseBody(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "CSP body deck")
	seedBasic(t, db, deck.ID, "FrontCSP", "BackCSP")

	const shellMarker = `<div id="app">`
	tests := []struct {
		name    string
		path    string
		cookies []*http.Cookie
		want    []string
	}{
		{name: "login page", path: "/login", want: []string{shellMarker}},
		{name: "review page", path: "/review?deck=" + deck.PublicID, cookies: cookies, want: []string{shellMarker}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := getWithCookies(t, srv, tc.path, tc.cookies)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want 200; body %s", tc.path, rec.Code, snippet(rec.Body.String()))
			}
			body := rec.Body.String()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("%s body lost %q: %s", tc.path, want, snippet(body))
				}
			}
		})
	}
}

// TestSecurityHeadersCSPMatchesPageResources pins the CSP directive by directive: every value maps to an actual
// page resource (evidence for the templates and static assets is in the comment in security_headers.go).
func TestSecurityHeadersCSPMatchesPageResources(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	csp := getWithCookies(t, srv, "/login", nil).Header().Get(wantCSPHeader)
	if csp == "" {
		t.Fatalf("%s is missing", wantCSPHeader)
	}

	for _, want := range []string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'", // Inline style= attributes (stats page bar widths, skeleton widths)
		"img-src 'self' https:",            // https external images allowed by /media and the card face
		"connect-src 'self'",
		"object-src 'none'",
		"frame-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP = %q, missing %q", csp, want)
		}
	}

	// None of the shipped scripts compiles JS at runtime (the guard test asserts this for our own scripts), so a policy carrying
	// 'unsafe-eval' would needlessly reopen script-src.
	if scriptSrc := cspDirectiveValue(csp, "script-src"); strings.Contains(scriptSrc, "'unsafe-eval'") {
		t.Errorf("script-src must not carry 'unsafe-eval': no served script compiles JS at runtime: %q", scriptSrc)
	}

	// The page's single inline <script> (theme bootstrap) must be allowed by hash; a nonce or script-src
	// 'unsafe-inline' would make "keep inline scripts out" meaningless.
	if !strings.Contains(csp, "'sha256-") {
		t.Errorf("CSP does not whitelist the inline theme bootstrap by hash: %q", csp)
	}
	if strings.Contains(csp, "'nonce-") {
		t.Errorf("CSP uses a nonce but the inline script is a compile-time constant: %q", csp)
	}
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Errorf("script-src must not blanket-allow inline scripts: %q", csp)
	}
}

// TestSecurityHeadersCSPHashMatchesRenderedThemeBootstrap takes the body of the inline theme bootstrap
// script out of the render result, computes SHA-256 on its own, and asserts the CSP carries exactly it. When the hash does not match, the browser blocks this single
// inline <script> as an unauthorized script, which amounts to the policy killing the first-paint theme bootstrap.
func TestSecurityHeadersCSPHashMatchesRenderedThemeBootstrap(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/login", nil)
	csp := rec.Header().Get(wantCSPHeader)

	body := rec.Body.String()
	const open = `<script>(function(){var s;try{s=localStorage.getItem("engram-theme")`
	start := strings.Index(body, open)
	if start < 0 {
		t.Fatal("rendered page has no inline theme bootstrap <script>")
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
		t.Errorf("CSP %q does not carry %q, the hash of the rendered inline theme bootstrap", csp, want)
	}
}

// cspStripLineComments drops whole-line comments starting with //. Both the scripts and the app shell markup carry comments, and the comment body
// may well contain strings like "on*=" or "eval" — without stripping them first the guard test would raise false positives on comments.
func cspStripLineComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			b.WriteString("\n")
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// cspInlineScriptBodies extracts the bodies of <script> tags without src: a tag containing src= counts as external and is skipped;
// the bodies of the remaining tags go back to the caller to hash independently.
func cspInlineScriptBodies(src string) []string {
	const closeTag = "</script>"
	var out []string
	for {
		i := strings.Index(src, "<script")
		if i < 0 {
			return out
		}
		rest := src[i:]
		open := strings.Index(rest, ">")
		if open < 0 {
			return out
		}
		tag := rest[:open+1]
		body := rest[open+1:]
		end := strings.Index(body, closeTag)
		if end < 0 {
			return out
		}
		if !strings.Contains(tag, "src=") {
			out = append(out, body[:end])
		}
		src = body[end+len(closeTag):]
	}
}

var (
	// cspStyleUse matches an inline <style> block or a style= attribute; both are governed by style-src.
	// It requires whitespace or a quote before style, to avoid tripping on compound attribute names like data-style=.
	cspStyleUse = regexp.MustCompile(`(?:^|[\s"'])style\s*=|(?i)<style[\s>]`)
	// cspEventAttr matches HTML event attributes on*=. It requires a leading whitespace/quote/angle bracket, to avoid tripping on
	// property assignment in JS (such as btn.onclick = fn, whose leading character is a dot).
	cspEventAttr = regexp.MustCompile(`(?:^|[\s"'<>])on[a-z]+\s*=`)
	// cspRuntimeCompile matches runtime JS compilation (eval / new Function). These need
	// script-src 'unsafe-eval', which the policy deliberately omits: as soon as our own scripts introduce one, the test turns red,
	// forcing the author to think through whether to widen the policy or change the implementation.
	cspRuntimeCompile = regexp.MustCompile(`\beval\s*\(|new\s+Function\s*\(`)
	// cspExternalResource matches an absolute address in src= / href= (http(s): or protocol-relative //).
	// The policy allows only 'self' and the theme bootstrap hash, so one more source gets blocked by the browser.
	cspExternalResource = regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["'](?:https?:)?//[^"']*`)
)

// TestSecurityHeadersGuardCoversServedShellAndScripts is the static guard test that stands in for "walking every page by hand".
//
// It extracts the sources that really reach the browser and checks them one by one against "the policy actually sent":
//
//   - the rendered SPA app shell (the test takes the response body directly) and the inline <script> in the /sw.js Go generates
//     (no src) → its SHA-256 must appear in script-src (allowed by hash);
//   - a <style> block or style= attribute in the same batch of sources → style-src must contain 'unsafe-inline'
//     (style attributes cannot be covered by hash/nonce; the reason is in security_headers.go);
//   - an event attribute on*= → script-src must contain 'unsafe-inline' (event attributes cannot be hashed either);
//   - eval / new Function in these two kinds of source → would need script-src 'unsafe-eval', which the policy deliberately omits,
//     so the test turns red;
//   - external scripts/styles in the app shell must be same-origin: the policy allows only 'self' and the theme bootstrap hash, and one more source
//     (a CDN, a separate static domain) would be blocked by the browser;
//   - the entry index.html generated at build time → must have no inline <script>: the server only adds hashes to the app shell,
//     never to the build output, so one appearing is a block that only shows up in production.
//
// Any combination that is "actually in use but not allowed by the policy" turns the test red. The two kinds it cannot catch need a browser smoke test:
// resources that scripts insert into the DOM at runtime, and the inside of minified bundle output (the /assets/index-*.js files are not
// text-matched).
func TestSecurityHeadersGuardCoversServedShellAndScripts(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	shell := getWithCookies(t, srv, "/login", nil)
	csp := shell.Header().Get(wantCSPHeader)
	if csp == "" {
		t.Fatalf("%s is missing", wantCSPHeader)
	}
	scriptSrc := cspDirectiveValue(csp, "script-src")
	styleSrc := cspDirectiveValue(csp, "style-src")
	if scriptSrc == "" || styleSrc == "" {
		t.Fatalf("CSP %q is missing script-src or style-src", csp)
	}

	shellBody := shell.Body.String()
	sw := getWithCookies(t, srv, "/sw.js", nil).Body.String()
	if !strings.Contains(sw, "STATIC_CACHE") {
		t.Fatalf("/sw.js did not serve the generated service worker: %s", snippet(sw))
	}
	// Only checks the two sources that really reach the browser at runtime: the rendered app shell and the /sw.js generated on the fly.
	sources := map[string]string{"rendered SPA shell": shellBody, "/sw.js": sw}

	for name, raw := range sources {
		src := cspStripLineComments(raw)

		for _, body := range cspInlineScriptBodies(src) {
			sum := sha256.Sum256([]byte(body))
			token := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
			if !strings.Contains(scriptSrc, token) {
				t.Errorf("%s: inline <script> with hash %s is not whitelisted by script-src %q", name, token, scriptSrc)
			}
		}
		if cspStyleUse.MatchString(src) && !strings.Contains(styleSrc, "'unsafe-inline'") {
			t.Errorf("%s: inline style (<style> or style=) is used but style-src %q lacks 'unsafe-inline'", name, styleSrc)
		}
		if cspEventAttr.MatchString(src) && !strings.Contains(scriptSrc, "'unsafe-inline'") {
			t.Errorf("%s: inline event attribute on*= is used but script-src %q lacks 'unsafe-inline'", name, scriptSrc)
		}
		if cspRuntimeCompile.MatchString(src) {
			t.Errorf("%s: eval / new Function needs script-src 'unsafe-eval', which the policy deliberately omits: %q", name, scriptSrc)
		}
	}

	for _, ref := range cspExternalResource.FindAllString(shellBody, -1) {
		t.Errorf("rendered SPA shell references a cross-origin resource (%s); the policy only allows 'self'", ref)
	}

	// The entry is generated by Vite and the server only injects shell markup into it; it never adds a hash for an inline script that appears at build time.
	dist, err := frontend.FS()
	if err != nil {
		t.Fatalf("frontend.FS: %v", err)
	}
	entry, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		t.Fatalf("read embedded dist/index.html: %v", err)
	}
	if bodies := cspInlineScriptBodies(string(entry)); len(bodies) != 0 {
		t.Errorf("embedded dist/index.html carries %d inline <script> block(s) that no CSP hash covers", len(bodies))
	}
}

// cspDirectiveValue takes the value of one directive out of the full policy; returns an empty string when the directive is missing.
func cspDirectiveValue(csp, name string) string {
	for _, part := range strings.Split(csp, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, name+" ") {
			return strings.TrimPrefix(part, name+" ")
		}
	}
	return ""
}
