package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"
)

// MediaStore.Put is the one write path for media bytes; these tests pin the rules every entry point inherits from it.

func putTestPNG(tag string) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), []byte("media-write-"+tag)...)
}

func shaOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func assertTempDirEmpty(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(MediaTempDir(root))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read temp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp dir holds %d leftover files, want 0", len(entries))
	}
}

func uploaderRows(t *testing.T, db *gorm.DB, sha string, userID uint64) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&MediaUploader{}).Where("media_sha = ? AND user_id = ?", sha, userID).Count(&n).Error; err != nil {
		t.Fatalf("count uploaders: %v", err)
	}
	return n
}

// TestMediaPutFreshWritePlacesFileByRename covers the miss path: the staged file is moved to the content-addressed path,
// the row and the uploader are recorded, and the returned path names the file this call placed.
func TestMediaPutFreshWritePlacesFileByRename(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, db, "put_fresh_owner")[0]
			root := t.TempDir()
			raw := putTestPNG("fresh-" + driver)

			row, written, err := NewMediaStore(db).SaveBytesTracked(ctx, root, "image/png", raw, Ptr(owner))
			if err != nil {
				t.Fatalf("SaveBytesTracked: %v", err)
			}
			sha := shaOf(raw)
			wantAbs := filepath.Join(root, sha[:2], sha+".png")
			if row.Sha256 != sha || row.RelPath != filepath.Join(sha[:2], sha+".png") || row.Bytes != int64(len(raw)) {
				t.Fatalf("row = %+v, want sha %s at %s/%s.png", row, sha, sha[:2], sha)
			}
			if written != wantAbs {
				t.Fatalf("written = %q, want %q", written, wantAbs)
			}
			onDisk, err := os.ReadFile(wantAbs)
			if err != nil || !bytes.Equal(onDisk, raw) {
				t.Fatalf("file on disk = %q (err %v), want the written bytes", onDisk, err)
			}
			assertTempDirEmpty(t, root)
			if n := uploaderRows(t, db, sha, owner); n != 1 {
				t.Fatalf("uploader rows = %d, want 1", n)
			}
		})
	}
}

// TestMediaPutDedupHitAppliesReuseRules covers the hit path: no file is rewritten, the second supplier becomes an uploader,
// and a stale orphan mark is cleared so reclamation restarts its grace period.
func TestMediaPutDedupHitAppliesReuseRules(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			users := seedUsers(t, db, "put_hit_first", "put_hit_second")
			first, second := users[0], users[1]
			root := t.TempDir()
			raw := putTestPNG("hit-" + driver)
			ms := NewMediaStore(db)

			orig, _, err := ms.SaveBytesTracked(ctx, root, "image/png", raw, Ptr(first))
			if err != nil {
				t.Fatalf("first SaveBytesTracked: %v", err)
			}
			stale := time.Now().UTC().Add(-30 * 24 * time.Hour)
			if err := db.Model(&Media{}).Where("sha256 = ?", orig.Sha256).Update("orphaned_at", stale).Error; err != nil {
				t.Fatalf("mark orphaned: %v", err)
			}
			abs := filepath.Join(root, orig.RelPath)
			before, err := os.Stat(abs)
			if err != nil {
				t.Fatalf("stat stored file: %v", err)
			}

			row, written, err := ms.SaveBytesTracked(ctx, root, "image/png", raw, Ptr(second))
			if err != nil {
				t.Fatalf("second SaveBytesTracked: %v", err)
			}
			if written != "" {
				t.Fatalf("written = %q on a dedup hit, want empty", written)
			}
			if row.Sha256 != orig.Sha256 || row.OrphanedAt != nil {
				t.Fatalf("returned row = %+v, want the existing row with no orphan mark", row)
			}
			stored, err := ms.BySha256(ctx, orig.Sha256)
			if err != nil || stored == nil || stored.OrphanedAt != nil {
				t.Fatalf("stored row = %+v (err %v), want orphaned_at cleared", stored, err)
			}
			after, err := os.Stat(abs)
			if err != nil || !after.ModTime().Equal(before.ModTime()) {
				t.Fatalf("stored file changed on a dedup hit (err %v)", err)
			}
			assertTempDirEmpty(t, root)
			if n := uploaderRows(t, db, orig.Sha256, second); n != 1 {
				t.Fatalf("second uploader rows = %d, want 1", n)
			}
		})
	}
}

// TestMediaPutConcurrentInsertKeepsTransactionUsable covers losing the insert race: another writer commits the same sha
// between the lookup and the insert. Put must return that row, claim no written file, record the uploader, and leave the
// surrounding transaction usable — a plain INSERT would abort it on PostgreSQL.
func TestMediaPutConcurrentInsertKeepsTransactionUsable(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, db, "put_race_owner")[0]
			root := t.TempDir()
			raw := putTestPNG("race-" + driver)
			sha := shaOf(raw)
			rel := filepath.Join(sha[:2], sha+".png")

			// Simulate the competing writer: right before Put's INSERT on media, a row for the same sha appears.
			const hook = "test:media_put_race"
			fired := false
			if err := db.Callback().Create().Before("gorm:create").Register(hook, func(tx *gorm.DB) {
				if fired || tx.Statement.Table != "media" {
					return
				}
				fired = true
				if err := tx.Session(&gorm.Session{NewDB: true}).Exec(
					"INSERT INTO media (sha256, rel_path, mime, bytes, created_at) VALUES (?, ?, ?, ?, ?)",
					sha, rel, "image/png", len(raw), time.Now().UTC()).Error; err != nil {
					t.Errorf("competing insert: %v", err)
				}
			}); err != nil {
				t.Fatalf("register callback: %v", err)
			}
			t.Cleanup(func() { _ = db.Callback().Create().Remove(hook) })

			err := db.Transaction(func(tx *gorm.DB) error {
				row, written, err := NewMediaStore(tx).SaveBytesTracked(ctx, root, "image/png", raw, Ptr(owner))
				if err != nil {
					t.Fatalf("SaveBytesTracked: %v", err)
				}
				if !fired {
					t.Fatalf("competing insert did not run")
				}
				if written != "" || row.Sha256 != sha || row.CreatedBy != nil {
					t.Fatalf("row = %+v written = %q, want the competing row and no written file", row, written)
				}
				// The transaction must still accept statements after the conflict.
				if n := uploaderRows(t, tx, sha, owner); n != 1 {
					t.Fatalf("uploader rows = %d, want 1", n)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("transaction: %v", err)
			}
			assertTempDirEmpty(t, root)
		})
	}
}

// TestImportPackageReusesStoredMediaRules covers a package import whose media bytes are already stored, unreferenced and
// marked orphaned: the import applies the same dedup-hit rules as an upload, so the mark is cleared at once and the
// importer is recorded as an uploader, instead of the mark lingering until the next reclamation round.
func TestImportPackageReusesStoredMediaRules(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, src, "import_reuse_owner")[0]
			deckID := seedPresetDeck(t, src, owner)
			srcRoot := t.TempDir()
			raw := putTestPNG("import-reuse-" + driver)
			sha := seedMediaNote(t, src, srcRoot, owner, deckID, raw, "image/png", "png")
			zip := exportZip(t, src, owner, deckID, PackageOptions{IncludeMedia: true, MediaRoot: srcRoot})

			dstRoot := t.TempDir()
			users := seedUsers(t, dst, "import_reuse_first", "import_reuse_importer")
			first, importer := users[0], users[1]
			if _, err := NewMediaStore(dst).SaveBytes(ctx, dstRoot, "image/png", raw, Ptr(first)); err != nil {
				t.Fatalf("seed stored media: %v", err)
			}
			stale := time.Now().UTC().Add(-30 * 24 * time.Hour)
			if err := dst.Model(&Media{}).Where("sha256 = ?", sha).Update("orphaned_at", stale).Error; err != nil {
				t.Fatalf("mark orphaned: %v", err)
			}

			report, err := NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(zip), PackageImportOptions{MediaRoot: dstRoot})
			if err != nil {
				t.Fatalf("ImportPackage: %v", err)
			}
			if report.MediaNew != 0 {
				t.Fatalf("media_new = %d, want 0 for bytes already stored", report.MediaNew)
			}
			stored, err := NewMediaStore(dst).BySha256(ctx, sha)
			if err != nil || stored == nil || stored.OrphanedAt != nil {
				t.Fatalf("stored row = %+v (err %v), want orphaned_at cleared by the import", stored, err)
			}
			if n := uploaderRows(t, dst, sha, importer); n != 1 {
				t.Fatalf("importer uploader rows = %d, want 1", n)
			}
		})
	}
}
