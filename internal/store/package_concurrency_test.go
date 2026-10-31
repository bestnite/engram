package store

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/retention"
)

// Media writing and transaction holding time.
//
// An .apkg may decompress to 4 GiB, so the import places the media bytes on disk before the
// database transaction opens and writes rows only inside it. On SQLite the write lock is held for
// the transaction's whole life, so copying bytes inside it would make every other writer wait or
// fail. These tests pin that ordering and the cleanup rule around it.

// countPlacedMedia counts the files already placed in the content-addressed shard directories.
// The staging directory (root/tmp) is a single level too, so only two-hex shard directories count.
func countPlacedMedia(t *testing.T, root string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "[0-9a-f][0-9a-f]", "*"))
	if err != nil {
		t.Fatalf("glob media shards: %v", err)
	}
	return len(matches)
}

// largeAnkiMedia builds an .apkg whose media set is large enough that the file-writing phase lasts
// long enough for another connection to run a write while it is under way.
func largeAnkiMedia(t *testing.T, count int) []byte {
	t.Helper()
	data := make(map[string][]byte, count)
	names := make(map[string]string, count)
	for i := 0; i < count; i++ {
		key := strconv.Itoa(i)
		data[key] = bytes.Repeat([]byte{byte(i)}, 8<<10)
		names[key] = fmt.Sprintf("media-%d.png", i)
	}
	// Keep the fixture's own reference resolvable, so the report has no missing-media noise.
	names["0"] = "placeholder.png"
	return ankiFixtureWithMedia(t, "stock.anki2.apkg", data, names)
}

// TestAnkiImportWritesMediaOutsideTheTransaction proves the ordering: while the import is still
// writing media files, a second connection to the same SQLite database commits a write.
//
// Before the fix the bytes were written inside the import's transaction, so the write lock was held
// for the whole media copy and this write could not commit until the import had finished.
func TestAnkiImportWritesMediaOutsideTheTransaction(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "import_lock.db")
	db, err := Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB(): %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	// A second connection pool to the same file with a short busy timeout: a write that meets the
	// import's transaction fails instead of waiting it out, so the assertion below is meaningful.
	other, err := Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(25)")
	if err != nil {
		t.Fatalf("open second sqlite connection: %v", err)
	}
	otherSQL, err := other.DB()
	if err != nil {
		t.Fatalf("second db.DB(): %v", err)
	}
	t.Cleanup(func() { _ = otherSQL.Close() })

	raw := largeAnkiMedia(t, 120)
	owner := seedUsers(t, db, "anki_outside_tx")[0]
	root := t.TempDir()

	done := make(chan struct{})
	var report *PackageImportReport
	var importErr error
	go func() {
		defer close(done)
		report, importErr = NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
			Target: PackageTargetNewDeck, MediaRoot: root,
		})
	}()

	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	committed := false
	seen := 0
	for time.Now().Before(deadline) {
		select {
		case <-done:
			t.Fatalf("import finished before the concurrent write could run (media files seen: %d)", seen)
		default:
		}
		seen = countPlacedMedia(t, root)
		if seen == 0 {
			time.Sleep(2 * time.Millisecond)
			continue
		}
		lastErr = other.Exec(
			"INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)",
			"test.concurrent_probe", `"committed"`, time.Now().UTC(),
		).Error
		if lastErr == nil {
			committed = true
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !committed {
		t.Fatalf("another connection could not commit while media was written (last error: %v)", lastErr)
	}

	<-done
	if importErr != nil {
		t.Fatalf("import: %v", importErr)
	}
	if report.MediaNew != 120 {
		t.Fatalf("media_new = %d, want 120", report.MediaNew)
	}
}

// TestAnkiImportKeepsFilesThatExistedBefore covers the cleanup rule: a dry run and a failed import
// leave no file the import created, and keep every file that was there before the run.
func TestAnkiImportKeepsFilesThatExistedBefore(t *testing.T) {
	ctx := context.Background()
	existingBytes := []byte("\x89PNG\r\n\x1a\nstored-before-the-import")
	newBytes := []byte("\x89PNG\r\n\x1a\ncreated-by-the-import")
	orphanBytes := []byte("\x89PNG\r\n\x1a\non-disk-without-a-row")
	existingSha, newSha, orphanSha := shaOf(existingBytes), shaOf(newBytes), shaOf(orphanBytes)

	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, db, "anki_cleanup_"+driver)[0]
			root := t.TempDir()

			// A file with a committed metadata row: the import must reuse it and never remove it.
			if _, _, err := NewMediaStore(db).SaveBytesTracked(ctx, root, "image/png", existingBytes, Ptr(owner)); err != nil {
				t.Fatalf("seed stored media: %v", err)
			}
			// A file on disk with no metadata row: it existed before the import, so it is not the
			// import's to remove either.
			orphanPath := contentAddressedPath(root, orphanSha, "image/png")
			if err := os.MkdirAll(filepath.Dir(orphanPath), 0o755); err != nil {
				t.Fatalf("create orphan shard dir: %v", err)
			}
			if err := os.WriteFile(orphanPath, orphanBytes, 0o644); err != nil {
				t.Fatalf("write orphan file: %v", err)
			}
			newPath := contentAddressedPath(root, newSha, "image/png")

			raw := ankiFixtureWithMedia(t, "stock.anki2.apkg",
				map[string][]byte{"0": existingBytes, "1": newBytes, "2": orphanBytes},
				map[string]string{"0": "placeholder.png", "1": "new.png", "2": "orphan.png"},
			)

			// Dry run: nothing is placed, nothing is removed.
			report, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
				Target: PackageTargetNewDeck, DryRun: true, MediaRoot: root,
			})
			if err != nil {
				t.Fatalf("dry-run import: %v", err)
			}
			if report.MediaNew != 2 {
				t.Fatalf("dry-run media_new = %d, want 2 (the bytes with no metadata row)", report.MediaNew)
			}
			assertFileMissing(t, newPath, "dry run")
			assertFilePresent(t, contentAddressedPath(root, existingSha, "image/png"), "dry run")
			assertFilePresent(t, orphanPath, "dry run")

			// A failed import: the bytes are placed before the transaction, and the rollback removes
			// exactly the file this run created.
			if _, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
				Target: "into_deck:999999", MediaRoot: root,
			}); err == nil {
				t.Fatal("import into a nonexistent deck succeeded")
			}
			assertFileMissing(t, newPath, "failed import")
			assertFilePresent(t, contentAddressedPath(root, existingSha, "image/png"), "failed import")
			assertFilePresent(t, orphanPath, "failed import")
			if row, err := NewMediaStore(db).BySha256(ctx, newSha); err != nil || row != nil {
				t.Fatalf("failed import left media row %+v (err %v), want none", row, err)
			}
		})
	}
}

// TestAnkiImportClearsOrphanMarkBeforeTheTransaction proves that a media collection run between the
// media placement and the import transaction cannot delete a stored row the import is about to
// reuse, even when that row's grace period has already passed.
//
// placeAnkiMedia skips a sha that already has a row, and the transaction later reuses that row. A
// row orphaned longer than the collection grace would be deleted, together with its file, by a
// collection run that starts meanwhile, and the transaction would then fail on media it never wrote.
func TestAnkiImportClearsOrphanMarkBeforeTheTransaction(t *testing.T) {
	ctx := context.Background()
	mediaBytes := []byte("\x89PNG\r\n\x1a\nreused-by-an-import")
	sha := shaOf(mediaBytes)
	raw := ankiFixtureWithMedia(t, "stock.anki2.apkg",
		map[string][]byte{"0": mediaBytes},
		map[string]string{"0": "placeholder.png"},
	)
	limits := DefaultPackageImportLimits()

	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, db, "anki_orphan_"+driver)[0]
			root := t.TempDir()
			mstore := NewMediaStore(db)
			if _, _, err := mstore.SaveBytesTracked(ctx, root, "image/png", mediaBytes, Ptr(owner)); err != nil {
				t.Fatalf("seed stored media: %v", err)
			}
			orphanedAt := time.Now().UTC().Add(-8 * 24 * time.Hour)
			if err := db.Model(&Media{}).Where("sha256 = ?", sha).Update("orphaned_at", orphanedAt).Error; err != nil {
				t.Fatalf("mark media orphaned: %v", err)
			}
			path := contentAddressedPath(root, sha, "image/png")

			// A dry run of the same package places nothing and clears nothing.
			before, err := mstore.BySha256(ctx, sha)
			if err != nil || before == nil || before.OrphanedAt == nil {
				t.Fatalf("seeded media row = %+v (err %v), want an orphaned row", before, err)
			}
			if _, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
				Target: PackageTargetNewDeck, DryRun: true, MediaRoot: root,
			}); err != nil {
				t.Fatalf("dry-run import: %v", err)
			}
			after, err := mstore.BySha256(ctx, sha)
			if err != nil || after == nil || after.OrphanedAt == nil {
				t.Fatalf("dry run left media row %+v (err %v), want the orphan mark unchanged", after, err)
			}
			if !after.OrphanedAt.Equal(*before.OrphanedAt) {
				t.Fatalf("dry run changed orphaned_at from %v to %v", before.OrphanedAt, after.OrphanedAt)
			}

			// Media placement runs, then a collection run with the real grace period, then the
			// transaction. The reused row and its file must survive and the import must succeed.
			actorCtx := WithActor(ctx, owner)
			store := NewDeckStore(db)
			placement, err := store.prepareAnkiImport(actorCtx, owner, bytes.NewReader(raw), int64(len(raw)), limits,
				PackageImportOptions{Target: PackageTargetNewDeck, MediaRoot: root, Now: func() time.Time { return time.Now().UTC() }}, PackageTargetNewDeck, 0)
			if err != nil {
				t.Fatalf("prepare import: %v", err)
			}
			collected, err := DeleteOrphanedMedia(ctx, db, time.Now().UTC().Add(-retention.MediaOrphanGrace), 10)
			if err != nil {
				t.Fatalf("DeleteOrphanedMedia: %v", err)
			}
			if len(collected) != 0 {
				t.Fatalf("collection selected %d row(s) a pending import was about to reuse", len(collected))
			}
			if _, err := store.importAnkiPlacement(actorCtx, placement); err != nil {
				t.Fatalf("import transaction: %v", err)
			}
			row, err := mstore.BySha256(ctx, sha)
			if err != nil || row == nil {
				t.Fatalf("imported media row = %+v (err %v), want the reused row", row, err)
			}
			if row.OrphanedAt != nil {
				t.Fatalf("imported media row is still orphaned at %v", row.OrphanedAt)
			}
			assertFilePresent(t, path, "import after a collection run")
		})
	}
}

// assertFilePresent asserts path exists.
func assertFilePresent(t *testing.T, path, stage string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s removed a file that existed before it: %s: %v", stage, path, err)
	}
}

// assertFileMissing asserts path does not exist.
func assertFileMissing(t *testing.T, path, stage string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s left a file it created: %s (stat err %v)", stage, path, err)
	}
}

// TestResolvePackageMaxConcurrentImports covers the resolution order of the concurrency cap:
// environment variable, then settings table, then the default.
func TestResolvePackageMaxConcurrentImports(t *testing.T) {
	db := packageDatabases(t)["sqlite"]
	ctx := context.Background()
	t.Setenv(EnvPackageMaxConcurrentImports, "")

	if got := ResolvePackageMaxConcurrentImports(ctx, db); got != DefaultPackageMaxConcurrentImports {
		t.Fatalf("default = %d, want %d", got, DefaultPackageMaxConcurrentImports)
	}
	if err := PutSetting(ctx, db, SettingKeyPackageMaxConcurrentImports, "5", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting: %v", err)
	}
	if got := ResolvePackageMaxConcurrentImports(ctx, db); got != 5 {
		t.Fatalf("settings = %d, want 5", got)
	}
	t.Setenv(EnvPackageMaxConcurrentImports, "7")
	if got := ResolvePackageMaxConcurrentImports(ctx, db); got != 7 {
		t.Fatalf("environment = %d, want 7", got)
	}
}
