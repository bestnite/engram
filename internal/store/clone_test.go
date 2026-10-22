package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestDeckCopyNameKeepsResultWithinLimit asserts that 'source name + copy suffix' always satisfies the shared name validation:
// when too long, the source name is truncated by rune, the kept part is a prefix of the source name (truncated, not rewritten), and the result still ends with the suffix.
func TestDeckCopyNameKeepsResultWithinLimit(t *testing.T) {
	cases := []struct {
		name   string
		source string
		suffix string
	}{
		{"short ascii", "deck", " (copy)"},
		{"exactly fits", strings.Repeat("a", maxDeckNameChars-len(" (copy)")), " (copy)"},
		{"over limit ascii", strings.Repeat("a", maxDeckNameChars), " (copy)"},
		{"over limit chinese", strings.Repeat("卡", maxDeckNameChars), "（副本）"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeckCopyName(tc.source, tc.suffix)
			if n := utf8.RuneCountInString(got); n > maxDeckNameChars {
				t.Errorf("DeckCopyName result has %d runes, want <= %d", n, maxDeckNameChars)
			}
			if !strings.HasSuffix(got, tc.suffix) {
				t.Errorf("DeckCopyName = %q, want it to end with %q", got, tc.suffix)
			}
			base := strings.TrimSuffix(got, tc.suffix)
			if !strings.HasPrefix(tc.source, base) {
				t.Errorf("DeckCopyName base %q is not a prefix of the source name", base)
			}
			if err := validateDeckName(got); err != nil {
				t.Errorf("validateDeckName(%q) = %v, want nil (clone name must pass the shared rule)", got, err)
			}
		})
	}
}

// TestCloneRejectsInvalidName is AUDIT-05 acceptance: cloning shares one name validation with deck creation and rename,
// so an over-long name is rejected and not stored (before the fix, cloning only checked non-emptiness and could write a deck over 200 characters).
func TestCloneRejectsInvalidName(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "clone_bad_name")[0]
			presetID := seedPresetRow(t, db, owner)
			src := Deck{OwnerUserID: owner, Name: "source", PresetID: presetID}
			if err := NewDeckStore(db).Create(ctx, &src); err != nil {
				t.Fatalf("create source deck: %v", err)
			}

			tooLong := strings.Repeat("x", maxDeckNameChars+1)
			if _, err := NewDeckStore(db).Clone(ctx, &src, owner, tooLong, presetID); !errors.Is(err, ErrDeckNameInvalid) {
				t.Fatalf("Clone(over-long name) error = %v, want ErrDeckNameInvalid", err)
			}
			var count int64
			if err := db.Model(&Deck{}).Where("owner_user_id = ?", owner).Count(&count).Error; err != nil {
				t.Fatalf("count decks: %v", err)
			}
			if count != 1 {
				t.Errorf("decks = %d, want 1 (a rejected clone must not persist)", count)
			}
		})
	}
}
