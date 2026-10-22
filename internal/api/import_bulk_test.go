package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// buildBulkBody builds a bulk import body with n valid basic notes, external_ref set to b:<i>.
func buildBulkBody(n int) string {
	var b strings.Builder
	b.WriteString(`{"notes":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"kind":"basic","fields":{"front":"q%d","back":"a%d"},"external_ref":"b:%d"}`, i, i, i)
	}
	b.WriteString(`]}`)
	return b.String()
}

// TestBulkImportReportsInvalidRowIndexAndImportsValidRows is the core acceptance:
// a batch containing one row of invalid data imports the valid rows and reports the failed row's index.
func TestBulkImportReportsInvalidRowIndexAndImportsValidRows(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "bulk_mixed", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	path := fmt.Sprintf("/api/v1/decks/%s/notes", deck.PublicID)

	// Index 1 is missing the required back field (invalid); indexes 0 and 2 are valid.
	body := `{"notes":[` +
		`{"kind":"basic","fields":{"front":"q0","back":"a0"},"external_ref":"r:0"},` +
		`{"kind":"basic","fields":{"front":"q1"},"external_ref":"r:1"},` +
		`{"kind":"basic","fields":{"front":"q2","back":"a2"},"external_ref":"r:2"}]}`

	status, raw := doJSON(t, router, http.MethodPost, path, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("import status = %d, want 200 (body %s)", status, raw)
	}
	var resp importResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("import response is not JSON: %v (%s)", err, raw)
	}
	if resp.Created != 2 || resp.Updated != 0 || resp.Skipped != 0 {
		t.Errorf("created/updated/skipped = %d/%d/%d, want 2/0/0", resp.Created, resp.Updated, resp.Skipped)
	}
	if len(resp.Errors) != 1 {
		t.Fatalf("errors = %+v, want exactly one row error", resp.Errors)
	}
	if resp.Errors[0].Index != 1 {
		t.Errorf("error index = %d, want 1", resp.Errors[0].Index)
	}
	if strings.TrimSpace(resp.Errors[0].Reason) == "" {
		t.Error("error reason is empty, want the failing field named")
	}
	// The valid rows must really be persisted (2 notes, 2 cards) and the invalid row must not be.
	assertNoteCardCount(t, env, deck.ID, 2, 2)
}

// TestBulkImportBatchesTwoHundredRowsPerTransaction verifies 205 rows span several 200-row transaction batches,
// and that resubmitting stays idempotent (no duplicate cards across batches).
func TestBulkImportBatchesTwoHundredRowsPerTransaction(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "bulk_batches", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	path := fmt.Sprintf("/api/v1/decks/%s/notes", deck.PublicID)

	const total = 205 // > ImportBatchSize(200), forcing at least two transaction batches.
	status, raw := doJSON(t, router, http.MethodPost, path, k.Plaintext, buildBulkBody(total))
	if status != http.StatusOK {
		t.Fatalf("first import status = %d, want 200 (body %s)", status, raw)
	}
	var first importResponse
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatalf("first import response is not JSON: %v (%s)", err, raw)
	}
	if first.Created != total || len(first.Errors) != 0 {
		t.Fatalf("first import = created %d errors %+v, want %d/0", first.Created, first.Errors, total)
	}
	assertNoteCardCount(t, env, deck.ID, total, total)

	// Resubmit the same batch: everything hits on external_ref and is skipped under the skip policy, producing no duplicate cards.
	body := `{"on_conflict":"skip",` + buildBulkBody(total)[1:]
	status, raw = doJSON(t, router, http.MethodPost, path, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("re-import status = %d, want 200 (body %s)", status, raw)
	}
	var second importResponse
	if err := json.Unmarshal(raw, &second); err != nil {
		t.Fatalf("re-import response is not JSON: %v (%s)", err, raw)
	}
	if second.Created != 0 || second.Skipped != total || len(second.Errors) != 0 {
		t.Errorf("re-import = created %d skipped %d errors %+v, want 0/%d/0",
			second.Created, second.Skipped, second.Errors, total)
	}
	assertNoteCardCount(t, env, deck.ID, total, total)
}

// TestBulkImportIsolatesRowErrorWithinBatch triggers a real database-level row error within a batch (the same external_ref
// appearing twice in one request -> the second row hits the unique constraint), proving the failing row is isolated and reported with its index,
// while the rest of the batch still commits (the savepoint rollback path).
func TestBulkImportIsolatesRowErrorWithinBatch(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "bulk_isolation", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	path := fmt.Sprintf("/api/v1/decks/%s/notes", deck.PublicID)

	// Indexes 0 and 1 use the same external_ref: the first pass finds no existing row for either, so both try to create;
	// the second row in the batch hits the (deck_id, external_ref) unique constraint, so only it is reported, while 0 and 2 persist normally.
	body := `{"notes":[` +
		`{"kind":"basic","fields":{"front":"a","back":"A"},"external_ref":"dup"},` +
		`{"kind":"basic","fields":{"front":"b","back":"B"},"external_ref":"dup"},` +
		`{"kind":"basic","fields":{"front":"c","back":"C"},"external_ref":"uniq"}]}`

	status, raw := doJSON(t, router, http.MethodPost, path, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("import status = %d, want 200 (body %s)", status, raw)
	}
	var resp importResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("import response is not JSON: %v (%s)", err, raw)
	}
	if resp.Created != 2 {
		t.Errorf("created = %d, want 2 (valid rows survive a row-level failure)", resp.Created)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Index != 1 {
		t.Fatalf("errors = %+v, want exactly one at index 1", resp.Errors)
	}
	assertNoteCardCount(t, env, deck.ID, 2, 2)
}

// TestBulkImportResumesAfterPartialFailure proves a failure is resumable: the first import had one invalid row yet its valid rows were persisted;
// fixing that row and replaying with skip leaves the already-imported rows skipped (not redone) and creates only the missing one.
func TestBulkImportResumesAfterPartialFailure(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "bulk_resume", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	path := fmt.Sprintf("/api/v1/decks/%s/notes", deck.PublicID)

	broken := `{"notes":[` +
		`{"kind":"basic","fields":{"front":"a","back":"A"},"external_ref":"r:0"},` +
		`{"kind":"basic","fields":{"front":"b"},"external_ref":"r:1"},` +
		`{"kind":"basic","fields":{"front":"c","back":"C"},"external_ref":"r:2"}]}`
	status, raw := doJSON(t, router, http.MethodPost, path, k.Plaintext, broken)
	if status != http.StatusOK {
		t.Fatalf("broken import status = %d, want 200 (body %s)", status, raw)
	}
	var first importResponse
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatalf("broken import response is not JSON: %v (%s)", err, raw)
	}
	if first.Created != 2 || len(first.Errors) != 1 {
		t.Fatalf("broken import = created %d errors %+v, want 2/1", first.Created, first.Errors)
	}
	assertNoteCardCount(t, env, deck.ID, 2, 2)

	// Replay the whole batch after fixing r:1: skip keeps the two imported rows from being redone, creating only the fixed row.
	fixed := `{"on_conflict":"skip","notes":[` +
		`{"kind":"basic","fields":{"front":"a","back":"A"},"external_ref":"r:0"},` +
		`{"kind":"basic","fields":{"front":"b","back":"B"},"external_ref":"r:1"},` +
		`{"kind":"basic","fields":{"front":"c","back":"C"},"external_ref":"r:2"}]}`
	status, raw = doJSON(t, router, http.MethodPost, path, k.Plaintext, fixed)
	if status != http.StatusOK {
		t.Fatalf("resume import status = %d, want 200 (body %s)", status, raw)
	}
	var resumed importResponse
	if err := json.Unmarshal(raw, &resumed); err != nil {
		t.Fatalf("resume import response is not JSON: %v (%s)", err, raw)
	}
	if resumed.Created != 1 || resumed.Skipped != 2 || len(resumed.Errors) != 0 {
		t.Errorf("resume import = created %d skipped %d errors %+v, want 1/2/0",
			resumed.Created, resumed.Skipped, resumed.Errors)
	}
	assertNoteCardCount(t, env, deck.ID, 3, 3)
}
