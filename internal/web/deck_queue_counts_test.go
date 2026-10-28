package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

func TestDeckQueueCountsUsesVisibleDecksAndSessionOnly(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	ownerDeck := seedDeck(t, db, ownerID, "owner-deck")
	secondDeck := seedDeck(t, db, ownerID, "second-deck")
	for _, d := range []*store.Deck{ownerDeck, secondDeck} {
		seedBasic(t, db, d.ID, "question", "answer")
	}
	// One card of secondDeck is already studied and not due until later: it adds to studied, not to today's queue.
	studied := seedBasic(t, db, secondDeck.ID, "studied", "answer")
	var studiedCard store.Card
	if err := db.Where("note_id = ?", studied.ID).First(&studiedCard).Error; err != nil {
		t.Fatal(err)
	}
	due := time.Now().UTC().Add(72 * time.Hour)
	if err := db.Create(&store.CardState{CardID: studiedCard.ID, UserID: ownerID, State: "review", DueAt: &due}).Error; err != nil {
		t.Fatal(err)
	}
	want := map[string]deckQueueCount{
		ownerDeck.PublicID:  {DeckID: ownerDeck.PublicID, NewCount: 1, ReviewCount: 0, StudiedCount: 0, RemainingCount: 1},
		secondDeck.PublicID: {DeckID: secondDeck.PublicID, NewCount: 1, ReviewCount: 0, StudiedCount: 1, RemainingCount: 1},
	}

	request := func(withSession bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/decks/queue-counts", nil)
		if withSession {
			for _, cookie := range cookies {
				req.AddCookie(cookie)
			}
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}
	resp := request(true)
	if resp.Code != http.StatusOK {
		t.Fatalf("session status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body deckQueueCountsResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Decks) != len(want) {
		t.Fatalf("counts = %+v, want exactly the %d visible decks", body.Decks, len(want))
	}
	for _, item := range body.Decks {
		if item != want[item.DeckID] {
			t.Errorf("count for deck %s = %+v, want %+v", item.DeckID, item, want[item.DeckID])
		}
	}

	if got := request(false).Code; got != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d, want 401", got)
	}
}
