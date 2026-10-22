package mcp

import (
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// ---- : the tools behave like REST ----

// TestToolsMatchREST is the core acceptance: each tool and its same-named REST path produce the same result for the same input.
func TestToolsMatchREST(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "user")
	deck := seedDeck(t, db, u.ID)
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite, store.ScopeReview})
	cs := connect(t, ts.URL, key)
	base := ts.URL
	deckPath := fmt.Sprintf("/api/v1/decks/%s/notes", deck.PublicID)

	// Pre-create two cards with identical content via REST, for the later comparisons.
	body := `{"notes":[` +
		`{"kind":"basic","fields":{"front":"q1","back":"a1"},"external_ref":"n:1"},` +
		`{"kind":"basic","fields":{"front":"q2","back":"a2"},"external_ref":"n:2"}]}`
	if st, raw := rest(t, base, http.MethodPost, deckPath, key, body); st != 200 {
		t.Fatalf("seed import status = %d body %v", st, raw)
	}

	// list_decks
	mcpOut, _, _ := callTool(t, cs, "list_decks", map[string]any{})
	if _, restOut := rest(t, base, http.MethodGet, "/api/v1/decks", key, ""); !reflect.DeepEqual(mcpOut, restOut) {
		t.Errorf("list_decks MCP=%v REST=%v", mcpOut, restOut)
	}

	// search_notes (explicit paging, consistent on both sides)
	mcpOut, _, _ = callTool(t, cs, "search_notes", map[string]any{"deck_id": deck.PublicID, "page": 1, "per_page": 20})
	if _, restOut := rest(t, base, http.MethodGet, fmt.Sprintf("/api/v1/decks/%s/notes?page=1&per_page=20", deck.PublicID), key, ""); !reflect.DeepEqual(mcpOut, restOut) {
		t.Errorf("search_notes MCP=%v REST=%v", mcpOut, restOut)
	}

	// get_stats
	mcpOut, _, _ = callTool(t, cs, "get_stats", map[string]any{})
	if _, restOut := rest(t, base, http.MethodGet, "/api/v1/stats/summary", key, ""); !reflect.DeepEqual(mcpOut, restOut) {
		t.Errorf("get_stats MCP=%v REST=%v", mcpOut, restOut)
	}

	// export_deck: returns the deck package document, with manifest/notes/cards/preset.
	mcpOut, isErr, text := callTool(t, cs, "export_deck", map[string]any{"deck_id": deck.PublicID})
	if isErr {
		t.Fatalf("export_deck error: %s", text)
	}
	manifest, _ := mcpOut["manifest.json"].(map[string]any)
	if manifest == nil {
		t.Fatalf("export_deck did not return a package document: %v", mcpOut)
	}
	counts, _ := manifest["counts"].(map[string]any)
	if got := counts["notes"]; got != float64(2) {
		t.Errorf("package manifest counts.notes = %v, want 2", got)
	}
	if _, ok := mcpOut["notes.json"]; !ok {
		t.Errorf("package document is missing notes.json: %v", mcpOut)
	}

	// create_notes / import_deck (dry_run, produces the same counts under the same deck state)
	dry := map[string]any{"deck_id": deck.PublicID, "dry_run": true, "notes": []any{
		map[string]any{"kind": "basic", "fields": map[string]any{"front": "x", "back": "y"}, "external_ref": "n:3"},
	}}
	mcpOut, _, _ = callTool(t, cs, "create_notes", dry)
	if _, restOut := rest(t, base, http.MethodPost, deckPath, key, `{"dry_run":true,"notes":[{"kind":"basic","fields":{"front":"x","back":"y"},"external_ref":"n:3"}]}`); !reflect.DeepEqual(mcpOut, restOut) {
		t.Errorf("create_notes(dry_run) MCP=%v REST=%v", mcpOut, restOut)
	}

	// import_deck: accepts the document export_deck outputs; a dry_run preview writes nothing.
	pkgDoc, _, _ := callTool(t, cs, "export_deck", map[string]any{"deck_id": deck.PublicID, "include_progress": true})
	mcpOut, isErr, text = callTool(t, cs, "import_deck", map[string]any{"package": pkgDoc, "dry_run": true, "target": "new_deck"})
	if isErr {
		t.Fatalf("import_deck error: %s", text)
	}
	if mcpOut["dry_run"] != true || mcpOut["notes_created"] != float64(2) {
		t.Errorf("import_deck(dry_run) report = %v, want dry_run=true notes_created=2", mcpOut)
	}
	if _, ok := mcpOut["errors"]; !ok {
		t.Errorf("import_deck report is missing the errors field: %v", mcpOut)
	}

	// Get the note's public id for update/delete.
	_, notesOut := rest(t, base, http.MethodGet, fmt.Sprintf("/api/v1/decks/%s/notes?per_page=50", deck.PublicID), key, "")
	byRef := map[string]string{}
	for _, raw := range notesOut["notes"].([]any) {
		n := raw.(map[string]any)
		byRef[fmt.Sprint(n["external_ref"])] = n["id"].(string)
	}
	note1, note2 := byRef["n:1"], byRef["n:2"]

	// update_note: same content, so it should match once the dynamic fields are stripped.
	mcpUpd, _, _ := callTool(t, cs, "update_note", map[string]any{"note_id": note1, "kind": "basic", "fields": map[string]any{"front": "u", "back": "Y"}, "tags": []any{"t1"}})
	_, restUpd := rest(t, base, http.MethodPatch, fmt.Sprintf("/api/v1/notes/%s", note2), key, `{"kind":"basic","fields":{"front":"u","back":"Y"},"tags":["t1"]}`)
	if !reflect.DeepEqual(stripDynamic(mcpUpd), stripDynamic(restUpd)) {
		t.Errorf("update_note MCP=%v REST=%v", stripDynamic(mcpUpd), stripDynamic(restUpd))
	}

	// get_due_cards (the note is not deleted yet, so the queues should match on both sides; due_at depends on the call time, so normalize before comparing)
	mcpOut, _, _ = callTool(t, cs, "get_due_cards", map[string]any{"deck_id": deck.PublicID, "limit": 50})
	if _, restOut := rest(t, base, http.MethodGet, fmt.Sprintf("/api/v1/review/due?deck=%s&limit=50", deck.PublicID), key, ""); !reflect.DeepEqual(normalizeCards(mcpOut), normalizeCards(restOut)) {
		t.Errorf("get_due_cards MCP=%v REST=%v", normalizeCards(mcpOut), normalizeCards(restOut))
	}

	// submit_review: the two cards have identical content, so the results should match once the id dimension is stripped.
	_, exp := rest(t, base, http.MethodGet, fmt.Sprintf("/api/v1/export?deck=%s&format=json", deck.PublicID), key, "")
	cards := exp["cards"].([]any)
	if len(cards) < 2 {
		t.Fatalf("expected >=2 cards for review, got %d", len(cards))
	}
	var ids []string
	for _, raw := range cards {
		ids = append(ids, raw.(map[string]any)["card_id"].(string))
	}
	mcpRev, isErr, text := callTool(t, cs, "submit_review", map[string]any{"card_id": ids[0], "rating": 3, "expected_version": 0})
	if isErr {
		t.Fatalf("submit_review MCP error: %s", text)
	}
	_, restRev := rest(t, base, http.MethodPost, "/api/v1/review", key, fmt.Sprintf(`{"card_id":"%s","rating":3,"expected_version":0}`, ids[1]))
	if !reflect.DeepEqual(stripReviewIDs(mcpRev), stripReviewIDs(restRev)) {
		t.Errorf("submit_review MCP=%v REST=%v", stripReviewIDs(mcpRev), stripReviewIDs(restRev))
	}

	// delete_note (kept last, so the export and review queue above are not affected)
	mcpDel, _, _ := callTool(t, cs, "delete_note", map[string]any{"note_id": note1})
	_, restDel := rest(t, base, http.MethodDelete, fmt.Sprintf("/api/v1/notes/%s", note2), key, "")
	if !reflect.DeepEqual(stripDynamic(mcpDel), stripDynamic(restDel)) {
		t.Errorf("delete_note MCP=%v REST=%v", mcpDel, restDel)
	}
}

// stripDynamic removes fields that change with each write, for comparing two same-content objects.
func stripDynamic(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		switch k {
		case "id", "created_at", "updated_at", "external_ref":
			continue
		}
		out[k] = v
	}
	return out
}

// stripReviewIDs removes the fields in the review result that depend on the specific card and call time.
func stripReviewIDs(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		switch k {
		case "card_id", "review_id", "due_at":
			continue
		}
		out[k] = v
	}
	return out
}

// normalizeCards removes due_at, which depends on the call time, from the due cards and sorts by card_id, for comparing both sides.
func normalizeCards(m map[string]any) map[string]any {
	cards, _ := m["cards"].([]any)
	for _, raw := range cards {
		if c, ok := raw.(map[string]any); ok {
			delete(c, "due_at")
		}
	}
	sort.Slice(cards, func(i, j int) bool {
		return cards[i].(map[string]any)["card_id"].(string) < cards[j].(map[string]any)["card_id"].(string)
	})
	m["cards"] = cards
	return m
}

// ---- : multi-deck review scope ----

// seedDueCardMCP creates a note and one of its cards without writing card_states: the card is new and must appear in the queue.
func seedDueCardMCP(t *testing.T, db *gorm.DB, deckID uint64) {
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

// TestGetDueCardsDeckIDsMatchesREST asserts get_due_cards's deck_ids is the same reading as REST's repeated deck parameter;
// giving deck_id and deck_ids together returns a tool error (isErr).
func TestGetDueCardsDeckIDsMatchesREST(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "multideck")
	deckA := seedDeck(t, db, u.ID)
	deckB := seedDeck(t, db, u.ID)
	seedDueCardMCP(t, db, deckA.ID)
	seedDueCardMCP(t, db, deckB.ID)
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeReview})
	cs := connect(t, ts.URL, key)

	mcpOut, isErr, text := callTool(t, cs, "get_due_cards", map[string]any{
		"deck_ids": []any{deckA.PublicID, deckB.PublicID}, "limit": 50,
	})
	if isErr {
		t.Fatalf("get_due_cards(deck_ids) error: %s", text)
	}
	_, restOut := rest(t, ts.URL, http.MethodGet,
		fmt.Sprintf("/api/v1/review/due?deck=%s&deck=%s&limit=50", deckA.PublicID, deckB.PublicID), key, "")
	if !reflect.DeepEqual(normalizeCards(mcpOut), normalizeCards(restOut)) {
		t.Errorf("get_due_cards(deck_ids) MCP=%v REST=%v", normalizeCards(mcpOut), normalizeCards(restOut))
	}

	// Mutually exclusive: giving deck_id and deck_ids together is an argument error, and the client must see isErr.
	_, isErr, text = callTool(t, cs, "get_due_cards", map[string]any{
		"deck_id": deckA.PublicID, "deck_ids": []any{deckB.PublicID},
	})
	if !isErr {
		t.Fatalf("get_due_cards with both deck_id and deck_ids must fail")
	}
	if !strings.Contains(text, "invalid_request") {
		t.Errorf("mutual-exclusion error text = %q, want it to carry code invalid_request", text)
	}
}

// TestGetDueCardsRejectsMixedUnreadableDeckIDs covers the mixed-set negative case: when deck_ids mixes a deck the caller can read
// with someone else's private deck, the whole call fails, and it must not return only the readable deck's cards.
// MCP reuses api.DueCards' per-id permission check, the same
// reading as REST's TestDueCardsFailsWholeRequestForUnreadableDeck.
func TestGetDueCardsRejectsMixedUnreadableDeckIDs(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	owner := seedUser(t, db, "mixed-owner")
	stranger := seedUser(t, db, "mixed-stranger")
	readable := seedDeck(t, db, owner.ID)
	foreign := seedDeck(t, db, stranger.ID)
	seedDueCardMCP(t, db, readable.ID)
	seedDueCardMCP(t, db, foreign.ID)
	key := newKey(t, keys, owner.ID, []string{store.ScopeRead, store.ScopeReview})
	cs := connect(t, ts.URL, key)

	_, isErr, text := callTool(t, cs, "get_due_cards", map[string]any{
		"deck_ids": []any{readable.PublicID, foreign.PublicID}, "limit": 50,
	})
	if !isErr {
		t.Fatalf("get_due_cards with a readable+foreign deck_ids must fail the whole call")
	}
	// The failure must be permission-denied/not-found rather than an argument error — otherwise this case does not exercise the "whole request fails" reason.
	if !strings.Contains(text, api.CodeForbidden) && !strings.Contains(text, api.CodeNotFound) {
		t.Errorf("mixed deck_ids error text = %q, want code %s or %s",
			text, api.CodeForbidden, api.CodeNotFound)
	}
}

// ---- : create_deck shares its source with the REST deck creation ----

// errorCode takes the stable code from the REST uniform error envelope.
func errorCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

// errorMessage takes the stable message from the REST uniform error envelope.
func errorMessage(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	c, _ := e["message"].(string)
	return c
}

// TestCreateDeckMatchesREST is the core acceptance: create_deck and REST POST /decks give the same result for the same arguments,
// an empty name is refused with the shared code, preset_id=0 lands on the caller's Default preset, and a key without write
// scope is refused even when calling by name.
func TestCreateDeckMatchesREST(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "deckmaker")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)

	// Same arguments: one deck created via MCP, one via REST; after stripping dynamic fields they should be identical.
	mcpOut, isErr, text := callTool(t, cs, "create_deck", map[string]any{
		"name": "  Parity  ", "description": "same args",
	})
	if isErr {
		t.Fatalf("create_deck error: %s", text)
	}
	if mcpOut["name"] != "Parity" {
		t.Errorf("create_deck name = %v, want trimmed %q", mcpOut["name"], "Parity")
	}
	st, restOut := rest(t, ts.URL, http.MethodPost, "/api/v1/decks", key,
		`{"name":"Parity","description":"same args"}`)
	if st != http.StatusCreated {
		t.Fatalf("REST create status = %d body %v", st, restOut)
	}
	if !reflect.DeepEqual(stripDynamic(mcpOut), stripDynamic(restOut)) {
		t.Errorf("create_deck MCP=%v REST=%v", stripDynamic(mcpOut), stripDynamic(restOut))
	}

	// When preset_id is omitted it lands on the caller's Default preset (non-empty and identical on both sides).
	pid, ok := mcpOut["preset_id"].(string)
	if !ok || pid == "" {
		t.Fatalf("create_deck preset_id = %v, want the caller's Default preset public id", mcpOut["preset_id"])
	}
	var defaultPreset store.Preset
	if err := db.Where("owner_user_id = ? AND name = ?", u.ID, "Default").First(&defaultPreset).Error; err != nil {
		t.Fatalf("load Default preset: %v", err)
	}
	if pid != defaultPreset.PublicID {
		t.Errorf("create_deck preset_id = %s, want Default preset %s", pid, defaultPreset.PublicID)
	}

	// Empty name: both MCP and REST refuse it with the shared invalid_request code, and the English copy matches strictly.
	_, isErr, text = callTool(t, cs, "create_deck", map[string]any{"name": "   "})
	if !isErr || !strings.Contains(text, api.CodeInvalidRequest) {
		t.Errorf("create_deck(blank name) isErr=%v text=%q, want %s", isErr, text, api.CodeInvalidRequest)
	}
	st, restOut = rest(t, ts.URL, http.MethodPost, "/api/v1/decks", key, `{"name":"   "}`)
	if st != http.StatusBadRequest || errorCode(restOut) != api.CodeInvalidRequest {
		t.Errorf("REST blank name status=%d code=%q, want 400 %s", st, errorCode(restOut), api.CodeInvalidRequest)
	}
	if wantText := api.CodeInvalidRequest + ": " + errorMessage(restOut); text != wantText {
		t.Errorf("MCP blank name error text = %q, want %q", text, wantText)
	}

	// The old client's visibility field: a removed field is ignored, the deck is created as usual, and it no longer appears in the response.
	st, restOut = rest(t, ts.URL, http.MethodPost, "/api/v1/decks", key, `{"name":"legacy","visibility":"public"}`)
	if st != http.StatusCreated {
		t.Fatalf("REST create with a legacy visibility field status = %d, want 201 (body %v)", st, restOut)
	}
	if _, present := restOut["visibility"]; present {
		t.Errorf("REST create response still carries visibility: %v", restOut)
	}

	// A key without write scope is refused even when calling create_deck by name, with copy strictly matching REST.
	roKey := newKey(t, keys, u.ID, []string{store.ScopeRead})
	roCS := connect(t, ts.URL, roKey)
	_, isErr, text = callTool(t, roCS, "create_deck", map[string]any{"name": "nope"})
	if !isErr || !strings.Contains(text, api.CodeScopeRequired) {
		t.Errorf("create_deck with read-only key isErr=%v text=%q, want %s", isErr, text, api.CodeScopeRequired)
	}
	st, restOut = rest(t, ts.URL, http.MethodPost, "/api/v1/decks", roKey, `{"name":"nope"}`)
	if st != http.StatusForbidden || errorCode(restOut) != api.CodeScopeRequired {
		t.Errorf("REST scope_required status=%d code=%q, want 403 %s", st, errorCode(restOut), api.CodeScopeRequired)
	}
	if wantText := api.CodeScopeRequired + ": " + errorMessage(restOut); text != wantText {
		t.Errorf("MCP scope error text = %q, want %q", text, wantText)
	}
}
