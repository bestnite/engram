package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Media writes from a deck package import accept every class: the type comes from the real bytes and, for text classes,
// the file name; the media.json declaration is informational. A package used to be rejected unless every file was on an
// allowlist, which made fonts, scripts and SVG impossible to import.

// mediaPackageZip builds a structurally valid .edeck: one note referencing media + media.json + the media bytes.
// ext is the in-package file extension; the sha256 is computed from body.
func mediaPackageZip(t *testing.T, declaredMime, ext string, body []byte) (raw []byte, sha string) {
	t.Helper()
	sum := sha256.Sum256(body)
	sha = hex.EncodeToString(sum[:])
	mediaPath := "media/" + sha + "." + ext
	files := map[string]string{
		"manifest.json":   fmt.Sprintf(`{"format_version":2,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"d"},"include_progress":false,"include_media":true,"include_reviews":false,"counts":{"notes":1,"cards":0,"media":1}}`),
		"note_types.json": testNoteTypesJSON(),
		"notes.json":      fmt.Sprintf(`[{"note_type":"nt-1","fields":{"front":"q","back":"%s"}}]`, mediaPath),
		"cards.json":      `[]`,
		"preset.json":     testPresetJSON,
		"media.json":      fmt.Sprintf(`{"%s":{"path":"%s","mime":"%s"}}`, sha, mediaPath, declaredMime),
		mediaPath:         string(body),
	}
	return buildZip(t, files), sha
}

// TestImportAcceptsEveryMediaClass imports one package per class and asserts the stored mime and the written file.
func TestImportAcceptsEveryMediaClass(t *testing.T) {
	cases := []struct {
		name     string
		declared string
		ext      string
		body     []byte
		wantMime string
	}{
		{"png", "image/png", "png", []byte("\x89PNG\r\n\x1a\nlegal-round-trip-payload"), "image/png"},
		{"svg", "image/svg+xml", "svg", []byte(`<svg xmlns="http://example.com"><script>alert(1)</script></svg>`), "image/svg+xml"},
		{"script", "text/javascript", "js", []byte("console.log('hello')"), "text/javascript"},
		{"font", "font/woff2", "woff2", []byte("wOF2\x00\x01\x00\x00fake-font"), "font/woff2"},
		{"unknown", "application/octet-stream", "dat", []byte("unrecognised bytes"), "application/octet-stream"},
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					raw, sha := mediaPackageZip(t, tc.declared, tc.ext, tc.body)
					importer := seedUsers(t, db, "pkg_mime_"+tc.name)[0]
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
					if row.Mime != tc.wantMime {
						t.Fatalf("stored mime = %q, want %q", row.Mime, tc.wantMime)
					}
					onDisk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(row.RelPath)))
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

// TestImportDryRunAcceptsMediaWritesNothing asserts dry_run classifies the media but writes neither a row nor a file.
func TestImportDryRunAcceptsMediaWritesNothing(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			raw, _ := mediaPackageZip(t, "text/javascript", "js", []byte("console.log(1)"))
			importer := seedUsers(t, db, "pkg_mime_dry")[0]
			root := t.TempDir()

			report, err := NewDeckStore(db).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{
				Target:    PackageTargetNewDeck,
				DryRun:    true,
				MediaRoot: root,
			})
			if err != nil {
				t.Fatalf("dry-run ImportPackage error = %v", err)
			}
			if report.NotesCreated != 1 || report.MediaNew != 1 {
				t.Fatalf("dry-run report notes=%d media=%d, want 1/1", report.NotesCreated, report.MediaNew)
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
