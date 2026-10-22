package web

import (
	"net/http"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// cardIDOfNote takes the id of the first card under a note; the SPA JSON request tests and the card render tests both use it to locate a card.
func cardIDOfNote(t *testing.T, db *gorm.DB, noteID uint64) uint64 {
	t.Helper()
	var c store.Card
	if err := db.Where("note_id = ?", noteID).First(&c).Error; err != nil {
		t.Fatalf("load card for note %d: %v", noteID, err)
	}
	return c.ID
}

// cardPublicIDOfNote takes the public id of the first card under a note; JSON requests use it to reference a card by public id.
func cardPublicIDOfNote(t *testing.T, db *gorm.DB, noteID uint64) string {
	t.Helper()
	var c store.Card
	if err := db.Where("note_id = ?", noteID).First(&c).Error; err != nil {
		t.Fatalf("load card for note %d: %v", noteID, err)
	}
	return c.PublicID
}

// TestReviewScopeRejectsUnreadableDeck covers the negative case: when a deck the user cannot read appears in the scope the whole request fails,
// and it must not carry on after silently dropping that deck (loadDeckForRole writes 403/404).
func TestReviewScopeRejectsUnreadableDeck(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	// A private deck belonging to another user; the logged-in user is neither owner nor authorized.
	foreign := seedReviewDeck(t, db, ownerID+1, "Foreign deck")

	rec := getWithCookies(t, srv, "/review?deck="+foreign.PublicID, cookies)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("GET /review with an unreadable deck status = %d, want 403 or 404 (body %s)",
			rec.Code, snippet(rec.Body.String()))
	}
}

// TestReviewScopeRejectsMixedUnreadableDeck covers the mixed-set negative case: when one deck of their own that is readable and one private deck of someone else's
// appear together, the whole request fails; the one without permission must not be silently dropped.
// Same criterion as REST's TestDueCardsFailsWholeRequestForUnreadableDeck.
func TestReviewScopeRejectsMixedUnreadableDeck(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	readable := seedReviewDeck(t, db, ownerID, "Mine")
	foreign := seedReviewDeck(t, db, ownerID+1, "Foreign deck")
	seedBasic(t, db, readable.ID, "Front", "Back")

	rec := getWithCookies(t, srv,
		"/review?deck="+readable.PublicID+"&deck="+foreign.PublicID, cookies)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("GET /review with a readable+foreign deck status = %d, want 403 or 404 (body %s)",
			rec.Code, snippet(rec.Body.String()))
	}
}
