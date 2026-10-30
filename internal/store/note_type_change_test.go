package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
)

// cleanupConfig is a normal note type with two templates: the forward card renders Front, the reverse card renders Back.
func cleanupConfig() notetype.Config {
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

// changeSourceConfig is a normal note type whose field and template ids differ from the target's, so a test can tell
// a mapped move from a value that was simply carried over.
func changeSourceConfig() notetype.Config {
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

// changeTargetConfig is the target type: different field and template ids, and two templates in the reverse order of
// the source's, so a moved card's ordinal is visibly realigned.
func changeTargetConfig() notetype.Config {
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

// clozeConfig is a cloze note type: one template over the Text field, so a change that crosses the kind boundary is
// refused before a card could be moved into the other kind's card-generation rule.
func clozeConfig() notetype.Config {
	return notetype.Config{
		Kind: notetype.KindCloze,
		Fields: []notetype.Field{
			{ID: "c-text", Name: "Text"},
			{ID: "c-extra", Name: "Back Extra"},
		},
		Templates: []notetype.Template{
			{ID: "c-cloze", Name: "Cloze", QFormat: "{{cloze:Text}}", AFormat: "{{cloze:Text}}<br>{{Back Extra}}"},
		},
		CSS:         ".cloze { font-weight: bold; }",
		SortFieldID: "c-text",
	}
}

// createType writes a note type with the given config and returns its row.
func createType(t *testing.T, db *gorm.DB, deckID uint64, name string, cfg notetype.Config) NoteType {
	t.Helper()
	nt := NoteType{DeckID: deckID, Name: name}
	if err := NewNoteTypeStore(db).Create(context.Background(), &nt, cfg); err != nil {
		t.Fatalf("create note type %q: %v", name, err)
	}
	return nt
}

// seedProgress writes one card_state and one review row for a card, so a test can prove a content operation kept them.
func seedProgress(t *testing.T, db *gorm.DB, userID, cardID uint64) {
	t.Helper()
	state := CardState{CardID: cardID, UserID: userID, State: "review", Reps: 3, Version: 0}
	if err := db.Create(&state).Error; err != nil {
		t.Fatalf("create card state for card %d: %v", cardID, err)
	}
	now := time.Now().UTC()
	review := Review{
		CardID: cardID, UserID: userID, Rating: 3, GradeSource: "self",
		ReviewedAt: now, ReviewDay: "2026-10-31", StateBefore: 2,
	}
	if err := db.Create(&review).Error; err != nil {
		t.Fatalf("create review for card %d: %v", cardID, err)
	}
}

// cardProgressAt reports whether the card still holds the seeded progress rows.
func cardProgressAt(t *testing.T, db *gorm.DB, cardID uint64) (bool, bool) {
	t.Helper()
	var states, reviews int64
	if err := db.Model(&CardState{}).Where("card_id = ?", cardID).Count(&states).Error; err != nil {
		t.Fatalf("count card states for card %d: %v", cardID, err)
	}
	if err := db.Model(&Review{}).Where("card_id = ?", cardID).Count(&reviews).Error; err != nil {
		t.Fatalf("count reviews for card %d: %v", cardID, err)
	}
	return states == 1, reviews == 1
}

// TestCleanupEmptyCards proves the cleanup removes only empty cards, requires confirmation, keeps progress and reviews,
// and reports a dry run without writing.
func TestCleanupEmptyCards(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "empty_cleanup_owner")[0]
			deckID := seedDeck(t, db, owner)
			nt := createType(t, db, deckID, "Both directions", cleanupConfig())
			notes := NewNoteStore(db)

			note := Note{DeckID: deckID, NoteTypeID: &nt.ID}
			cards, err := notes.Create(ctx, &note, map[string]any{"Front": "Q1", "Back": "A1"})
			if err != nil {
				t.Fatalf("create note: %v", err)
			}
			if len(cards) != 2 {
				t.Fatalf("created note has %d cards, want 2", len(cards))
			}
			byTemplate := cardTemplates(cards)
			for _, cardID := range byTemplate {
				seedProgress(t, db, owner, cardID)
			}

			// Clearing Back makes the reverse card's front empty; a note write keeps that card (editing never removes one).
			if _, err := notes.Update(ctx, &Note{ID: note.ID}, map[string]any{"Front": "Q1", "Back": ""}); err != nil {
				t.Fatalf("clear Back: %v", err)
			}

			listed, err := notes.ListEmptyCards(ctx, deckID)
			if err != nil {
				t.Fatalf("ListEmptyCards() error = %v", err)
			}
			if len(listed) != 1 || listed[0].Template != "t-reverse" {
				t.Fatalf("ListEmptyCards() = %+v, want only t-reverse", listed)
			}
			if listed[0].CardID != byTemplate["t-reverse"] || listed[0].NoteID != note.ID ||
				currentNoteTypeID(t, db, note.ID) != nt.ID {
				t.Errorf("empty card = %+v, want the reverse card of note %d", listed[0], note.ID)
			}

			// A dry run reports the count and writes nothing.
			impact, err := notes.CleanupEmptyCards(ctx, deckID, EmptyCardCleanupOptions{DryRun: true})
			if err != nil {
				t.Fatalf("CleanupEmptyCards(dry run) error = %v", err)
			}
			if impact.CardsRemoved != 1 || impact.ReviewsOnRemovedCards != 1 {
				t.Errorf("dry-run impact = %+v, want 1 card and 1 review", impact)
			}
			if !cardVisible(t, db, byTemplate["t-reverse"]) {
				t.Fatal("dry run soft-deleted a card")
			}

			// An unconfirmed cleanup is refused and writes nothing.
			if _, err := notes.CleanupEmptyCards(ctx, deckID, EmptyCardCleanupOptions{}); !errors.Is(err, ErrCardRemovalUnconfirmed) {
				t.Fatalf("unconfirmed CleanupEmptyCards() error = %v, want ErrCardRemovalUnconfirmed", err)
			}
			if !cardVisible(t, db, byTemplate["t-reverse"]) {
				t.Fatal("unconfirmed cleanup soft-deleted a card")
			}

			// The confirmed cleanup removes exactly the empty card and keeps its progress and review rows.
			impact, err = notes.CleanupEmptyCards(ctx, deckID, EmptyCardCleanupOptions{Confirm: true})
			if err != nil {
				t.Fatalf("CleanupEmptyCards(confirm) error = %v", err)
			}
			if impact.CardsRemoved != 1 {
				t.Errorf("confirmed impact = %+v, want 1 card", impact)
			}
			if cardVisible(t, db, byTemplate["t-reverse"]) {
				t.Error("the empty card is still visible after the confirmed cleanup")
			}
			if !cardVisible(t, db, byTemplate["t-forward"]) {
				t.Error("the cleanup also removed the non-empty card")
			}
			state, review := cardProgressAt(t, db, byTemplate["t-reverse"])
			if !state || !review {
				t.Errorf("removed card progress survived = state %v review %v, want both true", state, review)
			}

			// Nothing is left to clean.
			again, err := notes.CleanupEmptyCards(ctx, deckID, EmptyCardCleanupOptions{Confirm: true})
			if err != nil {
				t.Fatalf("second CleanupEmptyCards() error = %v", err)
			}
			if again.CardsRemoved != 0 {
				t.Errorf("second cleanup removed %d cards, want 0", again.CardsRemoved)
			}
		})
	}
}

// TestChangeNoteTypeMappedCards proves a mapping moves every note's values and cards while every mapped card keeps its
// id and progress, and that a target type of another deck is refused.
func TestChangeNoteTypeMappedCards(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_type_change_owner")[0]
			deckID := seedDeck(t, db, owner)
			otherDeckID := seedDeck(t, db, owner)
			source := createType(t, db, deckID, "Source", changeSourceConfig())
			target := createType(t, db, deckID, "Target", changeTargetConfig())
			foreign := createType(t, db, otherDeckID, "Other deck", changeTargetConfig())
			notes := NewNoteStore(db)

			note := Note{DeckID: deckID, NoteTypeID: &source.ID}
			cards, err := notes.Create(ctx, &note, map[string]any{"Front": "<b>Q1</b>", "Back": "A1"})
			if err != nil {
				t.Fatalf("create note: %v", err)
			}
			byTemplate := cardTemplates(cards)
			for _, cardID := range byTemplate {
				seedProgress(t, db, owner, cardID)
			}

			mapping := NoteTypeChangeOptions{
				FieldMapping:    map[string]string{"s-front": "g-question", "s-back": "g-answer"},
				TemplateMapping: map[string]string{"s-card": "g-card", "s-extra": "g-extra"},
				DryRun:          true,
			}

			// A target note type of another deck is refused before anything is written.
			if _, err := notes.ChangeNoteType(ctx, deckID, source.ID, foreign.ID, mapping); !errors.Is(err, ErrNoteTypeDeckMismatch) {
				t.Fatalf("other-deck target error = %v, want ErrNoteTypeDeckMismatch", err)
			}

			// The dry run reports the counts and rolls back.
			impact, err := notes.ChangeNoteType(ctx, deckID, source.ID, target.ID, mapping)
			if err != nil {
				t.Fatalf("ChangeNoteType(dry run) error = %v", err)
			}
			if impact.NotesMoved != 1 || impact.CardsMoved != 2 || impact.CardsRemoved != 0 {
				t.Errorf("dry-run impact = %+v, want 1 note and 2 moved cards", impact)
			}
			if got := currentNoteTypeID(t, db, note.ID); got != source.ID {
				t.Fatalf("dry run moved the note to note type %d, want %d", got, source.ID)
			}

			mapping.DryRun = false
			impact, err = notes.ChangeNoteType(ctx, deckID, source.ID, target.ID, mapping)
			if err != nil {
				t.Fatalf("ChangeNoteType() error = %v", err)
			}
			if impact.NotesMoved != 1 || impact.CardsMoved != 2 || impact.CardsRemoved != 0 {
				t.Errorf("impact = %+v, want 1 note and 2 moved cards", impact)
			}

			if got := currentNoteTypeID(t, db, note.ID); got != target.ID {
				t.Errorf("note type after change = %d, want %d", got, target.ID)
			}
			values := NoteFieldValues(currentNoteFieldsJSON(t, db, note.ID))
			if values["g-question"] != "<b>Q1</b>" || values["g-answer"] != "A1" {
				t.Errorf("mapped values = %v, want g-question <b>Q1</b> and g-answer A1", values)
			}
			if _, ok := values["s-front"]; ok {
				t.Errorf("mapped values still carry the source field id: %v", values)
			}

			// Every mapped card keeps its row id and progress; only its template and ordinal follow the target.
			var stored []Card
			if err := db.Where("note_id = ?", note.ID).Order("id ASC").Find(&stored).Error; err != nil {
				t.Fatalf("load cards: %v", err)
			}
			if len(stored) != 2 {
				t.Fatalf("cards after change = %d, want 2", len(stored))
			}
			wantOrdinal := map[string]int{"g-card": 1, "g-extra": 0}
			wantID := map[string]uint64{"g-card": byTemplate["s-card"], "g-extra": byTemplate["s-extra"]}
			for _, card := range stored {
				if card.ID != wantID[card.Template] {
					t.Errorf("card %q id = %d, want %d (the moved row)", card.Template, card.ID, wantID[card.Template])
				}
				if card.Ordinal != wantOrdinal[card.Template] {
					t.Errorf("card %q ordinal = %d, want %d", card.Template, card.Ordinal, wantOrdinal[card.Template])
				}
				state, review := cardProgressAt(t, db, card.ID)
				if !state || !review {
					t.Errorf("mapped card %q lost progress: state %v review %v", card.Template, state, review)
				}
			}

			// The same type pair again has no notes left to move.
			second, err := notes.ChangeNoteType(ctx, deckID, source.ID, target.ID, NoteTypeChangeOptions{})
			if err != nil {
				t.Fatalf("second ChangeNoteType() error = %v", err)
			}
			if second.NotesMoved != 0 {
				t.Errorf("second change moved %d notes, want 0", second.NotesMoved)
			}
		})
	}
}

// TestChangeNoteTypeUnmappedTemplate proves a template without a mapping needs confirmation, then soft-deletes only its
// card while the mapped card keeps its progress and the removed card's review row survives.
func TestChangeNoteTypeUnmappedTemplate(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_type_change_unmapped_owner")[0]
			deckID := seedDeck(t, db, owner)
			source := createType(t, db, deckID, "Source", changeSourceConfig())
			target := createType(t, db, deckID, "Target", changeTargetConfig())
			notes := NewNoteStore(db)

			note := Note{DeckID: deckID, NoteTypeID: &source.ID}
			cards, err := notes.Create(ctx, &note, map[string]any{"Front": "Q1", "Back": "A1"})
			if err != nil {
				t.Fatalf("create note: %v", err)
			}
			byTemplate := cardTemplates(cards)
			for _, cardID := range byTemplate {
				seedProgress(t, db, owner, cardID)
			}

			opts := NoteTypeChangeOptions{
				FieldMapping:    map[string]string{"s-front": "g-question", "s-back": "g-answer"},
				TemplateMapping: map[string]string{"s-card": "g-card"},
				DryRun:          true,
			}
			impact, err := notes.ChangeNoteType(ctx, deckID, source.ID, target.ID, opts)
			if err != nil {
				t.Fatalf("dry run error = %v", err)
			}
			if impact.CardsMoved != 1 || impact.CardsRemoved != 1 || impact.ReviewsOnRemovedCards != 1 {
				t.Errorf("dry-run impact = %+v, want 1 moved, 1 removed and 1 review", impact)
			}

			// Without confirmation the store refuses and writes nothing.
			opts.DryRun = false
			if _, err := notes.ChangeNoteType(ctx, deckID, source.ID, target.ID, opts); !errors.Is(err, ErrCardRemovalUnconfirmed) {
				t.Fatalf("unconfirmed ChangeNoteType() error = %v, want ErrCardRemovalUnconfirmed", err)
			}
			if got := currentNoteTypeID(t, db, note.ID); got != source.ID {
				t.Fatalf("unconfirmed change moved the note to %d, want %d", got, source.ID)
			}
			if !cardVisible(t, db, byTemplate["s-extra"]) {
				t.Fatal("unconfirmed change soft-deleted the unmapped card")
			}

			opts.ConfirmCardRemoval = true
			impact, err = notes.ChangeNoteType(ctx, deckID, source.ID, target.ID, opts)
			if err != nil {
				t.Fatalf("confirmed ChangeNoteType() error = %v", err)
			}
			if impact.CardsMoved != 1 || impact.CardsRemoved != 1 {
				t.Errorf("impact = %+v, want 1 moved and 1 removed", impact)
			}
			if got := currentNoteTypeID(t, db, note.ID); got != target.ID {
				t.Errorf("note type after change = %d, want %d", got, target.ID)
			}
			if cardVisible(t, db, byTemplate["s-extra"]) {
				t.Error("the unmapped card is still visible")
			}
			if !cardVisible(t, db, byTemplate["s-card"]) {
				t.Error("the mapped card disappeared")
			}
			state, review := cardProgressAt(t, db, byTemplate["s-extra"])
			if !state || !review {
				t.Errorf("removed card progress survived = state %v review %v, want both true", state, review)
			}
			state, review = cardProgressAt(t, db, byTemplate["s-card"])
			if !state || !review {
				t.Errorf("mapped card lost progress = state %v review %v, want both true", state, review)
			}
		})
	}
}

// TestChangeNoteTypeValidation covers the refused shapes: a kind change, an unknown mapping id, and a duplicate target.
func TestChangeNoteTypeValidation(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_type_change_validation_owner")[0]
			deckID := seedDeck(t, db, owner)
			source := createType(t, db, deckID, "Source", changeSourceConfig())
			target := createType(t, db, deckID, "Target", changeTargetConfig())
			cloze := createType(t, db, deckID, "Cloze", clozeConfig())
			// A second cloze type gives the template-mapping case a same-kind pair; a same-id pair would be refused
			// earlier for being the same type, which would hide the rule under test.
			clozeTarget := createType(t, db, deckID, "Cloze target", clozeConfig())
			notes := NewNoteStore(db)

			cases := []struct {
				name   string
				source uint64
				target uint64
				opts   NoteTypeChangeOptions
				want   error
			}{
				{
					name:   "kind change",
					source: source.ID,
					target: cloze.ID,
					opts:   NoteTypeChangeOptions{},
					want:   ErrNoteTypeKindMismatch,
				},
				{
					name:   "same note type",
					source: source.ID,
					target: source.ID,
					opts:   NoteTypeChangeOptions{},
					want:   ErrNoteTypeMappingInvalid,
				},
				{
					name:   "unknown source field",
					source: source.ID,
					target: target.ID,
					opts:   NoteTypeChangeOptions{FieldMapping: map[string]string{"s-missing": "g-question"}},
					want:   ErrNoteTypeMappingInvalid,
				},
				{
					name:   "unknown target field",
					source: source.ID,
					target: target.ID,
					opts:   NoteTypeChangeOptions{FieldMapping: map[string]string{"s-front": "g-missing"}},
					want:   ErrNoteTypeMappingInvalid,
				},
				{
					name:   "two sources to one target",
					source: source.ID,
					target: target.ID,
					opts: NoteTypeChangeOptions{FieldMapping: map[string]string{
						"s-front": "g-question", "s-back": "g-question",
					}},
					want: ErrNoteTypeMappingInvalid,
				},
				{
					name:   "unknown template",
					source: source.ID,
					target: target.ID,
					opts:   NoteTypeChangeOptions{TemplateMapping: map[string]string{"s-card": "g-missing"}},
					want:   ErrNoteTypeMappingInvalid,
				},
				{
					name:   "cloze template mapping",
					source: cloze.ID,
					target: clozeTarget.ID,
					opts:   NoteTypeChangeOptions{TemplateMapping: map[string]string{"c-cloze": "c-cloze"}},
					want:   ErrNoteTypeMappingInvalid,
				},
			}
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					if _, err := notes.ChangeNoteType(ctx, deckID, test.source, test.target, test.opts); !errors.Is(err, test.want) {
						t.Errorf("ChangeNoteType() error = %v, want %v", err, test.want)
					}
				})
			}
		})
	}
}

// currentNoteTypeID reads a note's stored note type id.
func currentNoteTypeID(t *testing.T, db *gorm.DB, noteID uint64) uint64 {
	t.Helper()
	var note Note
	if err := db.Unscoped().First(&note, "id = ?", noteID).Error; err != nil {
		t.Fatalf("load note %d: %v", noteID, err)
	}
	if note.NoteTypeID == nil {
		return 0
	}
	return *note.NoteTypeID
}

// currentNoteFieldsJSON reads a note's stored fields_json.
func currentNoteFieldsJSON(t *testing.T, db *gorm.DB, noteID uint64) string {
	t.Helper()
	var note Note
	if err := db.Unscoped().First(&note, "id = ?", noteID).Error; err != nil {
		t.Fatalf("load note %d: %v", noteID, err)
	}
	return note.FieldsJSON
}

// cardVisible reports whether a card is still visible (row present and not soft-deleted).
func cardVisible(t *testing.T, db *gorm.DB, cardID uint64) bool {
	t.Helper()
	var count int64
	if err := db.Model(&Card{}).Where("id = ?", cardID).Count(&count).Error; err != nil {
		t.Fatalf("count card %d: %v", cardID, err)
	}
	return count == 1
}
