package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// emptyCleanupTestConfig is a normal note type whose two templates render different fields, so clearing one field
// makes exactly one card's front empty while the other card stays.
func emptyCleanupTestConfig() notetype.Config {
	return notetype.Config{
		Kind: notetype.KindNormal,
		Fields: []notetype.Field{
			{ID: "f-front", Name: "Front"},
			{ID: "f-back", Name: "Back"},
		},
		Templates: []notetype.Template{
			{ID: "t-forward", Name: "Forward", QFormat: "{{Front}}", AFormat: "{{Back}}"},
			{ID: "t-reverse", Name: "Reverse", QFormat: "{{Back}}", AFormat: "{{Front}}"},
		},
		CSS:         ".card { color: black; }",
		SortFieldID: "f-front",
	}
}

// changeEndpointSourceConfig is the source type of a change: its field and template ids differ from the target's, so a
// test can tell a mapped value from one that was carried over by accident.
func changeEndpointSourceConfig() notetype.Config {
	return notetype.Config{
		Kind: notetype.KindNormal,
		Fields: []notetype.Field{
			{ID: "s-front", Name: "Front"},
			{ID: "s-back", Name: "Back"},
		},
		Templates: []notetype.Template{
			{ID: "s-card", Name: "Card 1", QFormat: "{{Front}}", AFormat: "{{Back}}"},
			{ID: "s-extra", Name: "Extra", QFormat: "{{Back}}", AFormat: "{{Front}}"},
		},
		CSS:         ".card { color: black; }",
		SortFieldID: "s-front",
	}
}

// changeEndpointTargetConfig is the target type: two templates in the reverse order of the source's, so a moved card's
// ordinal is visibly realigned.
func changeEndpointTargetConfig() notetype.Config {
	return notetype.Config{
		Kind: notetype.KindNormal,
		Fields: []notetype.Field{
			{ID: "g-question", Name: "Question"},
			{ID: "g-answer", Name: "Answer"},
		},
		Templates: []notetype.Template{
			{ID: "g-extra", Name: "Extra", QFormat: "{{Answer}}", AFormat: "{{Question}}"},
			{ID: "g-card", Name: "Card 1", QFormat: "{{Question}}", AFormat: "{{Answer}}"},
		},
		CSS:         ".card { color: black; }",
		SortFieldID: "g-question",
	}
}

// noteOfDeck loads the single note a cleanup or change test created in a deck.
func noteOfDeck(t *testing.T, env *testEnv, deckID uint64) store.Note {
	t.Helper()
	var notes []store.Note
	if err := env.db.Where("deck_id = ?", deckID).Order("id ASC").Find(&notes).Error; err != nil {
		t.Fatalf("load notes of deck %d: %v", deckID, err)
	}
	if len(notes) != 1 {
		t.Fatalf("deck %d holds %d notes, want 1", deckID, len(notes))
	}
	return notes[0]
}

// visibleCardsOfNote loads a note's visible cards, ordered by id.
func visibleCardsOfNote(t *testing.T, env *testEnv, noteID uint64) []store.Card {
	t.Helper()
	var cards []store.Card
	if err := env.db.Where("note_id = ?", noteID).Order("id ASC").Find(&cards).Error; err != nil {
		t.Fatalf("load cards of note %d: %v", noteID, err)
	}
	return cards
}

// cardByTemplate picks the card that carries one template id.
func cardByTemplate(t *testing.T, cards []store.Card, template string) store.Card {
	t.Helper()
	for _, card := range cards {
		if card.Template == template {
			return card
		}
	}
	t.Fatalf("no card with template %q in %+v", template, cards)
	return store.Card{}
}

// storedCard loads a card including its soft-deleted rows, so a test can prove a removal was a soft delete.
func storedCard(t *testing.T, env *testEnv, cardID uint64) store.Card {
	t.Helper()
	var card store.Card
	if err := env.db.Unscoped().First(&card, "id = ?", cardID).Error; err != nil {
		t.Fatalf("load card %d: %v", cardID, err)
	}
	return card
}

// seedCardProgress writes one card_state and one review row for a card, so a test can prove a content operation kept
// every progress row.
func seedCardProgress(t *testing.T, env *testEnv, userID, cardID uint64) {
	t.Helper()
	state := store.CardState{CardID: cardID, UserID: userID, State: "review", Reps: 3}
	if err := env.db.Create(&state).Error; err != nil {
		t.Fatalf("create card state for card %d: %v", cardID, err)
	}
	review := store.Review{
		CardID: cardID, UserID: userID, Rating: 3, GradeSource: "self",
		ReviewedAt: env.now, ReviewDay: "2026-10-31", StateBefore: 2,
	}
	if err := env.db.Create(&review).Error; err != nil {
		t.Fatalf("create review for card %d: %v", cardID, err)
	}
}

// progressRowCounts reports the card_states and reviews rows attached to a card.
func progressRowCounts(t *testing.T, env *testEnv, cardID uint64) (int64, int64) {
	t.Helper()
	var states, reviews int64
	if err := env.db.Model(&store.CardState{}).Where("card_id = ?", cardID).Count(&states).Error; err != nil {
		t.Fatalf("count card states for card %d: %v", cardID, err)
	}
	if err := env.db.Model(&store.Review{}).Where("card_id = ?", cardID).Count(&reviews).Error; err != nil {
		t.Fatalf("count reviews for card %d: %v", cardID, err)
	}
	return states, reviews
}

// TestEmptyCardsCleanupREST covers the deck-level flow: the list, a dry run that writes nothing, the unconfirmed
// refusal with its counts, and the confirmed cleanup that soft-deletes only the empty card and keeps its progress.
func TestEmptyCardsCleanupREST(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "empty_cards_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	typesBase := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)
	emptyPath := fmt.Sprintf("/api/v1/decks/%s/empty-cards", deck.PublicID)

	nt := createNoteTypeREST(t, router, typesBase, key, "Two directions", emptyCleanupTestConfig())
	createNotesREST(t, router, deck.PublicID, nt.ID, key, []map[string]string{{"Front": "Q1", "Back": "A1"}})
	note := noteOfDeck(t, env, deck.ID)
	cards := visibleCardsOfNote(t, env, note.ID)
	if len(cards) != 2 {
		t.Fatalf("created note has %d cards, want 2", len(cards))
	}
	reverse := cardByTemplate(t, cards, "t-reverse")
	seedCardProgress(t, env, owner.ID, reverse.ID)

	// Clearing Back empties the reverse card's front; the note write never removes the card itself.
	clear := fmt.Sprintf(`{"note_type":%q,"fields":{"Front":"Q1","Back":""}}`, nt.ID)
	code, raw := doJSON(t, router, http.MethodPatch, "/api/v1/notes/"+note.PublicID, key, clear)
	if code != http.StatusOK {
		t.Fatalf("PATCH note clearing Back = %d, want 200 (body %s)", code, raw)
	}

	code, raw = doJSON(t, router, http.MethodGet, emptyPath, key, "")
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200 (body %s)", emptyPath, code, raw)
	}
	var listed EmptyCardListResponse
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("empty-card list is not JSON: %v (%s)", err, raw)
	}
	if listed.Count != 1 || len(listed.Cards) != 1 {
		t.Fatalf("empty-card list = %+v, want exactly one card", listed)
	}
	if listed.Cards[0].ID != reverse.PublicID || listed.Cards[0].Template != "t-reverse" ||
		listed.Cards[0].NoteID != note.PublicID || listed.Cards[0].NoteTypeID != nt.ID {
		t.Errorf("empty card = %+v, want the reverse card %q of note %q", listed.Cards[0], reverse.PublicID, note.PublicID)
	}

	// A dry run reports the counts and writes nothing.
	code, raw = doJSON(t, router, http.MethodPost, emptyPath, key, `{"dry_run":true}`)
	if code != http.StatusOK {
		t.Fatalf("POST dry run = %d, want 200 (body %s)", code, raw)
	}
	var result CleanupEmptyCardsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("dry-run response is not JSON: %v (%s)", err, raw)
	}
	if !result.DryRun || result.CardsRemoved != 1 || result.ReviewsOnRemovedCards != 1 {
		t.Errorf("dry run = %+v, want dry_run true, 1 card and 1 review", result)
	}
	if stored := storedCard(t, env, reverse.ID); stored.DeletedAt.Valid {
		t.Fatal("the dry run soft-deleted the card")
	}
	if auditCount, err := store.NewAuditStore(env.db).CountByAction(t.Context(), store.ActionCardEmptyCleanup); err != nil {
		t.Fatalf("count audit rows: %v", err)
	} else if auditCount != 0 {
		t.Errorf("audit rows after dry run = %d, want 0", auditCount)
	}

	// An unconfirmed cleanup is refused with the counts and writes nothing.
	code, raw = doJSON(t, router, http.MethodPost, emptyPath, key, `{}`)
	if code != http.StatusConflict {
		t.Fatalf("POST unconfirmed cleanup = %d, want 409 (body %s)", code, raw)
	}
	var refusal struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		CardsRemoved          int `json:"cards_removed"`
		ReviewsOnRemovedCards int `json:"reviews_on_removed_cards"`
	}
	if err := json.Unmarshal(raw, &refusal); err != nil {
		t.Fatalf("refusal is not JSON: %v (%s)", err, raw)
	}
	if refusal.Error.Code != CodeCardRemovalUnconfirmed {
		t.Errorf("refusal code = %q, want %q", refusal.Error.Code, CodeCardRemovalUnconfirmed)
	}
	if refusal.CardsRemoved != 1 || refusal.ReviewsOnRemovedCards != 1 {
		t.Errorf("refusal counts = card %d review %d, want 1/1", refusal.CardsRemoved, refusal.ReviewsOnRemovedCards)
	}
	if stored := storedCard(t, env, reverse.ID); stored.DeletedAt.Valid {
		t.Fatal("the unconfirmed cleanup soft-deleted the card")
	}

	// The confirmed cleanup removes exactly the empty card.
	code, raw = doJSON(t, router, http.MethodPost, emptyPath, key, `{"confirm_card_removal":true}`)
	if code != http.StatusOK {
		t.Fatalf("POST confirmed cleanup = %d, want 200 (body %s)", code, raw)
	}
	result = CleanupEmptyCardsResult{}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("confirmed response is not JSON: %v (%s)", err, raw)
	}
	if result.DryRun || result.CardsRemoved != 1 || result.ReviewsOnRemovedCards != 1 {
		t.Errorf("confirmed cleanup = %+v, want 1 card and 1 review", result)
	}
	if stored := storedCard(t, env, reverse.ID); !stored.DeletedAt.Valid {
		t.Error("the empty card is still visible after the confirmed cleanup")
	}
	remaining := visibleCardsOfNote(t, env, note.ID)
	if len(remaining) != 1 || remaining[0].Template != "t-forward" {
		t.Errorf("cards after cleanup = %+v, want only t-forward", remaining)
	}
	if states, reviews := progressRowCounts(t, env, reverse.ID); states != 1 || reviews != 1 {
		t.Errorf("removed card progress = %d state(s) and %d review(s), want 1/1", states, reviews)
	}

	// One audit row records the counts.
	if auditCount, err := store.NewAuditStore(env.db).CountByAction(t.Context(), store.ActionCardEmptyCleanup); err != nil {
		t.Fatalf("count audit rows: %v", err)
	} else if auditCount != 1 {
		t.Errorf("audit rows after cleanup = %d, want 1", auditCount)
	}
	var audit store.AuditLog
	if err := env.db.Where("action = ?", store.ActionCardEmptyCleanup).Order("id DESC").First(&audit).Error; err != nil {
		t.Fatalf("load audit row: %v", err)
	}
	if audit.DetailJSON == nil || !strings.Contains(*audit.DetailJSON, `"cards_removed":1`) {
		t.Errorf("audit detail = %v, want the removed count", audit.DetailJSON)
	}

	// Nothing is left to clean.
	code, raw = doJSON(t, router, http.MethodGet, emptyPath, key, "")
	if code != http.StatusOK {
		t.Fatalf("GET after cleanup = %d, want 200 (body %s)", code, raw)
	}
	listed = EmptyCardListResponse{}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("second empty-card list is not JSON: %v (%s)", err, raw)
	}
	if listed.Count != 0 {
		t.Errorf("empty cards after cleanup = %d, want 0", listed.Count)
	}
}

// TestChangeNoteTypeREST covers the card-mapping success path and the two refused shapes a caller can send: a target
// type of another deck is 404, and a mapping id the configs do not declare is 400.
func TestChangeNoteTypeREST(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "note_type_change_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	otherDeck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	typesBase := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)

	source := createNoteTypeREST(t, router, typesBase, key, "Source", changeEndpointSourceConfig())
	target := createNoteTypeREST(t, router, typesBase, key, "Target", changeEndpointTargetConfig())
	foreign := createNoteTypeREST(t, router, fmt.Sprintf("/api/v1/decks/%s/note-types", otherDeck.PublicID),
		key, "Foreign", changeEndpointTargetConfig())
	changePath := typesBase + "/" + source.ID + "/change"

	createNotesREST(t, router, deck.PublicID, source.ID, key, []map[string]string{
		{"Front": "<b>Q1</b>", "Back": "A1"},
	})
	note := noteOfDeck(t, env, deck.ID)
	cards := visibleCardsOfNote(t, env, note.ID)
	if len(cards) != 2 {
		t.Fatalf("created note has %d cards, want 2", len(cards))
	}
	for _, card := range cards {
		seedCardProgress(t, env, owner.ID, card.ID)
	}

	// The full mapping both sides needs.
	body := func(extra map[string]any) string {
		t.Helper()
		payload := map[string]any{
			"target_note_type": target.ID,
			"field_mapping":    map[string]string{"s-front": "g-question", "s-back": "g-answer"},
			"template_mapping": map[string]string{"s-card": "g-card", "s-extra": "g-extra"},
		}
		for k, v := range extra {
			payload[k] = v
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal change body: %v", err)
		}
		return string(raw)
	}

	// A target type of another deck is not found, never a forbidden resource.
	foreignBody, _ := json.Marshal(map[string]any{"target_note_type": foreign.ID})
	code, raw := doJSON(t, router, http.MethodPost, changePath, key, string(foreignBody))
	if code != http.StatusNotFound {
		t.Fatalf("change to a type of another deck = %d, want 404 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeNotFound)

	// A mapping id the source config does not declare is a 400 that names the id.
	code, raw = doJSON(t, router, http.MethodPost, changePath, key,
		`{"target_note_type":"`+target.ID+`","field_mapping":{"s-missing":"g-question"}}`)
	if code != http.StatusBadRequest {
		t.Fatalf("change with an unknown field id = %d, want 400 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeInvalidRequest)
	if message := restErrorMessage(t, raw); !strings.Contains(message, "s-missing") {
		t.Errorf("unknown-field message = %q, want it to name s-missing", message)
	}

	// A dry run reports the impact and writes nothing.
	code, raw = doJSON(t, router, http.MethodPost, changePath, key, body(map[string]any{"dry_run": true}))
	if code != http.StatusOK {
		t.Fatalf("change dry run = %d, want 200 (body %s)", code, raw)
	}
	var result ChangeNoteTypeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("dry-run response is not JSON: %v (%s)", err, raw)
	}
	if !result.DryRun || result.NotesMoved != 1 || result.CardsMoved != 2 || result.CardsAdded != 0 || result.CardsRemoved != 0 {
		t.Errorf("dry run = %+v, want dry_run true, 1 note, 2 moved, 0 added and 0 removed", result)
	}
	if got := storedNoteTypeID(t, env, note.ID); got != source.ID {
		t.Fatalf("dry run moved the note to note type %q, want %q", got, source.ID)
	}
	if auditCount, err := store.NewAuditStore(env.db).CountByAction(t.Context(), store.ActionNoteTypeChange); err != nil {
		t.Fatalf("count audit rows: %v", err)
	} else if auditCount != 0 {
		t.Errorf("audit rows after dry run = %d, want 0", auditCount)
	}

	// The confirmed change moves the notes and the cards, and every mapped card keeps its id and progress.
	code, raw = doJSON(t, router, http.MethodPost, changePath, key, body(nil))
	if code != http.StatusOK {
		t.Fatalf("change = %d, want 200 (body %s)", code, raw)
	}
	result = ChangeNoteTypeResult{}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("change response is not JSON: %v (%s)", err, raw)
	}
	if result.DryRun || result.NotesMoved != 1 || result.CardsMoved != 2 || result.CardsAdded != 0 || result.CardsRemoved != 0 {
		t.Errorf("change = %+v, want 1 note, 2 moved, 0 added and 0 removed", result)
	}
	if got := storedNoteTypeID(t, env, note.ID); got != target.ID {
		t.Errorf("note type after change = %q, want %q", got, target.ID)
	}

	var moved store.Note
	if err := env.db.First(&moved, "id = ?", note.ID).Error; err != nil {
		t.Fatalf("load moved note: %v", err)
	}
	values, err := store.ParseFieldValues(moved.FieldsJSON)
	if err != nil {
		t.Fatalf("parse moved fields: %v", err)
	}
	if values["g-question"] != "<b>Q1</b>" || values["g-answer"] != "A1" {
		t.Errorf("mapped values = %v, want the source values under the target field ids", values)
	}

	wantID := map[string]uint64{
		"g-card":  cardByTemplate(t, cards, "s-card").ID,
		"g-extra": cardByTemplate(t, cards, "s-extra").ID,
	}
	wantOrdinal := map[string]int{"g-card": 1, "g-extra": 0}
	after := visibleCardsOfNote(t, env, note.ID)
	if len(after) != 2 {
		t.Fatalf("cards after change = %d, want 2", len(after))
	}
	for _, card := range after {
		if card.ID != wantID[card.Template] {
			t.Errorf("card %q id = %d, want the moved row %d", card.Template, card.ID, wantID[card.Template])
		}
		if card.Ordinal != wantOrdinal[card.Template] {
			t.Errorf("card %q ordinal = %d, want %d", card.Template, card.Ordinal, wantOrdinal[card.Template])
		}
		if states, reviews := progressRowCounts(t, env, card.ID); states != 1 || reviews != 1 {
			t.Errorf("mapped card %q progress = %d state(s) and %d review(s), want 1/1", card.Template, states, reviews)
		}
	}

	// The change keeps the review log and records its counts in one audit row.
	if auditCount, err := store.NewAuditStore(env.db).CountByAction(t.Context(), store.ActionNoteTypeChange); err != nil {
		t.Fatalf("count audit rows: %v", err)
	} else if auditCount != 1 {
		t.Errorf("audit rows after change = %d, want 1", auditCount)
	}
	var audit store.AuditLog
	if err := env.db.Where("action = ?", store.ActionNoteTypeChange).Order("id DESC").First(&audit).Error; err != nil {
		t.Fatalf("load audit row: %v", err)
	}
	if audit.DetailJSON == nil || !strings.Contains(*audit.DetailJSON, `"notes_moved":1`) ||
		!strings.Contains(*audit.DetailJSON, `"cards_moved":2`) ||
		!strings.Contains(*audit.DetailJSON, `"cards_added":0`) {
		t.Errorf("audit detail = %v, want the change counts", audit.DetailJSON)
	}
}

// TestChangeNoteTypeRESTRemovalRequiresConfirmation covers the unmapped-template path: without confirm_card_removal
// the change is 409 with the counts and writes nothing; with it, the unmapped card is soft-deleted while the mapped
// card and every progress row stay.
func TestChangeNoteTypeRESTRemovalRequiresConfirmation(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "note_type_change_removal_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	typesBase := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)

	source := createNoteTypeREST(t, router, typesBase, key, "Source", changeEndpointSourceConfig())
	target := createNoteTypeREST(t, router, typesBase, key, "Target", changeEndpointTargetConfig())
	changePath := typesBase + "/" + source.ID + "/change"

	createNotesREST(t, router, deck.PublicID, source.ID, key, []map[string]string{
		{"Front": "Q1", "Back": "A1"},
	})
	note := noteOfDeck(t, env, deck.ID)
	cards := visibleCardsOfNote(t, env, note.ID)
	if len(cards) != 2 {
		t.Fatalf("created note has %d cards, want 2", len(cards))
	}
	mapped := cardByTemplate(t, cards, "s-card")
	stale := cardByTemplate(t, cards, "s-extra")
	for _, card := range cards {
		seedCardProgress(t, env, owner.ID, card.ID)
	}

	// Only s-card is mapped, so the s-extra card would be removed.
	body := func(extra map[string]any) string {
		t.Helper()
		payload := map[string]any{
			"target_note_type": target.ID,
			"field_mapping":    map[string]string{"s-front": "g-question", "s-back": "g-answer"},
			"template_mapping": map[string]string{"s-card": "g-card"},
		}
		for k, v := range extra {
			payload[k] = v
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal change body: %v", err)
		}
		return string(raw)
	}

	code, raw := doJSON(t, router, http.MethodPost, changePath, key, body(nil))
	if code != http.StatusConflict {
		t.Fatalf("unconfirmed change = %d, want 409 (body %s)", code, raw)
	}
	var refusal struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		NotesMoved            int `json:"notes_moved"`
		CardsMoved            int `json:"cards_moved"`
		CardsAdded            int `json:"cards_added"`
		CardsRemoved          int `json:"cards_removed"`
		ReviewsOnRemovedCards int `json:"reviews_on_removed_cards"`
	}
	if err := json.Unmarshal(raw, &refusal); err != nil {
		t.Fatalf("refusal is not JSON: %v (%s)", err, raw)
	}
	if refusal.Error.Code != CodeCardRemovalUnconfirmed {
		t.Errorf("refusal code = %q, want %q", refusal.Error.Code, CodeCardRemovalUnconfirmed)
	}
	if refusal.NotesMoved != 1 || refusal.CardsMoved != 1 || refusal.CardsAdded != 1 ||
		refusal.CardsRemoved != 1 || refusal.ReviewsOnRemovedCards != 1 {
		t.Errorf("refusal counts = note %d moved %d added %d removed %d reviews %d, want 1/1/1/1/1",
			refusal.NotesMoved, refusal.CardsMoved, refusal.CardsAdded, refusal.CardsRemoved, refusal.ReviewsOnRemovedCards)
	}
	if got := storedNoteTypeID(t, env, note.ID); got != source.ID {
		t.Fatalf("unconfirmed change moved the note to note type %q, want %q", got, source.ID)
	}
	if stored := storedCard(t, env, stale.ID); stored.DeletedAt.Valid {
		t.Fatal("the unconfirmed change soft-deleted the unmapped card")
	}
	if auditCount, err := store.NewAuditStore(env.db).CountByAction(t.Context(), store.ActionNoteTypeChange); err != nil {
		t.Fatalf("count audit rows: %v", err)
	} else if auditCount != 0 {
		t.Errorf("audit rows after refusal = %d, want 0", auditCount)
	}

	// The confirmed change removes only the unmapped card.
	code, raw = doJSON(t, router, http.MethodPost, changePath, key, body(map[string]any{"confirm_card_removal": true}))
	if code != http.StatusOK {
		t.Fatalf("confirmed change = %d, want 200 (body %s)", code, raw)
	}
	var result ChangeNoteTypeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("confirmed response is not JSON: %v (%s)", err, raw)
	}
	if result.NotesMoved != 1 || result.CardsMoved != 1 || result.CardsAdded != 1 ||
		result.CardsRemoved != 1 || result.ReviewsOnRemovedCards != 1 {
		t.Errorf("confirmed change = %+v, want 1 note, 1 moved, 1 added, 1 removed and 1 review", result)
	}
	if got := storedNoteTypeID(t, env, note.ID); got != target.ID {
		t.Errorf("note type after change = %q, want %q", got, target.ID)
	}
	if stored := storedCard(t, env, stale.ID); !stored.DeletedAt.Valid {
		t.Error("the unmapped card is still visible after the confirmed change")
	}
	remaining := visibleCardsOfNote(t, env, note.ID)
	if len(remaining) != 2 {
		t.Fatalf("cards after change = %+v, want the moved g-card and the generated g-extra", remaining)
	}
	afterByTemplate := map[string]store.Card{}
	for _, card := range remaining {
		afterByTemplate[card.Template] = card
	}
	if afterByTemplate["g-card"].ID != mapped.ID {
		t.Errorf("g-card id = %d, want the moved row %d", afterByTemplate["g-card"].ID, mapped.ID)
	}
	if afterByTemplate["g-extra"].ID == 0 {
		t.Error("the generated g-extra card is missing")
	}
	for name, cardID := range map[string]uint64{"mapped": mapped.ID, "removed": stale.ID} {
		if states, reviews := progressRowCounts(t, env, cardID); states != 1 || reviews != 1 {
			t.Errorf("%s card progress = %d state(s) and %d review(s), want 1/1", name, states, reviews)
		}
	}
}

// TestNoteTypeChangeReaderRefused is the role negative case: a reader is refused both operations and the list, and the
// deck is left untouched.
func TestNoteTypeChangeReaderRefused(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "note_type_change_owner_roles", store.RoleUser)
	reader := seedUser(t, env.db, "note_type_change_reader", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	if err := store.NewGrantStore(env.db).Grant(context.Background(), deck.ID, reader.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant reader: %v", err)
	}
	ownerKey := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	readerKey := seedKey(t, env.keys, reader.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	typesBase := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)
	emptyPath := fmt.Sprintf("/api/v1/decks/%s/empty-cards", deck.PublicID)

	source := createNoteTypeREST(t, router, typesBase, ownerKey, "Source", changeEndpointSourceConfig())
	target := createNoteTypeREST(t, router, typesBase, ownerKey, "Target", changeEndpointTargetConfig())
	createNotesREST(t, router, deck.PublicID, source.ID, ownerKey, []map[string]string{
		{"Front": "Q1", "Back": "A1"},
	})
	note := noteOfDeck(t, env, deck.ID)
	before := len(visibleCardsOfNote(t, env, note.ID))

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"list empty cards", http.MethodGet, emptyPath, ""},
		{"cleanup empty cards", http.MethodPost, emptyPath, `{"confirm_card_removal":true}`},
		{"change note type", http.MethodPost, typesBase + "/" + source.ID + "/change",
			`{"target_note_type":"` + target.ID + `","field_mapping":{"s-front":"g-question"},"template_mapping":{"s-card":"g-card"},"confirm_card_removal":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, raw := doJSON(t, router, tc.method, tc.path, readerKey, tc.body)
			if code != http.StatusForbidden {
				t.Fatalf("reader %s = %d, want 403 (body %s)", tc.name, code, raw)
			}
			assertErrorCode(t, raw, CodeInsufficientRole)
		})
	}

	if got := storedNoteTypeID(t, env, note.ID); got != source.ID {
		t.Errorf("note type after refused operations = %q, want %q", got, source.ID)
	}
	if got := len(visibleCardsOfNote(t, env, note.ID)); got != before {
		t.Errorf("cards after refused operations = %d, want the original %d", got, before)
	}
}

// storedNoteTypeID reads the public id of a note's stored note type.
func storedNoteTypeID(t *testing.T, env *testEnv, noteID uint64) string {
	t.Helper()
	var note store.Note
	if err := env.db.Unscoped().First(&note, "id = ?", noteID).Error; err != nil {
		t.Fatalf("load note %d: %v", noteID, err)
	}
	if note.NoteTypeID == nil {
		return ""
	}
	var nt store.NoteType
	if err := env.db.Unscoped().First(&nt, "id = ?", *note.NoteTypeID).Error; err != nil {
		t.Fatalf("load note type %d: %v", *note.NoteTypeID, err)
	}
	return nt.PublicID
}
