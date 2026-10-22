package store

import (
	"context"
	"errors"
	"testing"
)

// TestDeckCapsDefaultsAndRoundTrip is a store-side acceptance case:
// without explicit caps the column defaults apply (20/200); SetCaps can write caps that differ from the defaults
// and can also write 0 explicitly (unlimited); negative values and non-owner writes are rejected.
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

			// no caps given explicitly: the database column defaults fill in 20/200.
			got, err := decks.Caps(ctx, d.ID)
			if err != nil {
				t.Fatalf("Caps() error = %v", err)
			}
			if got.NewPerDay != DefaultNewPerDay || got.ReviewsPerDay != DefaultReviewsPerDay {
				t.Fatalf("default caps = %+v, want %d/%d", got, DefaultNewPerDay, DefaultReviewsPerDay)
			}

			// write values different from the defaults and read them back.
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

			// 0 is a legal value (unlimited) and must be writable explicitly, without being overwritten by the column default.
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

			// a negative value is rejected.
			if err := decks.SetCaps(ctx, owner, d.ID, DeckCaps{NewPerDay: -1}); !errors.Is(err, ErrInvalidDeckCap) {
				t.Errorf("SetCaps(negative) error = %v, want ErrInvalidDeckCap", err)
			}

			// a non-owner cannot change the caps.
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
