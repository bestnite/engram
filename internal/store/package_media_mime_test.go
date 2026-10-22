package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Media writes from a deck package import must follow the same rules as ordinary uploads -- type from the real bytes, allowlist gate, and
// cross-check with the media.json declaration. Before the fix these cases were all red: SaveBytesTracked trusted the mime declared in the package,
// so a package with mime=text/html was stored as is and came back as the Content-Type verbatim (same-origin stored XSS).

// htmlMediaBytes is content a browser would execute as script/HTML.
func htmlMediaBytes() []byte {
	return []byte("<html><body><script>alert(document.domain)</script></body></html>")
}

// mediaPackageZip builds a structurally valid .edeck: one note referencing media + media.json + the media bytes.
// declaredMime goes into media.json; body is the real bytes of the media entry. The sha256 is computed from body.
func mediaPackageZip(t *testing.T, declaredMime string, body []byte) (raw []byte, sha string) {
	t.Helper()
	sum := sha256.Sum256(body)
	sha = hex.EncodeToString(sum[:])
	mediaPath := "media/" + sha + ".png"
	files := map[string]string{
		"manifest.json": fmt.Sprintf(`{"format_version":1,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"d"},"include_progress":false,"include_media":true,"include_reviews":false,"counts":{"notes":1,"cards":0,"media":1}}`),
		"notes.json":    fmt.Sprintf(`[{"kind":"basic","fields":{"front":"q","back":"%s"}}]`, mediaPath),
		"cards.json":    `[]`,
		"preset.json":   `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`,
		"media.json":    fmt.Sprintf(`{"%s":{"path":"%s","mime":"%s"}}`, sha, mediaPath, declaredMime),
		mediaPath:       string(body),
	}
	return buildZip(t, files), sha
}

// TestImportRejectsUnsafeMedia asserts the import chain determines the type from the real bytes before writing to disk:
// media whose type is outside the allowlist, or disagrees with the media.json declaration, rejects the whole package and lists the offending media one by one.
func TestImportRejectsUnsafeMedia(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nfake-png-body")
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`)
	cases := []struct {
		name     string
		declared string
		body     []byte
	}{
		{"declared text/html, bytes html", "text/html", htmlMediaBytes()},
		{"declared image/svg+xml, bytes svg", "image/svg+xml", svg},
		{"declared image/png, bytes html", "image/png", htmlMediaBytes()},
		{"declared image/gif, bytes png (mismatch)", "image/gif", png},
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					raw, sha := mediaPackageZip(t, tc.declared, tc.body)
					importer := seedUsers(t, db, "pkg_mime_"+sanitizeName(tc.name))[0]
					root := t.TempDir()

					_, err := NewDeckStore(db).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{
						Target:    PackageTargetNewDeck,
						MediaRoot: root,
					})
					var pe *PackageError
					if !errors.As(err, &pe) || pe.Code != CodePackageUnsafeMedia {
						t.Fatalf("ImportPackage error = %v, want PackageError code %s", err, CodePackageUnsafeMedia)
					}
					if len(pe.Entries) == 0 || !entryContains(pe.Entries, sha) {
						t.Fatalf("entries = %v, want one naming media/%s", pe.Entries, sha)
					}
					// The rejection happens before the disk write: no media row in the database, no file on disk.
					if row, err := NewMediaStore(db).BySha256(context.Background(), sha); err != nil {
						t.Fatalf("BySha256: %v", err)
					} else if row != nil {
						t.Fatalf("rejected import created media metadata %+v", row)
					}
					assertDirEmpty(t, root)
				})
			}
		})
	}
}

// TestImportDryRunRejectsUnsafeMediaWritesNothing asserts an illegal media package is rejected under dry_run too, with zero writes.
func TestImportDryRunRejectsUnsafeMediaWritesNothing(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			raw, _ := mediaPackageZip(t, "text/html", htmlMediaBytes())
			importer := seedUsers(t, db, "pkg_mime_dry")[0]
			root := t.TempDir()

			_, err := NewDeckStore(db).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{
				Target:    PackageTargetNewDeck,
				DryRun:    true,
				MediaRoot: root,
			})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageUnsafeMedia {
				t.Fatalf("dry-run ImportPackage error = %v, want PackageError code %s", err, CodePackageUnsafeMedia)
			}
			var decks, notes, media int64
			db.Model(&Deck{}).Count(&decks)
			db.Model(&Note{}).Count(&notes)
			db.Model(&Media{}).Count(&media)
			if decks != 0 || notes != 0 || media != 0 {
				t.Fatalf("dry run wrote to the database: decks=%d notes=%d media=%d", decks, notes, media)
			}
			assertDirEmpty(t, root)
		})
	}
}

// TestImportAllowsWhitelistedMediaRoundTrip is the positive case: legal PNG and audio still import and round-trip,
// proving the new validation does not reject the normal path along with it.
func TestImportAllowsWhitelistedMediaRoundTrip(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nlegal-round-trip-payload")
	ogg := []byte("OggS\x00\x02legal-audio-payload")
	cases := []struct {
		name string
		body []byte
		mime string
		ext  string
	}{
		{"png", png, "image/png", "png"},
		{"audio", ogg, "audio/ogg", "ogg"},
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					raw, sha := mediaPackageZip(t, tc.mime, tc.body)
					importer := seedUsers(t, db, "pkg_mime_ok_"+tc.name)[0]
					root := t.TempDir()

					report, err := NewDeckStore(db).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{
						Target:    PackageTargetNewDeck,
						MediaRoot: root,
					})
					if err != nil {
						t.Fatalf("ImportPackage: %v", err)
					}
					if report.MediaNew != 1 || report.NotesCreated != 1 {
						t.Fatalf("report media_new=%d notes_created=%d, want 1/1", report.MediaNew, report.NotesCreated)
					}
					row, err := NewMediaStore(db).BySha256(context.Background(), sha)
					if err != nil || row == nil {
						t.Fatalf("load imported media: row=%+v err=%v", row, err)
					}
					if row.Mime != tc.mime {
						t.Fatalf("stored mime = %q, want %q", row.Mime, tc.mime)
					}
					onDisk, err := os.ReadFile(mediaAbsPath(root, row.RelPath))
					if err != nil {
						t.Fatalf("read imported bytes: %v", err)
					}
					if !bytes.Equal(onDisk, tc.body) {
						t.Fatalf("imported bytes differ from source")
					}
				})
			}
		})
	}
}

// sanitizeName turns spaces/slashes in a subtest name into underscores, for use as a unique username suffix.
func sanitizeName(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// mediaAbsPath joins a media row's relative path into an absolute disk path (built the same way as SaveBytesTracked builds it).
func mediaAbsPath(root, rel string) string {
	return filepath.Join(root, filepath.FromSlash(rel))
}

// assertDirEmpty asserts the directory holds no files, proving the failure path wrote nothing to disk.
func assertDirEmpty(t *testing.T, root string) {
	t.Helper()
	var files int
	if err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files++
		}
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if files != 0 {
		t.Fatalf("media root %s holds %d file(s), want 0", root, files)
	}
}
