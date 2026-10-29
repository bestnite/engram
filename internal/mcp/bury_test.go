package mcp

import (
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestBuryCardTool: bury_card shares the service method with REST POST /cards/:id/bury; the buried card leaves
// get_due_cards, and an unknown card is a tool error rather than a transport failure.
func TestBuryCardTool(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "bury-mcp")
	deck := seedDeck(t, db, u.ID)
	seedDueCardMCP(t, db, deck.ID)
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeReview})
	cs := connect(t, ts.URL, key)

	var card store.Card
	if err := db.Joins("JOIN notes ON notes.id = cards.note_id").Where("notes.deck_id = ?", deck.ID).First(&card).Error; err != nil {
		t.Fatalf("load card: %v", err)
	}
	cases := []struct {
		name    string
		cardID  string
		wantErr bool
	}{
		{name: "own card", cardID: card.PublicID},
		{name: "unknown card", cardID: "01a00000-0000-7000-8000-000000000000", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, isErr, text := callTool(t, cs, "bury_card", map[string]any{"card_id": tc.cardID})
			if isErr != tc.wantErr {
				t.Fatalf("bury_card isErr = %v (%s), want %v", isErr, text, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if out["card_id"] != tc.cardID || out["due_at"] == "" {
				t.Errorf("bury_card = %v, want the card and its new due time", out)
			}
			if got := dueDeckIDs(t, cs, map[string]any{"deck_id": deck.PublicID}); got[deck.PublicID] {
				t.Errorf("the buried card's deck still serves cards today: %v", got)
			}
		})
	}
}
