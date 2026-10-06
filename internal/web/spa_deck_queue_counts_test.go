package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

func TestSPADeckQueueCountsUsesVisibleDecksAndSessionOnly(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	ownerDeck := seedDeck(t, db, ownerID, "owner-deck")
	publicDeck := seedDeck(t, db, ownerID, "public-deck")
	if err := store.NewDeckStore(db).SetVisibility(t.Context(), ownerID, publicDeck.ID, store.DeckVisibilityPublic); err != nil {
		t.Fatal(err)
	}
	for _, d := range []*store.Deck{ownerDeck, publicDeck} {
		seedBasic(t, db, d.ID, "question", "answer")
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
	seen := map[uint64]bool{}
	for _, item := range body.Decks {
		seen[item.DeckID] = true
		if item.NewCount != 1 || item.ReviewCount != 0 {
			t.Errorf("count for deck %d = %+v, want new=1 review=0", item.DeckID, item)
		}
	}
	if !seen[ownerDeck.ID] || !seen[publicDeck.ID] {
		t.Fatalf("visible decks missing from counts: %+v", body)
	}

	if got := request(false).Code; got != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d, want 401", got)
	}
}
