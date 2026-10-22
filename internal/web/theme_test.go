package web

import (
	"strings"
	"testing"
)

// TestThemeBootstrapRunsBeforeStylesheet is the acceptance: the theme bootstrap must be inline inside <head>.
// The external pwa.js and the stylesheets are separate requests, and the browser may paint a white frame before they arrive; the inline bootstrap moves the dark class,
// color-scheme and canvas background ahead of the first frame.
//
// Only the SPA app shell is left in the page layer: the bootstrap is injected into the entry <head> by server.go at wiring time (SPA.SetShell),
// and this case asserts it appears in the shell as-is and unescaped.
func TestThemeBootstrapRunsBeforeStylesheet(t *testing.T) {
	srv := newRenderServer(t, nil)
	body := get(t, srv, "/", nil).Body.String()

	head := strings.Index(body, "<head>")
	if head < 0 {
		t.Fatalf("rendered shell has no <head>: %s", snippet(body))
	}
	script := strings.Index(body, `localStorage.getItem("engram-theme")`)
	if script < 0 {
		t.Fatalf("head has no inline theme bootstrap (engram-theme lookup): %s", snippet(body))
	}
	if headEnd := strings.Index(body, "</head>"); headEnd >= 0 && script > headEnd {
		t.Errorf("theme bootstrap must sit inside <head>: head=%d script=%d headEnd=%d", head, script, headEnd)
	}

	// Asserts item by item what the bootstrap must cover: the storage key, color-scheme, the dark canvas background, and the system-theme fallback.
	for _, want := range []string{
		`engram-theme`,
		`colorScheme`,
		`#09090b`,
		`#f8fafc`,
		`(prefers-color-scheme: dark)`,
		`classList.toggle("dark"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("theme bootstrap is missing %q: %s", want, snippet(body))
		}
	}

	// Verbatim output means the injection was not escaped; when HTML-escaped (&#34; and the like) it would not match the constant itself.
	if !strings.Contains(body, themeBootstrap) {
		t.Errorf("theme bootstrap is not rendered verbatim (escaped?): %s", snippet(body))
	}
	// Pinned once more on "unescaped": this raw JS containing quotes can only appear in unescaped script content.
	if !strings.Contains(body, `r.style.backgroundColor=d?"#09090b":"#f8fafc"`) {
		t.Errorf("theme bootstrap script content looks HTML-escaped: %s", snippet(body))
	}
}
