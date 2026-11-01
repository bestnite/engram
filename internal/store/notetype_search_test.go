package store

import (
	"context"
	"testing"

	"git.nite07.com/nite/engram/internal/notetype"
)

// searchFields returns the two fields the search tests vary: front and back.
func searchFields() []notetype.Field {
	return []notetype.Field{{ID: "f-front", Name: "Front"}, {ID: "f-back", Name: "Back"}}
}

// searchConfig builds a note type over the given fields, so a config change that
// adds, removes or flags a field is the only difference between two updates.
func searchConfig(fields []notetype.Field, qFormat, aFormat string) notetype.Config {
	return notetype.Config{
		Kind:        notetype.KindNormal,
		Fields:      fields,
		Templates:   []notetype.Template{{ID: "t-card", Name: "Card 1", QFormat: qFormat, AFormat: aFormat}},
		CSS:         ".card { color: black; }",
		SortFieldID: "f-front",
	}
}

// TestNoteTypeUpdateRewritesSearchText asserts a note type edit recomputes the
// search text of its notes in the same transaction: excluding a field stops its
// text matching, including it makes the text match again, and removing the field
// takes its text out of the index for good.
func TestNoteTypeUpdateRewritesSearchText(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_type_search_owner")[0]
			deckID := seedDeck(t, db, owner)
			types := NewNoteTypeStore(db)
			notes := NewNoteStore(db)

			nt := NoteType{DeckID: deckID, Name: "Searchable"}
			if err := types.Create(ctx, &nt, searchConfig(searchFields(), "{{Front}}", "{{Back}}")); err != nil {
				t.Fatalf("create note type: %v", err)
			}
			note := Note{DeckID: deckID, NoteTypeID: &nt.ID}
			if _, err := notes.Create(ctx, &note, map[string]any{"Front": "alpha", "Back": "beta"}); err != nil {
				t.Fatalf("create note: %v", err)
			}
			found := func(query string) int64 {
				t.Helper()
				list, total, err := notes.List(ctx, NoteListOptions{DeckID: deckID, Query: query})
				if err != nil {
					t.Fatalf("List(%q) error = %v", query, err)
				}
				if int64(len(list)) != total {
					t.Fatalf("List(%q) returned %d rows but total %d", query, len(list), total)
				}
				return total
			}
			update := func(cfg notetype.Config, version int) {
				t.Helper()
				row := nt
				if _, err := types.Update(ctx, &row, cfg, NoteTypeUpdateOptions{ExpectedVersion: version, ConfirmCardRemoval: true}); err != nil {
					t.Fatalf("Update() error = %v", err)
				}
			}

			if got := found("alpha"); got != 1 {
				t.Fatalf("search for the front before any edit = %d rows, want 1", got)
			}

			excluded := searchFields()
			excluded[0].ExcludeFromSearch = true
			update(searchConfig(excluded, "{{Front}}", "{{Back}}"), 0)
			if got := found("alpha"); got != 0 {
				t.Errorf("search for an excluded field = %d rows, want 0", got)
			}
			if got := found("beta"); got != 1 {
				t.Errorf("search for the remaining field = %d rows, want 1", got)
			}

			update(searchConfig(searchFields(), "{{Front}}", "{{Back}}"), 1)
			if got := found("alpha"); got != 1 {
				t.Errorf("search for a re-included field = %d rows, want 1", got)
			}

			update(searchConfig(searchFields()[:1], "{{Front}}", "{{Front}}"), 2)
			if got := found("beta"); got != 0 {
				t.Errorf("search for a deleted field = %d rows, want 0", got)
			}
			if got := found("alpha"); got != 1 {
				t.Errorf("search for the kept field after a field delete = %d rows, want 1", got)
			}
		})
	}
}
