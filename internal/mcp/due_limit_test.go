package mcp

import (
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestGetDueCardsDefaultLimitMatchesREST asserts the tool's omitted limit is the REST default, so one due
// request returns the same card set through both transports.
func TestGetDueCardsDefaultLimitMatchesREST(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "due-limit-mcp")
	deck := seedDeck(t, db, u.ID)
	const want = 5
	for i := 0; i < want; i++ {
		seedDueCardMCP(t, db, deck.ID)
	}
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeReview})
	cs := connect(t, ts.URL, key)

	out, isErr, text := callTool(t, cs, "get_due_cards", map[string]any{"deck_id": deck.PublicID})
	if isErr {
		t.Fatalf("get_due_cards without limit error: %s", text)
	}
	cards, _ := out["cards"].([]any)
	if len(cards) != want {
		t.Fatalf("get_due_cards without limit returned %d cards, want %d (the REST default)", len(cards), want)
	}

	status, restOut := rest(t, ts.URL, http.MethodGet, "/api/v1/review/due?deck="+deck.PublicID, key, "")
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/review/due = %d, want 200", status)
	}
	restCards, _ := restOut["cards"].([]any)
	if len(restCards) != want {
		t.Fatalf("REST due without limit returned %d cards, want %d", len(restCards), want)
	}
	if !sameCardIDs(cards, restCards) {
		t.Fatalf("MCP cards %v and REST cards %v differ, want the same card set", cardIDs(cards), cardIDs(restCards))
	}
}

// cardIDs returns the card_id of each card in arrival order.
func cardIDs(cards []any) []string {
	ids := make([]string, 0, len(cards))
	for _, raw := range cards {
		card, _ := raw.(map[string]any)
		id, _ := card["card_id"].(string)
		ids = append(ids, id)
	}
	return ids
}

// sameCardIDs reports whether two card lists carry the same ids, ignoring order.
func sameCardIDs(a, b []any) bool {
	left, right := cardIDs(a), cardIDs(b)
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]int, len(left))
	for _, id := range left {
		seen[id]++
	}
	for _, id := range right {
		seen[id]--
		if seen[id] < 0 {
			return false
		}
	}
	return true
}
