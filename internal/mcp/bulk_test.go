package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
	gorm "gorm.io/gorm"
)

// ---- the bulk_notes tool and create_notes addressing by note_id ----

// toMap turns a service layer response body into a JSON map, for comparing field by field with the MCP structured content.
func toMap(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return m
}

// noteIDsInDeck takes the public ids of the deck's notes in ascending primary key order, so each side can take one for comparison.
func noteIDsInDeck(t *testing.T, db *gorm.DB, deckID uint64) []string {
	t.Helper()
	var notes []store.Note
	if err := db.Where("deck_id = ?", deckID).Order("id").Find(&notes).Error; err != nil {
		t.Fatalf("list notes: %v", err)
	}
	ids := make([]string, 0, len(notes))
	for _, n := range notes {
		ids = append(ids, n.PublicID)
	}
	return ids
}

// TestBulkNotesMatchesREST is the core acceptance: bulk_notes and a direct call to api.BulkNotes (the service method behind REST
// POST /notes/bulk) produce the same response body for the same input, and really rewrite tags_json in the database;
// submitting the same request a second time gives affected=0 (idempotent).
func TestBulkNotesMatchesREST(t *testing.T) {
	apiSrv, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "bulkuser")
	deck := seedDeck(t, db, u.ID)
	// Two notes with identical content: one goes through MCP, one through a direct service call, comparing the results on both sides.
	seedDueCardMCP(t, db, deck.ID)
	seedDueCardMCP(t, db, deck.ID)
	ids := noteIDsInDeck(t, db, deck.ID)
	if len(ids) != 2 {
		t.Fatalf("seeded notes = %d, want 2", len(ids))
	}
	n1, n2 := ids[0], ids[1]

	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeWrite}))

	// First add_tags: each side should affect one row.
	mcpOut, isErr, text := callTool(t, cs, "bulk_notes", map[string]any{
		"action": "add_tags", "note_ids": []any{n1}, "tags": []any{"t1"},
	})
	if isErr {
		t.Fatalf("bulk_notes(add_tags) error: %s", text)
	}
	direct, err := apiSrv.BulkNotes(context.Background(), u.ID, nil, api.BulkNotesInput{
		Action: "add_tags", NoteIDs: []string{n2}, Tags: []string{"t1"},
	})
	if err != nil {
		t.Fatalf("direct BulkNotes: %v", err)
	}
	if !reflect.DeepEqual(mcpOut, toMap(t, direct)) {
		t.Errorf("bulk_notes(add_tags) MCP=%v direct=%v", mcpOut, toMap(t, direct))
	}
	if mcpOut["affected"] != float64(1) {
		t.Errorf("bulk_notes(add_tags) affected = %v, want 1", mcpOut["affected"])
	}

	// Read back tags_json from the database: both notes should carry t1.
	noteStore := store.NewNoteStore(db)
	for _, id := range ids {
		n, err := noteStore.ByPublicID(context.Background(), id)
		if err != nil {
			t.Fatalf("reload note %s: %v", id, err)
		}
		if tags, _ := store.ParseTags(n.TagsJSON); !containsStr(tags, "t1") {
			t.Errorf("note %s tags = %v, want to contain t1", id, tags)
		}
	}

	// Submit the same request again: affected goes to zero on both sides (idempotent).
	mcpOut, _, _ = callTool(t, cs, "bulk_notes", map[string]any{
		"action": "add_tags", "note_ids": []any{n1}, "tags": []any{"t1"},
	})
	direct, err = apiSrv.BulkNotes(context.Background(), u.ID, nil, api.BulkNotesInput{
		Action: "add_tags", NoteIDs: []string{n2}, Tags: []string{"t1"},
	})
	if err != nil {
		t.Fatalf("direct BulkNotes (repeat): %v", err)
	}
	if !reflect.DeepEqual(mcpOut, toMap(t, direct)) {
		t.Errorf("bulk_notes(repeat) MCP=%v direct=%v", mcpOut, toMap(t, direct))
	}
	if mcpOut["affected"] != float64(0) {
		t.Errorf("bulk_notes(repeat) affected = %v, want 0", mcpOut["affected"])
	}
}

// containsStr reports whether a string slice contains the target value (a helper for this package's tests).
func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestBulkNotesHiddenFromReadKey asserts a read-only key does not see bulk_notes in tools/list,
// and that calling it by name returns scope_required (the re-check logic covers new tools).
func TestBulkNotesHiddenFromReadKey(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "readonlybulk")
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeRead}))

	for _, name := range toolNames(t, cs) {
		if name == "bulk_notes" {
			t.Fatalf("read-only key must not see bulk_notes in tools/list")
		}
	}
	_, isErr, text := callTool(t, cs, "bulk_notes", map[string]any{"action": "delete", "note_ids": []any{"x"}})
	if !isErr || !strings.Contains(text, api.CodeScopeRequired) {
		t.Fatalf("hard-calling bulk_notes with a read-only key: isErr=%v text=%q, want %s", isErr, text, api.CodeScopeRequired)
	}
}

// TestCreateNotesByNoteIDRewritesInPlace covers the second part: when an item of create_notes carries note_id
// it rewrites the existing note's fields in place, keeping the card id and review progress; when note_id and external_ref are both given, that row reports an errors[] entry.
func TestCreateNotesByNoteIDRewritesInPlace(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "byid")
	deck := seedDeck(t, db, u.ID)
	seedDueCardMCP(t, db, deck.ID)

	var note store.Note
	if err := db.Where("deck_id = ?", deck.ID).First(&note).Error; err != nil {
		t.Fatalf("load seeded note: %v", err)
	}
	var card store.Card
	if err := db.Where("note_id = ?", note.ID).First(&card).Error; err != nil {
		t.Fatalf("load seeded card: %v", err)
	}
	due := time.Now().UTC()
	if err := db.Create(&store.CardState{
		CardID: card.ID, UserID: u.ID, State: "review", DueAt: &due, Reps: 4, Lapses: 2,
	}).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}

	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeWrite}))

	out, isErr, text := callTool(t, cs, "create_notes", map[string]any{
		"deck_id": deck.PublicID,
		"notes": []any{map[string]any{
			"note_id": note.PublicID, "kind": "basic",
			"fields": map[string]any{"front": "new", "back": "newA"}, "tags": []any{"t1"},
		}},
	})
	if isErr {
		t.Fatalf("create_notes(note_id) error: %s", text)
	}
	if out["updated"] != float64(1) || out["created"] != float64(0) {
		t.Errorf("create_notes(note_id) updated/created = %v/%v, want 1/0", out["updated"], out["created"])
	}

	// The fields were rewritten in place.
	updated, err := store.NewNoteStore(db).ByID(context.Background(), note.ID)
	if err != nil {
		t.Fatalf("reload note: %v", err)
	}
	if fields, _ := store.ParseFields(updated.FieldsJSON); fields["front"] != "new" || fields["back"] != "newA" {
		t.Errorf("note fields = %v, want the imported values", fields)
	}

	// The card id and review progress must be kept verbatim.
	var cards []store.Card
	if err := db.Where("note_id = ?", note.ID).Find(&cards).Error; err != nil {
		t.Fatalf("reload cards: %v", err)
	}
	if len(cards) != 1 || cards[0].ID != card.ID {
		t.Fatalf("cards after in-place rewrite = %+v, want the original card %d", cards, card.ID)
	}
	var state store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", card.ID, u.ID).First(&state).Error; err != nil {
		t.Fatalf("reload card state: %v", err)
	}
	if state.Reps != 4 || state.Lapses != 2 {
		t.Errorf("card state = (reps %d, lapses %d), want (4, 2)", state.Reps, state.Lapses)
	}

	// note_id and external_ref both given: that row reports one row error, and nothing else changes.
	out, isErr, text = callTool(t, cs, "create_notes", map[string]any{
		"deck_id": deck.PublicID,
		"notes": []any{map[string]any{
			"note_id": note.PublicID, "kind": "basic",
			"fields": map[string]any{"front": "z", "back": "z"}, "external_ref": "e:1",
		}},
	})
	if isErr {
		t.Fatalf("create_notes(note_id+external_ref) transport error: %s", text)
	}
	errs, _ := out["errors"].([]any)
	if len(errs) != 1 {
		t.Fatalf("create_notes(note_id+external_ref) errors = %v, want exactly one row error", out["errors"])
	}
	if out["updated"] != float64(0) || out["created"] != float64(0) {
		t.Errorf("create_notes(note_id+external_ref) updated/created = %v/%v, want 0/0", out["updated"], out["created"])
	}
	reloaded, err := store.NewNoteStore(db).ByID(context.Background(), note.ID)
	if err != nil {
		t.Fatalf("reload note after rejected row: %v", err)
	}
	if fields, _ := store.ParseFields(reloaded.FieldsJSON); fields["front"] != "new" {
		t.Errorf("rejected row must not change the note: fields = %v, want front=new", fields)
	}
}

// ---- the bulk_decks tool ----

// TestBulkDecksDeletesOwnedAndReportsShared is the core acceptance for bulk_decks: through MCP the caller's own deck is
// deleted while a deck only shared with them is reported as forbidden and survives, matching the REST contract.
func TestBulkDecksDeletesOwnedAndReportsShared(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "bulkdecks")
	own := seedDeck(t, db, u.ID)

	// Another user's deck granted to the caller: visible, but the caller does not own it.
	owner := seedUser(t, db, "bulkdecksowner")
	shared := seedDeck(t, db, owner.ID)
	grant := store.DeckGrant{DeckID: shared.ID, UserID: u.ID, Role: store.RoleEditor, CreatedAt: time.Now().UTC()}
	if err := db.Create(&grant).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}

	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeWrite}))
	out, isErr, text := callTool(t, cs, "bulk_decks", map[string]any{
		"action":   "delete",
		"deck_ids": []any{own.PublicID, shared.PublicID},
	})
	if isErr {
		t.Fatalf("bulk_decks error: %s", text)
	}
	if out["affected"] != float64(1) {
		t.Errorf("affected = %v, want 1", out["affected"])
	}
	skipped, _ := out["skipped"].([]any)
	if len(skipped) != 1 {
		t.Fatalf("skipped = %v, want one entry", out["skipped"])
	}
	entry, _ := skipped[0].(map[string]any)
	if entry["deck_id"] != shared.PublicID || entry["code"] != api.CodeForbidden {
		t.Errorf("skipped[0] = %v, want deck_id=%s code=%s", entry, shared.PublicID, api.CodeForbidden)
	}

	decks := store.NewDeckStore(db)
	if _, err := decks.ByID(context.Background(), own.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("owned deck survives the bulk delete: err = %v, want gorm.ErrRecordNotFound", err)
	}
	if _, err := decks.ByID(context.Background(), shared.ID); err != nil {
		t.Errorf("shared deck was deleted: %v", err)
	}
}
