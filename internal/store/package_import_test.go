package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
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
			src.Where("deck_id = ?", deckID).Order("kind ASC").Find(&srcNotes)
			dst.Where("deck_id = ?", report.DeckID).Order("kind ASC").Find(&dstNotes)
			if len(srcNotes) != len(dstNotes) {
				t.Fatalf("note count: dst=%d src=%d", len(dstNotes), len(srcNotes))
			}
			for i := range srcNotes {
				if srcNotes[i].Kind != dstNotes[i].Kind {
					t.Fatalf("note %d kind: %q != %q", i, dstNotes[i].Kind, srcNotes[i].Kind)
				}
				if CanonicalFieldsJSON(srcNotes[i].FieldsJSON) != CanonicalFieldsJSON(dstNotes[i].FieldsJSON) {
					t.Fatalf("note %d fields differ:\n src=%s\n dst=%s", i, srcNotes[i].FieldsJSON, dstNotes[i].FieldsJSON)
				}
				if srcNotes[i].TagsJSON != dstNotes[i].TagsJSON {
					t.Fatalf("note %d tags: %q != %q", i, dstNotes[i].TagsJSON, srcNotes[i].TagsJSON)
				}
			}
			var formula Note
			if err := dst.Where("deck_id = ? AND kind = ?", report.DeckID, "basic").First(&formula).Error; err != nil {
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
