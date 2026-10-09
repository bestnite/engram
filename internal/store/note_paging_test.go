package store

import (
	"context"
	"math"
	"testing"
)

// TestNormalizeNoteListOptionsClampsBounds 断言分页归一化的上下界：非正页码/条数取默认，
// 超大条数被夹到 MaxNotePageSize，超大页码被夹到 MaxNotePage（避免偏移量溢出）。
func TestNormalizeNoteListOptionsClampsBounds(t *testing.T) {
	cases := []struct {
		name string
		in   NoteListOptions
		want NoteListOptions
	}{
		{"zero page and size take defaults", NoteListOptions{}, NoteListOptions{Page: 1, PerPage: DefaultNotePageSize}},
		{"negative page and size take defaults", NoteListOptions{Page: -5, PerPage: -1}, NoteListOptions{Page: 1, PerPage: DefaultNotePageSize}},
		{"normal values untouched", NoteListOptions{Page: 3, PerPage: 20}, NoteListOptions{Page: 3, PerPage: 20}},
		{"huge size clamped to max", NoteListOptions{Page: 1, PerPage: 1_000_000}, NoteListOptions{Page: 1, PerPage: MaxNotePageSize}},
		{"huge page clamped to max", NoteListOptions{Page: math.MaxInt, PerPage: 50}, NoteListOptions{Page: MaxNotePage, PerPage: 50}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeNoteListOptions(tc.in)
			if got.Page != tc.want.Page || got.PerPage != tc.want.PerPage {
				t.Errorf("NormalizeNoteListOptions(%+v) = page %d per_page %d, want page %d per_page %d",
					tc.in, got.Page, got.PerPage, tc.want.Page, tc.want.PerPage)
			}
		})
	}
}

// TestNoteListHugePageAndSizeIsSafe 是 AUDIT-06 的验收：极大 per_page 不会一次拉走整库
// （结果不超过单页上限），极大 page 不会因偏移量溢出报错或返回错乱数据。
func TestNoteListHugePageAndSizeIsSafe(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_paging_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			// 造出比单页上限还多一条的卡片，让「上限」是可观察的。
			for i := 0; i < MaxNotePageSize+1; i++ {
				n := &Note{DeckID: deckID, Kind: "basic"}
				if _, err := notes.Create(ctx, n, map[string]any{"front": "q", "back": "a"}); err != nil {
					t.Fatalf("create note %d: %v", i, err)
				}
			}

			got, total, err := notes.List(ctx, NoteListOptions{DeckID: deckID, Page: 1, PerPage: 1_000_000})
			if err != nil {
				t.Fatalf("List(huge per_page) error = %v", err)
			}
			if len(got) != MaxNotePageSize {
				t.Errorf("List(huge per_page) returned %d notes, want %d (clamped)", len(got), MaxNotePageSize)
			}
			if total != int64(MaxNotePageSize+1) {
				t.Errorf("List total = %d, want %d", total, MaxNotePageSize+1)
			}

			// 极大页码：偏移量被夹住，不报错，返回空页。
			got, _, err = notes.List(ctx, NoteListOptions{DeckID: deckID, Page: math.MaxInt, PerPage: 50})
			if err != nil {
				t.Fatalf("List(huge page) error = %v", err)
			}
			if len(got) != 0 {
				t.Errorf("List(huge page) returned %d notes, want 0 (beyond the last page)", len(got))
			}
		})
	}
}
