package web

import (
	"net/http"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// TestDeckListRedirectsAnonymousToLogin covers the acceptance point that anonymous access is redirected to the login page.
func TestDeckListRedirectsAnonymousToLogin(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/decks", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /decks (anonymous) status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("GET /decks (anonymous) Location = %q, want /login", loc)
	}
}

// seedCardRow creates one basic note + forward card and returns the card id (used by the queue count fixtures, renders no card content).
// Shared by several test files related to decks and card settings.
func seedCardRow(t *testing.T, db *gorm.DB, deckID uint64, front string, createdAt time.Time) uint64 {
	t.Helper()
	note := store.Note{DeckID: deckID, Kind: "basic",
		FieldsJSON: `{"front":"` + front + `","back":"b"}`, TagsJSON: "[]",
		CreatedAt: createdAt, UpdatedAt: createdAt}
	if err := db.Create(&note).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	card := store.Card{NoteID: note.ID, Template: "forward", CreatedAt: createdAt}
	if err := db.Create(&card).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	return card.ID
}
