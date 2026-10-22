package web

import (
	"net/http"
	"strings"
	"testing"
)

// This file covers the part of the "link the orphan pages" task that still holds after the SPA migration: the entry pages themselves are reachable and still login-protected.
//   - /decks: anonymous access still redirects to the login page, so its import entry does not leak to anonymous visitors;
//   - /settings/email: reachable for signed-in users (returns the app shell), anonymous still redirects to login first.
//
// The SSR settings page used to render the /settings/email card conditionally on whether the mail stack was enabled; with the page layer gone that condition belongs to the frontend
// view, the Go side no longer renders the card, and the matching assertion went with it. All the tests go through real routes (httptest + a signed-in session).

// TestDeckListImportHiddenFromAnonymous asserts /decks is still a protected route: anonymous access is redirected to
// the login page, so the /import entry does not leak to anonymous visitors.
func TestDeckListImportHiddenFromAnonymous(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/decks", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("anonymous GET /decks status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("anonymous GET /decks Location = %q, want /login", loc)
	}
	if strings.Contains(rec.Body.String(), `href="/import"`) {
		t.Errorf("anonymous /decks response leaked the /import entry: %s", snippet(rec.Body.String()))
	}
}

// TestSettingsEmailChangeRouteReachable asserts the change-email entry page /settings/email is still a real page reachable
// by signed-in users (returns the app shell), and anonymous access is still redirected to the login page first. Whether the card is shown inside the page is the frontend's decision.
func TestSettingsEmailChangeRouteReachable(t *testing.T) {
	ts := newSecurityServer(t, false)

	page := getWithCookies(t, ts.srv, "/settings/email", ts.cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /settings/email status = %d, want 200 (body %s)", page.Code, snippet(page.Body.String()))
	}
	if !strings.Contains(page.Body.String(), `id="app"`) {
		t.Errorf("GET /settings/email did not serve the SPA shell: %s", snippet(page.Body.String()))
	}

	anon := getWithCookies(t, ts.srv, "/settings/email", nil)
	if anon.Code != http.StatusSeeOther {
		t.Fatalf("anonymous GET /settings/email status = %d, want 303", anon.Code)
	}
	if loc := anon.Header().Get("Location"); loc != "/login" {
		t.Errorf("anonymous GET /settings/email Location = %q, want /login", loc)
	}
}
