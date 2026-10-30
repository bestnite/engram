package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// ---- import: round-trip fidelity / idempotency / safety / progress ownership ----

// TestImportRoundTripIdentical asserts that after exporting and importing into an empty database the notes
// (fields, tags, source_url, extra), cards (template, ordinal, deck_path), note types (name, config) and media
// names match exactly, and a second import creates no duplicate notes or cards.
// It runs once on SQLite and once on PostgreSQL, with fresh instances as both source and target.
func TestImportRoundTripIdentical(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, src, "pkg_rt_owner")[0]
			deckID, _ := seedPackageDeck(t, src, owner)

			// One named media reference: the note's HTML uses "diagram.png", so the name must travel in
			// media.json and be registered in the target deck for the import to resolve it.
			srcRoot := t.TempDir()
			mediaRaw := append([]byte("\x89PNG\r\n\x1a\n"), []byte("round-trip-named-media")...)
			m, err := NewMediaStore(src).SaveBytes(ctx, srcRoot, "image/png", mediaRaw, Ptr(owner))
			if err != nil {
				t.Fatalf("SaveBytes: %v", err)
			}
			if err := NewMediaNameStore(src).Upsert(ctx, deckID, "diagram.png", m.Sha256); err != nil {
				t.Fatalf("register media name: %v", err)
			}
			mediaNote := Note{DeckID: deckID, NoteTypeID: Ptr(stockNoteTypeID(t, src, deckID, "basic")), TagsJSON: "[]", CreatedBy: Ptr(owner)}
			if _, err := NewNoteStore(src).Create(ctx, &mediaNote, map[string]any{
				"front": "media?", "back": `<img src="diagram.png">`,
			}); err != nil {
				t.Fatalf("create media note: %v", err)
			}

			raw := exportZip(t, src, owner, deckID, PackageOptions{IncludeMedia: true, MediaRoot: srcRoot})

			importer := seedUsers(t, dst, "pkg_rt_importer")[0]
			dstRoot := t.TempDir()
			report, err := NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(raw), PackageImportOptions{
				Target: PackageTargetNewDeck, MediaRoot: dstRoot,
			})
			if err != nil {
				t.Fatalf("ImportPackage: %v", err)
			}
			if report.NotesCreated != 5 {
				t.Fatalf("notes_created = %d, want 5", report.NotesCreated)
			}

			// Note types: same names and configs, independent of the assigned ids.
			srcTypes := noteTypeSnapshot(t, src, deckID)
			dstTypes := noteTypeSnapshot(t, dst, report.DeckID)
			if len(srcTypes) != len(dstTypes) {
				t.Fatalf("note type count: dst=%d src=%d", len(dstTypes), len(srcTypes))
			}
			for i := range srcTypes {
				if srcTypes[i] != dstTypes[i] {
					t.Fatalf("note type %d differs:\n src=%s\n dst=%s", i, srcTypes[i], dstTypes[i])
				}
			}

			var srcNotes, dstNotes []Note
			src.Where("deck_id = ?", deckID).Order("id ASC").Find(&srcNotes)
			dst.Where("deck_id = ?", report.DeckID).Order("id ASC").Find(&dstNotes)
			if len(srcNotes) != len(dstNotes) {
				t.Fatalf("note count: dst=%d src=%d", len(dstNotes), len(srcNotes))
			}
			for i := range srcNotes {
				if srcNotes[i].DeckID == deckID && srcNotes[i].NoteTypeID == nil {
					t.Fatalf("source note %d has no note type", srcNotes[i].ID)
				}
				if CanonicalFieldsJSON(srcNotes[i].FieldsJSON) != CanonicalFieldsJSON(dstNotes[i].FieldsJSON) {
					t.Fatalf("note %d fields differ:\n src=%s\n dst=%s", i, srcNotes[i].FieldsJSON, dstNotes[i].FieldsJSON)
				}
				if srcNotes[i].TagsJSON != dstNotes[i].TagsJSON {
					t.Fatalf("note %d tags: %q != %q", i, dstNotes[i].TagsJSON, srcNotes[i].TagsJSON)
				}
				if ptrText(srcNotes[i].SourceURL) != ptrText(dstNotes[i].SourceURL) {
					t.Fatalf("note %d source_url: %q != %q", i, ptrText(dstNotes[i].SourceURL), ptrText(srcNotes[i].SourceURL))
				}
				if ptrText(srcNotes[i].ExtraJSON) != ptrText(dstNotes[i].ExtraJSON) {
					t.Fatalf("note %d extra: %q != %q", i, ptrText(dstNotes[i].ExtraJSON), ptrText(srcNotes[i].ExtraJSON))
				}
				if srcCards, dstCards := cardSnapshot(t, src, srcNotes[i].ID), cardSnapshot(t, dst, dstNotes[i].ID); srcCards != dstCards {
					t.Fatalf("note %d cards differ:\n src=%s\n dst=%s", i, dstCards, srcCards)
				}
			}
			var formula Note
			if err := dst.Where("deck_id = ? AND fields_json LIKE ?", report.DeckID, "%E=mc^2%").First(&formula).Error; err != nil {
				t.Fatalf("load formula note: %v", err)
			}
			if !bytes.Contains([]byte(formula.FieldsJSON), []byte(`\\(E=mc^2\\)`)) {
				t.Fatalf("formula not preserved: %s", formula.FieldsJSON)
			}

			// Media names: the target deck resolves the same file name to the same content.
			srcNames, err := NewMediaNameStore(src).ListByDeck(ctx, deckID)
			if err != nil {
				t.Fatalf("list source media names: %v", err)
			}
			dstNames, err := NewMediaNameStore(dst).ListByDeck(ctx, report.DeckID)
			if err != nil {
				t.Fatalf("list imported media names: %v", err)
			}
			if len(srcNames) != len(dstNames) {
				t.Fatalf("media name count: dst=%v src=%v", dstNames, srcNames)
			}
			for i := range srcNames {
				if srcNames[i].Name != dstNames[i].Name || srcNames[i].Sha256 != dstNames[i].Sha256 {
					t.Fatalf("media name %d: %+v != %+v", i, dstNames[i], srcNames[i])
				}
			}

			// Importing the same package a second time creates no duplicate notes or cards (the default update matches on the fingerprint).
			var cardsBefore int64
			dst.Model(&Card{}).Where("note_id IN (SELECT id FROM notes WHERE deck_id = ?)", report.DeckID).Count(&cardsBefore)
			report2, err := NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(raw), PackageImportOptions{
				Target: fmt.Sprintf("into_deck:%d", report.DeckID), MediaRoot: dstRoot,
			})
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

// noteTypeSnapshot returns a comparable "name + config" line per note type of a deck, sorted, so a
// round trip can compare the definitions without comparing their database ids.
func noteTypeSnapshot(t *testing.T, db *gorm.DB, deckID uint64) []string {
	t.Helper()
	types, err := NewNoteTypeStore(db).ListByDeck(context.Background(), deckID)
	if err != nil {
		t.Fatalf("list note types of deck %d: %v", deckID, err)
	}
	out := make([]string, 0, len(types))
	for i := range types {
		cfg, err := types[i].Config()
		if err != nil {
			t.Fatalf("decode note type %d: %v", types[i].ID, err)
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("encode note type %d: %v", types[i].ID, err)
		}
		out = append(out, types[i].Name+"\n"+string(raw))
	}
	sort.Strings(out)
	return out
}

// cardSnapshot returns a comparable "template/ordinal/deck_path" line per card of a note, sorted.
func cardSnapshot(t *testing.T, db *gorm.DB, noteID uint64) string {
	t.Helper()
	var cards []Card
	if err := db.Where("note_id = ?", noteID).Order("template ASC, ordinal ASC").Find(&cards).Error; err != nil {
		t.Fatalf("list cards of note %d: %v", noteID, err)
	}
	lines := make([]string, 0, len(cards))
	for i := range cards {
		lines = append(lines, fmt.Sprintf("%s/%d/%s", cards[i].Template, cards[i].Ordinal, ptrText(cards[i].DeckPath)))
	}
	return strings.Join(lines, "\n")
}

// ptrText returns the pointed-to text, or the empty string for nil.
func ptrText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
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

// TestImportUnknownNoteTypeListsEntries asserts a note naming a note type the package does not declare errors
// and lists the offending entries one by one.
func TestImportUnknownNoteTypeListsEntries(t *testing.T) {
	raw := buildZip(t, map[string]string{
		"manifest.json":   `{"format_version":2,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"d"},"include_progress":false,"include_media":false,"include_reviews":false,"counts":{"notes":2,"cards":0}}`,
		"note_types.json": testNoteTypesJSON(),
		"notes.json":      `[{"note_type":"nt-1","fields":{"front":"a","back":"b"}},{"note_type":"nt-9","fields":{"x":"y"}}]`,
		"cards.json":      `[]`,
		"preset.json":     testPresetJSON,
	})
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			_, err := NewDeckStore(db).ImportPackage(context.Background(), 1, bytes.NewReader(raw), PackageImportOptions{})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageUnknownKind {
				t.Fatalf("error = %v, want PackageError code %s", err, CodePackageUnknownKind)
			}
			if len(pe.Entries) != 1 || pe.Entries[0] != "notes[1] note_type=nt-9" {
				t.Fatalf("entries = %v, want [notes[1] note_type=nt-9]", pe.Entries)
			}
		})
	}
}

// TestImportRejectsFormat1 asserts a format-1 package is rejected with the stable obsolete-format code,
// and the message tells the user to import it with an older release and export it again.
func TestImportRejectsFormat1(t *testing.T) {
	raw := buildZip(t, map[string]string{
		"manifest.json": `{"format_version":1,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"d"},"include_progress":false,"include_media":false,"include_reviews":false,"counts":{"notes":1,"cards":0}}`,
		"notes.json":    `[{"kind":"basic","fields":{"front":"a","back":"b"}}]`,
		"cards.json":    `[]`,
		"preset.json":   testPresetJSON,
	})
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			_, err := NewDeckStore(db).ImportPackage(context.Background(), 1, bytes.NewReader(raw), PackageImportOptions{})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageFormatObsolete {
				t.Fatalf("error = %v, want PackageError code %s", err, CodePackageFormatObsolete)
			}
			if !strings.Contains(pe.Message, "older engram release") || !strings.Contains(pe.Message, "export it again") {
				t.Fatalf("message = %q, want guidance to use an older release and export again", pe.Message)
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
	manifest := `{"format_version":2,"exported_at":"2026-10-06T00:00:00Z","app_version":"test",` +
		`"deck":{"name":` + jsonQuote(t, deckName) + `},` +
		`"include_progress":false,"include_media":false,"include_reviews":false,"counts":{"notes":0,"cards":0}}`
	return buildZip(t, map[string]string{
		"manifest.json":   manifest,
		"note_types.json": `[]`,
		"notes.json":      `[]`,
		"cards.json":      `[]`,
		"preset.json":     `{"name":` + jsonQuote(t, presetName) + `,"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":false,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`,
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
