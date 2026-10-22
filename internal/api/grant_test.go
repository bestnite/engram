package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// newDeckWithNote creates a deck and a basic note and returns both.
func newDeckWithNote(t *testing.T, env *testEnv, ownerID uint64) (*store.Deck, *store.Note) {
	t.Helper()
	deck := seedDeck(t, env.db, ownerID)
	note := &store.Note{DeckID: deck.ID, Kind: "basic", CreatedBy: store.Ptr(ownerID)}
	if _, err := store.NewNoteStore(env.db).Create(context.Background(), note,
		map[string]any{"front": "q", "back": "a"}); err != nil {
		t.Fatalf("create note: %v", err)
	}
	return deck, note
}

// serviceCode pulls the stable code out of an error, for asserting the unauthorized branches.
func serviceCode(t *testing.T, err error) (int, string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
	se := asServiceError(err)
	return se.Status, se.Code
}

// TestReaderCannotModifyNote is the negative case: a reader can read and review but cannot change card content.
func TestReaderCannotModifyNote(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "reader_owner", store.RoleUser)
	reader := seedUser(t, env.db, "reader_user", store.RoleUser)
	deck, note := newDeckWithNote(t, env, owner.ID)

	grants := store.NewGrantStore(env.db)
	if err := grants.Grant(context.Background(), deck.ID, reader.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant reader: %v", err)
	}
	ctx := context.Background()

	// Positive: a reader can list cards (can read).
	if _, _, err := env.api.ListNotes(ctx, reader.ID, deck.ID, store.NoteListOptions{}); err != nil {
		t.Fatalf("reader ListNotes error = %v, want nil", err)
	}

	// Negative: a reader updating card content is rejected, code = insufficient_role.
	_, err := env.api.UpdateNote(ctx, reader.ID, note.ID, nil, UpdateNoteInput{
		Kind:   "basic",
		Fields: map[string]any{"front": "hacked", "back": "hacked"},
	})
	status, code := serviceCode(t, err)
	if status != http.StatusForbidden || code != CodeInsufficientRole {
		t.Fatalf("reader UpdateNote = (%d,%q), want (403,%q)", status, code, CodeInsufficientRole)
	}

	// Negative: a reader deleting a card is likewise rejected.
	if _, err := env.api.DeleteNote(ctx, reader.ID, note.ID, nil); err != nil {
		if _, code := serviceCode(t, err); code != CodeInsufficientRole {
			t.Fatalf("reader DeleteNote code = %q, want %q", code, CodeInsufficientRole)
		}
	} else {
		t.Fatalf("reader DeleteNote succeeded, want insufficient_role")
	}

	// Confirm the content was not changed.
	got, err := store.NewNoteStore(env.db).ByID(ctx, note.ID)
	if err != nil {
		t.Fatalf("reload note: %v", err)
	}
	fields, _ := store.ParseFields(got.FieldsJSON)
	if fields["front"] == "hacked" {
		t.Fatalf("reader mutated note content, want unchanged")
	}
}

// TestEditorCannotChangeDeckSettingsOrGrants is the negative case:
// an editor can change card content but not deck settings (owner level) or grants.
func TestEditorCannotChangeDeckSettingsOrGrants(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "editor_owner", store.RoleUser)
	editor := seedUser(t, env.db, "editor_user", store.RoleUser)
	deck, note := newDeckWithNote(t, env, owner.ID)

	grants := store.NewGrantStore(env.db)
	if err := grants.Grant(context.Background(), deck.ID, editor.ID, store.RoleEditor, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant editor: %v", err)
	}
	ctx := context.Background()

	// Positive: an editor can change card content.
	if _, err := env.api.UpdateNote(ctx, editor.ID, note.ID, nil, UpdateNoteInput{
		Kind:   "basic",
		Fields: map[string]any{"front": "edited", "back": "edited"},
	}); err != nil {
		t.Fatalf("editor UpdateNote error = %v, want nil", err)
	}

	// Negative: an editor falls short of the owner threshold (deck settings / grants are owner level), code = insufficient_role.
	_, err := env.api.RequireDeckRole(ctx, editor.ID, deck.ID, store.RoleOwner)
	status, code := serviceCode(t, err)
	if status != http.StatusForbidden || code != CodeInsufficientRole {
		t.Fatalf("editor RequireDeckRole(owner) = (%d,%q), want (403,%q)", status, code, CodeInsufficientRole)
	}
}

// TestUnauthorizedCannotSeeDeck covers "an unauthorized user cannot see the deck": a user with no grant at all is rejected even for reading (403 forbidden).
func TestUnauthorizedCannotSeeDeck(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "stranger_owner", store.RoleUser)
	stranger := seedUser(t, env.db, "stranger_user", store.RoleUser)
	deck, _ := newDeckWithNote(t, env, owner.ID)
	ctx := context.Background()

	_, err := env.api.RequireDeckRole(ctx, stranger.ID, deck.ID, store.RoleReader)
	status, code := serviceCode(t, err)
	if status != http.StatusForbidden || code != CodeForbidden {
		t.Fatalf("stranger RequireDeckRole = (%d,%q), want (403,%q)", status, code, CodeForbidden)
	}

	if _, _, err := env.api.ListNotes(ctx, stranger.ID, deck.ID, store.NoteListOptions{}); err == nil {
		t.Fatalf("stranger ListNotes succeeded, want forbidden")
	}
}

// TestRevokeTakesEffectImmediately covers "revocation applies immediately": the next request after revocation is rejected.
func TestRevokeTakesEffectImmediately(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "revoke_owner", store.RoleUser)
	member := seedUser(t, env.db, "revoke_user", store.RoleUser)
	deck, _ := newDeckWithNote(t, env, owner.ID)
	ctx := context.Background()
	grants := store.NewGrantStore(env.db)

	if err := grants.Grant(ctx, deck.ID, member.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, err := env.api.RequireDeckRole(ctx, member.ID, deck.ID, store.RoleReader); err != nil {
		t.Fatalf("granted read denied: %v", err)
	}

	// After revocation, the very same request is judged 403.
	if err := grants.Revoke(ctx, deck.ID, member.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err := env.api.RequireDeckRole(ctx, member.ID, deck.ID, store.RoleReader)
	status, code := serviceCode(t, err)
	if status != http.StatusForbidden || code != CodeForbidden {
		t.Fatalf("after revoke = (%d,%q), want (403,%q)", status, code, CodeForbidden)
	}
}

// TestHTTPReaderUpdateReturnsStableCode confirms at the HTTP layer that the unauthorized response body carries a stable English code.
func TestHTTPReaderUpdateReturnsStableCode(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "http_owner", store.RoleUser)
	reader := seedUser(t, env.db, "http_reader", store.RoleUser)
	deck, note := newDeckWithNote(t, env, owner.ID)
	if err := store.NewGrantStore(env.db).Grant(context.Background(), deck.ID, reader.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant reader: %v", err)
	}
	// The reader holds a key with write scope, but the deck role is not enough - both doors must open.
	key := seedKey(t, env.keys, reader.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	path := "/api/v1/notes/" + note.PublicID

	status, raw := doJSON(t, router, http.MethodPatch, path, key.Plaintext,
		`{"kind":"basic","fields":{"front":"x","back":"y"}}`)
	if status != http.StatusForbidden {
		t.Fatalf("reader PATCH note status = %d, want 403 (body %s)", status, raw)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("response not JSON: %v (%s)", err, raw)
	}
	if body.Error.Code != CodeInsufficientRole {
		t.Fatalf("reader PATCH note code = %q, want %q (body %s)", body.Error.Code, CodeInsufficientRole, raw)
	}
}
