package mcp

import (
	"context"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestNoteToolsReturnStoredFieldHTML covers the MCP note reads after the response stopped reshaping fields: get_note and
// search_notes both return a choice note's option as the one stored HTML string, so a client round-trips it unchanged.
func TestNoteToolsReturnStoredFieldHTML(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "note_html_reader")
	deck := seedDeck(t, db, u.ID)
	noteType, err := store.NewNoteStore(db).EnsureStockNoteType(context.Background(), deck.ID, "choice_single")
	if err != nil {
		t.Fatalf("ensure choice_single note type: %v", err)
	}
	const optionHTML = `<img src="x.png"><b>bold option</b>`
	note := &store.Note{DeckID: deck.ID, NoteTypeID: store.Ptr(noteType.ID), CreatedBy: store.Ptr(u.ID)}
	if _, err := store.NewNoteStore(db).Create(context.Background(), note, map[string]any{
		"question": "Which one?",
		"options":  optionHTML,
		"answer":   "0",
	}); err != nil {
		t.Fatalf("create choice note: %v", err)
	}
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeRead}))

	assertOptions := func(t *testing.T, got any) {
		t.Helper()
		out, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("note = %T, want an object", got)
		}
		fields, ok := out["fields"].(map[string]any)
		if !ok {
			t.Fatalf("fields = %T, want an object", out["fields"])
		}
		if fields["options"] != optionHTML {
			t.Errorf("fields.options = %#v, want the stored HTML string %#v", fields["options"], optionHTML)
		}
	}

	out, isErr, text := callTool(t, cs, "get_note", map[string]any{"note_id": note.PublicID})
	if isErr {
		t.Fatalf("get_note returned error: %s", text)
	}
	assertOptions(t, out)

	listed, isErr, text := callTool(t, cs, "search_notes", map[string]any{"deck_id": deck.PublicID, "q": "bold"})
	if isErr {
		t.Fatalf("search_notes returned error: %s", text)
	}
	notes, ok := listed["notes"].([]any)
	if !ok || len(notes) != 1 {
		t.Fatalf("search_notes notes = %#v, want one note", listed["notes"])
	}
	assertOptions(t, notes[0])
}

// TestGetNoteToolReadsByPublicID asserts get_note reads a single card by public id,
// returning not_found for an unknown id; same source as REST `GET /notes/:id` (the same service method).
func TestGetNoteToolReadsByPublicID(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "note_reader")
	deck := seedDeck(t, db, u.ID)
	note := &store.Note{DeckID: deck.ID, NoteTypeID: store.Ptr(stockNoteTypeID(t, db, deck.ID, "basic")), CreatedBy: store.Ptr(u.ID)}
	if _, err := store.NewNoteStore(db).Create(context.Background(), note,
		map[string]any{"front": "q", "back": "a"}); err != nil {
		t.Fatalf("create note: %v", err)
	}
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeRead}))

	out, isErr, text := callTool(t, cs, "get_note", map[string]any{"note_id": note.PublicID})
	if isErr {
		t.Fatalf("get_note returned error: %s", text)
	}
	if out["id"] != note.PublicID {
		t.Errorf("get_note id = %v, want %q", out["id"], note.PublicID)
	}
	if out["deck_id"] != deck.PublicID {
		t.Errorf("get_note deck_id = %v, want %q", out["deck_id"], deck.PublicID)
	}

	_, isErr, text = callTool(t, cs, "get_note", map[string]any{"note_id": "missing-note"})
	if !isErr || !strings.Contains(text, "not_found") {
		t.Fatalf("get_note unknown id = (isErr %v, text %q), want a not_found error", isErr, text)
	}
}
