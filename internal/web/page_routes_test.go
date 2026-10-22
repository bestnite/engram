package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// The uniform contract for page-type GETs.
//
// All page rendering has been deleted from the server (the SSR page layer removed wholesale), so every page path is left with two things: decide reachability first
// (session / role / first-run window), then return the app shell if it passes, with the client router rendering the page. These checks used to be repeated page by
// page in the same shape; here they are merged into one table, while each page's own business assertions stay in its own *_test.go.

// assertShell asserts the response is the SPA app shell: 200, text/html, revalidation/no-cache, an ETag,
// and the app mount point <div id="app"></div>.
func assertShell(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Errorf("Cache-Control = %q, want containing no-cache", cc)
	}
	if rec.Header().Get("ETag") == "" {
		t.Errorf("ETag is missing")
	}
	if !strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
		t.Errorf("response is not the SPA shell: %s", snippet(rec.Body.String()))
	}
}

// assertRedirectsToLogin asserts an anonymous request is sent to the login page instead of getting the app shell.
func assertRedirectsToLogin(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Errorf("anonymous %s = %d %q, want 303 /login", what, rec.Code, rec.Header().Get("Location"))
	}
	if strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
		t.Errorf("anonymous %s returned the SPA shell", what)
	}
}

// TestSessionPageRoutesServeShell covers the page paths that only require a signed-in session.
func TestSessionPageRoutesServeShell(t *testing.T) {
	for _, path := range []string{"/decks", "/review", "/settings", "/settings/security", "/settings/email", "/settings/keys"} {
		t.Run(path, func(t *testing.T) {
			srv, _, _, cookies, _ := newNotesServer(t)
			assertShell(t, getWithCookies(t, srv, path, cookies))
			assertRedirectsToLogin(t, getWithCookies(t, srv, path, nil), "GET "+path)
		})
	}
}

// TestPresetsRouteServesShell covers /presets: it needs the preset page's wiring (jobRunner and friends).
func TestPresetsRouteServesShell(t *testing.T) {
	srv, _, _, cookies, _, _ := newPresetsServer(t)
	assertShell(t, getWithCookies(t, srv, "/presets", cookies))
	assertRedirectsToLogin(t, getWithCookies(t, srv, "/presets", nil), "GET /presets")
}

// deckPageRouteCase: one row = the reachability assertion for one deck-scoped page path.
type deckPageRouteCase struct {
	name string
	// path is built from the fixture's deck / card public ids.
	path func(deckPublicID, notePublicID string) string
	// stranger is the "signed in but unauthorized" session: none = no grant at all, reader = granted reader.
	stranger string
}

// TestDeckScopedPageRoutesEnforceRole covers deck-scoped page paths: owner gets the app shell, anonymous redirects
// to the login page, an unauthorized user gets 403 and no shell — the permission check runs before the shell is served.
func TestDeckScopedPageRoutesEnforceRole(t *testing.T) {
	cases := []deckPageRouteCase{
		{"note list", func(d, _ string) string { return "/decks/" + d + "/notes" }, "none"},
		{"note edit", func(d, n string) string { return "/decks/" + d + "/notes/" + n }, "reader"},
		{"note create", func(d, _ string) string { return "/decks/" + d + "/new-note" }, "reader"},
		{"deck settings", func(d, _ string) string { return "/decks/" + d + "/settings" }, "none"},
		{"deck sharing", func(d, _ string) string { return "/decks/" + d + "/sharing" }, "reader"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
			deck := seedReviewDeck(t, db, ownerID, "Page route deck")
			note := seedBasic(t, db, deck.ID, "Q", "A")
			path := tc.path(deck.PublicID, note.PublicID)

			assertShell(t, getWithCookies(t, srv, path, ownerCookies))
			assertRedirectsToLogin(t, get(t, srv, path, nil), "GET "+path)

			strangerID, strangerCookies, _ := createUserAndLogin(t, srv, db, "page-route-"+strings.ReplaceAll(tc.name, " ", "-"))
			if tc.stranger == "reader" {
				grantRole(t, srv, deck.ID, strangerID, store.RoleReader, ownerCookies, ownerCSRF)
			}
			denied := getWithCookies(t, srv, path, strangerCookies)
			if denied.Code != http.StatusForbidden {
				t.Fatalf("%s GET %s = %d, want 403 (body %s)", tc.stranger, path, denied.Code, snippet(denied.Body.String()))
			}
			if strings.Contains(denied.Body.String(), `<div id="app"></div>`) {
				t.Errorf("a denied user must not receive the SPA shell at %s", path)
			}
		})
	}
}

// TestHomeRouteBranches covers the two mutually exclusive branches of GET /, with written order as priority.
func TestHomeRouteBranches(t *testing.T) {
	t.Run("with an active admin", func(t *testing.T) {
		srv, _, _, cookies, _ := newNotesServer(t)
		assertShell(t, getWithCookies(t, srv, "/", cookies))

		// The first-run window looks only at the admin count and makes no login decision: anonymous visitors get the app shell too, otherwise there would be a redirect loop with the SPA home page.
		anon := getWithCookies(t, srv, "/", nil)
		assertShell(t, anon)
		if loc := anon.Header().Get("Location"); loc != "" {
			t.Errorf("anonymous GET / redirected to %q, want the SPA shell", loc)
		}
	})

	t.Run("first run redirects to setup", func(t *testing.T) {
		srv := newFreshServer(t, nil)
		rec := get(t, srv, "/", nil)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/setup" {
			t.Fatalf("GET / before any admin = %d %q, want 303 /setup", rec.Code, rec.Header().Get("Location"))
		}
		if strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
			t.Error("the first-run redirect must not return the SPA shell")
		}
	})
}
