package store

import (
	"context"
	"errors"
	"testing"
)

// TestDeckCapsDefaultsAndRoundTrip 是 M3-8 的 store 侧验收用例：
// 未显式设置时列默认值生效（20/200）；SetCaps 能写入与默认值不同的上限，
// 也能显式写入 0（不限）；负数与非 owner 的写入被拒。
func TestDeckCapsDefaultsAndRoundTrip(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "caps_owner", "caps_stranger")
			owner, stranger := users[0], users[1]
			presetID := seedPresetRow(t, db, owner)
			decks := NewDeckStore(db)

			d := &Deck{OwnerUserID: owner, Name: "Capped", PresetID: presetID}
			if err := decks.Create(ctx, d); err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			// 未显式给上限：由数据库列默认值补齐为 20/200。
			got, err := decks.Caps(ctx, d.ID)
			if err != nil {
				t.Fatalf("Caps() error = %v", err)
			}
			if got.NewPerDay != DefaultNewPerDay || got.ReviewsPerDay != DefaultReviewsPerDay {
				t.Fatalf("default caps = %+v, want %d/%d", got, DefaultNewPerDay, DefaultReviewsPerDay)
			}

			// 写入与默认不同的值并读回。
			if err := decks.SetCaps(ctx, owner, d.ID, DeckCaps{NewPerDay: 3, ReviewsPerDay: 7}); err != nil {
				t.Fatalf("SetCaps() error = %v", err)
			}
			got, err = decks.Caps(ctx, d.ID)
			if err != nil {
				t.Fatalf("Caps() after SetCaps error = %v", err)
			}
			if got.NewPerDay != 3 || got.ReviewsPerDay != 7 {
				t.Errorf("caps after SetCaps = %+v, want 3/7", got)
			}

			// 0 是合法值（不限），必须能显式写入、不被列默认值覆盖。
			if err := decks.SetCaps(ctx, owner, d.ID, DeckCaps{NewPerDay: 0, ReviewsPerDay: 0}); err != nil {
				t.Fatalf("SetCaps(unlimited) error = %v", err)
			}
			got, err = decks.Caps(ctx, d.ID)
			if err != nil {
				t.Fatalf("Caps() after unlimited error = %v", err)
			}
			if got.NewPerDay != 0 || got.ReviewsPerDay != 0 {
				t.Errorf("caps after unlimited = %+v, want 0/0", got)
			}

			// 负数被拒。
			if err := decks.SetCaps(ctx, owner, d.ID, DeckCaps{NewPerDay: -1}); !errors.Is(err, ErrInvalidDeckCap) {
				t.Errorf("SetCaps(negative) error = %v, want ErrInvalidDeckCap", err)
			}

			// 非 owner 不能改上限。
			if err := decks.SetCaps(ctx, stranger, d.ID, DeckCaps{NewPerDay: 1, ReviewsPerDay: 1}); !errors.Is(err, ErrNotOwner) {
				t.Errorf("non-owner SetCaps() error = %v, want ErrNotOwner", err)
			}
			got, err = decks.Caps(ctx, d.ID)
			if err != nil {
				t.Fatalf("Caps() after denied write error = %v", err)
			}
			if got.NewPerDay != 0 || got.ReviewsPerDay != 0 {
				t.Errorf("caps were modified by a non-owner: %+v", got)
			}
		})
	}
}
