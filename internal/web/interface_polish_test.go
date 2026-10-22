package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// This file holds the part of the interface polish that can still be verified from the server and does not depend on SSR page rendering:
// the favicon redirect, the icons the SPA app shell declares, and the cache lists of the manifest and service worker.
// Page-level navigation/footer/dialog appearance is the SPA client router's job now and is no longer rendered by the server.

// TestFaviconRedirectsToHashedIcon is the acceptance for (f): /favicon.ico no longer 404s,
// it redirects to the content-hashed SVG icon.
func TestFaviconRedirectsToHashedIcon(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/favicon.ico", nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("GET /favicon.ico status = %d, want 302 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	loc := rec.Header().Get("Location")
	icon := srv.assets.URL("icons/icon.svg")
	if icon == "" {
		t.Fatal("icons/icon.svg is not embedded")
	}
	if loc != icon {
		t.Errorf("GET /favicon.ico Location = %q, want the hashed icon %q", loc, icon)
	}
	if !strings.HasPrefix(loc, staticPathPrefix) {
		t.Errorf("favicon target %q is not a content-hashed static path", loc)
	}
}

// TestPagesDeclareIconLinks asserts the SPA app shell declares an SVG favicon and an apple-touch-icon in <head>,
// both through the content-hashed path ((f)). Only the app shell is left at the page layer; server.go injects the icons into the entry.
func TestPagesDeclareIconLinks(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	icon := srv.assets.URL("icons/icon.svg")
	apple := srv.assets.URL("icons/apple-touch-icon.png")
	if icon == "" || apple == "" {
		t.Fatalf("icon assets are not embedded: icon=%q apple=%q", icon, apple)
	}

	body := get(t, srv, "/", nil).Body.String()
	if !strings.Contains(body, `rel="icon" type="image/svg+xml" href="`+icon+`"`) {
		t.Errorf("SPA shell does not declare the hashed SVG icon: %s", snippet(body))
	}
	if !strings.Contains(body, `rel="apple-touch-icon" href="`+apple+`"`) {
		t.Errorf("SPA shell does not declare the hashed apple-touch-icon: %s", snippet(body))
	}
}

// TestManifestDeclaresAdaptiveAndMaskableIcons asserts the manifest declares two icons: icon.svg with adaptive any
// and icon-maskable.svg with a white-background maskable, and that maskable uses 512x512 ((f)).
func TestManifestDeclaresAdaptiveAndMaskableIcons(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, manifestPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", manifestPath, rec.Code)
	}
	var body struct {
		Icons []struct {
			Src, Sizes, Type, Purpose string
		} `json:"icons"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}

	anyIcon := srv.assets.URL("icons/icon.svg")
	maskableIcon := srv.assets.URL("icons/icon-maskable.svg")
	if anyIcon == "" || maskableIcon == "" {
		t.Fatalf("manifest icon assets are not embedded: any=%q maskable=%q", anyIcon, maskableIcon)
	}

	var haveAny, haveMaskable bool
	for _, ic := range body.Icons {
		if ic.Src == anyIcon && ic.Sizes == "any" && ic.Type == "image/svg+xml" && ic.Purpose == "any" {
			haveAny = true
		}
		if ic.Src == maskableIcon && ic.Sizes == "512x512" && ic.Type == "image/svg+xml" && ic.Purpose == "maskable" {
			haveMaskable = true
		}
	}
	if !haveAny {
		t.Errorf("manifest is missing the adaptive icon (any, any size): %+v", body.Icons)
	}
	if !haveMaskable {
		t.Errorf("manifest is missing the maskable icon (512x512): %+v", body.Icons)
	}
}

// TestServiceWorkerCachesIcons asserts both icons and the apple-touch-icon are in the shell cache list ((f)).
func TestServiceWorkerCachesIcons(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	body := getWithCookies(t, srv, serviceWorkerPath, nil).Body.String()
	for _, logical := range []string{"icons/icon.svg", "icons/icon-maskable.svg", "icons/apple-touch-icon.png"} {
		url := srv.assets.URL(logical)
		if url == "" {
			t.Fatalf("asset %q is not embedded", logical)
		}
		if !strings.Contains(body, `"`+url+`"`) {
			t.Errorf("service worker cache list is missing %q (%s)", logical, url)
		}
	}
}
