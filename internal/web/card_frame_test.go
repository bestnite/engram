package web

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

// TestCardFrameServesExactHeaders pins the frame route's headers. The frame is an opaque-origin sandbox that renders card
// content, so it cannot share the site policy: it carries its own CSP with the configured origin substituted, no-referrer, and
// no X-Frame-Options (the global DENY would stop the iframe from loading at all; frame-ancestors 'self' is the remaining control).
func TestCardFrameServesExactHeaders(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	const origin = "https://engram.example.com"
	srv.baseURL = origin

	rec := getWithCookies(t, srv, "/card-frame/20261030", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /card-frame/<version> status = %d, want 200; body %s", rec.Code, snippet(rec.Body.String()))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	wantCSP := "sandbox allow-scripts; " +
		"default-src 'none'; " +
		"script-src " + origin + " 'unsafe-inline' 'unsafe-eval'; " +
		"style-src " + origin + " 'unsafe-inline'; " +
		"img-src " + origin + " https: data: blob:; " +
		"media-src " + origin + " https: blob:; " +
		"font-src " + origin + " https: data:; " +
		"connect-src " + origin + "/fm/; " +
		"form-action 'none'; " +
		"frame-ancestors 'self'"
	if got := rec.Header().Get(wantCSPHeader); got != wantCSP {
		t.Errorf("frame CSP = %q, want %q", got, wantCSP)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("frame Referrer-Policy = %q, want no-referrer", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "" {
		t.Errorf("frame X-Frame-Options = %q, want it absent so the iframe can load", got)
	}
	// The other global headers are untouched.
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("frame X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Permissions-Policy"); !strings.Contains(got, "autoplay=(self)") {
		t.Errorf("frame Permissions-Policy = %q, want autoplay=(self)", got)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `<div id="qa">`) {
		t.Errorf("frame document has no content root: %s", snippet(body))
	}
	if !strings.Contains(body, `id="cardFrame"`) && !strings.Contains(body, `cardFrame-`) {
		t.Errorf("frame document does not reference the built frame entry: %s", snippet(body))
	}
	// The MathJax meta is injected into the frame entry too, so the runtime can load the self-hosted script.
	if want := srv.assets.URL("js/mathjax/tex-svg.js"); want != "" {
		if !strings.Contains(body, `<meta name="`+mathjaxMetaName+`" content="`+want+`" />`) {
			t.Errorf("frame document is missing the MathJax meta %q: %s", want, snippet(body))
		}
	}
	// A minimal frame document must never pull in the SPA shell: no manifest, no service worker, no theme bootstrap.
	for _, forbidden := range []string{"manifest", "pwa.js", "localStorage"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("frame document must not contain %q: %s", forbidden, snippet(body))
		}
	}
}

// TestCardFrameRejectsEmptyBuildEntry documents the 404 path when the embedded build has no frame entry. It injects a Shell
// without card-frame.html; the production loader rejects that build earlier, so this is only the last-resort response.
func TestCardFrameRejectsEmptyBuildEntry(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	srv.shell = &Shell{}
	if code := getWithCookies(t, srv, "/card-frame/v1", nil).Code; code != http.StatusNotFound {
		t.Errorf("GET /card-frame/v1 without a frame entry status = %d, want 404", code)
	}
}

// TestOtherRoutesKeepSiteCSPAndAllowAutoplay asserts the site-wide headers still apply everywhere else: the old CSP exactly,
// with only autoplay opened up for the same origin (the iframe element declares allow="autoplay").
func TestOtherRoutesKeepSiteCSPAndAllowAutoplay(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)

	sum := sha256.Sum256([]byte(themeBootstrapJS))
	hash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	wantCSP := "default-src 'self'; " +
		"script-src 'self' " + hash + "; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' https:; " +
		"connect-src 'self'; " +
		"object-src 'none'; " +
		"frame-src 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'; " +
		"frame-ancestors 'none'"

	for _, path := range []string{"/", "/login"} {
		rec := getWithCookies(t, srv, path, nil)
		if got := rec.Header().Get(wantCSPHeader); got != wantCSP {
			t.Errorf("GET %s CSP = %q, want the unchanged site policy %q", path, got, wantCSP)
		}
		if got := rec.Header().Get("Referrer-Policy"); got != wantReferrerPolicy {
			t.Errorf("GET %s Referrer-Policy = %q, want %q", path, got, wantReferrerPolicy)
		}
		if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("GET %s X-Frame-Options = %q, want DENY", path, got)
		}
		got := rec.Header().Get("Permissions-Policy")
		if !strings.Contains(got, "autoplay=(self)") {
			t.Errorf("GET %s Permissions-Policy = %q, want autoplay=(self)", path, got)
		}
		if strings.Contains(got, "autoplay=()") {
			t.Errorf("GET %s Permissions-Policy still turns autoplay off: %q", path, got)
		}
	}
}
