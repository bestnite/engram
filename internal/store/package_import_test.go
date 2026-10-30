package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"gorm.io/gorm"
)

// ---- import: round-trip fidelity / idempotency / safety / progress ownership ----

// TestImportRoundTripIdentical asserts that after exporting and importing into an empty database the note count/fields/tags match exactly, and a second import creates no duplicate cards.
// It runs once on SQLite and once on PostgreSQL, with fresh instances as both source and target.
func TestImportRoundTripIdentical(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, src, "pkg_rt_owner")[0]
			deckID, _ := seedPackageDeck(t, src, owner)
			raw := exportZip(t, src, owner, deckID, PackageOptions{IncludeMedia: false})

			importer := seedUsers(t, dst, "pkg_rt_importer")[0]
			report, err := NewDeckStore(dst).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{Target: PackageTargetNewDeck})
			if err != nil {
				t.Fatalf("ImportPackage: %v", err)
			}
			if report.NotesCreated != 4 {
				t.Fatalf("notes_created = %d, want 4", report.NotesCreated)
			}

			var srcNotes, dstNotes []Note
			src.Where("deck_id = ?", deckID).Order("id ASC").Find(&srcNotes)
			dst.Where("deck_id = ?", report.DeckID).Order("id ASC").Find(&dstNotes)
			if len(srcNotes) != len(dstNotes) {
				t.Fatalf("note count: dst=%d src=%d", len(dstNotes), len(srcNotes))
			}
			srcStore, dstStore := NewNoteStore(src), NewNoteStore(dst)
			for i := range srcNotes {
				if srcKind, dstKind := srcStore.KindOf(context.Background(), &srcNotes[i]), dstStore.KindOf(context.Background(), &dstNotes[i]); srcKind != dstKind {
					t.Fatalf("note %d kind: %q != %q", i, dstKind, srcKind)
				}
				if CanonicalFieldsJSON(srcNotes[i].FieldsJSON) != CanonicalFieldsJSON(dstNotes[i].FieldsJSON) {
					t.Fatalf("note %d fields differ:\n src=%s\n dst=%s", i, srcNotes[i].FieldsJSON, dstNotes[i].FieldsJSON)
				}
				if srcNotes[i].TagsJSON != dstNotes[i].TagsJSON {
					t.Fatalf("note %d tags: %q != %q", i, dstNotes[i].TagsJSON, srcNotes[i].TagsJSON)
				}
			}
			var formula Note
			if err := dst.Where("deck_id = ? AND fields_json LIKE ?", report.DeckID, "%E=mc^2%").First(&formula).Error; err != nil {
				t.Fatalf("load formula note: %v", err)
			}
			if !bytes.Contains([]byte(formula.FieldsJSON), []byte(`\\(E=mc^2\\)`)) {
				t.Fatalf("formula not preserved: %s", formula.FieldsJSON)
			}

			// Importing the same package a second time creates no duplicate cards (the default update matches on the fingerprint).
			var cardsBefore int64
			dst.Model(&Card{}).Where("note_id IN (SELECT id FROM notes WHERE deck_id = ?)", report.DeckID).Count(&cardsBefore)
			report2, err := NewDeckStore(dst).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{Target: fmt.Sprintf("into_deck:%d", report.DeckID)})
			if err != nil {
				t.Fatalf("second ImportPackage: %v", err)
			}
			if report2.NotesCreated != 0 || report2.CardsCreated != 0 {
				t.Fatalf("second import created notes=%d cards=%d, want 0/0", report2.NotesCreated, report2.CardsCreated)
			}
			var cardsAfter int64
			dst.Model(&Card{}).Where("note_id IN (SELECT id FROM notes WHERE deck_id = ?)", report.DeckID).Count(&cardsAfter)
			if cardsBefore != cardsAfter {
				t.Fatalf("card count changed on re-import: %d -> %d", cardsBefore, cardsAfter)
			}
		})
	}
}

// TestImportRejectsUnsafeArchive asserts a package containing path traversal / symlinks is rejected
func TestImportRejectsUnsafeArchive(t *testing.T) {
	cases := []struct {
		name string
		zip  func() []byte
	}{
		{"path traversal", func() []byte { return buildZip(t, map[string]string{"../evil.txt": "x", "manifest.json": "{}"}) }},
		{"absolute path", func() []byte { return buildZip(t, map[string]string{"/etc/passwd": "x"}) }},
		{"symlink", func() []byte { return buildSymlinkZip(t) }},
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					raw := tc.zip()
					_, err := NewDeckStore(db).ImportPackage(context.Background(), 1, bytes.NewReader(raw), PackageImportOptions{})
					var pe *PackageError
					if !errors.As(err, &pe) || pe.Code != CodePackageUnsafeEntry {
						t.Fatalf("error = %v, want PackageError code %s", err, CodePackageUnsafeEntry)
					}
				})
			}
		})
	}
}

// TestReadPackageArchiveRejectsBomb asserts a package with too many entries is rejected (zip bomb defence).
// Pure function, touches no database, so it needs no per-driver rerun.
func TestReadPackageArchiveRejectsBomb(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 5; i++ {
		files[fmt.Sprintf("f%d", i)] = "x"
	}
	raw := buildZip(t, files)
	_, err := ReadPackageArchive(bytes.NewReader(raw), PackageLimits{MaxEntries: 3, MaxFileBytes: 1 << 20, MaxTotalBytes: 1 << 20})
	var pe *PackageError
	if !errors.As(err, &pe) || pe.Code != CodePackageTooLarge {
		t.Fatalf("error = %v, want PackageError code %s", err, CodePackageTooLarge)
	}
}

// TestImportUnknownKindListsEntries asserts an unknown kind errors and lists the offending entries one by one
func TestImportUnknownKindListsEntries(t *testing.T) {
	raw := buildZip(t, map[string]string{
		"manifest.json": `{"format_version":1,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"d"},"include_progress":false,"include_media":false,"include_reviews":false,"counts":{"notes":2,"cards":0}}`,
		"notes.json":    `[{"kind":"basic","fields":{"front":"a","back":"b"}},{"kind":"bogus_type","fields":{"x":"y"}}]`,
		"cards.json":    `[]`,
		"preset.json":   `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`,
	})
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			_, err := NewDeckStore(db).ImportPackage(context.Background(), 1, bytes.NewReader(raw), PackageImportOptions{})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageUnknownKind {
				t.Fatalf("error = %v, want PackageError code %s", err, CodePackageUnknownKind)
			}
			if len(pe.Entries) != 1 || pe.Entries[0] != "notes[1] kind=bogus_type" {
				t.Fatalf("entries = %v, want [notes[1] kind=bogus_type]", pe.Entries)
			}
		})
	}
}

// TestImportDiscardsOthersProgress asserts someone else's progress.json is discarded by default and reported
func TestImportDiscardsOthersProgress(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	sameOwner := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		dst2 := sameOwner[driver]
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, src, "pkg_prog_owner")[0]
			deckID, _ := seedPackageDeck(t, src, owner)
			raw := exportZip(t, src, owner, deckID, PackageOptions{IncludeProgress: true, IncludeMedia: false})

			other := seedUsers(t, dst, "pkg_prog_other")[0]
			report, err := NewDeckStore(dst).ImportPackage(context.Background(), other, bytes.NewReader(raw), PackageImportOptions{Target: PackageTargetNewDeck})
			if err != nil {
				t.Fatalf("ImportPackage: %v", err)
			}
			if !report.ProgressDiscarded || report.ProgressApplied != 0 {
				t.Fatalf("report discarded=%v applied=%d, want discarded=true applied=0", report.ProgressDiscarded, report.ProgressApplied)
			}
			var states int64
			dst.Model(&CardState{}).Where("user_id = ?", other).Count(&states)
			if states != 0 {
				t.Fatalf("other user got %d progress rows, want 0", states)
			}

			// The same user importing their own package: the progress is applied.
			self := seedUsers(t, dst2, "pkg_prog_owner")[0]
			report2, err := NewDeckStore(dst2).ImportPackage(context.Background(), self, bytes.NewReader(raw), PackageImportOptions{Target: PackageTargetNewDeck})
			if err != nil {
				t.Fatalf("ImportPackage (same user): %v", err)
			}
			if report2.ProgressApplied != 1 {
				t.Fatalf("progress_applied = %d, want 1", report2.ProgressApplied)
			}
		})
	}
}

// presetImportZip builds a minimal package whose preset.json carries presetName verbatim, the way an export of a deck on the default preset does.
func presetImportZip(t *testing.T, deckName, presetName string) []byte {
	t.Helper()
	manifest := `{"format_version":1,"exported_at":"2026-10-06T00:00:00Z","app_version":"test",` +
		`"deck":{"name":` + jsonQuote(t, deckName) + `},` +
		`"include_progress":false,"include_media":false,"include_reviews":false,"counts":{"notes":0,"cards":0}}`
	return buildZip(t, map[string]string{
		"manifest.json": manifest,
		"notes.json":    `[]`,
		"cards.json":    `[]`,
		"preset.json":   `{"name":` + jsonQuote(t, presetName) + `,"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":false,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`,
	})
}

// presetOfDeck loads the preset attached to deckID.
func presetOfDeck(t *testing.T, db *gorm.DB, deckID uint64) Preset {
	t.Helper()
	var d Deck
	if err := db.First(&d, "id = ?", deckID).Error; err != nil {
		t.Fatalf("load deck %d: %v", deckID, err)
	}
	var p Preset
	if err := db.First(&p, "id = ?", d.PresetID).Error; err != nil {
		t.Fatalf("load preset %d of deck %d: %v", d.PresetID, deckID, err)
	}
	return p
}

// presetNamesOf returns all of owner's preset names, so a test can assert both uniqueness and that a name was not taken from elsewhere.
func presetNamesOf(t *testing.T, db *gorm.DB, owner uint64) []string {
	t.Helper()
	var names []string
	if err := db.Model(&Preset{}).Where("owner_user_id = ?", owner).Order("id ASC").Pluck("name", &names).Error; err != nil {
		t.Fatalf("list preset names of user %d: %v", owner, err)
	}
	return names
}

// TestImportPresetDefaultNameIsNotCopied covers the defect where an export of a deck on the default preset carries the literal "Default" in
// preset.json, and the import inserted another preset under that name: the duplicate looked like a default preset and could not be deleted.
// Importing twice must leave two distinct names and exactly one preset named Default, the account's own default.
func TestImportPresetDefaultNameIsNotCopied(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, db, "pkg_preset_owner")[0]
			if _, err := EnsureDefaultPreset(ctx, db, owner); err != nil {
				t.Fatalf("EnsureDefaultPreset: %v", err)
			}
			raw := presetImportZip(t, "Shared", DefaultPresetName)
			store := NewDeckStore(db)

			first, err := store.ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{Target: PackageTargetNewDeck})
			if err != nil {
				t.Fatalf("first ImportPackage: %v", err)
			}
			firstName := presetOfDeck(t, db, first.DeckID).Name
			if firstName == DefaultPresetName {
				t.Fatalf("imported preset name = %q, want anything but the reserved default name", firstName)
			}
			if firstName != "Imported — Shared" {
				t.Fatalf("first imported preset name = %q, want %q", firstName, "Imported — Shared")
			}

			second, err := store.ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{Target: PackageTargetNewDeck})
			if err != nil {
				t.Fatalf("second ImportPackage: %v", err)
			}
			secondName := presetOfDeck(t, db, second.DeckID).Name
			if secondName == DefaultPresetName || secondName == firstName {
				t.Fatalf("second imported preset name = %q, want a name distinct from %q and the reserved default name", secondName, firstName)
			}

			var defaults int64
			if err := db.Model(&Preset{}).Where("owner_user_id = ? AND name = ?", owner, DefaultPresetName).Count(&defaults).Error; err != nil {
				t.Fatalf("count default presets: %v", err)
			}
			if defaults != 1 {
				t.Fatalf("presets named %q = %d, want exactly 1 (names: %v)", DefaultPresetName, defaults, presetNamesOf(t, db, owner))
			}
		})
	}
}

// TestImportPresetNameFallback asserts an absent or blank preset name still yields a name derived from the deck and never the reserved default name.
func TestImportPresetNameFallback(t *testing.T) {
	cases := []struct {
		name       string
		presetName string
	}{
		{"empty name", ""},
		{"whitespace name", "   "},
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			for i, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					owner := seedUsers(t, db, fmt.Sprintf("pkg_preset_fallback_%d", i))[0]
					deckName := fmt.Sprintf("Fallback %d", i)
					report, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(presetImportZip(t, deckName, tc.presetName)),
						PackageImportOptions{Target: PackageTargetNewDeck})
					if err != nil {
						t.Fatalf("ImportPackage: %v", err)
					}
					name := presetOfDeck(t, db, report.DeckID).Name
					if want := "Imported — " + deckName; name != want {
						t.Fatalf("preset name = %q, want %q", name, want)
					}
				})
			}
		})
	}
}

// TestImportDryRunWritesNothing asserts dry_run only counts and writes nothing (SQLite + PG).
func TestImportDryRunWritesNothing(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, src, "pkg_dry_owner")[0]
			deckID, _ := seedPackageDeck(t, src, owner)
			raw := exportZip(t, src, owner, deckID, PackageOptions{IncludeMedia: false})

			importer := seedUsers(t, dst, "pkg_dry_importer")[0]
			report, err := NewDeckStore(dst).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{DryRun: true})
			if err != nil {
				t.Fatalf("dry run ImportPackage: %v", err)
			}
			if report.NotesCreated != 4 {
				t.Fatalf("dry_run notes_created = %d, want 4", report.NotesCreated)
			}
			var decks, notes int64
			dst.Model(&Deck{}).Count(&decks)
			dst.Model(&Note{}).Count(&notes)
			if decks != 0 || notes != 0 {
				t.Fatalf("dry run wrote to the database: decks=%d notes=%d", decks, notes)
			}
		})
	}
}
