package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// deckDescriptionBody is the part of DeckResponse the description cases read; raw keeps every key of the entry.
type deckDescriptionBody struct {
	ID              string `json:"id"`
	Description     string `json:"description"`
	DescriptionText string `json:"description_text"`
	raw             map[string]json.RawMessage
}

// TestDeckResponseDescriptionIsPlainText pins the description contract: the stored text comes back unchanged, markup
// included, because every client shows it as text; description_text is the same text on one line; and no rendered
// variant exists for a client to inject into a page.
func TestDeckResponseDescriptionIsPlainText(t *testing.T) {
	cases := []struct {
		name     string
		source   string
		wantText string
	}{
		{name: "multi-line text", source: "First line\n\nSecond   line", wantText: "First line Second line"},
		{name: "markup is text", source: "<p>a <b>tag</b></p><script>x()</script>", wantText: "<p>a <b>tag</b></p><script>x()</script>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, 60, 60)
			owner := seedUser(t, env.db, "desc_owner_"+safeName(tc.name), store.RoleUser)
			deck := seedDeck(t, env.db, owner.ID)
			if err := env.db.WithContext(context.Background()).Model(&store.Deck{}).Where("id = ?", deck.ID).
				Update("description", tc.source).Error; err != nil {
				t.Fatalf("store deck description: %v", err)
			}
			key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)

			body := deckDescriptionFromList(t, env, key.Plaintext, deck.PublicID)
			if body.Description != tc.source {
				t.Errorf("description = %q, want the stored text %q", body.Description, tc.source)
			}
			if body.DescriptionText != tc.wantText {
				t.Errorf("description_text = %q, want %q", body.DescriptionText, tc.wantText)
			}
			for _, removed := range []string{"description_html", "description_markdown"} {
				if _, ok := body.raw[removed]; ok {
					t.Errorf("deck response still carries %q", removed)
				}
			}
		})
	}
}

// deckDescriptionFromList reads GET /api/v1/decks and returns the entry of one deck; the API has no single-deck GET, and the
// SPA's deck detail page takes its deck object from this list as well.
func deckDescriptionFromList(t *testing.T, env *testEnv, plaintext, publicID string) deckDescriptionBody {
	t.Helper()
	status, raw := doJSON(t, env.router(), http.MethodGet, "/api/v1/decks", plaintext, "")
	if status != http.StatusOK {
		t.Fatalf("GET decks = %d, want 200 (body %s)", status, raw)
	}
	var list struct {
		Decks []map[string]json.RawMessage `json:"decks"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode deck list: %v (%s)", err, raw)
	}
	for _, entry := range list.Decks {
		encoded, _ := json.Marshal(entry)
		var item deckDescriptionBody
		if err := json.Unmarshal(encoded, &item); err != nil {
			t.Fatalf("decode deck entry: %v", err)
		}
		if item.ID == publicID {
			item.raw = entry
			return item
		}
	}
	t.Fatalf("deck %s missing from the list response %s", publicID, raw)
	return deckDescriptionBody{}
}
