package store

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// Deck package zip index validation.
//
//  1. **duplicate file names/repeated paths** inside the archive must be rejected -- the parser used to silently overwrite same-named entries (the later one won),
//     so a package could hide an overlay layer that disagreed with the index;
//  2. the sha256 declared in media.json must equal **the real sha256 of the entry's bytes**, and the file name must be
//     `<sha256>.<ext>`; otherwise a "fake sha" can decouple the index from the content entirely.
//
// The failure must happen before any write: no deck/card/media rows in the database and no leftover files on disk.

// zipWithEntries builds a zip in the given order: same-named entries are allowed, and two same-named entries may differ in content -- exactly the attack surface.
func zipWithEntries(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatalf("zip create %s: %v", e[0], err)
		}
		if _, err := w.Write([]byte(e[1])); err != nil {
			t.Fatalf("zip write %s: %v", e[0], err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// indexPackageEntries returns the JSON entries of a minimally valid package (without the media byte entry itself).
// mediaPath serves both as the note's reference and as the path in media.json; sha is the key declared in media.json.
func indexPackageEntries(mediaPath, sha, mime string) [][2]string {
	return [][2]string{
		{"manifest.json", `{"format_version":1,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"d"},"include_progress":false,"include_media":true,"include_reviews":false,"counts":{"notes":1,"cards":0,"media":1}}`},
		{"notes.json", fmt.Sprintf(`[{"kind":"basic","fields":{"front":"q","back":"%s"}}]`, mediaPath)},
		{"cards.json", `[]`},
		{"preset.json", `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`},
		{"media.json", fmt.Sprintf(`{"%s":{"path":"%s","mime":"%s"}}`, sha, mediaPath, mime)},
	}
}

// png64 returns 64 bytes of valid media carrying the PNG magic number (the fill byte is chosen by fill).
func png64(fill string) []byte {
	body := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte(fill), 56)...)
	return body
}

// assertNoImportSideEffects asserts a rejected import leaves no deck/card/media rows and no files on disk.
func assertNoImportSideEffects(t *testing.T, db *gorm.DB, root string) {
	t.Helper()
	var decks, notes, media int64
	db.Model(&Deck{}).Count(&decks)
	db.Model(&Note{}).Count(&notes)
	db.Model(&Media{}).Count(&media)
	if decks != 0 || notes != 0 || media != 0 {
		t.Fatalf("rejected import wrote rows: decks=%d notes=%d media=%d", decks, notes, media)
	}
	assertDirEmpty(t, root)
}

// TestReadPackageArchiveRejectsDuplicateEntry is the minimal negative case for "repeated path": the same name appears twice,
// and ReadPackageArchive must reject it (before the fix the later one overwrote the map entry and it passed silently).
func TestReadPackageArchiveRejectsDuplicateEntry(t *testing.T) {
	sha := strings.Repeat("a", 64)
	path := "media/" + sha + ".png"
	raw := zipWithEntries(t, [][2]string{
		{"manifest.json", `{}`},
		{path, "first"},
		{path, "second"},
	})
	_, err := ReadPackageArchive(bytes.NewReader(raw), DefaultPackageLimits())
	var pe *PackageError
	if !errors.As(err, &pe) || pe.Code != CodePackageBadFormat {
		t.Fatalf("error = %v, want PackageError code %s", err, CodePackageBadFormat)
	}
	if !entryContains(pe.Entries, path) {
		t.Fatalf("entries = %v, want one naming %s", pe.Entries, path)
	}
}

// TestImportPackageRejectsDuplicateShaNamedEntryDifferentContent covers "different content, same declared sha":
// one media/<sha>.png appears twice with different content -- they claim the same sha, but the two byte runs cannot both be right.
// The whole package is rejected, with zero side effects.
func TestImportPackageRejectsDuplicateShaNamedEntryDifferentContent(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			body := png64("x")
			other := png64("y")
			sum := sha256.Sum256(body)
			sha := hex.EncodeToString(sum[:])
			path := "media/" + sha + ".png"
			entries := append(indexPackageEntries(path, sha, "image/png"),
				[2]string{path, string(body)},
				[2]string{path, string(other)},
			)
			raw := zipWithEntries(t, entries)

			importer := seedUsers(t, db, "pkg_dup_same_sha")[0]
			root := t.TempDir()
			_, err := NewDeckStore(db).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{
				Target:    PackageTargetNewDeck,
				MediaRoot: root,
			})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageBadFormat {
				t.Fatalf("error = %v, want PackageError code %s", err, CodePackageBadFormat)
			}
			if !entryContains(pe.Entries, path) {
				t.Fatalf("entries = %v, want one naming %s", pe.Entries, path)
			}
			assertNoImportSideEffects(t, db, root)
		})
	}
}

// TestImportPackageRejectsDeclaredShaMismatch covers "declared sha ≠ actual byte sha": media.json uses a run of
// valid hexadecimal sha as the key and the path carries that sha too, but the bytes' real sha256 is another value (a fake sha).
// Before the fix the import succeeded as usual (the bytes were stored under their real sha); after the fix the whole package is rejected with zero side effects.
func TestImportPackageRejectsDeclaredShaMismatch(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			body := png64("z")
			fakeSha := strings.Repeat("f", 64)
			path := "media/" + fakeSha + ".png"
			entries := append(indexPackageEntries(path, fakeSha, "image/png"), [2]string{path, string(body)})
			raw := zipWithEntries(t, entries)

			importer := seedUsers(t, db, "pkg_fake_sha")[0]
			root := t.TempDir()
			_, err := NewDeckStore(db).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{
				Target:    PackageTargetNewDeck,
				MediaRoot: root,
			})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageBadFormat {
				t.Fatalf("error = %v, want PackageError code %s", err, CodePackageBadFormat)
			}
			if !entryContains(pe.Entries, path) {
				t.Fatalf("entries = %v, want one naming %s", pe.Entries, path)
			}
			assertNoImportSideEffects(t, db, root)
		})
	}
}

// TestImportPackageRejectsMediaEntryNameNotMatchingSha is the neighbouring negative case: the bytes' sha256 matches the media.json
// key and the note references the canonical path media/<sha>.png, but media.json points that sha at an entry whose
// file name has nothing to do with the sha (index does not match content). The whole package is rejected, zero side effects.
func TestImportPackageRejectsMediaEntryNameNotMatchingSha(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			body := png64("w")
			sum := sha256.Sum256(body)
			sha := hex.EncodeToString(sum[:])
			// The sha declared in media.json points at an entry whose basename is unrelated to the sha.
			path := "media/" + strings.Repeat("b", 64) + ".png"
			entries := [][2]string{
				{"manifest.json", `{"format_version":1,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"d"},"include_progress":false,"include_media":true,"include_reviews":false,"counts":{"notes":1,"cards":0,"media":1}}`},
				{"notes.json", fmt.Sprintf(`[{"kind":"basic","fields":{"front":"q","back":"media/%s.png"}}]`, sha)},
				{"cards.json", `[]`},
				{"preset.json", `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`},
				{"media.json", fmt.Sprintf(`{"%s":{"path":"%s","mime":"image/png"}}`, sha, path)},
				{path, string(body)},
			}
			raw := zipWithEntries(t, entries)

			importer := seedUsers(t, db, "pkg_name_mismatch")[0]
			root := t.TempDir()
			_, err := NewDeckStore(db).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{
				Target:    PackageTargetNewDeck,
				MediaRoot: root,
			})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageBadFormat {
				t.Fatalf("error = %v, want PackageError code %s", err, CodePackageBadFormat)
			}
			if !entryContains(pe.Entries, path) {
				t.Fatalf("entries = %v, want one naming %s", pe.Entries, path)
			}
			assertNoImportSideEffects(t, db, root)
		})
	}
}
