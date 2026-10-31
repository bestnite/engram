package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// seedNote creates a basic note under the deck and returns it (already carrying its external id).
func seedNote(t *testing.T, env *testEnv, deckID, ownerID uint64, front string) *store.Note {
	t.Helper()
	n := &store.Note{DeckID: deckID, NoteTypeID: store.Ptr(stockNoteTypeID(t, env.db, deckID, "basic")), CreatedBy: store.Ptr(ownerID)}
	if _, err := store.NewNoteStore(env.db).Create(context.Background(), n,
		map[string]any{"front": front, "back": "a"}); err != nil {
		t.Fatalf("create note: %v", err)
	}
	return n
}

// TestGetNoteReadsBeyondFirstPage is the AUDIT-01 regression: the edit page reads the target card directly by external id,
// so cards outside a single page are readable too - before the fix the edit page only pulled the first page and then looked, so anything past the 101st never opened.
func TestGetNoteReadsBeyondFirstPage(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "note_reader", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	key := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)
	router := env.router()

	first := seedNote(t, env, deck.ID, user.ID, "first")
	second := seedNote(t, env, deck.ID, user.ID, "second")

	// A single row per page: second is not on the first page (the list orders by created_at DESC, id DESC; second is on the first page, first is not).
	listPath := fmt.Sprintf("/api/v1/decks/%s/notes?page=1&per_page=1", deck.PublicID)
	status, raw := doJSON(t, router, http.MethodGet, listPath, key.Plaintext, "")
	if status != http.StatusOK {
		t.Fatalf("list status = %d, want 200 (body %s)", status, raw)
	}
	var listed struct {
		Notes []map[string]any `json:"notes"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("list response not JSON: %v (%s)", err, raw)
	}
	if len(listed.Notes) != 1 {
		t.Fatalf("page 1 has %d notes, want 1 (fixture assumes a single-item page)", len(listed.Notes))
	}
	onPage, _ := listed.Notes[0]["id"].(string)
	offPage := first.PublicID
	if onPage == offPage {
		offPage = second.PublicID
	}

	// The one not on the first page is still readable directly by id.
	status, raw = doJSON(t, router, http.MethodGet, "/api/v1/notes/"+offPage, key.Plaintext, "")
	if status != http.StatusOK {
		t.Fatalf("get off-page note status = %d, want 200 (body %s)", status, raw)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("get note response not JSON: %v (%s)", err, raw)
	}
	if got["id"] != offPage {
		t.Errorf("get note id = %v, want %q", got["id"], offPage)
	}
	if got["deck_id"] != deck.PublicID {
		t.Errorf("get note deck_id = %v, want %q", got["deck_id"], deck.PublicID)
	}
}

// TestNoteResponsesReturnStoredFieldHTML is the M17-49 acceptance: a choice note's option is one HTML string, and every
// note read path returns it unchanged instead of splitting it into lines. The editor saves what it loaded, so a reshaped
// response would delete the image and the formatting on the next save.
func TestNoteResponsesReturnStoredFieldHTML(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "note_html_reader", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	key := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil).Plaintext
	router := env.router()

	const optionHTML = `<img src="x.png"><b>bold option</b>`
	note := &store.Note{DeckID: deck.ID, NoteTypeID: store.Ptr(stockNoteTypeID(t, env.db, deck.ID, "choice_single")), CreatedBy: store.Ptr(user.ID)}
	if _, err := store.NewNoteStore(env.db).Create(context.Background(), note, map[string]any{
		"question": "Which one?",
		"options":  optionHTML,
		"answer":   "0",
	}); err != nil {
		t.Fatalf("create choice note: %v", err)
	}

	assertOptions := func(t *testing.T, raw []byte) {
		t.Helper()
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("response is not JSON: %v (%s)", err, raw)
		}
		fields, ok := got["fields"].(map[string]any)
		if !ok {
			t.Fatalf("fields is %T, want an object (response %s)", got["fields"], raw)
		}
		if fields["options"] != optionHTML {
			t.Errorf("fields.options = %#v, want the stored HTML string %#v", fields["options"], optionHTML)
		}
	}

	// Get one note.
	status, raw := doJSON(t, router, http.MethodGet, "/api/v1/notes/"+note.PublicID, key, "")
	if status != http.StatusOK {
		t.Fatalf("get note status = %d, want 200 (body %s)", status, raw)
	}
	assertOptions(t, raw)

	// List the deck's notes.
	status, raw = doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/decks/%s/notes?per_page=50", deck.PublicID), key, "")
	if status != http.StatusOK {
		t.Fatalf("list notes status = %d, want 200 (body %s)", status, raw)
	}
	var listed struct {
		Notes []json.RawMessage `json:"notes"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("list response is not JSON: %v (%s)", err, raw)
	}
	if len(listed.Notes) != 1 {
		t.Fatalf("list returned %d notes, want 1 (body %s)", len(listed.Notes), raw)
	}
	assertOptions(t, listed.Notes[0])

	// Search the deck's notes by plain text inside the option HTML.
	status, raw = doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/decks/%s/notes?q=bold", deck.PublicID), key, "")
	if status != http.StatusOK {
		t.Fatalf("search notes status = %d, want 200 (body %s)", status, raw)
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("search response is not JSON: %v (%s)", err, raw)
	}
	if len(listed.Notes) != 1 {
		t.Fatalf("search returned %d notes, want 1 (body %s)", len(listed.Notes), raw)
	}
	assertOptions(t, listed.Notes[0])
}

// TestGetNoteAuthorization covers the negative cases: unknown id, non-member, and a key with write scope only.
func TestGetNoteAuthorization(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "note_owner", store.RoleUser)
	outsider := seedUser(t, env.db, "note_outsider", store.RoleUser)
	deck, note := newDeckWithNote(t, env, owner.ID)
	router := env.router()
	ctx := context.Background()

	// Unknown id -> 404 not_found.
	readKey := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)
	status, raw := doJSON(t, router, http.MethodGet, "/api/v1/notes/does-not-exist", readKey.Plaintext, "")
	if status != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404 (body %s)", status, raw)
	}

	// A non-member reading a card of someone else's deck -> 403/404 (no existence leak).
	outsiderKey := seedKey(t, env.keys, outsider.ID, []string{store.ScopeRead}, nil)
	status, raw = doJSON(t, router, http.MethodGet, "/api/v1/notes/"+note.PublicID, outsiderKey.Plaintext, "")
	if status != http.StatusForbidden && status != http.StatusNotFound {
		t.Fatalf("outsider status = %d, want 403/404 (body %s)", status, raw)
	}
	if _, _, err := env.api.ListNotes(ctx, outsider.ID, deck.ID, store.NoteListOptions{}); err == nil {
		t.Error("outsider ListNotes succeeded, want an error (fixture sanity)")
	}

	// A key with write scope only calling a read endpoint -> 403 scope_required.
	writeKey := seedKey(t, env.keys, owner.ID, []string{store.ScopeWrite}, nil)
	status, raw = doJSON(t, router, http.MethodGet, "/api/v1/notes/"+note.PublicID, writeKey.Plaintext, "")
	if status != http.StatusForbidden {
		t.Fatalf("write-only key read status = %d, want 403 (body %s)", status, raw)
	}
}

// TestGetNoteAllowsReader is the positive case: a reader of a shared deck can read card content by id (whoever can see it can populate an edit form).
func TestGetNoteAllowsReader(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "read_owner", store.RoleUser)
	reader := seedUser(t, env.db, "read_reader", store.RoleUser)
	deck, note := newDeckWithNote(t, env, owner.ID)
	if err := store.NewGrantStore(env.db).Grant(context.Background(), deck.ID, reader.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant reader: %v", err)
	}
	got, err := env.api.GetNote(context.Background(), reader.ID, note.PublicID)
	if err != nil {
		t.Fatalf("reader GetNote error = %v, want nil", err)
	}
	if got.ID != note.ID {
		t.Errorf("reader GetNote id = %d, want %d", got.ID, note.ID)
	}
}
