package media

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// TestCollectGarbage covers the full media reclamation timeline (real DB + real files):
//   - referenced media (a mapping row exists, including mappings of soft-deleted notes) is never reclaimed;
//   - unreferenced media is only flagged in the first round and not deleted within the grace period;
//   - after the grace period the row, the file and the uploader record are deleted;
//   - media referenced again after being flagged has the flag cleared and is not deleted; re-uploaded media has the flag cleared and the grace period restarted.
func TestCollectGarbage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	uploader := uint64(7)
	save := func(body []byte) *store.Media {
		t.Helper()
		m, err := s.Save(ctx, bytes.NewReader(body), SaveOptions{Name: "a.png", CreatedBy: &uploader})
		if err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		return m
	}
	png := func(tag string) []byte { return append(pngBytes(), []byte(tag)...) }
	referenced := save(png("referenced"))
	orphan := save(png("orphan"))
	reReferenced := save(png("re-referenced"))
	reUploaded := save(png("re-uploaded"))
	ref := func(sha string, noteID uint64) {
		t.Helper()
		if err := s.db.Create(&store.MediaNote{MediaSha: sha, NoteID: noteID, CreatedAt: time.Now().UTC()}).Error; err != nil {
			t.Fatalf("create mapping: %v", err)
		}
	}
	ref(referenced.Sha256, 1)

	const grace = 7 * 24 * time.Hour
	t0 := time.Now().UTC()
	if n, err := s.CollectGarbage(ctx, t0, grace); err != nil || n != 0 {
		t.Fatalf("first sweep = %d, %v; want 0 removed (marking only)", n, err)
	}
	// Within the grace period: the flagged media is referenced again / uploaded again.
	ref(reReferenced.Sha256, 2)
	save(png("re-uploaded"))

	t1 := t0.Add(grace + time.Hour)
	n, err := s.CollectGarbage(ctx, t1, grace)
	if err != nil {
		t.Fatalf("second sweep error = %v", err)
	}
	if n != 1 {
		t.Fatalf("second sweep removed %d, want 1 (only the orphan)", n)
	}

	cases := []struct {
		name string
		m    *store.Media
		gone bool
	}{
		{"referenced media is kept", referenced, false},
		{"orphan past the grace period is removed", orphan, true},
		{"media referenced again is kept", reReferenced, false},
		{"media uploaded again is kept", reUploaded, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rows int64
			if err := s.db.Model(&store.Media{}).Where("sha256 = ?", tc.m.Sha256).Count(&rows).Error; err != nil {
				t.Fatalf("count media: %v", err)
			}
			var uploaders int64
			if err := s.db.Model(&store.MediaUploader{}).Where("media_sha = ?", tc.m.Sha256).Count(&uploaders).Error; err != nil {
				t.Fatalf("count uploaders: %v", err)
			}
			abs, _ := s.absPath(tc.m.RelPath)
			_, statErr := os.Stat(abs)
			fileGone := errors.Is(statErr, os.ErrNotExist)
			if tc.gone != (rows == 0) || tc.gone != (uploaders == 0) || tc.gone != fileGone {
				t.Errorf("row=%d uploaders=%d fileGone=%v, want gone=%v", rows, uploaders, fileGone, tc.gone)
			}
		})
	}

	// The re-uploaded media is flagged again in the second round (t1), so another grace period must pass before it is deleted.
	var again store.Media
	if err := s.db.First(&again, "sha256 = ?", reUploaded.Sha256).Error; err != nil {
		t.Fatalf("reload re-uploaded media: %v", err)
	}
	if again.OrphanedAt == nil || !again.OrphanedAt.Equal(t1) {
		t.Errorf("re-uploaded media orphaned_at = %v, want %v", again.OrphanedAt, t1)
	}
}

// TestCollectGarbageKeepsNoteTypeMedia is the acceptance case for a CSS-only `_`-prefixed asset: the font is registered
// in media_names and referenced only by a note type's CSS, with no notes at all, so it must survive garbage collection.
// Removing the note type (which deletes its media_note_types rows) lets a later sweep reclaim it.
func TestCollectGarbageKeepsNoteTypeMedia(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	font, err := s.Save(ctx, bytes.NewReader([]byte("wOF2\x00\x01\x00\x00fake-font")), SaveOptions{Name: "_font.woff2"})
	if err != nil {
		t.Fatalf("Save(font) error = %v", err)
	}
	if err := store.NewMediaNameStore(s.db).Upsert(ctx, 1, "_font.woff2", font.Sha256); err != nil {
		t.Fatalf("register media name: %v", err)
	}
	nt := store.NoteType{DeckID: 1, Name: "Fonts"}
	cfg := notetype.Config{
		Kind: notetype.KindNormal,
		Fields: []notetype.Field{
			{ID: "f-front", Name: "Front"},
			{ID: "f-back", Name: "Back"},
		},
		Templates: []notetype.Template{
			{ID: "t-card", Name: "Card 1", QFormat: "{{Front}}", AFormat: "{{Back}}"},
		},
		CSS:         `@font-face { font-family: x; src: url(_font.woff2); }`,
		SortFieldID: "f-front",
	}
	if err := store.NewNoteTypeStore(s.db).Create(ctx, &nt, cfg); err != nil {
		t.Fatalf("create note type: %v", err)
	}

	now := time.Now().UTC()
	if _, err := s.CollectGarbage(ctx, now, time.Minute); err != nil {
		t.Fatalf("first CollectGarbage: %v", err)
	}
	// Past the grace period: still referenced by the note type, so the row and file stay.
	removed, err := s.CollectGarbage(ctx, now.Add(2*time.Minute), time.Minute)
	if err != nil {
		t.Fatalf("second CollectGarbage: %v", err)
	}
	if removed != 0 {
		t.Fatalf("CollectGarbage removed %d, want 0 while the note type references the font", removed)
	}
	if _, err := store.NewMediaStore(s.db).BySha256(ctx, font.Sha256); err != nil {
		t.Fatalf("load media after GC: %v", err)
	}

	// Deleting the type drops its references; the next sweep marks the font, and the one after the grace period reclaims it.
	if err := store.NewNoteTypeStore(s.db).Delete(ctx, nt.ID); err != nil {
		t.Fatalf("delete note type: %v", err)
	}
	if removed, err = s.CollectGarbage(ctx, now.Add(4*time.Minute), time.Minute); err != nil {
		t.Fatalf("third CollectGarbage: %v", err)
	}
	if removed != 0 {
		t.Fatalf("third CollectGarbage removed %d, want 0 (marking only)", removed)
	}
	removed, err = s.CollectGarbage(ctx, now.Add(6*time.Minute), time.Minute)
	if err != nil {
		t.Fatalf("fourth CollectGarbage: %v", err)
	}
	if removed != 1 {
		t.Fatalf("CollectGarbage removed %d after the note type was deleted, want 1", removed)
	}
}
