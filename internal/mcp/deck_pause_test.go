package mcp

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// dueDeckIDs returns the deck ids of the cards get_due_cards serves for args.
func dueDeckIDs(t *testing.T, cs *sdkmcp.ClientSession, args map[string]any) map[string]bool {
	t.Helper()
	out, isErr, text := callTool(t, cs, "get_due_cards", args)
	if isErr {
		t.Fatalf("get_due_cards(%v) error: %s", args, text)
	}
	got := map[string]bool{}
	cards, _ := out["cards"].([]any)
	for _, c := range cards {
		if m, ok := c.(map[string]any); ok {
			got[m["deck_id"].(string)] = true
		}
	}
	return got
}

// TestSetDeckPausedMatchesREST: set_deck_paused and REST PUT /decks/:id/paused share one service method, list_decks reports the flag,
// and get_due_cards drops the paused deck only when no deck is named.
func TestSetDeckPausedMatchesREST(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "pause-mcp")
	paused := seedDeck(t, db, u.ID)
	active := seedDeck(t, db, u.ID)
	seedDueCardMCP(t, db, paused.ID)
	seedDueCardMCP(t, db, active.ID)
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeReview})
	cs := connect(t, ts.URL, key)

	out, isErr, text := callTool(t, cs, "set_deck_paused", map[string]any{"deck_id": paused.PublicID, "paused": true})
	if isErr {
		t.Fatalf("set_deck_paused error: %s", text)
	}
	if want := map[string]any{"deck_id": paused.PublicID, "paused": true}; !reflect.DeepEqual(out, want) {
		t.Errorf("set_deck_paused = %v, want %v", out, want)
	}
	// The same request over REST is idempotent and answers the same body.
	status, restOut := rest(t, ts.URL, http.MethodPut, "/api/v1/decks/"+paused.PublicID+"/paused", key, `{"paused":true}`)
	if status != http.StatusOK || !reflect.DeepEqual(out, restOut) {
		t.Errorf("REST PUT paused = %d %v, want 200 %v", status, restOut, out)
	}

	list, isErr, text := callTool(t, cs, "list_decks", map[string]any{})
	if isErr {
		t.Fatalf("list_decks error: %s", text)
	}
	flags := map[string]any{}
	for _, d := range list["decks"].([]any) {
		m := d.(map[string]any)
		flags[m["id"].(string)] = m["paused"]
	}
	if flags[paused.PublicID] != true || flags[active.PublicID] != false {
		t.Errorf("list_decks paused flags = %v, want %s true and %s false", flags, paused.PublicID, active.PublicID)
	}

	if got := dueDeckIDs(t, cs, map[string]any{"limit": 50}); got[paused.PublicID] || !got[active.PublicID] {
		t.Errorf("get_due_cards default decks = %v, want only %s", got, active.PublicID)
	}
	if got := dueDeckIDs(t, cs, map[string]any{"deck_id": paused.PublicID}); !got[paused.PublicID] {
		t.Errorf("get_due_cards naming the paused deck = %v, want its cards", got)
	}

	if _, isErr, text = callTool(t, cs, "set_deck_paused", map[string]any{"deck_id": paused.PublicID, "paused": false}); isErr {
		t.Fatalf("set_deck_paused(false) error: %s", text)
	}
	if got := dueDeckIDs(t, cs, map[string]any{"limit": 50}); !got[paused.PublicID] {
		t.Errorf("get_due_cards default decks after resuming = %v, want %s back", got, paused.PublicID)
	}
}

// TestSetDeckPausedNegatives: a stranger gets forbidden, an unknown deck not_found, and a key without the review scope scope_required.
func TestSetDeckPausedNegatives(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	owner := seedUser(t, db, "pause-mcp-owner")
	stranger := seedUser(t, db, "pause-mcp-stranger")
	deck := seedDeck(t, db, owner.ID)
	ownerCS := connect(t, ts.URL, newKey(t, keys, owner.ID, []string{store.ScopeReview}))
	strangerCS := connect(t, ts.URL, newKey(t, keys, stranger.ID, []string{store.ScopeReview}))
	readCS := connect(t, ts.URL, newKey(t, keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}))

	cases := []struct {
		name     string
		cs       *sdkmcp.ClientSession
		args     map[string]any
		wantCode string
	}{
		{"stranger", strangerCS, map[string]any{"deck_id": deck.PublicID, "paused": true}, api.CodeForbidden},
		{"unknown deck", ownerCS, map[string]any{"deck_id": "01920000-0000-7000-8000-000000000000", "paused": true}, api.CodeNotFound},
		{"key without review scope", readCS, map[string]any{"deck_id": deck.PublicID, "paused": true}, "scope_required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, isErr, text := callTool(t, tc.cs, "set_deck_paused", tc.args)
			if !isErr || !strings.Contains(text, tc.wantCode) {
				t.Errorf("isErr=%v text=%q, want an error mentioning %s", isErr, text, tc.wantCode)
			}
		})
	}

	var rows int64
	if err := db.Model(&store.DeckPause{}).Count(&rows).Error; err != nil {
		t.Fatalf("count pauses: %v", err)
	}
	if rows != 0 {
		t.Errorf("pause rows after rejected calls = %d, want 0", rows)
	}
}
