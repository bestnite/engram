package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"gorm.io/gorm"
)

// TestNoteWriteNormalisesMediaNames is the acceptance for the editor's absolute references: a note written with
// /media/<sha>, <a href="/media/<sha>"> and [sound:<sha>] stores deck-scoped names, registers them in media_names,
// builds media_notes, and keeps the bytes through garbage collection. External URLs and non-hex paths stay untouched.
func TestNoteWriteNormalisesMediaNames(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, db, "note_media_owner")[0]
			deckID := seedPresetDeck(t, db, owner)
			root := t.TempDir()
			ms := NewMediaStore(db)

			image, err := ms.SaveBytes(ctx, root, "image/png", append([]byte("\x89PNG\r\n\x1a\n"), []byte("note-image")...), Ptr(owner))
			if err != nil {
				t.Fatalf("SaveBytes(image): %v", err)
			}
			audio, err := ms.SaveBytes(ctx, root, "audio/mpeg", []byte("ID3note-audio"), Ptr(owner))
			if err != nil {
				t.Fatalf("SaveBytes(audio): %v", err)
			}

			front := fmt.Sprintf(`<img src="/media/%s"><a href="/media/%s">img</a><img src="/media/%s.png">`+
				`<a href="https://example.com/media/%s">external</a> /media/not-hex`,
				image.Sha256, image.Sha256, image.Sha256, image.Sha256)
			back := fmt.Sprintf(`[sound:%s]`, audio.Sha256)
			n := Note{DeckID: deckID, NoteTypeID: Ptr(stockNoteTypeID(t, db, deckID, "basic")), TagsJSON: "[]", CreatedBy: Ptr(owner)}
			if _, err := NewNoteStore(db).Create(ctx, &n, map[string]any{"front": front, "back": back}); err != nil {
				t.Fatalf("Create(note): %v", err)
			}

			stored := loadFieldValues(t, db, n.ID)
			imageName := image.Sha256 + ".png"
			audioName := audio.Sha256 + ".mp3"
			wantFront := fmt.Sprintf(`<img src="%s"><a href="%s">img</a><img src="%s">`+
				`<a href="https://example.com/media/%s">external</a> /media/not-hex`,
				imageName, imageName, imageName, image.Sha256)
			if stored["front"] != wantFront {
				t.Fatalf("front = %q, want %q", stored["front"], wantFront)
			}
			if stored["back"] != "[sound:"+audioName+"]" {
				t.Fatalf("back = %q, want [sound:%s]", stored["back"], audioName)
			}

			names := NewMediaNameStore(db)
			for name, sha := range map[string]string{imageName: image.Sha256, audioName: audio.Sha256} {
				row, err := names.ByName(ctx, deckID, name)
				if err != nil {
					t.Fatalf("media_names %q: %v", name, err)
				}
				if row.Sha256 != sha {
					t.Fatalf("media_names %q = %s, want %s", name, row.Sha256, sha)
				}
			}
			assertMediaNotes(t, db, n.ID, image.Sha256, audio.Sha256)

			// Past the grace period: both references are live, so neither row nor file may be reclaimed.
			now := time.Now().UTC()
			if _, _, err := MarkOrphanedMedia(ctx, db, now); err != nil {
				t.Fatalf("MarkOrphanedMedia: %v", err)
			}
			deleted, err := DeleteOrphanedMedia(ctx, db, now.Add(48*time.Hour), 10)
			if err != nil {
				t.Fatalf("DeleteOrphanedMedia: %v", err)
			}
			for _, m := range deleted {
				if m.Sha256 == image.Sha256 || m.Sha256 == audio.Sha256 {
					t.Fatalf("garbage collection removed referenced media %s", m.Sha256)
				}
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(image.RelPath))); err != nil {
				t.Fatalf("referenced image file missing: %v", err)
			}

			// Update is covered by the same path: new absolute references are normalised too.
			if _, err := NewNoteStore(db).Update(ctx, &n, map[string]any{
				"front": fmt.Sprintf(`<a href="/media/%s">audio link</a>`, audio.Sha256),
				"back":  fmt.Sprintf(`[sound:%s]`, image.Sha256),
			}); err != nil {
				t.Fatalf("Update(note): %v", err)
			}
			stored = loadFieldValues(t, db, n.ID)
			if stored["front"] != fmt.Sprintf(`<a href="%s">audio link</a>`, audioName) {
				t.Fatalf("updated front = %q, want the normalised audio name", stored["front"])
			}
			if stored["back"] != "[sound:"+imageName+"]" {
				t.Fatalf("updated back = %q, want [sound:%s]", stored["back"], imageName)
			}
			assertMediaNotes(t, db, n.ID, image.Sha256, audio.Sha256)
		})
	}
}

// loadFieldValues reloads a note and decodes its stored field map.
func loadFieldValues(t *testing.T, db *gorm.DB, noteID uint64) map[string]string {
	t.Helper()
	var n Note
	if err := db.First(&n, "id = ?", noteID).Error; err != nil {
		t.Fatalf("load note %d: %v", noteID, err)
	}
	values, err := ParseFieldValues(n.FieldsJSON)
	if err != nil {
		t.Fatalf("parse note %d fields: %v", noteID, err)
	}
	return values
}

// assertMediaNotes asserts the note's derived media_notes rows are exactly the given shas.
func assertMediaNotes(t *testing.T, db *gorm.DB, noteID uint64, shas ...string) {
	t.Helper()
	var rows []MediaNote
	if err := db.Where("note_id = ?", noteID).Order("media_sha ASC").Find(&rows).Error; err != nil {
		t.Fatalf("load media_notes: %v", err)
	}
	want := append([]string(nil), shas...)
	sort.Strings(want)
	if len(rows) != len(want) {
		t.Fatalf("media_notes rows = %d (%v), want %v", len(rows), rows, want)
	}
	for i := range want {
		if rows[i].MediaSha != want[i] {
			t.Fatalf("media_notes[%d] = %s, want %s", i, rows[i].MediaSha, want[i])
		}
	}
}
