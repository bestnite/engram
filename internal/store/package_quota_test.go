package store

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

// The media bytes a deck package import adds must count against the **importer's** quota; over the limit the whole package fails with zero residue.
//
// Before the fix ImportPackage ignored the quota entirely: as long as the package unpacked and the media was allowlisted, the bytes hit the disk. The upload chain
// (checkMediaQuota in internal/web) guards the /media and /decks/:id/media entry points, while
// all four import paths -- web / REST / MCP / CLI -- went around it, so a package stuffed with media could take up space without limit.

// quotaImportFixture builds a source database: one 64-byte PNG media + a note referencing it, then exports a package with media.
// It returns the package bytes, the media sha256 and the media byte count.
func quotaImportFixture(t *testing.T, src *gorm.DB) (raw []byte, sha string, size int) {
	t.Helper()
	owner := seedUsers(t, src, "pkg_quota_owner")[0]
	deckID := seedPresetDeck(t, src, owner)
	srcRoot := t.TempDir()
	body := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("q"), 56)...)
	sha = seedMediaNote(t, src, srcRoot, owner, deckID, body, "image/png", "png")
	raw = exportZip(t, src, owner, deckID, PackageOptions{IncludeMedia: true, MediaRoot: srcRoot})
	return raw, sha, len(body)
}

// TestImportPackageMediaQuotaRejectsAndLeavesNoTrace is the core negative case: when the added media would exceed the importer's
// quota the whole package is rejected, with no rows in the database and no leftover files on disk.
func TestImportPackageMediaQuotaRejectsAndLeavesNoTrace(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			raw, sha, size := quotaImportFixture(t, src)
			importer := seedUsers(t, dst, "pkg_quota_importer")[0]
			root := t.TempDir()

			// The quota is 1 byte below the package media: 0 used + size added > size-1.
			_, err := NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(raw), PackageImportOptions{
				Target:          PackageTargetNewDeck,
				MediaRoot:       root,
				MediaQuotaBytes: int64(size - 1),
			})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageQuotaExceeded {
				t.Fatalf("error = %v, want PackageError code %s", err, CodePackageQuotaExceeded)
			}

			var decks, notes, media int64
			dst.Model(&Deck{}).Count(&decks)
			dst.Model(&Note{}).Count(&notes)
			dst.Model(&Media{}).Count(&media)
			if decks != 0 || notes != 0 || media != 0 {
				t.Fatalf("rejected import wrote rows: decks=%d notes=%d media=%d", decks, notes, media)
			}
			if row, err := NewMediaStore(dst).BySha256(ctx, sha); err != nil || row != nil {
				t.Fatalf("media row = %+v err=%v, want none", row, err)
			}
			assertDirEmpty(t, root)
		})
	}
}

// TestImportPackageMediaQuotaAllowsWithinLimit is the positive control: when the limit exactly equals the added bytes it still passes,
// and the media really lands in the database and on disk (proving the new check does not reject the normal import).
func TestImportPackageMediaQuotaAllowsWithinLimit(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			raw, sha, size := quotaImportFixture(t, src)
			importer := seedUsers(t, dst, "pkg_quota_ok")[0]
			root := t.TempDir()

			report, err := NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(raw), PackageImportOptions{
				Target:          PackageTargetNewDeck,
				MediaRoot:       root,
				MediaQuotaBytes: int64(size),
			})
			if err != nil {
				t.Fatalf("ImportPackage: %v", err)
			}
			if report.MediaNew != 1 || report.NotesCreated != 1 {
				t.Fatalf("report media_new=%d notes_created=%d, want 1/1", report.MediaNew, report.NotesCreated)
			}
			row, err := NewMediaStore(dst).BySha256(ctx, sha)
			if err != nil || row == nil {
				t.Fatalf("media row = %+v err=%v, want the imported blob", row, err)
			}
		})
	}
}

// TestImportPackageMediaQuotaDoesNotChargeTwice verifies the de-duplication accounting: when the importer is already charged for the same blob,
// importing the same sha again adds 0 -- the second import must still pass when the quota exactly equals that blob's size.
// Counting size+size would reject it as over the limit, which would mean de-duplication is not working.
func TestImportPackageMediaQuotaDoesNotChargeTwice(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			raw, _, size := quotaImportFixture(t, src)
			importer := seedUsers(t, dst, "pkg_quota_dedupe")[0]

			// First import: the media counts toward the importer's usage (the note referencing it belongs to that user).
			if _, err := NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(raw), PackageImportOptions{
				Target:          PackageTargetNewDeck,
				MediaRoot:       t.TempDir(),
				MediaQuotaBytes: int64(size),
			}); err != nil {
				t.Fatalf("first import: %v", err)
			}

			// Second import of the same package into a new deck: the sha is already charged, the added amount is 0, and the unchanged quota still passes.
			if _, err := NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(raw), PackageImportOptions{
				Target:          PackageTargetNewDeck,
				MediaRoot:       t.TempDir(),
				MediaQuotaBytes: int64(size),
			}); err != nil {
				t.Fatalf("second import (same sha) rejected, want dedupe to allow it: %v", err)
			}
		})
	}
}
