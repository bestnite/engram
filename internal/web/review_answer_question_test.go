package web

import (
	"testing"

	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// TestReviewRenderAnswerIncludesQuestion pins the flag the review page reads on reveal: an Anki-style answer template
// that already shows the question reports true, so the page shows that face alone, and engram's basic type now answers
// that way too; a template whose answer adds only the answer (the shape the note migration still writes) reports false,
// so the question stays above it.
func TestReviewRenderAnswerIncludesQuestion(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Answer screen deck")

	anki := seedTypeInNote(t, db, deck, "{{type:Back}}", "answer", "", 0)
	ankiCard := cardOfNoteTemplate(t, db, anki.ID, "t-typein")
	if got := renderTypeInCard(t, srv, cookies, csrf, ankiCard.PublicID, deck.PublicID, ""); !got.AnswerIncludesQuestion {
		t.Error("a {{FrontSide}} answer reports answer_includes_question = false, want true")
	}

	cases := []struct {
		name   string
		lookup func(string) (notetype.EngramStock, bool)
		want   bool
	}{
		{"engram basic", notetype.EngramStockByKind, true},
		{"answer-only migrated basic", notetype.EngramMigrationStockByKind, false},
	}
	for _, tc := range cases {
		stock, ok := tc.lookup("basic")
		if !ok {
			t.Fatalf("%s: stock is missing", tc.name)
		}
		noteType := &store.NoteType{DeckID: deck.ID, Name: tc.name}
		if err := store.NewNoteTypeStore(db).Create(t.Context(), noteType, stock.Config); err != nil {
			t.Fatalf("%s: create note type: %v", tc.name, err)
		}
		note := &store.Note{DeckID: deck.ID, NoteTypeID: store.Ptr(noteType.ID)}
		if _, err := store.NewNoteStore(db).Create(t.Context(), note, map[string]any{"front": "Q", "back": "A"}); err != nil {
			t.Fatalf("%s: create note: %v", tc.name, err)
		}
		card := cardOfNoteTemplate(t, db, note.ID, "forward")
		if got := renderTypeInCard(t, srv, cookies, csrf, card.PublicID, deck.PublicID, ""); got.AnswerIncludesQuestion != tc.want {
			t.Errorf("%s: answer_includes_question = %v, want %v", tc.name, got.AnswerIncludesQuestion, tc.want)
		}
	}
}
