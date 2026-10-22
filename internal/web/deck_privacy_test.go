package web

import (
	"net/http"
	"strings"
	"testing"
)

// This file pins down the deck visibility boundary: a deck is visible only to its owner and to explicitly granted users.
// The read side goes through the GET /api/v1/decks JSON list; direct content access by id goes through GET /decks/:id/notes.
// Historically there were also three visibility tiers private/unlisted/public and a /sharing/visibility write endpoint; both were removed together.

// deckListingBody returns the JSON text of the deck list the current session sees on GET /api/v1/decks.
func deckListingBody(t *testing.T, srv *Server, cookies []*http.Cookie) string {
	t.Helper()
	rec := getWithCookies(t, srv, "/api/v1/decks", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/decks status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	return rec.Body.String()
}

// TestStrangerCannotSeeOrOpenAnotherUsersDeck is the main acceptance test for this boundary:
// a deck built by someone else neither appears in the list nor has its content accessible directly by id — there is no in-between state of "visible but ungranted".
func TestStrangerCannotSeeOrOpenAnotherUsersDeck(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Private deck")
	seedBasic(t, db, deck.ID, "private front", "private back")

	_, u2Cookies, _ := createUserAndLogin(t, srv, db, "stranger_viewer")

	if body := deckListingBody(t, srv, u2Cookies); strings.Contains(body, "Private deck") {
		t.Errorf("another user's deck appeared in a stranger's listing: %s", snippet(body))
	}
	if rec := getWithCookies(t, srv, "/decks/"+deck.PublicID+"/notes", u2Cookies); rec.Code != http.StatusForbidden {
		t.Errorf("stranger reading another user's deck by id = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}

// TestDeckVisibilityEndpointRemoved is the regression guard for the removal:
// the /api/v1/decks/:id/sharing/visibility route does not exist, so even the owner writing to it only gets 404 —
// as soon as it can return 200 again, that means the three visibility tiers are back.
func TestDeckVisibilityEndpointRemoved(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "No visibility deck")

	path := "/api/v1/decks/" + deck.PublicID + "/sharing/visibility"
	if got := jsonRequest(t, srv, http.MethodPatch, path, `{"visibility":"public"}`, ownerCookies, ownerCSRF); got.Code != http.StatusNotFound {
		t.Errorf("PATCH %s = %d, want 404 (route must be gone)", path, got.Code)
	}
	if got := jsonRequest(t, srv, http.MethodPost, path, `{"visibility":"public"}`, ownerCookies, ownerCSRF); got.Code != http.StatusNotFound {
		t.Errorf("POST %s = %d, want 404 (route must be gone)", path, got.Code)
	}
}
