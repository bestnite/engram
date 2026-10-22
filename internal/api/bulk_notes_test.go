package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// seedBasicNote creates one basic note with NoteStore.Create (which also generates its cards) and returns the note.
// The fixture goes through the real generation pipeline, so the bulk action tests face notes and cards that are genuinely persisted.
func seedBasicNote(t *testing.T, env *testEnv, deckID uint64, front, back string, tags []string) *store.Note {
	t.Helper()
	n := &store.Note{DeckID: deckID, Kind: "basic", TagsJSON: store.TagsJSON(tags), CreatedAt: time.Now().UTC()}
	if _, err := store.NewNoteStore(env.db).Create(context.Background(), n, map[string]any{"front": front, "back": back}); err != nil {
		t.Fatalf("create note: %v", err)
	}
	return n
}

// bulkResponse parses the bulk action response body for field-by-field assertions. note_id is the external id (a string).
type bulkResponse struct {
	DryRun   bool  `json:"dry_run"`
	Affected int64 `json:"affected"`
	Skipped  []struct {
		NoteID string `json:"note_id"`
		Code   string `json:"code"`
	} `json:"skipped"`
}

// missingPublicID is a well-formed note external id that does not exist in the database, used to assert per-row not_found.
const missingPublicID = "00000000-0000-7000-8000-000000000000"

func decodeBulk(t *testing.T, raw []byte) bulkResponse {
	t.Helper()
	var r bulkResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("bulk response is not JSON: %v (%s)", err, raw)
	}
	return r
}

// assertErrorCode asserts the stable code in the error envelope.
func assertErrorCode(t *testing.T, raw []byte, want string) {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("error response is not JSON: %v (%s)", err, raw)
	}
	if body.Error.Code != want {
		t.Errorf("error code = %q, want %q (body %s)", body.Error.Code, want, raw)
	}
}

// TestBulkNotesRejectsInvalidRequestShapes covers four categories (five cases here) of request-level rejection:
// unknown action, empty note_ids, note_ids above 500, a tag action without tags, delete with tags.
// Each must be 400 invalid_request, writing no row and no audit entry.
func TestBulkNotesRejectsInvalidRequestShapes(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "bulk_reject", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	note := seedBasicNote(t, env, deck.ID, "q", "a", nil)
	ctx := context.Background()

	var b strings.Builder
	b.WriteString("[")
	for i := 1; i <= 501; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		// note_ids is a string array; building strings here is what actually exercises the "count cap" rather than "wrong type".
		fmt.Fprintf(&b, "%q", itoa(i))
	}
	b.WriteString("]")

	cases := []struct{ name, body string }{
		{"unknown action", fmt.Sprintf(`{"action":"archive","note_ids":["%s"]}`, note.PublicID)},
		{"empty note_ids", `{"action":"delete","note_ids":[]}`},
		{"oversized note_ids", `{"action":"delete","note_ids":` + b.String() + `}`},
		{"tag action without tags", fmt.Sprintf(`{"action":"add_tags","note_ids":["%s"]}`, note.PublicID)},
		{"delete with tags", fmt.Sprintf(`{"action":"delete","note_ids":["%s"],"tags":["x"]}`, note.PublicID)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := doJSON(t, router, http.MethodPost, "/api/v1/notes/bulk", k.Plaintext, tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", status, raw)
			}
			assertErrorCode(t, raw, CodeInvalidRequest)
		})
	}

	// Zero writes: the note is not deleted, not tagged, and there is no audit row at all.
	var notes int64
	if err := env.db.Model(&store.Note{}).Where("deck_id = ?", deck.ID).Count(&notes).Error; err != nil {
		t.Fatalf("count notes: %v", err)
	}
	if notes != 1 {
		t.Errorf("notes in deck = %d, want 1 (rejected requests must write nothing)", notes)
	}
	if n, err := store.NewAuditStore(env.db).CountByAction(ctx, store.ActionNoteDelete); err != nil || n != 0 {
		t.Errorf("note.delete audit rows = (%d, %v), want (0, nil)", n, err)
	}
	// No bulk action may leave an audit row behind (the auth middleware writes one for the request itself, hence no whole-table count).
	audit := store.NewAuditStore(env.db)
	for _, action := range []string{store.ActionNoteDelete, store.ActionNoteTagAdd, store.ActionNoteTagRemove, store.ActionNoteTagSet} {
		if n, err := audit.CountByAction(ctx, action); err != nil || n != 0 {
			t.Errorf("audit rows for %q = (%d, %v), want (0, nil)", action, n, err)
		}
	}
}

// TestBulkNotesSkipsMissingDeletedAndUnreadableRows covers per-row authorization:
// a missing or soft-deleted id -> not_found, an id without edit permission -> insufficient_role,
// while the other permitted rows in the same request still take effect (one rejected row does not roll back the batch).
func TestBulkNotesSkipsMissingDeletedAndUnreadableRows(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	ctx := context.Background()
	owner := seedUser(t, env.db, "bulk_owner", store.RoleUser)
	other := seedUser(t, env.db, "bulk_other", store.RoleUser)
	deckA := seedDeck(t, env.db, owner.ID) // owner is the owner of the deck and can edit
	deckB := seedDeck(t, env.db, other.ID) // owner is only granted reader
	deckC := seedDeck(t, env.db, other.ID) // owner has no rights at all

	editable := seedBasicNote(t, env, deckA.ID, "q1", "a1", nil)
	deleted := seedBasicNote(t, env, deckA.ID, "q2", "a2", nil)
	readable := seedBasicNote(t, env, deckB.ID, "q3", "a3", nil)
	hidden := seedBasicNote(t, env, deckC.ID, "q4", "a4", nil)

	if err := store.NewGrantStore(env.db).Grant(ctx, deckB.ID, owner.ID, store.RoleReader, store.Ptr(other.ID)); err != nil {
		t.Fatalf("grant reader: %v", err)
	}
	if err := store.NewNoteStore(env.db).Delete(ctx, deleted.ID); err != nil {
		t.Fatalf("soft-delete note: %v", err)
	}

	k := seedKey(t, env.keys, owner.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	body := fmt.Sprintf(`{"action":"add_tags","note_ids":["%s","%s","%s","%s","%s"],"tags":["bulk"]}`,
		editable.PublicID, deleted.PublicID, readable.PublicID, hidden.PublicID, missingPublicID)

	status, raw := doJSON(t, router, http.MethodPost, "/api/v1/notes/bulk", k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	resp := decodeBulk(t, raw)
	if resp.Affected != 1 {
		t.Errorf("affected = %d, want 1 (only the editable row changes)", resp.Affected)
	}
	want := []struct {
		id   string
		code string
	}{
		{deleted.PublicID, "not_found"},
		{readable.PublicID, "insufficient_role"},
		{hidden.PublicID, "insufficient_role"},
		{missingPublicID, "not_found"},
	}
	if len(resp.Skipped) != len(want) {
		t.Fatalf("skipped = %+v, want %d entries", resp.Skipped, len(want))
	}
	for i, w := range want {
		if resp.Skipped[i].NoteID != w.id || resp.Skipped[i].Code != w.code {
			t.Errorf("skipped[%d] = (%s,%q), want (%s,%q)", i, resp.Skipped[i].NoteID, resp.Skipped[i].Code, w.id, w.code)
		}
	}

	// Only the permitted row was actually changed; the other three are left untouched.
	noteStore := store.NewNoteStore(env.db)
	tags, _ := noteStore.ByID(ctx, editable.ID)
	if got, _ := store.ParseTags(tags.TagsJSON); !containsTag(got, "bulk") {
		t.Errorf("editable note tags = %v, want to contain bulk", got)
	}
	for _, id := range []uint64{readable.ID, hidden.ID} {
		n, err := noteStore.ByID(ctx, id)
		if err != nil {
			t.Fatalf("reload note %d: %v", id, err)
		}
		if got, _ := store.ParseTags(n.TagsJSON); len(got) != 0 {
			t.Errorf("note %d tags = %v, want unchanged (empty)", id, got)
		}
	}
}

// TestBulkNotesIsIdempotentAndAuditsOnce asserts that resubmitting the same request a second time yields affected=0,
// and that the whole batch writes exactly one audit row, with ids and affected in detail.
func TestBulkNotesIsIdempotentAndAuditsOnce(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "bulk_idem", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	note := seedBasicNote(t, env, deck.ID, "q", "a", nil)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	body := fmt.Sprintf(`{"action":"add_tags","note_ids":["%s"],"tags":["alpha","beta"]}`, note.PublicID)

	status, raw := doJSON(t, router, http.MethodPost, "/api/v1/notes/bulk", k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("first status = %d, want 200 (body %s)", status, raw)
	}
	if first := decodeBulk(t, raw); first.Affected != 1 {
		t.Fatalf("first affected = %d, want 1", first.Affected)
	}

	status, raw = doJSON(t, router, http.MethodPost, "/api/v1/notes/bulk", k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("second status = %d, want 200 (body %s)", status, raw)
	}
	if second := decodeBulk(t, raw); second.Affected != 0 {
		t.Errorf("second affected = %d, want 0 (repeating the request is idempotent)", second.Affected)
	}

	audit := store.NewAuditStore(env.db)
	if n, err := audit.CountByAction(context.Background(), store.ActionNoteTagAdd); err != nil || n != 2 {
		// Each of the two requests writes one audit row (one per batch); the second leaves a trace even with affected=0.
		t.Errorf("note.tag_add audit rows = (%d, %v), want (2, nil)", n, err)
	}
	var last store.AuditLog
	if err := env.db.Where("action = ?", store.ActionNoteTagAdd).Order("id DESC").First(&last).Error; err != nil {
		t.Fatalf("load latest audit: %v", err)
	}
	if last.DetailJSON == nil {
		t.Fatal("bulk audit detail_json is nil, want ids and affected")
	}
	for _, want := range []string{`"ids":[`, fmt.Sprintf(`"affected":%d`, 0)} {
		if !strings.Contains(*last.DetailJSON, want) {
			t.Errorf("audit detail %s does not contain %s", *last.DetailJSON, want)
		}
	}
}

// TestBulkNotesDryRunChangesNothing asserts dry_run only counts: it changes no row and writes no audit.
// The request deliberately mixes in an id this user cannot edit: dry_run must not leave even a permission.denied entry behind
// (per-row silent authorization), yet must still report that row's code faithfully in skipped.
func TestBulkNotesDryRunChangesNothing(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	ctx := context.Background()
	user := seedUser(t, env.db, "bulk_dry", store.RoleUser)
	other := seedUser(t, env.db, "bulk_dry_other", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	a := seedBasicNote(t, env, deck.ID, "q1", "a1", nil)
	b := seedBasicNote(t, env, deck.ID, "q2", "a2", nil)
	// Someone else's deck where user is only granted reader: readable, not editable.
	otherDeck := seedDeck(t, env.db, other.ID)
	readonly := seedBasicNote(t, env, otherDeck.ID, "q3", "a3", nil)
	if err := store.NewGrantStore(env.db).Grant(ctx, otherDeck.ID, other.ID, store.RoleReader, store.Ptr(other.ID)); err != nil {
		t.Fatalf("grant reader: %v", err)
	}
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()

	body := fmt.Sprintf(`{"action":"add_tags","dry_run":true,"note_ids":["%s","%s","%s"],"tags":["gamma"]}`,
		a.PublicID, b.PublicID, readonly.PublicID)
	status, raw := doJSON(t, router, http.MethodPost, "/api/v1/notes/bulk", k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	resp := decodeBulk(t, raw)
	if !resp.DryRun || resp.Affected != 2 {
		t.Errorf("dry_run response = (dry_run=%v affected=%d), want (true, 2)", resp.DryRun, resp.Affected)
	}
	if len(resp.Skipped) != 1 || resp.Skipped[0].NoteID != readonly.PublicID || resp.Skipped[0].Code != CodeInsufficientRole {
		t.Errorf("dry_run skipped = %+v, want exactly (%s, %q)", resp.Skipped, readonly.PublicID, CodeInsufficientRole)
	}

	noteStore := store.NewNoteStore(env.db)
	for _, id := range []uint64{a.ID, b.ID, readonly.ID} {
		n, err := noteStore.ByID(ctx, id)
		if err != nil {
			t.Fatalf("reload note %d: %v", id, err)
		}
		if got, _ := store.ParseTags(n.TagsJSON); len(got) != 0 {
			t.Errorf("note %d tags = %v, want unchanged (dry_run must not write)", id, got)
		}
	}
	// dry_run writes no bulk action audit row (the one the auth middleware writes for the request itself does not count),
	// and a per-row authorization failure must not leave permission.denied either - that is part of "dry_run has zero side effects".
	audit := store.NewAuditStore(env.db)
	for _, action := range []string{store.ActionNoteDelete, store.ActionNoteTagAdd, store.ActionNoteTagRemove, store.ActionNoteTagSet, store.ActionPermissionDenied} {
		if n, err := audit.CountByAction(ctx, action); err != nil || n != 0 {
			t.Errorf("dry_run audit rows for %q = (%d, %v), want (0, nil)", action, n, err)
		}
	}
}
