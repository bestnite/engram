package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// deckDescriptionBody is the part of DeckResponse the description rendering cases read.
type deckDescriptionBody struct {
	ID                  string `json:"id"`
	Description         string `json:"description"`
	DescriptionMarkdown bool   `json:"description_markdown"`
	DescriptionHTML     string `json:"description_html"`
	DescriptionText     string `json:"description_text"`
}

// TestDeckResponseDescriptionRendering pins the flag switch: the stored text stays the source, while DescriptionHTML is the
// Markdown rendering when the flag is set and the stored HTML passed through when it is not. DescriptionText is the plain
// text of whichever of the two DescriptionHTML holds, so a list that prints the description never prints markup.
func TestDeckResponseDescriptionRendering(t *testing.T) {
	cases := []struct {
		name       string
		source     string
		markdown   bool
		wantHTML   []string
		denyHTML   []string
		wantSource string
		wantText   string
	}{
		{
			name:       "HTML description passes through",
			source:     "<p>plain <strong>HTML</strong></p>",
			markdown:   false,
			wantHTML:   []string{"<p>plain <strong>HTML</strong></p>"},
			denyHTML:   []string{"<h1>"},
			wantSource: "<p>plain <strong>HTML</strong></p>",
			wantText:   "plain HTML",
		},
		{
			name:       "Markdown description renders",
			source:     "# Heading\n\n**bold**",
			markdown:   true,
			wantHTML:   []string{"<h1>Heading</h1>", "<strong>bold</strong>"},
			denyHTML:   []string{"# Heading", "**bold**"},
			wantSource: "# Heading\n\n**bold**",
			wantText:   "Heading bold",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, 60, 60)
			owner := seedUser(t, env.db, "desc_owner_"+safeName(tc.name), store.RoleUser)
			deck := seedDeck(t, env.db, owner.ID)
			if err := env.db.WithContext(context.Background()).Model(&store.Deck{}).Where("id = ?", deck.ID).
				Updates(map[string]any{"description": tc.source, "description_markdown": tc.markdown}).Error; err != nil {
				t.Fatalf("store deck description: %v", err)
			}
			key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)

			body := deckDescriptionFromList(t, env, key.Plaintext, deck.PublicID)
			if body.Description != tc.wantSource {
				t.Errorf("description = %q, want the stored source %q", body.Description, tc.wantSource)
			}
			if body.DescriptionMarkdown != tc.markdown {
				t.Errorf("description_markdown = %v, want %v", body.DescriptionMarkdown, tc.markdown)
			}
			for _, want := range tc.wantHTML {
				if !strings.Contains(body.DescriptionHTML, want) {
					t.Errorf("description_html = %q, want it to contain %q", body.DescriptionHTML, want)
				}
			}
			for _, deny := range tc.denyHTML {
				if strings.Contains(body.DescriptionHTML, deny) {
					t.Errorf("description_html = %q, must not contain %q", body.DescriptionHTML, deny)
				}
			}
			if body.DescriptionText != tc.wantText {
				t.Errorf("description_text = %q, want %q", body.DescriptionText, tc.wantText)
			}
		})
	}
}

// TestDeckResponseDescriptionScriptStaysData asserts an HTML description carrying a script is delivered verbatim as a JSON
// field and never inlined into the SPA shell: the client can only put it inside the sandboxed frame.
func TestDeckResponseDescriptionScriptStaysData(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "desc_script_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	const payload = `<p>intro</p><script>window.__deck_description_ran = true</script>`
	if err := env.db.WithContext(context.Background()).Model(&store.Deck{}).Where("id = ?", deck.ID).
		Updates(map[string]any{"description": payload}).Error; err != nil {
		t.Fatalf("store deck description: %v", err)
	}
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)

	body := deckDescriptionFromList(t, env, key.Plaintext, deck.PublicID)
	if body.DescriptionHTML != payload {
		t.Errorf("description_html = %q, want the stored HTML verbatim", body.DescriptionHTML)
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
		Decks []deckDescriptionBody `json:"decks"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode deck list: %v (%s)", err, raw)
	}
	for _, item := range list.Decks {
		if item.ID == publicID {
			return item
		}
	}
	t.Fatalf("deck %s missing from the list response %s", publicID, raw)
	return deckDescriptionBody{}
}
