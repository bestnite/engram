package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// Media is the only path in a deck package that writes files to disk, yet it has the thinnest coverage: this requires that after export → import into a new
// DB the files really land on disk byte-identically, and asserts both values of skip_missing_media.

// seedMediaNote writes raw into srcRoot and the media metadata table by content address, and puts a note referencing it in the deck;
// it returns the sha256. Media references look like media/<sha256>.<ext>.
func seedMediaNote(t *testing.T, db *gorm.DB, srcRoot string, owner, deckID uint64, raw []byte, mime, ext string) string {
	t.Helper()
	m, err := NewMediaStore(db).SaveBytes(context.Background(), srcRoot, mime, raw, Ptr(owner))
	if err != nil {
		t.Fatalf("SaveBytes: %v", err)
	}
	n := Note{DeckID: deckID, NoteTypeID: Ptr(stockNoteTypeID(t, db, deckID, "basic")), TagsJSON: "[]", CreatedBy: Ptr(owner)}
	if _, err := NewNoteStore(db).Create(context.Background(), &n, map[string]any{
		"front": "img?", "back": `<img src="` + m.Sha256 + `.` + ext + `">`,
	}); err != nil {
		t.Fatalf("create media note: %v", err)
	}
	return m.Sha256
}

// TestImportPackageMediaRoundTrip asserts that a package with media, exported and imported into a brand-new DB, really lands its media files on disk byte-identically.
func TestImportPackageMediaRoundTrip(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, src, "pkg_media_rt_owner")[0]
			deckID := seedPresetDeck(t, src, owner)
			srcRoot := t.TempDir()

			// a chunk of bytes with the PNG magic number: SaveBytes only addresses by content and does not validate magic, so a recognizable header is kept deliberately here.
			raw := append([]byte("\x89PNG\r\n\x1a\n"), []byte("media-round-trip-payload")...)
			sum := sha256.Sum256(raw)
			sha := hex.EncodeToString(sum[:])
			if got := seedMediaNote(t, src, srcRoot, owner, deckID, raw, "image/png", "png"); got != sha {
				t.Fatalf("seeded media sha = %s, want %s", got, sha)
			}

			pkg, err := NewDeckStore(src).ExportPackage(ctx, owner, deckID, PackageOptions{IncludeMedia: true, MediaRoot: srcRoot})
			if err != nil {
				t.Fatalf("ExportPackage: %v", err)
			}
			if !pkg.Manifest.IncludeMedia || pkg.Manifest.Counts.Media != 1 {
				t.Fatalf("manifest media: include=%v counts=%d, want true/1", pkg.Manifest.IncludeMedia, pkg.Manifest.Counts.Media)
			}
			if !bytes.Equal(pkg.MediaBytes[sha], raw) {
				t.Fatalf("exported media bytes differ from source: %d bytes", len(pkg.MediaBytes[sha]))
			}
			var buf bytes.Buffer
			if err := pkg.WriteZip(&buf); err != nil {
				t.Fatalf("WriteZip: %v", err)
			}

			dstRoot := t.TempDir()
			importer := seedUsers(t, dst, "pkg_media_rt_importer")[0]
			report, err := NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(buf.Bytes()), PackageImportOptions{MediaRoot: dstRoot})
			if err != nil {
				t.Fatalf("ImportPackage: %v", err)
			}
			if report.MediaNew != 1 || report.MediaMissing != 0 {
				t.Fatalf("report media_new=%d media_missing=%d, want 1/0", report.MediaNew, report.MediaMissing)
			}

			row, err := NewMediaStore(dst).BySha256(ctx, sha)
			if err != nil {
				t.Fatalf("load imported media metadata: %v", err)
			}
			if row == nil || row.Sha256 != sha || row.Mime != "image/png" {
				t.Fatalf("imported media metadata = %+v, want sha %s mime image/png", row, sha)
			}
			onDisk, err := os.ReadFile(filepath.Join(dstRoot, filepath.FromSlash(row.RelPath)))
			if err != nil {
				t.Fatalf("read imported media from disk: %v", err)
			}
			if !bytes.Equal(onDisk, raw) {
				t.Fatalf("imported media bytes differ from source: got %d bytes, disk %v", len(onDisk), row.RelPath)
			}
		})
	}
}

// TestImportMissingMediaRespectsSkipFlag asserts the behavior of both skip_missing_media values:
// false (the default) fails on missing media and lists them; true only counts them, keeps importing, and writes no file at all.
func TestImportMissingMediaRespectsSkipFlag(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, src, "pkg_missing_owner")[0]
			deckID := seedPresetDeck(t, src, owner)

			// the note references a sha whose bytes are not in the package at all (export used include_media=0, the reference is still in the fields).
			sha := strings.Repeat("a", 64)
			n := Note{DeckID: deckID, NoteTypeID: Ptr(stockNoteTypeID(t, src, deckID, "basic")), TagsJSON: "[]", CreatedBy: Ptr(owner)}
			if _, err := NewNoteStore(src).Create(ctx, &n, map[string]any{"front": "x?", "back": `<img src="` + sha + `.png">`}); err != nil {
				t.Fatalf("create note: %v", err)
			}
			raw := exportZip(t, src, owner, deckID, PackageOptions{IncludeMedia: false})

			// default (skip_missing_media=false): it fails and names the missing media entries.
			_, err := NewDeckStore(dst).ImportPackage(ctx, 1, bytes.NewReader(raw), PackageImportOptions{})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageBadFormat {
				t.Fatalf("error = %v, want PackageError code %s", err, CodePackageBadFormat)
			}
			if !entryContains(pe.Entries, sha) {
				t.Fatalf("entries = %v, want one naming media/%s", pe.Entries, sha)
			}

			// skip_missing_media=true: it only counts and keeps importing.
			dstRoot := t.TempDir()
			importer := seedUsers(t, dst, "pkg_missing_importer")[0]
			report, err := NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(raw), PackageImportOptions{SkipMissingMedia: true, MediaRoot: dstRoot})
			if err != nil {
				t.Fatalf("ImportPackage (skip_missing_media): %v", err)
			}
			if report.MediaMissing != 1 || report.MediaNew != 0 || report.NotesCreated != 1 {
				t.Fatalf("report media_missing=%d media_new=%d notes_created=%d, want 1/0/1", report.MediaMissing, report.MediaNew, report.NotesCreated)
			}
			row, err := NewMediaStore(dst).BySha256(ctx, sha)
			if err != nil {
				t.Fatalf("load media metadata: %v", err)
			}
			if row != nil {
				t.Fatalf("missing media must not create metadata, got %+v", row)
			}
			entries, err := os.ReadDir(dstRoot)
			if err != nil {
				t.Fatalf("read media root: %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("missing media must not write files, found %d entries", len(entries))
			}
		})
	}
}

// entryContains reports whether any entry in the per-item error list mentions needle.
func entryContains(entries []string, needle string) bool {
	for _, e := range entries {
		if strings.Contains(e, needle) {
			return true
		}
	}
	return false
}

// TestImportPackageRollbackLeavesNoOrphanMedia is an acceptance test: media is written
// before the transaction commits, so if the transaction then fails and rolls back, the written bytes must be cleaned up too, leaving no orphaned file nobody references.
//
// How it is set up: the target deck is pre-seeded with a row sharing the packaged note's external_ref, and on_conflict=fail
// makes the import fail wholesale after 'media is on disk and the note has been judged conflicting'. The conflict check happens after the media write
// (see the ordering in importInTx), so this path really does write files and then roll back.
//
// Both sides are asserted: the media shard directory exists (proving the disk-write path really ran, so the test is not a no-op) and the file has been cleaned up,
// while no metadata row exists.
func TestImportPackageRollbackLeavesNoOrphanMedia(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, src, "pkg_orphan_owner")[0]
			srcDeck := seedPresetDeck(t, src, owner)
			srcRoot := t.TempDir()

			raw := append([]byte("\x89PNG\r\n\x1a\n"), []byte("orphan-media-payload")...)
			sum := sha256.Sum256(raw)
			sha := hex.EncodeToString(sum[:])
			m, err := NewMediaStore(src).SaveBytes(ctx, srcRoot, "image/png", raw, Ptr(owner))
			if err != nil {
				t.Fatalf("SaveBytes: %v", err)
			}
			if m.Sha256 != sha {
				t.Fatalf("seeded media sha = %s, want %s", m.Sha256, sha)
			}
			srcNote := Note{DeckID: srcDeck, NoteTypeID: Ptr(stockNoteTypeID(t, src, srcDeck, "basic")), TagsJSON: "[]", CreatedBy: Ptr(owner), ExternalRef: Ptr("ext:conflict")}
			if _, err := NewNoteStore(src).Create(ctx, &srcNote, map[string]any{
				"front": "q?", "back": `<img src="` + sha + `.png">`,
			}); err != nil {
				t.Fatalf("create source note: %v", err)
			}
			rawZip := exportZip(t, src, owner, srcDeck, PackageOptions{IncludeMedia: true, MediaRoot: srcRoot})

			importer := seedUsers(t, dst, "pkg_orphan_importer")[0]
			dstDeck := seedPresetDeck(t, dst, importer)
			// pre-seed a note with the same external_ref so the packaged note hits the conflict (on_conflict=fail → the whole package rolls back).
			pre := Note{DeckID: dstDeck, NoteTypeID: Ptr(stockNoteTypeID(t, dst, dstDeck, "basic")), TagsJSON: "[]", CreatedBy: Ptr(importer), ExternalRef: Ptr("ext:conflict")}
			if _, err := NewNoteStore(dst).Create(ctx, &pre, map[string]any{"front": "already here", "back": "x"}); err != nil {
				t.Fatalf("create conflicting note: %v", err)
			}

			dstRoot := t.TempDir()
			target := "into_deck:" + strconv.FormatUint(dstDeck, 10)
			_, err = NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(rawZip), PackageImportOptions{
				Target: target, OnConflict: "fail", MediaRoot: dstRoot,
			})
			if err == nil {
				t.Fatal("ImportPackage succeeded, want a conflict failure after the media write")
			}
			var pe *PackageError
			if !errors.As(err, &pe) {
				t.Fatalf("error = %v, want *PackageError", err)
			}

			// the metadata row must roll back with the transaction.
			row, err := NewMediaStore(dst).BySha256(ctx, sha)
			if err != nil {
				t.Fatalf("load media metadata: %v", err)
			}
			if row != nil {
				t.Fatalf("rolled-back import left media metadata %+v", row)
			}
			// the disk-write path really ran: the shard directory was created by MkdirAll, and cleanup only deletes files, not directories.
			shardDir := filepath.Join(dstRoot, sha[:2])
			if fi, err := os.Stat(shardDir); err != nil || !fi.IsDir() {
				t.Fatalf("shard dir %s missing (err=%v); the media write path did not run", shardDir, err)
			}
			// the key assertion: no orphaned file is left behind.
			orphan := filepath.Join(shardDir, sha+".png")
			if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("orphan media file %s survived the rollback (err=%v)", orphan, err)
			}
			var files int
			if err := filepath.WalkDir(dstRoot, func(_ string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() {
					files++
				}
				return nil
			}); err != nil {
				t.Fatalf("walk media root: %v", err)
			}
			if files != 0 {
				t.Fatalf("media root %s still holds %d orphan file(s)", dstRoot, files)
			}
		})
	}
}

// TestExportIncludesNoteTypeOnlyMedia is the export counterpart of the GC rule: a `_`-prefixed font referenced only by a
// note type's CSS must travel with the package, or the target deck would receive a template whose asset was left behind.
func TestExportIncludesNoteTypeOnlyMedia(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, db, "pkg_font_owner")[0]
			deckID := seedPresetDeck(t, db, owner)
			root := t.TempDir()

			font, err := NewMediaStore(db).SaveBytes(ctx, root, "font/woff2", []byte("wOF2\x00\x01\x00\x00fake-font"), Ptr(owner))
			if err != nil {
				t.Fatalf("SaveBytes(font): %v", err)
			}
			if err := NewMediaNameStore(db).Upsert(ctx, deckID, "_font.woff2", font.Sha256); err != nil {
				t.Fatalf("register font name: %v", err)
			}
			nt := NoteType{DeckID: deckID, Name: "Fonts"}
			cfg := noteTypeConfig("Card 1")
			cfg.CSS = `@font-face { src: url(_font.woff2); }`
			if err := NewNoteTypeStore(db).Create(ctx, &nt, cfg); err != nil {
				t.Fatalf("create note type: %v", err)
			}

			pkg, err := NewDeckStore(db).ExportPackage(ctx, owner, deckID, PackageOptions{IncludeMedia: true, MediaRoot: root})
			if err != nil {
				t.Fatalf("ExportPackage: %v", err)
			}
			if _, ok := pkg.MediaBytes[font.Sha256]; !ok {
				t.Fatalf("exported media lacks the note-type-only font %s (media: %v)", font.Sha256, pkg.Media)
			}
		})
	}
}

// TestExportIncludesEditorAbsoluteMedia is the export half of the note-write normalisation: a note created from the
// editor's /media/<sha> reference stores a deck name, and the package must carry those bytes.
func TestExportIncludesEditorAbsoluteMedia(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, db, "pkg_abs_media_owner")[0]
			deckID := seedPresetDeck(t, db, owner)
			root := t.TempDir()

			raw := append([]byte("\x89PNG\r\n\x1a\n"), []byte("editor-image")...)
			media, err := NewMediaStore(db).SaveBytes(ctx, root, "image/png", raw, Ptr(owner))
			if err != nil {
				t.Fatalf("SaveBytes: %v", err)
			}
			n := Note{DeckID: deckID, NoteTypeID: Ptr(stockNoteTypeID(t, db, deckID, "basic")), TagsJSON: "[]", CreatedBy: Ptr(owner)}
			if _, err := NewNoteStore(db).Create(ctx, &n, map[string]any{
				"front": "q", "back": `<img src="/media/` + media.Sha256 + `">`,
			}); err != nil {
				t.Fatalf("Create(note): %v", err)
			}

			pkg, err := NewDeckStore(db).ExportPackage(ctx, owner, deckID, PackageOptions{IncludeMedia: true, MediaRoot: root})
			if err != nil {
				t.Fatalf("ExportPackage: %v", err)
			}
			if !bytes.Equal(pkg.MediaBytes[media.Sha256], raw) {
				t.Fatalf("exported media lacks the normalized reference %s", media.Sha256)
			}
		})
	}
}
