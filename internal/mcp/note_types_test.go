package mcp

import (
	"reflect"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

func TestNoteTypeToolsStockAndConfigRoundTrip(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "mcp_note_type_write")
	d := seedDeck(t, db, u.ID)
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeAdmin}))
	for _, name := range []string{"list_note_type_stocks", "list_answer_controls"} {
		out, bad, text := callTool(t, cs, name, map[string]any{})
		if bad || len(out) == 0 {
			t.Fatalf("%s: %v %s", name, out, text)
		}
	}
	args := map[string]any{"deck_id": d.PublicID, "stock_id": "anki:basic"}
	created, bad, text := callTool(t, cs, "create_note_type", args)
	if bad {
		t.Fatalf("create: %s", text)
	}
	got, bad, text := callTool(t, cs, "get_note_type", map[string]any{"deck_id": d.PublicID, "note_type_id": created["id"]})
	if bad || !reflect.DeepEqual(got["config"], created["config"]) {
		t.Fatalf("get: %v %s", got, text)
	}
	update := map[string]any{"deck_id": d.PublicID, "note_type_id": created["id"], "name": created["name"], "config": created["config"], "version": created["version"]}
	_, bad, text = callTool(t, cs, "update_note_type", update)
	if bad {
		t.Fatalf("update: %s", text)
	}
	_, bad, text = callTool(t, cs, "update_note_type", update)
	if !bad || !strings.Contains(text, "version_conflict") {
		t.Fatalf("stale update: %s", text)
	}
	_, bad, text = callTool(t, cs, "delete_note_type", map[string]any{"deck_id": d.PublicID, "note_type_id": created["id"]})
	if bad {
		t.Fatalf("delete: %s", text)
	}
	args["stock_id"] = "unknown"
	_, bad, text = callTool(t, cs, "create_note_type", args)
	if !bad || !strings.Contains(text, "invalid_request") {
		t.Fatalf("unknown stock: %s", text)
	}
}

// Acceptance for list_note_types and get_note_type: the list is scoped to one deck and returns the
// note type config an agent needs to write a note (field ids and names, templates), and both tools are
// open only to keys with the read scope.

func seedStockNoteType(t *testing.T, db *gorm.DB, deckID uint64) *store.NoteType {
	t.Helper()
	return createStockNoteType(t, db, deckID, "basic")
}

func TestListNoteTypesIsDeckScoped(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "mcp_note_types")
	deck := seedDeck(t, db, u.ID)
	other := seedDeck(t, db, u.ID)
	nt := seedStockNoteType(t, db, deck.ID)
	seedStockNoteType(t, db, other.ID)
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeRead}))

	out, isErr, text := callTool(t, cs, "list_note_types", map[string]any{"deck_id": deck.PublicID})
	if isErr {
		t.Fatalf("list_note_types error: %s", text)
	}
	types, _ := out["note_types"].([]any)
	if len(types) != 1 {
		t.Fatalf("list_note_types returned %d note types, want 1 for this deck", len(types))
	}
	entry, _ := types[0].(map[string]any)
	if entry["id"] != nt.PublicID || entry["name"] != "Basic" {
		t.Fatalf("note type entry = %v, want id %q name Basic", entry, nt.PublicID)
	}
	cfg, _ := entry["config"].(map[string]any)
	fields, _ := cfg["fields"].([]any)
	if len(fields) == 0 {
		t.Fatalf("note type config has no fields: %v", entry)
	}
	first, _ := fields[0].(map[string]any)
	if first["id"] == "" || first["name"] == "" {
		t.Fatalf("field entry = %v, want an id and a name", first)
	}
}

func TestGetNoteType(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "mcp_get_note_type")
	deck := seedDeck(t, db, u.ID)
	nt := seedStockNoteType(t, db, deck.ID)
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeRead}))

	out, isErr, text := callTool(t, cs, "get_note_type", map[string]any{
		"deck_id": deck.PublicID, "note_type_id": nt.PublicID,
	})
	if isErr {
		t.Fatalf("get_note_type error: %s", text)
	}
	if out["id"] != nt.PublicID || out["deck_id"] != deck.PublicID {
		t.Fatalf("get_note_type = %v, want id %q deck %q", out, nt.PublicID, deck.PublicID)
	}
	cfg, _ := out["config"].(map[string]any)
	if templates, _ := cfg["templates"].([]any); len(templates) == 0 {
		t.Fatalf("get_note_type config has no templates: %v", out)
	}

	// A note type id from another deck is not visible through this deck's endpoint.
	other := seedDeck(t, db, u.ID)
	foreign := seedStockNoteType(t, db, other.ID)
	_, isErr, text = callTool(t, cs, "get_note_type", map[string]any{
		"deck_id": deck.PublicID, "note_type_id": foreign.PublicID,
	})
	if !isErr || !strings.Contains(text, "not_found") {
		t.Fatalf("get_note_type with a foreign note type = (isErr=%v, %q), want not_found", isErr, text)
	}
}

func TestListNoteTypesRequiresReadScope(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "mcp_note_types_review")
	deck := seedDeck(t, db, u.ID)
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeReview}))

	_, isErr, text := callTool(t, cs, "list_note_types", map[string]any{"deck_id": deck.PublicID})
	if !isErr || !strings.Contains(text, "scope_required") {
		t.Fatalf("review-only list_note_types = (isErr=%v, %q), want scope_required", isErr, text)
	}
}
