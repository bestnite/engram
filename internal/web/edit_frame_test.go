package web

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

// TestEditFrameServesTheFrameIsolation pins the editing frame's route: the editing area has the card
// frame's opaque-origin sandbox, because a field value is untrusted note content. The route must
// therefore carry a frame CSP, no-referrer, and no X-Frame-Options; the site policy would block the
// iframe and would not allow the inline styles the editor writes. Its script-src is the origin only:
// the runtime is a same-origin module, so no inline script is needed, and the field value is written
// with innerHTML, so an inline event handler attribute must not run.
func TestEditFrameServesTheFrameIsolation(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	const origin = "https://engram.example.com"
	srv.baseURL = origin

	rec := getWithCookies(t, srv, "/edit-frame/20261030", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /edit-frame/<version> status = %d, want 200; body %s", rec.Code, snippet(rec.Body.String()))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	wantCSP := "sandbox allow-scripts; " +
		"default-src 'none'; " +
		"script-src " + origin + "; " +
		"style-src " + origin + " 'unsafe-inline'; " +
		"img-src " + origin + " https: data: blob:; " +
		"media-src " + origin + " https: blob:; " +
		"font-src " + origin + " https: data:; " +
		"connect-src " + origin + "/fm/; " +
		"form-action 'none'; " +
		"frame-ancestors 'self'"
	got := rec.Header().Get(cspHeader)
	if got != wantCSP {
		t.Errorf("edit frame CSP = %q, want %q", got, wantCSP)
	}
	// Every directive other than script-src stays identical to the card frame's, so a directive added
	// to one frame cannot drift apart from the other.
	if got, want := nonScriptDirectives(rec.Header().Get(cspHeader)), nonScriptDirectives(cardFrameCSP(origin)); !slices.Equal(got, want) {
		t.Errorf("edit frame non-script directives = %q, want the card frame's %q", got, want)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("edit frame Referrer-Policy = %q, want no-referrer", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "" {
		t.Errorf("edit frame X-Frame-Options = %q, want it absent so the iframe can load", got)
	}
	if got := rec.Header().Get("Permissions-Policy"); !strings.Contains(got, "autoplay=(self)") {
		t.Errorf("edit frame Permissions-Policy = %q, want autoplay=(self)", got)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `id="editor"`) {
		t.Errorf("edit frame document has no editing root: %s", snippet(body))
	}
	if !strings.Contains(body, `editFrame`) && !strings.Contains(body, `edit-frame`) {
		t.Errorf("edit frame document does not reference the built entry: %s", snippet(body))
	}
	// The editor is a minimal document, not the SPA shell: no manifest, service worker or theme bootstrap.
	for _, forbidden := range []string{"manifest", "pwa.js", "localStorage"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("edit frame document must not contain %q: %s", forbidden, snippet(body))
		}
	}
}

// nonScriptDirectives splits a CSP into its directives and drops script-src, so a test can compare two
// policies without the one directive they deliberately differ in.
func nonScriptDirectives(csp string) []string {
	var out []string
	for _, directive := range strings.Split(csp, "; ") {
		if directive == "" || strings.HasPrefix(directive, "script-src ") {
			continue
		}
		out = append(out, directive)
	}
	return out
}

// TestEditFrameRejectsEmptyBuildEntry documents the 404 path when the embedded build has no editing
// frame entry; the production loader rejects that build earlier.
func TestEditFrameRejectsEmptyBuildEntry(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	srv.shell = &Shell{}
	if code := getWithCookies(t, srv, "/edit-frame/v1", nil).Code; code != http.StatusNotFound {
		t.Errorf("GET /edit-frame/v1 without an editor entry status = %d, want 404", code)
	}
}
