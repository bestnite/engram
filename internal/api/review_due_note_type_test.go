package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// TestDueCardsAnswerFlowForCustomNoteType covers a note type created through the API whose shape matches no engram stock
// kind: its derived kind is empty, so the review page cannot look it up in the card-kind catalog. The due-card payload
// must still say how the card is answered: no answer spec gives answer_control null and machine_graded false, and the
// field order lets the page list the note's values without the catalog.
func TestDueCardsAnswerFlowForCustomNoteType(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "custom_note_type_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite, store.ScopeReview}, nil).Plaintext

	cfg := notetype.Config{
		Kind: notetype.KindNormal,
		Fields: []notetype.Field{
			{ID: "f-front", Name: "Question"},
			{ID: "f-back", Name: "Answer"},
		},
		Templates: []notetype.Template{{
			ID: "card-1", Name: "Card 1", QFormat: "{{Question}}", AFormat: "{{Answer}}",
		}},
		CSS:         ".card { color: black; }",
		SortFieldID: "f-front",
	}
	created := createNoteTypeREST(t, router, fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID), key, "Custom", cfg)
	createdType, err := store.NewNoteTypeStore(env.db).ByPublicID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("load created note type: %v", err)
	}

	now := env.now.UTC()
	note := store.Note{
		DeckID:     deck.ID,
		NoteTypeID: &createdType.ID,
		FieldsJSON: `{"f-front":"自定义问题","f-back":"自定义答案"}`,
		TagsJSON:   "[]",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := env.db.Create(&note).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	card := store.Card{NoteID: note.ID, Template: "card-1", Ordinal: 0, CreatedAt: now}
	if err := env.db.Create(&card).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	due := env.now.Add(-time.Hour).UTC()
	if err := env.db.Create(&store.CardState{CardID: card.ID, UserID: owner.ID, State: "review", DueAt: &due, Version: 1}).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}

	status, raw := doJSON(t, router, http.MethodGet, "/api/v1/review/due?deck="+deck.PublicID, key, "")
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/review/due = %d (body %s)", status, raw)
	}
	// The key is present and null, rather than omitted: the client reads "no answer spec" from that null.
	if !bytes.Contains(raw, []byte(`"answer_control":null`)) {
		t.Errorf("due queue body has no answer_control null: %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"machine_graded":false`)) {
		t.Errorf("due queue body has no machine_graded false: %s", raw)
	}
	var body struct {
		Cards []DueCard `json:"cards"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("due queue response is not JSON: %v (%s)", err, raw)
	}
	if len(body.Cards) != 1 {
		t.Fatalf("due queue = %d cards, want 1 (%s)", len(body.Cards), raw)
	}
	got := body.Cards[0]
	if got.AnswerControl != nil {
		t.Errorf("answer_control = %q, want null for a template without an answer spec", *got.AnswerControl)
	}
	if got.MachineGraded {
		t.Error("machine_graded = true, want false for a template without an answer spec")
	}
	if got.Kind != "" {
		t.Errorf("kind = %q, want the empty string for a note type matching no stock kind", got.Kind)
	}
	if want := []string{"f-front", "f-back"}; !reflect.DeepEqual(got.FieldOrder, want) {
		t.Errorf("field_order = %v, want %v in config order", got.FieldOrder, want)
	}
}
