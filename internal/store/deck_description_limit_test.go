package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TODO item 5: the deck description shares the same bounds as the deck name (≤2000 characters, counted by rune, C0 control characters
// rejected, invalid UTF-8 rejected; an empty description is allowed). Before the fix the description was not validated at all, so the web UI could create a deck 'its own importer would reject'.
// Here both store-layer write paths, creation and rename, are exercised on real SQLite (and on gated PG).

// assertDescriptionRejected asserts that the store layer rejected writing that description.
func assertDescriptionRejected(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("description write was accepted, want a rejection")
	}
	if errors.Is(err, ErrDeckNameInvalid) {
		t.Fatalf("description rejection must not reuse ErrDeckNameInvalid: %v", err)
	}
}

func TestDeckDescriptionLimitCreateAndRename(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "deck_desc_limit_owner")[0]
			presetID := seedPresetRow(t, db, owner)
			decks := NewDeckStore(db)

			desc2000 := strings.Repeat("d", 2000)
			desc2001 := strings.Repeat("d", 2001)

			t.Run("create 2000 accepted", func(t *testing.T) {
				d := &Deck{OwnerUserID: owner, Name: "desc-limit", PresetID: presetID, Description: desc2000}
				if err := decks.Create(ctx, d); err != nil {
					t.Fatalf("Create(description 2000) error = %v, want nil", err)
				}
				got, err := decks.ByID(ctx, d.ID)
				if err != nil {
					t.Fatalf("ByID() error = %v", err)
				}
				if n := len([]rune(got.Description)); n != 2000 {
					t.Fatalf("stored description = %d runes, want 2000", n)
				}
			})

			t.Run("create 2001 rejected", func(t *testing.T) {
				err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: "desc-limit-2", PresetID: presetID, Description: desc2001})
				if err == nil {
					t.Fatalf("Create(description 2001) error = nil, want a rejection")
				}
				if !errors.Is(err, ErrDeckDescriptionInvalid) {
					t.Fatalf("Create(description 2001) error = %v, want ErrDeckDescriptionInvalid", err)
				}
			})

			t.Run("rename 2000 accepted then 2001 rejected", func(t *testing.T) {
				d := &Deck{OwnerUserID: owner, Name: "desc-rename", PresetID: presetID}
				if err := decks.Create(ctx, d); err != nil {
					t.Fatalf("Create() error = %v", err)
				}
				d.Description = desc2000
				if err := decks.Update(ctx, owner, d.ID, nil, &d.Description); err != nil {
					t.Fatalf("Update(description 2000) error = %v, want nil", err)
				}
				d.Description = desc2001
				if err := decks.Update(ctx, owner, d.ID, nil, &d.Description); !errors.Is(err, ErrDeckDescriptionInvalid) {
					t.Fatalf("Update(description 2001) error = %v, want ErrDeckDescriptionInvalid", err)
				}
				// a rejected rename must not reach the DB: it still holds the last valid 2000-character description.
				got, err := decks.ByID(ctx, d.ID)
				if err != nil {
					t.Fatalf("ByID() error = %v", err)
				}
				if n := len([]rune(got.Description)); n != 2000 {
					t.Fatalf("rejected update leaked to DB: description = %d runes, want 2000", n)
				}
			})

			t.Run("2000 hanzi counted by character", func(t *testing.T) {
				// 2000 Han characters = 6000 bytes: counted by rune it must pass, counted by bytes it would be wrongly rejected.
				hanzi := strings.Repeat("汉", 2000)
				if len(hanzi) <= 2000 {
					t.Fatalf("fixture is not multi-byte: len = %d", len(hanzi))
				}
				if err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: "desc-hanzi", PresetID: presetID, Description: hanzi}); err != nil {
					t.Fatalf("Create(2000 hanzi description) error = %v, want nil", err)
				}
			})

			t.Run("control character rejected", func(t *testing.T) {
				err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: "desc-ctrl", PresetID: presetID, Description: "bad\ndesc"})
				if !errors.Is(err, ErrDeckDescriptionInvalid) {
					t.Fatalf("Create(newline in description) error = %v, want ErrDeckDescriptionInvalid", err)
				}
			})

			t.Run("invalid UTF-8 rejected", func(t *testing.T) {
				err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: "desc-utf8", PresetID: presetID, Description: "bad\xffdesc"})
				if !errors.Is(err, ErrDeckDescriptionInvalid) {
					t.Fatalf("Create(invalid UTF-8 description) error = %v, want ErrDeckDescriptionInvalid", err)
				}
			})

			t.Run("empty description allowed", func(t *testing.T) {
				if err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: "desc-empty", PresetID: presetID, Description: ""}); err != nil {
					t.Fatalf("Create(empty description) error = %v, want nil", err)
				}
			})
		})
	}
}

// TestDeckDescriptionLimitExportImportRoundTrip asserts the symmetry of the shared bounds: a deck with a 2000-character
// description can be exported as a deck package as-is and then accepted by the import side (another new account) — before the fix the web UI could create
// decks with 2000+ descriptions that the importer would reject, so the round trip was asymmetric.
func TestDeckDescriptionLimitExportImportRoundTrip(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			users := seedUsers(t, db, "deck_desc_export", "deck_desc_import")
			exporter, importer := users[0], users[1]
			ctx := context.Background()
			deckID := seedPresetDeck(t, db, exporter)

			desc := strings.Repeat("x", 2000)
			d, err := NewDeckStore(db).ByID(ctx, deckID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			d.Description = desc
			if err := NewDeckStore(db).Update(ctx, exporter, d.ID, nil, &d.Description); err != nil {
				t.Fatalf("set 2000-char description: %v", err)
			}

			raw := exportZip(t, db, exporter, deckID, PackageOptions{})
			report := importPackageOK(t, db, importer, raw, PackageImportOptions{})
			got, err := NewDeckStore(db).ByID(ctx, report.DeckID)
			if err != nil {
				t.Fatalf("ByID(imported) error = %v", err)
			}
			if len([]rune(got.Description)) != len([]rune(desc)) {
				t.Fatalf("round-tripped description = %d runes, want %d", len([]rune(got.Description)), len([]rune(desc)))
			}
		})
	}
}
