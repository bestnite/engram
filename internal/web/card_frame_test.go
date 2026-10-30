package web

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
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

// TestCardFrameCarriesRequestLocale pins the frame document's <html lang>. The frame attaches the shortcode controls, whose
// labels come from the translation catalog, so the document must carry the language the request resolved to - the same source
// as the SPA entry. The ETag is computed from the written body, so two languages cannot share a cached response.
func TestCardFrameCarriesRequestLocale(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)

	if err := db.Model(&store.User{}).Where("id = ?", ownerID).Update("locale", "zh-CN").Error; err != nil {
		t.Fatalf("set owner locale: %v", err)
	}

	zh := getWithCookies(t, srv, "/card-frame/20261031", cookies)
	if zh.Code != http.StatusOK {
		t.Fatalf("GET /card-frame/<version> status = %d, want 200; body %s", zh.Code, snippet(zh.Body.String()))
	}
	body := zh.Body.String()
	if !strings.Contains(body, `<html lang="zh-CN">`) {
		t.Errorf("frame document does not carry the resolved language: %s", snippet(body))
	}
	// The ETag names the body actually written, so a language switch revalidates instead of hitting a stale 304.
	wantETag := `"` + indexHashOf([]byte(body)) + `"`
	if got := zh.Header().Get("ETag"); got != wantETag {
		t.Errorf("frame ETag = %q, want %q (hash of the written body)", got, wantETag)
	}

	// The same document for another language is a different body, and therefore a different ETag.
	en := getWithCookies(t, srv, "/card-frame/20261031?lang=en", cookies)
	if en.Code != http.StatusOK {
		t.Fatalf("GET /card-frame/<version>?lang=en status = %d, want 200; body %s", en.Code, snippet(en.Body.String()))
	}
	if !strings.Contains(en.Body.String(), `<html lang="en">`) {
		t.Errorf("frame document does not carry the overridden language: %s", snippet(en.Body.String()))
	}
	if en.Header().Get("ETag") == zh.Header().Get("ETag") {
		t.Error("frame ETag is the same for two languages, so a language switch would be served from cache")
	}
	if en.Body.String() == body {
		t.Error("frame body is unchanged by the language, so the runtime cannot pick the catalog")
	}

	// A matching If-None-Match still answers 304 for the body that was written.
	req := httptest.NewRequest(http.MethodGet, "/card-frame/20261031", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	req.Header.Set("If-None-Match", zh.Header().Get("ETag"))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("conditional GET with the current ETag = %d, want 304", rec.Code)
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
		"frame-src 'self'; " +
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
