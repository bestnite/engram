package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// bulkDeckResponse is the response body of POST /api/v1/decks/bulk for field-by-field assertions.
// deck_id is the external id (a string), never the numeric primary key.
type bulkDeckResponse struct {
	DryRun   bool  `json:"dry_run"`
	Affected int64 `json:"affected"`
	Skipped  []struct {
		DeckID string `json:"deck_id"`
		Code   string `json:"code"`
	} `json:"skipped"`
}

// decodeBulkDecks parses the bulk deck response body.
func decodeBulkDecks(t *testing.T, raw []byte) bulkDeckResponse {
	t.Helper()
	var r bulkDeckResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("bulk decks response is not JSON: %v (%s)", err, raw)
	}
	return r
}

// postBulkDecks calls POST /api/v1/decks/bulk with a bearer key.
func postBulkDecks(t *testing.T, env *testEnv, key, body string) (int, []byte) {
	t.Helper()
	return doJSON(t, env.router(), http.MethodPost, "/api/v1/decks/bulk", key, body)
}

// seedDeckContent creates one note (through the real generation pipeline, so cards exist) plus a card state for userID.
// It returns the note; the cascade test uses the ids to assert that every related row disappeared with the deck.
func seedDeckContent(t *testing.T, env *testEnv, deckID, userID uint64) *store.Note {
	t.Helper()
	n := seedBasicNote(t, env, deckID, "front", "back", nil)
	var cards []store.Card
	if err := env.db.Where("note_id = ?", n.ID).Find(&cards).Error; err != nil {
		t.Fatalf("load cards: %v", err)
	}
	if len(cards) == 0 {
		t.Fatalf("note %d generated no cards", n.ID)
	}
	st := store.CardState{CardID: cards[0].ID, UserID: userID, State: "review"}
	if err := env.db.Create(&st).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}
	return n
}

// countRows counts rows of one model matching a where clause; used to assert the cascade really removed related rows.
func countRows(t *testing.T, env *testEnv, model any, where string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := env.db.Model(model).Where(where, args...).Count(&n).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

// TestBulkDecksDeletesOwnedAndReportsEachSkip is the core acceptance for the happy path mixed with rejections:
// one owned deck is deleted, a deck only shared with the caller is forbidden, an unknown id is not_found,
// a repeated id is deduplicated, and the response reports every row in request order.
func TestBulkDecksDeletesOwnedAndReportsEachSkip(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	caller := seedUser(t, env.db, "bulk_deck_caller", store.RoleUser)
	other := seedUser(t, env.db, "bulk_deck_other", store.RoleUser)
	own := seedDeck(t, env.db, caller.ID)
	shared := seedDeck(t, env.db, other.ID)
	if err := store.NewGrantStore(env.db).Grant(context.Background(), shared.ID, caller.ID, store.RoleEditor, store.Ptr(other.ID)); err != nil {
		t.Fatalf("grant shared deck: %v", err)
	}
	k := seedKey(t, env.keys, caller.ID, []string{store.ScopeWrite}, nil)
	const missing = "00000000-0000-7000-8000-0000000000ff"

	body := fmt.Sprintf(`{"action":"delete","deck_ids":[%q,%q,%q,%q]}`, own.PublicID, own.PublicID, shared.PublicID, missing)
	status, raw := postBulkDecks(t, env, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	resp := decodeBulkDecks(t, raw)
	if resp.DryRun {
		t.Errorf("dry_run = true, want false")
	}
	if resp.Affected != 1 {
		t.Errorf("affected = %d, want 1", resp.Affected)
	}
	want := []struct{ deckID, code string }{
		{shared.PublicID, CodeForbidden},
		{missing, CodeNotFound},
	}
	if len(resp.Skipped) != len(want) {
		t.Fatalf("skipped = %+v, want %d entries", resp.Skipped, len(want))
	}
	for i, w := range want {
		if resp.Skipped[i].DeckID != w.deckID || resp.Skipped[i].Code != w.code {
			t.Errorf("skipped[%d] = (%q,%q), want (%q,%q)", i, resp.Skipped[i].DeckID, resp.Skipped[i].Code, w.deckID, w.code)
		}
	}

	decks := store.NewDeckStore(env.db)
	if _, err := decks.ByID(context.Background(), own.ID); err == nil {
		t.Errorf("owned deck survives the bulk delete")
	}
	if _, err := decks.ByID(context.Background(), shared.ID); err != nil {
		t.Errorf("shared deck was deleted: %v", err)
	}
	if n, err := store.NewAuditStore(env.db).CountByAction(context.Background(), store.ActionDeckDelete); err != nil || n != 1 {
		t.Errorf("deck.delete audit rows = (%d, %v), want (1, nil)", n, err)
	}
}

// TestBulkDecksAllForbiddenIsNotAnError asserts a batch where every row is rejected still answers 200 with affected=0:
// the caller learns per row why nothing happened, and no deck content is touched.
func TestBulkDecksAllForbiddenIsNotAnError(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	caller := seedUser(t, env.db, "bulk_all_denied_caller", store.RoleUser)
	owner := seedUser(t, env.db, "bulk_all_denied_owner", store.RoleUser)
	first := seedDeck(t, env.db, owner.ID)
	second := seedDeck(t, env.db, owner.ID)
	grants := store.NewGrantStore(env.db)
	if err := grants.Grant(context.Background(), first.ID, caller.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant first: %v", err)
	}
	if err := grants.Grant(context.Background(), second.ID, caller.ID, store.RoleEditor, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant second: %v", err)
	}
	k := seedKey(t, env.keys, caller.ID, []string{store.ScopeWrite}, nil)

	body := fmt.Sprintf(`{"action":"delete","deck_ids":[%q,%q]}`, first.PublicID, second.PublicID)
	status, raw := postBulkDecks(t, env, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	resp := decodeBulkDecks(t, raw)
	if resp.Affected != 0 {
		t.Errorf("affected = %d, want 0", resp.Affected)
	}
	if len(resp.Skipped) != 2 {
		t.Fatalf("skipped = %+v, want two forbidden entries", resp.Skipped)
	}
	for _, s := range resp.Skipped {
		if s.Code != CodeForbidden {
			t.Errorf("skipped code = %q, want %q", s.Code, CodeForbidden)
		}
	}
	decks := store.NewDeckStore(env.db)
	for _, d := range []*store.Deck{first, second} {
		if _, err := decks.ByID(context.Background(), d.ID); err != nil {
			t.Errorf("deck %d was removed despite being forbidden: %v", d.ID, err)
		}
	}
	if n, err := store.NewAuditStore(env.db).CountByAction(context.Background(), store.ActionDeckDelete); err != nil || n != 0 {
		t.Errorf("deck.delete audit rows = (%d, %v), want (0, nil)", n, err)
	}
}

// TestBulkDecksDryRunWritesNothing asserts dry_run counts the decks that would be deleted and performs no write:
// the deck and its content survive and no audit row is written.
func TestBulkDecksDryRunWritesNothing(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "bulk_dry_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	note := seedDeckContent(t, env, deck.ID, owner.ID)
	k := seedKey(t, env.keys, owner.ID, []string{store.ScopeWrite}, nil)

	body := fmt.Sprintf(`{"action":"delete","deck_ids":[%q],"dry_run":true}`, deck.PublicID)
	status, raw := postBulkDecks(t, env, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	resp := decodeBulkDecks(t, raw)
	if !resp.DryRun {
		t.Errorf("dry_run = false, want true")
	}
	if resp.Affected != 1 {
		t.Errorf("affected = %d, want 1", resp.Affected)
	}
	if _, err := store.NewDeckStore(env.db).ByID(context.Background(), deck.ID); err != nil {
		t.Errorf("dry_run removed the deck: %v", err)
	}
	if n := countRows(t, env, &store.Note{}, "id = ?", note.ID); n != 1 {
		t.Errorf("dry_run removed the note: rows = %d, want 1", n)
	}
	if n, err := store.NewAuditStore(env.db).CountByAction(context.Background(), store.ActionDeckDelete); err != nil || n != 0 {
		t.Errorf("dry_run wrote deck.delete audit rows = (%d, %v), want (0, nil)", n, err)
	}
}

// TestBulkDecksRejectsInvalidRequestShapes covers the request-level rejections: unknown action, empty deck_ids,
// and deck_ids above the cap. Each is 400 invalid_request with an English message, writing no row and no audit entry.
func TestBulkDecksRejectsInvalidRequestShapes(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "bulk_deck_reject", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	k := seedKey(t, env.keys, owner.ID, []string{store.ScopeWrite}, nil)

	var over strings.Builder
	over.WriteString("[")
	for i := 0; i <= bulkMaxDecks; i++ {
		if i > 0 {
			over.WriteString(",")
		}
		fmt.Fprintf(&over, "%q", fmt.Sprintf("01920000-0000-7000-8000-%012d", i))
	}
	over.WriteString("]")

	cases := []struct{ name, body string }{
		{"unknown action", fmt.Sprintf(`{"action":"archive","deck_ids":[%q]}`, deck.PublicID)},
		{"empty deck_ids", `{"action":"delete","deck_ids":[]}`},
		{"oversized deck_ids", `{"action":"delete","deck_ids":` + over.String() + `}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := postBulkDecks(t, env, k.Plaintext, tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", status, raw)
			}
			assertErrorCode(t, raw, CodeInvalidRequest)
			var envelope struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatalf("error response is not JSON: %v (%s)", err, raw)
			}
			if envelope.Error.Message == "" {
				t.Errorf("error message is empty; want an English explanation")
			}
		})
	}

	if _, err := store.NewDeckStore(env.db).ByID(context.Background(), deck.ID); err != nil {
		t.Errorf("rejected requests removed the deck: %v", err)
	}
	if n, err := store.NewAuditStore(env.db).CountByAction(context.Background(), store.ActionDeckDelete); err != nil || n != 0 {
		t.Errorf("deck.delete audit rows = (%d, %v), want (0, nil)", n, err)
	}
}

// TestBulkDecksRequiresWriteScope asserts a read-only key is stopped by the scope gate and the deck survives untouched.
func TestBulkDecksRequiresWriteScope(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "bulk_scope_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	k := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)

	body := fmt.Sprintf(`{"action":"delete","deck_ids":[%q]}`, deck.PublicID)
	status, raw := postBulkDecks(t, env, k.Plaintext, body)
	if status != http.StatusForbidden {
		t.Fatalf("read-scope status = %d, want 403 (body %s)", status, raw)
	}
	if _, err := store.NewDeckStore(env.db).ByID(context.Background(), deck.ID); err != nil {
		t.Errorf("deck removed by a read-only key: %v", err)
	}
}

// TestBulkDecksCascadesOnlySelectedDecks asserts the deleted deck's notes, cards, card_states and deck_grants are gone,
// the rows of another deck survive, and each deleted deck writes one deck.delete audit row carrying its name.
func TestBulkDecksCascadesOnlySelectedDecks(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "bulk_cascade_owner", store.RoleUser)
	member := seedUser(t, env.db, "bulk_cascade_member", store.RoleUser)
	doomed := seedDeck(t, env.db, owner.ID)
	kept := seedDeck(t, env.db, owner.ID)
	doomedNote := seedDeckContent(t, env, doomed.ID, owner.ID)
	keptNote := seedDeckContent(t, env, kept.ID, owner.ID)
	grants := store.NewGrantStore(env.db)
	if err := grants.Grant(context.Background(), doomed.ID, member.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant doomed: %v", err)
	}
	if err := grants.Grant(context.Background(), kept.ID, member.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant kept: %v", err)
	}
	k := seedKey(t, env.keys, owner.ID, []string{store.ScopeWrite}, nil)

	body := fmt.Sprintf(`{"action":"delete","deck_ids":[%q]}`, doomed.PublicID)
	status, raw := postBulkDecks(t, env, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}

	// The cascade removes every row hanging off the deleted deck.
	if n := countRows(t, env, &store.Note{}, "id = ?", doomedNote.ID); n != 0 {
		t.Errorf("doomed notes = %d, want 0", n)
	}
	if n := countRows(t, env, &store.Card{}, "note_id = ?", doomedNote.ID); n != 0 {
		t.Errorf("doomed cards = %d, want 0", n)
	}
	if n := countRows(t, env, &store.CardState{}, "card_id NOT IN (SELECT id FROM cards)"); n != 0 {
		t.Errorf("orphaned card_states = %d, want 0", n)
	}
	if n := countRows(t, env, &store.DeckGrant{}, "deck_id = ?", doomed.ID); n != 0 {
		t.Errorf("doomed deck_grants = %d, want 0", n)
	}

	// The other deck keeps all of its rows.
	if n := countRows(t, env, &store.Note{}, "id = ?", keptNote.ID); n != 1 {
		t.Errorf("kept notes = %d, want 1", n)
	}
	if n := countRows(t, env, &store.Card{}, "note_id = ?", keptNote.ID); n != 1 {
		t.Errorf("kept cards = %d, want 1", n)
	}
	if n := countRows(t, env, &store.CardState{}, "card_id IN (SELECT id FROM cards WHERE note_id = ?)", keptNote.ID); n != 1 {
		t.Errorf("kept card_states = %d, want 1", n)
	}
	if n := countRows(t, env, &store.DeckGrant{}, "deck_id = ?", kept.ID); n != 1 {
		t.Errorf("kept deck_grants = %d, want 1", n)
	}

	// One audit row per deleted deck with the same detail shape as the single-deck delete.
	rows, err := store.NewAuditStore(env.db).List(context.Background(), 50)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	var audits []store.AuditLog
	for _, row := range rows {
		if row.Action == store.ActionDeckDelete {
			audits = append(audits, row)
		}
	}
	if len(audits) != 1 {
		t.Fatalf("deck.delete audit rows = %d, want 1", len(audits))
	}
	if audits[0].TargetID == nil || *audits[0].TargetID != doomed.ID {
		t.Errorf("audit target = %v, want deck %d", audits[0].TargetID, doomed.ID)
	}
	var detail map[string]any
	if audits[0].DetailJSON == nil {
		t.Fatalf("audit detail is empty")
	}
	if err := json.Unmarshal([]byte(*audits[0].DetailJSON), &detail); err != nil {
		t.Fatalf("decode audit detail: %v", err)
	}
	if detail["deck_name"] != doomed.Name {
		t.Errorf("audit detail deck_name = %v, want %q", detail["deck_name"], doomed.Name)
	}
}
