package store

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// TestImportRenamesClashingMedia is the Anki-fidelity acceptance for a name clash: the target deck already has img.png
// pointing at different bytes, so the new bytes must be registered under img-<sha1>.png and every reference in the
// package (note fields and note-type template/CSS) must be rewritten to the new name. The old name keeps its old content.
func TestImportRenamesClashingMedia(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, db, "clash_owner")[0]
			deckID := seedPresetDeck(t, db, owner)
			root := t.TempDir()

			oldBody := append([]byte("\x89PNG\r\n\x1a\n"), []byte("old-image")...)
			oldMedia, err := NewMediaStore(db).SaveBytes(ctx, root, "image/png", oldBody, Ptr(owner))
			if err != nil {
				t.Fatalf("SaveBytes(old): %v", err)
			}
			if err := NewMediaNameStore(db).Upsert(ctx, deckID, "img.png", oldMedia.Sha256); err != nil {
				t.Fatalf("register old name: %v", err)
			}

			newBody := append([]byte("\x89PNG\r\n\x1a\n"), []byte("new-image")...)
			sum := sha256.Sum256(newBody)
			newSHA := hex.EncodeToString(sum[:])
			sha1Sum := sha1.Sum(newBody)
			wantName := "img-" + hex.EncodeToString(sha1Sum[:]) + ".png"

			raw := clashPackageZip(t, newSHA, newBody)
			target := "into_deck:" + strconv.FormatUint(deckID, 10)
			if _, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{Target: target, MediaRoot: root}); err != nil {
				t.Fatalf("ImportPackage: %v", err)
			}

			// The old name still resolves to the old bytes; the new bytes got the suffixed name.
			oldRow, err := NewMediaNameStore(db).ByName(ctx, deckID, "img.png")
			if err != nil {
				t.Fatalf("resolve old name: %v", err)
			}
			if oldRow.Sha256 != oldMedia.Sha256 {
				t.Fatalf("img.png = %s, want the old sha %s", oldRow.Sha256, oldMedia.Sha256)
			}
			newRow, err := NewMediaNameStore(db).ByName(ctx, deckID, wantName)
			if err != nil {
				t.Fatalf("resolve renamed name %q: %v", wantName, err)
			}
			if newRow.Sha256 != newSHA {
				t.Fatalf("%s = %s, want %s", wantName, newRow.Sha256, newSHA)
			}

			// The note field reference was rewritten.
			var notes []Note
			if err := db.Where("deck_id = ?", deckID).Find(&notes).Error; err != nil {
				t.Fatalf("load notes: %v", err)
			}
			fields, err := ParseFieldValues(notes[0].FieldsJSON)
			if err != nil {
				t.Fatalf("parse fields: %v", err)
			}
			if got := fields["back"]; got != `<img src="`+wantName+`">` {
				t.Fatalf("note field = %q, want the rewritten name %q", got, wantName)
			}

			// The note type's template and CSS references were rewritten too.
			var types []NoteType
			if err := db.Where("deck_id = ?", deckID).Find(&types).Error; err != nil {
				t.Fatalf("load note types: %v", err)
			}
			var clashType *NoteType
			for i := range types {
				if types[i].Name == "ClashType" {
					clashType = &types[i]
				}
			}
			if clashType == nil {
				t.Fatal("imported note type not found")
			}
			cfg, err := clashType.Config()
			if err != nil {
				t.Fatalf("decode note type: %v", err)
			}
			if got := cfg.CSS; got != ".card { background: url("+wantName+"); }" {
				t.Fatalf("note type CSS = %q, want the rewritten name", got)
			}
			if got := cfg.Templates[0].AFormat; got != `{{back}}<img src="`+wantName+`">` {
				t.Fatalf("note type AFormat = %q, want the rewritten name", got)
			}

			// Importing the same bytes again reuses the same renamed registration.
			if _, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{Target: target, MediaRoot: root}); err != nil {
				t.Fatalf("second ImportPackage: %v", err)
			}
			var count int64
			if err := db.Model(&MediaName{}).Where("deck_id = ? AND sha256 = ?", deckID, newSHA).Count(&count).Error; err != nil {
				t.Fatalf("count names for new sha: %v", err)
			}
			if count != 1 {
				t.Fatalf("names for the new sha = %d, want 1 (same bytes reuse the renamed name)", count)
			}
			if err := db.Model(&MediaName{}).Where("deck_id = ? AND sha256 = ? AND name = ?", deckID, newSHA, wantName).Count(&count).Error; err != nil {
				t.Fatalf("count renamed names: %v", err)
			}
			if count != 1 {
				t.Fatalf("renamed name rows = %d, want 1", count)
			}
		})
	}
}

// clashPackageZip builds a one-note package whose media is named img.png and referenced from both the note field and the
// note type's template/CSS.
func clashPackageZip(t *testing.T, sha string, body []byte) []byte {
	t.Helper()
	mediaPath := "media/" + sha + ".png"
	noteTypes := `[{"ref":"nt-1","name":"ClashType","config":{"kind":"normal","fields":[{"id":"front","name":"front"},{"id":"back","name":"back"}],"templates":[{"id":"t-forward","name":"Forward","qfmt":"{{front}}","afmt":"{{back}}<img src=\"img.png\">"}],"css":".card { background: url(img.png); }"}}]`
	files := map[string]string{
		"manifest.json":   fmt.Sprintf(`{"format_version":2,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"clash"},"include_progress":false,"include_media":true,"include_reviews":false,"counts":{"notes":1,"cards":0,"media":1}}`),
		"note_types.json": noteTypes,
		"notes.json":      `[{"note_type":"nt-1","fields":{"front":"q","back":"<img src=\"img.png\">"}}]`,
		"cards.json":      `[]`,
		"preset.json":     testPresetJSON,
		"media.json":      fmt.Sprintf(`{"%s":{"path":"%s","mime":"image/png","names":["img.png"]}}`, sha, mediaPath),
		mediaPath:         string(body),
	}
	return buildZip(t, files)
}

// TestAddHashSuffixToFileStem pins the Anki port: the same SHA-1 suffix Anki produces and the 120-byte cap.
func TestAddHashSuffixToFileStem(t *testing.T) {
	// Anki's own test vector: sha1("hello").
	const hello = "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"
	if got, want := addHashSuffixToFileStem("test.jpg", hello), "test-"+hello+".jpg"; got != want {
		t.Fatalf("addHashSuffixToFileStem = %q, want %q", got, want)
	}
	// A very long stem is truncated so the whole name stays within 120 bytes, extension capped at 10 bytes.
	long := strings.Repeat("x", 200) + ".verylongext"
	got := addHashSuffixToFileStem(long, hello)
	if len(got) > maxMediaFilenameLength {
		t.Fatalf("renamed name is %d bytes, want at most %d", len(got), maxMediaFilenameLength)
	}
	if !strings.HasSuffix(got, "-"+hello+".verylongex") {
		t.Fatalf("renamed name = %q, want the hash suffix and a 10-byte extension", got)
	}
}
