package store

import (
	"context"
	"sort"
	"testing"

	"gorm.io/gorm"
)

// noteSearchSeed is the fixture shared by the search tests: each entry becomes one note, named by its key.
var noteSearchSeed = []struct {
	name   string
	kind   string
	fields map[string]any
}{
	{"html", "basic", map[string]any{"front": "Is a<b true?", "back": "AT&T > Bell"}},
	{"quote", "basic", map[string]any{"front": `He said "hello world"`, "back": "greeting"}},
	{"multiline", "basic", map[string]any{"front": "first line\nsecond line", "back": "lines"}},
	{"accent", "basic", map[string]any{"front": "Été", "back": "summer"}},
	{"cjk", "basic", map[string]any{"front": "间隔重复是一种记忆方法", "back": "spaced repetition"}},
	{"wildcard", "basic", map[string]any{"front": "100% sure", "back": "snake_case"}},
	{"choice", "choice_single", map[string]any{"question": "Closest planet?", "options": []any{"Venus", "Mercury", "Mars"}, "answer": 2}},
}

// seedSearchNotes creates the fixture notes through NoteStore.Create and returns the note name for each id.
func seedSearchNotes(t *testing.T, db *gorm.DB, deckID uint64) map[uint64]string {
	t.Helper()
	ctx := context.Background()
	notes := NewNoteStore(db)
	byID := make(map[uint64]string, len(noteSearchSeed))
	for _, s := range noteSearchSeed {
		n := &Note{DeckID: deckID, Kind: s.kind}
		if _, err := notes.Create(ctx, n, s.fields); err != nil {
			t.Fatalf("create note %s: %v", s.name, err)
		}
		byID[n.ID] = s.name
	}
	return byID
}

// noteSearchCases are the queries both the write path and the backfill migration must answer the same way.
var noteSearchCases = []struct {
	name  string
	query string
	want  []string
}{
	{"field key front hits nothing", "front", nil},
	{"field key back hits nothing", "back", nil},
	{"field key of another kind hits nothing", "question", nil},
	{"less-than sign", "a<b", []string{"html"}},
	{"ampersand", "AT&T", []string{"html"}},
	{"greater-than sign", "> bell", []string{"html"}},
	{"quoted phrase", `"hello world"`, []string{"quote"}},
	{"terms on two lines", "first second", []string{"multiline"}},
	{"non-ASCII case folding", "ÉTÉ", []string{"accent"}},
	{"CJK substring", "重复", []string{"cjk"}},
	{"non-adjacent terms in any order", "记忆 间隔", []string{"cjk"}},
	{"every term must occur", "记忆 venus", nil},
	{"ASCII case folding", "SPACED", []string{"cjk"}},
	{"array element", "mercury", []string{"choice"}},
	{"percent is literal", "%", []string{"wildcard"}},
	{"underscore is literal", "_", []string{"wildcard"}},
	{"numbers are not indexed", "2", nil},
	{"blank query matches everything", "   ", []string{"accent", "choice", "cjk", "html", "multiline", "quote", "wildcard"}},
}

// assertNoteSearch runs every case of noteSearchCases through NoteStore.List.
func assertNoteSearch(t *testing.T, db *gorm.DB, deckID uint64, byID map[uint64]string) {
	t.Helper()
	notes := NewNoteStore(db)
	for _, tc := range noteSearchCases {
		t.Run(tc.name, func(t *testing.T) {
			got, total, err := notes.List(context.Background(), NoteListOptions{DeckID: deckID, Query: tc.query})
			if err != nil {
				t.Fatalf("List(%q) error = %v", tc.query, err)
			}
			names := make([]string, 0, len(got))
			for _, n := range got {
				names = append(names, byID[n.ID])
			}
			sort.Strings(names)
			if int(total) != len(tc.want) || len(names) != len(tc.want) {
				t.Fatalf("List(%q) = %v (total %d), want %v", tc.query, names, total, tc.want)
			}
			for i := range names {
				if names[i] != tc.want[i] {
					t.Fatalf("List(%q) = %v, want %v", tc.query, names, tc.want)
				}
			}
		})
	}
}

// TestNoteListSearch pins note search on notes written through the store: it matches field text, never field keys or
// the JSON escapes of the encoding, folds case the same way on both databases, and requires every query term.
func TestNoteListSearch(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			owner := seedUsers(t, db, "note_search_owner")[0]
			deckID := seedDeck(t, db, owner)
			byID := seedSearchNotes(t, db, deckID)
			assertNoteSearch(t, db, deckID, byID)
		})
	}
}

// TestNoteSearchTextFollowsUpdates asserts an update rewrites search_text: the old text stops matching and the new one matches.
func TestNoteSearchTextFollowsUpdates(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_search_update_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			n := &Note{DeckID: deckID, Kind: "basic"}
			if _, err := notes.Create(ctx, n, map[string]any{"front": "alpha", "back": "beta"}); err != nil {
				t.Fatalf("create note: %v", err)
			}
			if _, err := notes.Update(ctx, &Note{ID: n.ID, DeckID: deckID}, map[string]any{"front": "gamma", "back": "delta"}); err != nil {
				t.Fatalf("update note: %v", err)
			}
			for query, want := range map[string]int64{"alpha": 0, "gamma": 1} {
				_, total, err := notes.List(ctx, NoteListOptions{DeckID: deckID, Query: query})
				if err != nil {
					t.Fatalf("List(%q) error = %v", query, err)
				}
				if total != want {
					t.Errorf("List(%q) total = %d, want %d", query, total, want)
				}
			}
		})
	}
}

// TestBackfillNoteSearchText asserts migration 0011 makes notes written before the column existed searchable,
// soft-deleted ones included, and gives a note with corrupt fields an empty search text instead of failing.
func TestBackfillNoteSearchText(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			if err := AutoMigrate(ctx, db); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			if applied, err := Apply(ctx, db, BuiltinMigrations[:10]); err != nil {
				t.Fatalf("Apply(BuiltinMigrations[:10]) = %d, %v", applied, err)
			}
			owner := seedUsers(t, db, "note_backfill_owner")[0]
			deckID := seedDeck(t, db, owner)
			byID := seedSearchNotes(t, db, deckID)

			corrupt := Note{DeckID: deckID, Kind: "basic", FieldsJSON: "{not json", TagsJSON: "[]"}
			if err := db.Create(&corrupt).Error; err != nil {
				t.Fatalf("create corrupt note: %v", err)
			}
			var deletedID uint64
			for id, name := range byID {
				if name == "accent" {
					deletedID = id
				}
			}
			if err := db.Delete(&Note{}, "id = ?", deletedID).Error; err != nil {
				t.Fatalf("soft-delete note: %v", err)
			}
			// Put the table back in its pre-upgrade state: NULL is what AutoMigrate leaves in a newly added column.
			if err := db.Exec("UPDATE notes SET search_text = NULL").Error; err != nil {
				t.Fatalf("clear search_text: %v", err)
			}
			if _, total, err := NewNoteStore(db).List(ctx, NoteListOptions{DeckID: deckID, Query: "mercury"}); err != nil || total != 0 {
				t.Fatalf("before backfill List(mercury) = %d, %v; want 0, nil", total, err)
			}

			if applied, err := Apply(ctx, db, BuiltinMigrations[:11]); err != nil || applied != 1 {
				t.Fatalf("Apply(BuiltinMigrations[:11]) = %d, %v; want 1, nil", applied, err)
			}

			var deleted Note
			if err := db.Unscoped().First(&deleted, "id = ?", deletedID).Error; err != nil {
				t.Fatalf("load soft-deleted note: %v", err)
			}
			// Keys are visited in sorted order, so back ("summer") comes before front ("été").
			if deleted.SearchText != "summer\nété" {
				t.Errorf("soft-deleted note search_text = %q, want %q", deleted.SearchText, "summer\nété")
			}
			var broken Note
			if err := db.First(&broken, "id = ?", corrupt.ID).Error; err != nil {
				t.Fatalf("load corrupt note: %v", err)
			}
			if broken.SearchText != "" {
				t.Errorf("corrupt note search_text = %q, want empty", broken.SearchText)
			}

			// The soft-deleted note drops out of the default listing, so it is restored before the shared cases run.
			if err := db.Unscoped().Model(&Note{}).Where("id = ?", deletedID).Update("deleted_at", nil).Error; err != nil {
				t.Fatalf("restore note: %v", err)
			}
			if err := db.Unscoped().Delete(&Note{}, "id = ?", corrupt.ID).Error; err != nil {
				t.Fatalf("remove corrupt note: %v", err)
			}
			assertNoteSearch(t, db, deckID, byID)
		})
	}
}

// TestNoteSearchText pins the derivation rule on its own: string values only, sorted keys, nested arrays and maps walked.
func TestNoteSearchText(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]any
		want   string
	}{
		{"nil fields", nil, ""},
		{"keys sorted, values lowered", map[string]any{"z": "Last", "a": "First"}, "first\nlast"},
		{"numbers, booleans and empty strings skipped", map[string]any{"a": 1.5, "b": true, "c": "", "d": "Kept"}, "kept"},
		{"nested arrays and maps", map[string]any{"a": []any{"X", map[string]any{"k": "Y"}, 2.0}}, "x\ny"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := noteSearchText(tc.fields); got != tc.want {
				t.Errorf("noteSearchText(%v) = %q, want %q", tc.fields, got, tc.want)
			}
		})
	}
}
