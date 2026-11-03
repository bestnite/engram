package store

import (
	"context"
	"slices"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
)

// TestCardStoreByNoteScopesToNote covers that ByNote returns only the target note's cards, in ascending ordinal order.
func TestCardStoreByNoteScopesToNote(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "card_scope_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			cards := NewCardStore(db)

			noteA := noteOfKind(t, db, deckID, "basic_both")
			if _, err := notes.Create(ctx, noteA, map[string]any{"front": "qa", "back": "aa"}); err != nil {
				t.Fatalf("Create(noteA) error = %v", err)
			}
			noteB := noteOfKind(t, db, deckID, "basic")
			if _, err := notes.Create(ctx, noteB, map[string]any{"front": "qb", "back": "ab"}); err != nil {
				t.Fatalf("Create(noteB) error = %v", err)
			}

			gotA, err := cards.ByNote(ctx, noteA.ID)
			if err != nil {
				t.Fatalf("ByNote(noteA) error = %v", err)
			}
			if len(gotA) != 2 {
				t.Fatalf("ByNote(noteA) = %d cards, want 2", len(gotA))
			}
			if gotA[0].Ordinal > gotA[1].Ordinal {
				t.Errorf("ByNote(noteA) not ordered by ordinal: %+v", gotA)
			}
			if _, err := cards.ByID(ctx, gotA[0].ID); err != nil {
				t.Errorf("ByID() error = %v", err)
			}

			gotB, err := cards.ByNote(ctx, noteB.ID)
			if err != nil {
				t.Fatalf("ByNote(noteB) error = %v", err)
			}
			if len(gotB) != 1 || gotB[0].NoteID != noteB.ID {
				t.Errorf("ByNote(noteB) = %+v, want only noteB's single card", gotB)
			}
		})
	}
}

// TestCardStoreHidesCardsOfSoftDeletedNote proves that after soft-deleting a note its cards are filtered by the query
// layer, and that the filtering is not achieved by rewriting cards rows (the rows are still there).
func TestCardStoreHidesCardsOfSoftDeletedNote(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "card_hide_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			cards := NewCardStore(db)

			n := noteOfKind(t, db, deckID, "basic_both")
			created, err := notes.Create(ctx, n, map[string]any{"front": "q", "back": "a"})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			firstID := created[0].ID

			if err := notes.Delete(ctx, n.ID); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}

			if got, err := cards.ByNote(ctx, n.ID); err != nil || len(got) != 0 {
				t.Errorf("ByNote() after soft delete = %+v, %v; want empty slice, nil", got, err)
			}
			if _, err := cards.ByID(ctx, firstID); !IsNotFound(err) {
				t.Errorf("ByID() after soft delete error = %v, want ErrRecordNotFound", err)
			}

			// cards rows (and their ids) were neither rewritten nor deleted.
			var cardRow Card
			if err := db.Unscoped().First(&cardRow, "id = ?", firstID).Error; err != nil {
				t.Fatalf("card row vanished after soft delete: %v", err)
			}
			var noteRow Note
			if err := db.Unscoped().First(&noteRow, "id = ?", n.ID).Error; err != nil {
				t.Fatalf("note row vanished after soft delete: %v", err)
			}
			if !noteRow.DeletedAt.Valid {
				t.Error("note deleted_at is not set after Delete()")
			}
			if cardRow.DeletedAt.Valid {
				t.Error("card deleted_at was set by note soft delete, want query-layer filtering instead")
			}
		})
	}
}

// TestSyncCardsRejectsUnknownTemplateShape covers the defensive path: an empty template and a duplicate template
// are both rejected before anything is written (the Go-side guard for the (note_id, template) unique constraint).
func TestSyncCardsRejectsUnknownTemplateShape(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "sync_shape_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)

			n := noteOfKind(t, db, deckID, "basic")
			if _, err := notes.Create(ctx, n, map[string]any{"front": "q", "back": "a"}); err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			emptyErr := db.Transaction(func(tx *gorm.DB) error {
				_, err := syncCards(tx, n.ID, []notetype.GeneratedCard{{Template: ""}}, time.Now().UTC())
				return err
			})
			if emptyErr == nil {
				t.Error("syncCards() accepted an empty template, want an error")
			}

			dupErr := db.Transaction(func(tx *gorm.DB) error {
				_, err := syncCards(tx, n.ID, []notetype.GeneratedCard{
					{Template: "reverse", Ordinal: 0},
					{Template: "reverse", Ordinal: 1},
				}, time.Now().UTC())
				return err
			})
			if dupErr == nil {
				t.Error("syncCards() accepted duplicate templates, want an error")
			}

			// neither failed sync may leave a half-written row.
			var rows int64
			if err := db.Unscoped().Model(&Card{}).Where("note_id = ?", n.ID).Count(&rows).Error; err != nil {
				t.Fatalf("count cards: %v", err)
			}
			if rows != 1 {
				t.Errorf("card rows after failed syncs = %d, want 1 (transaction must roll back)", rows)
			}
		})
	}
}

// TestCardStoreCountByNoteType covers the grouping behind the deck preview's note type jump: counts
// follow the note type name order, skip cards whose note is soft-deleted, and stay inside the deck.
func TestCardStoreCountByNoteType(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "card_note_type_owner")[0]
			deckID := seedDeck(t, db, owner)
			otherDeckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			create := func(kind string, fields map[string]any) *Note {
				t.Helper()
				note := noteOfKind(t, db, deckID, kind)
				if _, err := notes.Create(ctx, note, fields); err != nil {
					t.Fatalf("Create() error = %v", err)
				}
				return note
			}
			create("basic", map[string]any{"front": "q1", "back": "a1"})
			deleted := create("basic", map[string]any{"front": "q2", "back": "a2"})
			create("cloze", map[string]any{"text": "{{c1::water}} and {{c2::fire}}"})
			if _, err := notes.Create(ctx, noteOfKind(t, db, otherDeckID, "basic"),
				map[string]any{"front": "q3", "back": "a3"}); err != nil {
				t.Fatalf("Create(other deck) error = %v", err)
			}
			if err := notes.Delete(ctx, deleted.ID); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}

			cards := NewCardStore(db)
			counts, err := cards.CountByNoteType(ctx, deckID)
			if err != nil {
				t.Fatalf("CountByNoteType() error = %v", err)
			}
			want := []CardNoteTypeCount{{Name: "Basic", Count: 1}, {Name: "Cloze", Count: 2}}
			if !slices.Equal(counts, want) {
				t.Fatalf("CountByNoteType() = %+v, want %+v", counts, want)
			}

			// The counts are the list's own grouping, so they add up to the listed total.
			_, total, err := cards.ListByDeck(ctx, deckID, CardListOptions{})
			if err != nil {
				t.Fatalf("ListByDeck() error = %v", err)
			}
			var sum int64
			for _, count := range counts {
				sum += count.Count
			}
			if sum != total {
				t.Errorf("counts sum to %d, want the deck total %d", sum, total)
			}
		})
	}
}

// TestCardStoreNoteStepPosition covers the deck preview's note stepping in both orders: a forward
// step lands on the first card of the next note, a backward step on the first card of the previous
// note, and zero reports that no other note exists in that direction.
func TestCardStoreNoteStepPosition(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "card_note_step_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			create := func(kind string, fields map[string]any) *Note {
				t.Helper()
				note := noteOfKind(t, db, deckID, kind)
				if _, err := notes.Create(ctx, note, fields); err != nil {
					t.Fatalf("Create() error = %v", err)
				}
				return note
			}
			// A has one card, B four and C one: creation order is A, B's four cards, C.
			a := create("basic", map[string]any{"front": "qa", "back": "aa"})
			create("cloze", map[string]any{"text": "{{c1::a}} {{c2::b}} {{c3::c}} {{c4::d}}"})
			create("basic", map[string]any{"front": "qc", "back": "ac"})
			cards := NewCardStore(db)

			cases := []struct {
				name    string
				sort    string
				from    int
				forward bool
				want    int64
			}{
				{name: "forward into the four-card note", sort: CardListSortCreated, from: 1, forward: true, want: 2},
				{name: "forward past the four-card note", sort: CardListSortCreated, from: 2, forward: true, want: 6},
				{name: "forward from inside the note", sort: CardListSortCreated, from: 5, forward: true, want: 6},
				{name: "no note after the last", sort: CardListSortCreated, from: 6, forward: true, want: 0},
				{name: "back into the four-card note starts at its first card", sort: CardListSortCreated, from: 6, forward: false, want: 2},
				{name: "back from inside the note", sort: CardListSortCreated, from: 3, forward: false, want: 1},
				{name: "no note before the first", sort: CardListSortCreated, from: 1, forward: false, want: 0},
				{name: "cursor past the end", sort: CardListSortCreated, from: 99, forward: true, want: 0},
				{name: "cursor before the start", sort: CardListSortCreated, from: 0, forward: true, want: 0},
				{name: "note type order steps between notes of one type", sort: CardListSortNoteType, from: 1, forward: true, want: 2},
				{name: "note type order steps into the next type", sort: CardListSortNoteType, from: 2, forward: true, want: 3},
				{name: "note type order ends after the last type", sort: CardListSortNoteType, from: 3, forward: true, want: 0},
				{name: "note type order backwards", sort: CardListSortNoteType, from: 3, forward: false, want: 2},
				{name: "note type order backwards from the end", sort: CardListSortNoteType, from: 6, forward: false, want: 2},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					got, err := cards.NoteStepPosition(ctx, deckID, tc.sort, tc.from, tc.forward)
					if err != nil {
						t.Fatalf("NoteStepPosition() error = %v", err)
					}
					if got != tc.want {
						t.Errorf("NoteStepPosition(%q, from %d, forward %t) = %d, want %d",
							tc.sort, tc.from, tc.forward, got, tc.want)
					}
				})
			}

			// A soft-deleted note is not a step target and does not shift the positions around it:
			// the list is now B's four cards and then C.
			if err := notes.Delete(ctx, a.ID); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			for _, tc := range []struct {
				name    string
				from    int
				forward bool
				want    int64
			}{
				{name: "forward skips the deleted note", from: 1, forward: true, want: 5},
				{name: "backward lands on the first card of the previous note", from: 5, forward: false, want: 1},
				{name: "the deleted note is gone from the start", from: 1, forward: false, want: 0},
			} {
				t.Run(tc.name, func(t *testing.T) {
					got, err := cards.NoteStepPosition(ctx, deckID, CardListSortCreated, tc.from, tc.forward)
					if err != nil {
						t.Fatalf("NoteStepPosition() error = %v", err)
					}
					if got != tc.want {
						t.Errorf("NoteStepPosition(from %d, forward %t) = %d, want %d", tc.from, tc.forward, got, tc.want)
					}
				})
			}
		})
	}
}
