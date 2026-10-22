package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// seedDueCard creates a note and one of its cards but writes no card_states: the card is new and bound to appear in the queue.
// Used only by this file's review-scope cases (no real scheduling parameters needed).
func seedDueCard(t *testing.T, db *gorm.DB, deckID uint64) {
	t.Helper()
	now := time.Now().UTC()
	n := store.Note{DeckID: deckID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`, TagsJSON: "[]", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	c := store.Card{NoteID: n.ID, Template: "forward", Ordinal: 0, CreatedAt: now}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
}

// TestDueCardsAcceptsRepeatedDeckParams is the REST acceptance: repeated deck parameters take the union of several decks.
func TestDueCardsAcceptsRepeatedDeckParams(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "reviewer", store.RoleUser)
	deckA := seedDeck(t, env.db, user.ID)
	deckB := seedDeck(t, env.db, user.ID)
	seedDueCard(t, env.db, deckA.ID)
	seedDueCard(t, env.db, deckB.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeReview}, nil)
	router := env.router()

	status, raw := doJSON(t, router, http.MethodGet,
		fmt.Sprintf("/api/v1/review/due?deck=%s&deck=%s&limit=50", deckA.PublicID, deckB.PublicID), k.Plaintext, "")
	if status != http.StatusOK {
		t.Fatalf("GET /review/due?deck=A&deck=B status = %d, want 200 (body %s)", status, raw)
	}
	var body struct {
		Cards []DueCard `json:"cards"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, raw)
	}
	seen := map[string]bool{}
	for _, c := range body.Cards {
		seen[c.DeckID] = true
	}
	if !seen[deckA.PublicID] || !seen[deckB.PublicID] {
		t.Errorf("cards by deck = %v, want both deck %s and deck %s", seen, deckA.PublicID, deckB.PublicID)
	}
}

// TestDueCardsRejectsBadDeckParam covers the negative cases: an unknown, empty or malformed deck external id returns 404.
// An external id is an opaque string, and when the routing layer cannot resolve an entity it follows the same convention as "deck does not exist", hence 404 rather than 400.
func TestDueCardsRejectsBadDeckParam(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "badparam", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	seedDueCard(t, env.db, deck.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeReview}, nil)
	router := env.router()

	for _, q := range []string{"deck=abc", "deck=0", fmt.Sprintf("deck=%s&deck=oops", deck.PublicID)} {
		status, raw := doJSON(t, router, http.MethodGet, "/api/v1/review/due?"+q, k.Plaintext, "")
		if status != http.StatusNotFound {
			t.Errorf("GET /review/due?%s status = %d, want 404 (body %s)", q, status, raw)
		}
	}
}

// TestDueCardsFailsWholeRequestForUnreadableDeck covers the negative case: when any deck in the set is unreadable the whole call fails,
// and it must not silently filter it out and return cards from the other decks.
func TestDueCardsFailsWholeRequestForUnreadableDeck(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "owner", store.RoleUser)
	stranger := seedUser(t, env.db, "stranger", store.RoleUser)
	readable := seedDeck(t, env.db, user.ID)
	foreign := seedDeck(t, env.db, stranger.ID)
	seedDueCard(t, env.db, readable.ID)
	seedDueCard(t, env.db, foreign.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeReview}, nil)
	router := env.router()

	for _, q := range []string{
		fmt.Sprintf("deck=%s&deck=%s", readable.PublicID, foreign.PublicID),
		fmt.Sprintf("deck=%s", foreign.PublicID),
	} {
		status, raw := doJSON(t, router, http.MethodGet, "/api/v1/review/due?"+q, k.Plaintext, "")
		if status != http.StatusForbidden {
			t.Errorf("GET /review/due?%s status = %d, want 403 (body %s)", q, status, raw)
		}
	}
}
