package mcp

import (
	"encoding/json"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// seedNoteMCP creates a note of any kind plus one forward card (a new card), returning the note's public id. It differs
// from seedTaggedCardMCP only in the card kind and fields, which the choice-card contract needs.
func seedNoteMCP(t *testing.T, db *gorm.DB, deckID uint64, kind, fieldsJSON string) string {
	t.Helper()
	now := time.Now().UTC()
	noteType := createStockNoteType(t, db, deckID, kind)
	cfg, err := noteType.Config()
	if err != nil {
		t.Fatalf("decode note type %q: %v", kind, err)
	}
	payload, err := store.ParseFields(fieldsJSON)
	if err != nil {
		t.Fatalf("decode fixture fields of kind %q: %v", kind, err)
	}
	values, err := store.PayloadFieldValues(cfg, payload)
	if err != nil {
		t.Fatalf("convert fixture fields of kind %q: %v", kind, err)
	}
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("encode fixture fields of kind %q: %v", kind, err)
	}
	n := store.Note{DeckID: deckID, NoteTypeID: store.Ptr(noteType.ID), FieldsJSON: string(raw), TagsJSON: "[]", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	c := store.Card{NoteID: n.ID, Template: "forward", Ordinal: 0, CreatedAt: now}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	return n.PublicID
}

// TestGetDueCardsPublishesOptionOrder asserts the MCP transport carries the same option_order as REST: a permutation of
// the stored option indices for a choice card, and null for a card kind without option controls.
func TestGetDueCardsPublishesOptionOrder(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "option-order-mcp")
	deck := seedDeck(t, db, u.ID)
	choiceNote := seedNoteMCP(t, db, deck.ID, "choice_single",
		`{"question":"q","options":["a","b","c"],"answer":1}`)
	basicNote := seedNoteMCP(t, db, deck.ID, "basic", `{"front":"q","back":"a"}`)
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeReview})
	cs := connect(t, ts.URL, key)

	out, isErr, text := callTool(t, cs, "get_due_cards", map[string]any{"deck_id": deck.PublicID, "limit": 50})
	if isErr {
		t.Fatalf("get_due_cards error: %s", text)
	}
	cards, _ := out["cards"].([]any)
	if len(cards) != 2 {
		t.Fatalf("get_due_cards returned %d cards, want 2 (%v)", len(cards), out)
	}
	for _, raw := range cards {
		card, _ := raw.(map[string]any)
		noteID, _ := card["note_id"].(string)
		order, _ := card["option_order"].([]any)
		switch noteID {
		case choiceNote:
			if len(order) != 3 {
				t.Fatalf("choice card option_order = %v, want a permutation of 0..2", order)
			}
			seen := map[float64]bool{}
			for _, slot := range order {
				idx, ok := slot.(float64)
				if !ok || idx < 0 || idx > 2 || seen[idx] {
					t.Fatalf("choice card option_order = %v, want a permutation of 0..2", order)
				}
				seen[idx] = true
			}
		case basicNote:
			if card["option_order"] != nil {
				t.Errorf("basic card option_order = %v, want null", card["option_order"])
			}
		}
	}
}
