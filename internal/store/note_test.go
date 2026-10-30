package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
)

// seedDeck creates a deck owned by the given user (together with the preset it references) and returns the deck primary key.
func seedDeck(t *testing.T, db *gorm.DB, ownerID uint64) uint64 {
	t.Helper()
	presetID := seedPresetRow(t, db, ownerID)
	d := Deck{OwnerUserID: ownerID, Name: "Deck", Description: "",
		PresetID: presetID, CreatedAt: time.Now().UTC()}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	return d.ID
}

// cardTemplates flattens a set of cards into template→id, making reuse and order easy to assert.
func cardTemplates(cards []Card) map[string]uint64 {
	out := make(map[string]uint64, len(cards))
	for _, c := range cards {
		out[c.Template] = c.ID
	}
	return out
}

// TestNoteCreateGeneratesCards covers the first half: creating a note generates cards through the note type.
func TestNoteCreateGeneratesCards(t *testing.T) {
	cases := []struct {
		name          string
		kind          string
		fields        map[string]any
		wantTemplates []string
		wantOrdinals  []int
	}{
		{"basic", "basic", map[string]any{"front": "q", "back": "a"}, []string{"forward"}, []int{0}},
		{"basic_both", "basic_both", map[string]any{"front": "q", "back": "a"}, []string{"forward", "reverse"}, []int{0, 1}},
		{"cloze", "cloze", map[string]any{"text": "a {{c1::x}} b {{c2::y}}"}, []string{"cloze:1", "cloze:2"}, []int{0, 1}},
		{"list", "list", map[string]any{"prompt": "p", "items": []any{"a", "b"}}, []string{"forward"}, []int{0}},
	}
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_create_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			cards := NewCardStore(db)

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					n := noteOfKind(t, db, deckID, tc.kind)
					created, err := notes.Create(ctx, n, tc.fields)
					if err != nil {
						t.Fatalf("Create() error = %v", err)
					}
					if n.ID == 0 {
						t.Fatal("Create() did not populate note ID")
					}
					if len(created) != len(tc.wantTemplates) {
						t.Fatalf("Create() produced %d cards, want %d", len(created), len(tc.wantTemplates))
					}
					if n.FieldsJSON == "" || n.TagsJSON != "[]" {
						t.Errorf("Create() left FieldsJSON=%q TagsJSON=%q, want encoded fields and []", n.FieldsJSON, n.TagsJSON)
					}
					got, err := cards.ByNote(ctx, n.ID)
					if err != nil {
						t.Fatalf("ByNote() error = %v", err)
					}
					if len(got) != len(tc.wantTemplates) {
						t.Fatalf("ByNote() returned %d cards, want %d", len(got), len(tc.wantTemplates))
					}
					for i, c := range got {
						if c.Template != tc.wantTemplates[i] || c.Ordinal != tc.wantOrdinals[i] {
							t.Errorf("card[%d] = {%s, %d}, want {%s, %d}",
								i, c.Template, c.Ordinal, tc.wantTemplates[i], tc.wantOrdinals[i])
						}
						if c.ID == 0 || c.NoteID != n.ID {
							t.Errorf("card[%d] id/note_id = %d/%d, want nonzero/%d", i, c.ID, c.NoteID, n.ID)
						}
					}
				})
			}
		})
	}
}

// TestNoteWriteStoresFieldIDsAndSearchText covers the stored shape: values keyed by field id, and the search text taken
// from the plain text of the searchable fields.
func TestNoteWriteStoresFieldIDsAndSearchText(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_fields_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)

			n := noteOfKind(t, db, deckID, "basic")
			if _, err := notes.Create(ctx, n, map[string]any{
				"front": "<p>What is <strong>bold</strong>?</p>", "back": "Paris",
			}); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			values, err := ParseFieldValues(n.FieldsJSON)
			if err != nil {
				t.Fatalf("ParseFieldValues() error = %v", err)
			}
			if values["front"] != "<p>What is <strong>bold</strong>?</p>" || values["back"] != "Paris" {
				t.Errorf("stored values = %+v, want the HTML keyed by field id", values)
			}
			if !strings.Contains(n.SearchText, "what is bold?") || !strings.Contains(n.SearchText, "paris") {
				t.Errorf("search_text = %q, want the plain text of both fields, lowered", n.SearchText)
			}
		})
	}
}

// TestNoteUpdateKeepsCardIDs is the acceptance case:
// after the note's fields are updated, the existing cards and their ids stay unchanged.
func TestNoteUpdateKeepsCardIDs(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_update_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			cards := NewCardStore(db)

			// Bidirectional card: two cards, the easiest way to expose an id change caused by "delete the old, insert the new".
			n := noteOfKind(t, db, deckID, "basic_both")
			noteTypeID := *n.NoteTypeID
			created, err := notes.Create(ctx, n, map[string]any{"front": "q1", "back": "a1"})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			before := cardTemplates(created)
			if len(before) != 2 {
				t.Fatalf("setup produced %d cards, want 2", len(before))
			}

			// Change the fields (the content changes, the template does not).
			n2 := &Note{ID: n.ID, DeckID: deckID, NoteTypeID: Ptr(noteTypeID)}
			updated, err := notes.Update(ctx, n2, map[string]any{"front": "q1-edited", "back": "a1-edited"})
			if err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			after := cardTemplates(updated)
			if len(after) != len(before) {
				t.Fatalf("Update() changed card count to %d, want %d", len(after), len(before))
			}
			for template, id := range before {
				if after[template] != id {
					t.Errorf("card %q id changed from %d to %d after Update(), want unchanged",
						template, id, after[template])
				}
			}

			// Re-read from the database to confirm this is not an in-memory illusion and that no extra rows appeared.
			reloaded, err := cards.ByNote(ctx, n.ID)
			if err != nil {
				t.Fatalf("ByNote() after update error = %v", err)
			}
			if len(reloaded) != 2 {
				t.Fatalf("after Update() DB has %d cards, want 2", len(reloaded))
			}
			got := cardTemplates(reloaded)
			for template, id := range before {
				if got[template] != id {
					t.Errorf("reloaded card %q id = %d, want %d", template, got[template], id)
				}
			}

			// Idempotent: updating with the same fields again leaves the ids unchanged.
			if _, err := notes.Update(ctx, &Note{ID: n.ID, DeckID: deckID, NoteTypeID: Ptr(noteTypeID)},
				map[string]any{"front": "q1-edited", "back": "a1-edited"}); err != nil {
				t.Fatalf("second Update() error = %v", err)
			}
			reloaded, err = cards.ByNote(ctx, n.ID)
			if err != nil {
				t.Fatalf("ByNote() after second update error = %v", err)
			}
			for template, id := range before {
				if cardTemplates(reloaded)[template] != id {
					t.Errorf("card %q id changed after idempotent Update()", template)
				}
			}
		})
	}
}

// TestNoteUpdateGrowingClozeAddsCardsOnly covers a change of the generated card set:
// a new cloze number only adds a card; existing cards and their ids are unaffected.
func TestNoteUpdateGrowingClozeAddsCardsOnly(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_cloze_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			cards := NewCardStore(db)

			n := noteOfKind(t, db, deckID, "cloze")
			noteTypeID := *n.NoteTypeID
			created, err := notes.Create(ctx, n, map[string]any{"text": "a {{c1::x}} b {{c2::y}}"})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			before := cardTemplates(created)
			if len(before) != 2 {
				t.Fatalf("setup produced %d cards, want 2", len(before))
			}

			// Add a third ordinal: cloze:1 / cloze:2 must keep their original ids, only cloze:3 is added.
			if _, err := notes.Update(ctx, &Note{ID: n.ID, DeckID: deckID, NoteTypeID: Ptr(noteTypeID)},
				map[string]any{"text": "a {{c1::x}} b {{c2::y}} c {{c3::z}}"}); err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			reloaded, err := cards.ByNote(ctx, n.ID)
			if err != nil {
				t.Fatalf("ByNote() error = %v", err)
			}
			if len(reloaded) != 3 {
				t.Fatalf("after Update() DB has %d cards, want 3", len(reloaded))
			}
			after := cardTemplates(reloaded)
			for _, template := range []string{"cloze:1", "cloze:2"} {
				if after[template] != before[template] {
					t.Errorf("card %q id = %d, want unchanged %d", template, after[template], before[template])
				}
			}
			if after["cloze:3"] == 0 {
				t.Error("new card cloze:3 was not created")
			}
		})
	}
}

// TestNoteUpdateKeepingEmptyClozeCard covers the shrinking direction: removing {{c2::}} leaves the
// cloze:2 card in place, visible and with its progress, because a note write adds cards and never
// removes one. It also covers the two rules around an older soft-deleted row: a row an earlier
// version soft-deleted stays soft-deleted while the note does not generate it again, and the same
// row is restored when the note generates it again.
func TestNoteUpdateKeepingEmptyClozeCard(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_cloze_shrink_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			cards := NewCardStore(db)

			n := noteOfKind(t, db, deckID, "cloze")
			noteTypeID := *n.NoteTypeID
			created, err := notes.Create(ctx, n, map[string]any{"text": "a {{c1::x}} b {{c2::y}}"})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			before := cardTemplates(created)
			c2 := before["cloze:2"]
			if err := db.Create(&CardState{CardID: c2, UserID: owner, State: "review", Reps: 3, Version: 3}).Error; err != nil {
				t.Fatalf("seed card state: %v", err)
			}

			// Removing c2 must keep the card and its progress.
			if _, err := notes.Update(ctx, &Note{ID: n.ID, DeckID: deckID, NoteTypeID: Ptr(noteTypeID)},
				map[string]any{"text": "a {{c1::x}} b y"}); err != nil {
				t.Fatalf("Update(remove c2) error = %v", err)
			}
			live, err := cards.ByNote(ctx, n.ID)
			if err != nil {
				t.Fatalf("ByNote() error = %v", err)
			}
			templates := cardTemplates(live)
			if len(templates) != 2 {
				t.Fatalf("after removing c2 the note has %d visible cards, want both cloze:1 and cloze:2", len(templates))
			}
			if templates["cloze:2"] != c2 {
				t.Errorf("cloze:2 card id after removing the marker = %d, want the unchanged %d", templates["cloze:2"], c2)
			}

			// A row an earlier version soft-deleted stays soft-deleted while the note does not
			// generate it again. The row is written directly, because the current write path cannot
			// produce that state any more.
			now := time.Now().UTC()
			if err := db.Model(&Card{}).Where("id = ?", c2).Update("deleted_at", now).Error; err != nil {
				t.Fatalf("simulate an older soft delete: %v", err)
			}
			if _, err := notes.Update(ctx, &Note{ID: n.ID, DeckID: deckID, NoteTypeID: Ptr(noteTypeID)},
				map[string]any{"text": "a {{c1::x}} b y"}); err != nil {
				t.Fatalf("Update(remove c2 again) error = %v", err)
			}
			afterRemove, err := cards.ByNote(ctx, n.ID)
			if err != nil {
				t.Fatalf("ByNote() error = %v", err)
			}
			if got := cardTemplates(afterRemove); len(got) != 1 || got["cloze:1"] == 0 {
				t.Fatalf("visible cards after the older soft delete = %v, want only cloze:1", got)
			}
			var stillDeleted Card
			if err := db.Unscoped().First(&stillDeleted, "id = ?", c2).Error; err != nil {
				t.Fatalf("load cloze:2 row: %v", err)
			}
			if !stillDeleted.DeletedAt.Valid {
				t.Error("an older soft-deleted card was restored although the note still does not generate it")
			}

			// Generating c2 again restores the same row.
			if _, err := notes.Update(ctx, &Note{ID: n.ID, DeckID: deckID, NoteTypeID: Ptr(noteTypeID)},
				map[string]any{"text": "a {{c1::x}} b {{c2::y}}"}); err != nil {
				t.Fatalf("Update(bring c2 back) error = %v", err)
			}
			restored, err := cards.ByNote(ctx, n.ID)
			if err != nil {
				t.Fatalf("ByNote() error = %v", err)
			}
			if got := cardTemplates(restored); got["cloze:2"] != c2 {
				t.Fatalf("restored cloze:2 card id = %d, want the original %d", got["cloze:2"], c2)
			}

			// Throughout the whole flow c2's progress row is never deleted or modified.
			var st CardState
			if err := db.Where("card_id = ? AND user_id = ?", c2, owner).Take(&st).Error; err != nil {
				t.Fatalf("card state of c2 disappeared: %v", err)
			}
			if st.Reps != 3 || st.Version != 3 {
				t.Errorf("card state of c2 = reps %d version %d, want 3 and 3", st.Reps, st.Version)
			}
		})
	}
}

// TestNoteUpdateKeepingEmptyReverseCard is the acceptance case for an optional-reversed note:
// clearing the field behind the reverse template keeps the reverse card and its card_states.
func TestNoteUpdateKeepingEmptyReverseCard(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_optional_reverse_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			cards := NewCardStore(db)

			cfg, ok := optionalReversedStockConfig()
			if !ok {
				t.Fatal("notetype.StockConfigs() has no basic_optional_reversed config")
			}
			nt := &NoteType{DeckID: deckID, Name: "Basic (optional reversed card)"}
			if err := NewNoteTypeStore(db).Create(ctx, nt, cfg); err != nil {
				t.Fatalf("create optional reversed note type: %v", err)
			}

			n := &Note{DeckID: deckID, NoteTypeID: Ptr(nt.ID)}
			created, err := notes.Create(ctx, n, map[string]any{
				"Front": "question", "Back": "answer", "Add Reverse": "1",
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			before := cardTemplates(created)
			reverseID, ok := before["t1"]
			if !ok {
				t.Fatalf("created cards = %v, want the reverse template t1", before)
			}
			if err := db.Create(&CardState{CardID: reverseID, UserID: owner, State: "review", Reps: 4, Version: 2}).Error; err != nil {
				t.Fatalf("seed reverse card state: %v", err)
			}

			// Clearing Add Reverse makes the reverse template render an empty front, so the note no
			// longer generates that card.
			if _, err := notes.Update(ctx, &Note{ID: n.ID, DeckID: deckID, NoteTypeID: Ptr(nt.ID)},
				map[string]any{"Front": "question", "Back": "answer", "Add Reverse": ""}); err != nil {
				t.Fatalf("Update(clear Add Reverse) error = %v", err)
			}
			live, err := cards.ByNote(ctx, n.ID)
			if err != nil {
				t.Fatalf("ByNote() error = %v", err)
			}
			after := cardTemplates(live)
			if after["t1"] != reverseID {
				t.Fatalf("reverse card id after clearing Add Reverse = %d, want the unchanged %d (visible cards %v)", after["t1"], reverseID, after)
			}

			var st CardState
			if err := db.Where("card_id = ? AND user_id = ?", reverseID, owner).Take(&st).Error; err != nil {
				t.Fatalf("reverse card state disappeared: %v", err)
			}
			if st.Reps != 4 || st.Version != 2 {
				t.Errorf("reverse card state = reps %d version %d, want 4 and 2", st.Reps, st.Version)
			}
		})
	}
}

// optionalReversedStockConfig returns Anki's basic_optional_reversed stock config, so the test
// speaks the real field ids (f0, f1, f2) and template ids (t0, t1) of that note type.
func optionalReversedStockConfig() (notetype.Config, bool) {
	for _, cfg := range notetype.StockConfigs() {
		if cfg.OriginalStockKind != nil && *cfg.OriginalStockKind == notetype.StockBasicOptionalReversed {
			return cfg, true
		}
	}
	return notetype.Config{}, false
}

// TestNoteSoftDeleteHidesCardsAndRestore covers the other half:
// after a note is soft-deleted its cards are invisible, visible again once restored, and no row is physically deleted along the way.
func TestNoteSoftDeleteHidesCardsAndRestore(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_delete_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			cards := NewCardStore(db)

			n := noteOfKind(t, db, deckID, "basic_both")
			created, err := notes.Create(ctx, n, map[string]any{"front": "q", "back": "a"})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			ids := cardTemplates(created)

			if err := notes.Delete(ctx, n.ID); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			if _, err := notes.ByID(ctx, n.ID); !IsNotFound(err) {
				t.Errorf("ByID() after Delete() error = %v, want ErrRecordNotFound", err)
			}
			visible, err := cards.ByNote(ctx, n.ID)
			if err != nil {
				t.Fatalf("ByNote() after Delete() error = %v", err)
			}
			if len(visible) != 0 {
				t.Errorf("ByNote() after Delete() returned %d cards, want 0", len(visible))
			}
			for template, id := range ids {
				if _, err := cards.ByID(ctx, id); !IsNotFound(err) {
					t.Errorf("ByID(%d) for %q after Delete() error = %v, want ErrRecordNotFound", id, template, err)
				}
			}

			// The rows must still be there (progress hangs off the card, so nothing may be physically deleted).
			var cardRows int64
			if err := db.Unscoped().Model(&Card{}).Where("note_id = ?", n.ID).Count(&cardRows).Error; err != nil {
				t.Fatalf("count card rows: %v", err)
			}
			if cardRows != 2 {
				t.Errorf("card rows after Delete() = %d, want 2 (soft delete must not remove rows)", cardRows)
			}

			// Deleting an already-deleted note again -> not found.
			if err := notes.Delete(ctx, n.ID); !IsNotFound(err) {
				t.Errorf("second Delete() error = %v, want ErrRecordNotFound", err)
			}

			if err := notes.RestoreInTx(ctx, db, n.ID); err != nil {
				t.Fatalf("RestoreInTx() error = %v", err)
			}
			if _, err := notes.ByID(ctx, n.ID); err != nil {
				t.Errorf("ByID() after Restore() error = %v", err)
			}
			restored, err := cards.ByNote(ctx, n.ID)
			if err != nil {
				t.Fatalf("ByNote() after Restore() error = %v", err)
			}
			if len(restored) != 2 {
				t.Fatalf("ByNote() after Restore() returned %d cards, want 2", len(restored))
			}
			for template, id := range ids {
				if cardTemplates(restored)[template] != id {
					t.Errorf("card %q id changed across delete/restore, want %d", template, id)
				}
			}

			// Restoring a non-existent note -> not found.
			if err := notes.RestoreInTx(ctx, db, 999999); !IsNotFound(err) {
				t.Errorf("RestoreInTx(missing) error = %v, want ErrRecordNotFound", err)
			}
		})
	}
}

// TestNoteCardTemplateUniqueness covers "enforce (note_id, template) uniqueness":
// the database unique index is the backstop, and the pipeline itself produces no duplicate template either.
func TestNoteCardTemplateUniqueness(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_unique_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)

			n := noteOfKind(t, db, deckID, "basic")
			if _, err := notes.Create(ctx, n, map[string]any{"front": "q", "back": "a"}); err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			// Database backstop: a bare insert with the same (note_id, template) must fail.
			dup := Card{NoteID: n.ID, Template: "forward", Ordinal: 0, CreatedAt: time.Now().UTC()}
			if err := db.Create(&dup).Error; err == nil {
				t.Error("duplicate (note_id, template) insert was accepted, want unique violation")
			}

			// Pipeline-side defence: a duplicate template within one note is rejected before the write.
			err := db.Transaction(func(tx *gorm.DB) error {
				wanted := []notetype.GeneratedCard{
					{Template: "forward", Ordinal: 0},
					{Template: "forward", Ordinal: 1},
				}
				_, err := syncCards(tx, n.ID, wanted, time.Now().UTC())
				return err
			})
			if err == nil {
				t.Error("syncCards() accepted duplicate templates, want an error")
			}

			// No duplicate template appears in the result.
			var rows []Card
			if err := db.Unscoped().Where("note_id = ?", n.ID).Find(&rows).Error; err != nil {
				t.Fatalf("load cards: %v", err)
			}
			seen := map[string]int{}
			for _, c := range rows {
				seen[c.Template]++
			}
			for template, count := range seen {
				if count != 1 {
					t.Errorf("template %q appears %d times for one note, want 1", template, count)
				}
			}
		})
	}
}

// TestNoteValidationRejectsInvalid covers the negative cases of the write path: missing deck, missing note type,
// an unknown field name (named in the error) and an unknown stock kind.
func TestNoteValidationRejectsInvalid(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_validate_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)

			if _, err := notes.Create(ctx, &Note{NoteTypeID: Ptr(uint64(1))}, map[string]any{"front": "q", "back": "a"}); !errors.Is(err, ErrNoteDeckRequired) {
				t.Errorf("Create(no deck) error = %v, want ErrNoteDeckRequired", err)
			}
			if _, err := notes.Create(ctx, &Note{DeckID: deckID}, map[string]any{"front": "q", "back": "a"}); !errors.Is(err, ErrNoteTypeRequired) {
				t.Errorf("Create(no note type) error = %v, want ErrNoteTypeRequired", err)
			}
			if _, err := NewNoteStore(db).EnsureStockNoteType(ctx, deckID, "nope"); err == nil {
				t.Error("EnsureStockNoteType(unknown kind) error = nil, want an error")
			}
			if _, err := notes.Create(ctx, noteOfKind(t, db, deckID, "basic"), map[string]any{"front": "q", "nosuch": "x"}); err == nil ||
				!strings.Contains(err.Error(), "nosuch") {
				t.Errorf("Create(unknown field) error = %v, want an error naming the field", err)
			}

			// Illegal content must never reach the database.
			var count int64
			if err := db.Model(&Note{}).Count(&count).Error; err != nil {
				t.Fatalf("count notes: %v", err)
			}
			if count != 0 {
				t.Errorf("notes persisted after failed creates = %d, want 0", count)
			}
		})
	}
}
