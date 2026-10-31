package web

import (
	"testing"

	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// TestReviewRenderAnswerIncludesQuestion pins the flag the review page reads on reveal: an Anki-style answer template
// that already shows the question reports true, so the page shows that face alone; engram's basic type, whose answer
// adds only the answer, reports false, so the question stays above it.
func TestReviewRenderAnswerIncludesQuestion(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Answer screen deck")

	anki := seedTypeInNote(t, db, deck, "{{type:Back}}", "answer", "", 0)
	ankiCard := cardOfNoteTemplate(t, db, anki.ID, "t-typein")
	if got := renderTypeInCard(t, srv, cookies, csrf, ankiCard.PublicID, deck.PublicID, ""); !got.AnswerIncludesQuestion {
		t.Error("a {{FrontSide}} answer reports answer_includes_question = false, want true")
	}

	stock, ok := notetype.EngramStockByKind("basic")
	if !ok {
		t.Fatal("engram basic stock is missing")
	}
	noteType := &store.NoteType{DeckID: deck.ID, Name: stock.Name}
	if err := store.NewNoteTypeStore(db).Create(t.Context(), noteType, stock.Config); err != nil {
		t.Fatalf("create engram basic note type: %v", err)
	}
	note := &store.Note{DeckID: deck.ID, NoteTypeID: store.Ptr(noteType.ID)}
	if _, err := store.NewNoteStore(db).Create(t.Context(), note, map[string]any{"front": "Q", "back": "A"}); err != nil {
		t.Fatalf("create engram basic note: %v", err)
	}
	engramCard := cardOfNoteTemplate(t, db, note.ID, "forward")
	if got := renderTypeInCard(t, srv, cookies, csrf, engramCard.PublicID, deck.PublicID, ""); got.AnswerIncludesQuestion {
		t.Error("an answer-only template reports answer_includes_question = true, want false")
	}
}
