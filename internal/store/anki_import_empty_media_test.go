package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// TestAnkiImportKeepsZeroByteMedia pins the import side of an empty attachment: Anki imports a
// package whose media index names a 0-byte file and keeps that file, so engram must not report it
// as missing and drop it. The fixture's own placeholder.png is replaced by one empty entry, which
// the import has to place on disk, register under its deck name and record in the media table.
func TestAnkiImportKeepsZeroByteMedia(t *testing.T) {
	raw := ankiFixtureWithMedia(t, "stock.anki2.apkg",
		map[string][]byte{"0": nil},
		map[string]string{"0": "empty.txt"},
	)
	emptySha := sha256.Sum256(nil)
	wantSha := hex.EncodeToString(emptySha[:])

	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, db, "anki_empty_"+driver)[0]
			root := t.TempDir()

			report, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
				Target: PackageTargetNewDeck, MediaRoot: root, NewDeckName: "empty media " + driver,
			})
			if err != nil {
				t.Fatalf("ImportPackage() error = %v", err)
			}
			for _, missing := range report.MissingMedia {
				if missing == "empty.txt" {
					t.Fatalf("report lists the 0-byte entry as missing: %v", report.MissingMedia)
				}
			}

			names, err := NewMediaNameStore(db).ListByDeck(ctx, report.DeckID)
			if err != nil {
				t.Fatalf("list imported media names: %v", err)
			}
			var sha string
			for _, row := range names {
				if row.Name == "empty.txt" {
					sha = row.Sha256
				}
			}
			if sha == "" {
				t.Fatalf("imported media names = %+v, want empty.txt", names)
			}
			if sha != wantSha {
				t.Fatalf("empty.txt sha = %s, want %s", sha, wantSha)
			}
			media, err := NewMediaStore(db).BySha256(ctx, sha)
			if err != nil || media == nil {
				t.Fatalf("load empty media row: %v", err)
			}
			if media.Bytes != 0 {
				t.Fatalf("empty media row bytes = %d, want 0", media.Bytes)
			}
			info, err := os.Stat(filepath.Join(root, filepath.FromSlash(media.RelPath)))
			if err != nil {
				t.Fatalf("stat the imported empty file: %v", err)
			}
			if info.Size() != 0 {
				t.Fatalf("imported empty file = %d bytes, want 0", info.Size())
			}
		})
	}
}
