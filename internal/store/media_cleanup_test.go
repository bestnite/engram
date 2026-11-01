package store

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TestCancelledImportKeepsMediaCommittedByAnother covers the cleanup rule when the lookup itself fails:
// a failed query is not proof that no metadata row exists. Import A places the file before its transaction;
// import B reuses that file and commits the metadata row; A is then cancelled, so its cleanup runs on a
// cancelled context. Before the fix the failed lookup was read as "no row" and the bytes B committed were
// deleted, leaving a metadata row pointing at a missing file for every later upload.
func TestCancelledImportKeepsMediaCommittedByAnother(t *testing.T) {
	mediaBytes := []byte("\x89PNG\r\n\x1a\ncancelled-import-shared-file")
	sha := shaOf(mediaBytes)
	raw := ankiFixtureWithMedia(t, "stock.anki2.apkg",
		map[string][]byte{"0": mediaBytes},
		map[string]string{"0": "placeholder.png"},
	)
	limits := DefaultPackageImportLimits()

	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ids := seedUsers(t, db, "cleanup_a_"+driver, "cleanup_b_"+driver)
			ownerA, ownerB := ids[0], ids[1]
			root := t.TempDir()
			path := contentAddressedPath(root, sha, "image/png")

			// A writes the bytes outside its transaction; no metadata row exists yet.
			store := NewDeckStore(db)
			placement, err := store.prepareAnkiImport(WithActor(context.Background(), ownerA), ownerA,
				bytes.NewReader(raw), int64(len(raw)), limits,
				PackageImportOptions{Target: PackageTargetNewDeck, PresetMode: PackagePresetNew, MediaRoot: root},
				PackageTargetNewDeck, 0)
			if err != nil {
				t.Fatalf("prepare import A: %v", err)
			}
			assertFilePresent(t, path, "prepare import A")
			if len(placement.writtenMedia) != 1 {
				t.Fatalf("A recorded %d written media files, want 1", len(placement.writtenMedia))
			}

			// B imports the same package and commits the metadata row for that sha.
			if _, err := store.ImportPackage(context.Background(), ownerB, bytes.NewReader(raw), PackageImportOptions{
				Target: PackageTargetNewDeck, PresetMode: PackagePresetNew, MediaRoot: root,
			}); err != nil {
				t.Fatalf("import B: %v", err)
			}
			row, err := NewMediaStore(db).BySha256(context.Background(), sha)
			if err != nil || row == nil {
				t.Fatalf("B's media row = %+v (err %v), want the committed row", row, err)
			}

			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			defer slog.SetDefault(previous)

			// A is cancelled: its transaction fails and its cleanup runs with the cancelled context.
			cancelled, cancel := context.WithCancel(WithActor(context.Background(), ownerA))
			cancel()
			if _, err := store.importAnkiPlacement(cancelled, placement); err == nil {
				t.Fatal("import A with a cancelled context succeeded")
			}
			// Prove the cleanup's lookup really fails on this context; otherwise the case would pass for the wrong reason.
			if _, err := NewMediaStore(db).BySha256(cancelled, sha); err == nil {
				t.Fatal("a lookup on the cancelled context succeeded; the failed-lookup path is not exercised")
			}

			assertFilePresent(t, path, "cancelled import A")
			if !strings.Contains(logs.String(), "media cleanup kept a file whose metadata lookup failed") || !strings.Contains(logs.String(), sha) {
				t.Errorf("cleanup did not report the failed lookup; logs: %s", logs.String())
			}

			// A later upload of the same bytes reuses the row and the surviving file.
			reused, _, err := NewMediaStore(db).SaveBytesTracked(context.Background(), root, "image/png", mediaBytes, Ptr(ownerB))
			if err != nil {
				t.Fatalf("later upload of the same bytes: %v", err)
			}
			if reused == nil || reused.Sha256 != sha {
				t.Fatalf("later upload row = %+v, want the existing row for %s", reused, sha)
			}
			assertFilePresent(t, path, "later upload")
		})
	}
}
