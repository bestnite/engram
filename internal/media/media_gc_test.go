package media

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	if _, err := store.NewNoteTypeStore(s.db).Delete(ctx, nt.ID, store.NoteTypeDeleteOptions{}); err != nil {
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

// TestCollectGarbageKeepsBatchAfterFailedRemoval covers a filesystem error during one removal. The sweep must attempt
// every file of the batch, reclaim the ones it can, and keep the row and uploader record of the file it could not
// remove, so the next sweep retries that file instead of leaking it with nothing pointing at it.
func TestCollectGarbageKeepsBatchAfterFailedRemoval(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	uploader := uint64(7)
	type saved struct {
		row *store.Media
	}
	files := make([]saved, 0, 3)
	for _, tag := range []string{"first", "second", "third"} {
		body := append(pngBytes(), []byte(tag)...)
		row, err := s.Save(ctx, bytes.NewReader(body), SaveOptions{Name: "a.png", CreatedBy: &uploader})
		if err != nil {
			t.Fatalf("Save(%s) error = %v", tag, err)
		}
		files = append(files, saved{row: row})
	}
	// Break the file of the row whose sha sorts first: a collector that stops at the first failure cannot have
	// reclaimed the other two before reaching it.
	sort.Slice(files, func(i, j int) bool { return files[i].row.Sha256 < files[j].row.Sha256 })
	failing := files[0]
	failPath, err := s.absPath(failing.row.RelPath)
	if err != nil {
		t.Fatalf("absPath: %v", err)
	}
	if err := os.Remove(failPath); err != nil {
		t.Fatalf("remove file to make room for a failing path: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(failPath, "keep"), 0o755); err != nil {
		t.Fatalf("create non-empty directory at the file path: %v", err)
	}

	const grace = time.Hour
	t0 := time.Now().UTC()
	if n, err := s.CollectGarbage(ctx, t0, grace); err != nil || n != 0 {
		t.Fatalf("marking sweep = %d, %v; want 0 removed", n, err)
	}

	removed, err := s.CollectGarbage(ctx, t0.Add(grace+time.Minute), grace)
	if err == nil || !strings.Contains(err.Error(), failing.row.Sha256) {
		t.Fatalf("sweep error = %v, want the failed removal of %s reported", err, failing.row.Sha256)
	}
	if want := int64(len(files) - 1); removed != want {
		t.Fatalf("sweep removed %d, want %d: the rest of the batch must still be reclaimed", removed, want)
	}

	for i, f := range files {
		var rows, uploaders int64
		if err := s.db.Model(&store.Media{}).Where("sha256 = ?", f.row.Sha256).Count(&rows).Error; err != nil {
			t.Fatalf("count media %s: %v", f.row.Sha256, err)
		}
		if err := s.db.Model(&store.MediaUploader{}).Where("media_sha = ?", f.row.Sha256).Count(&uploaders).Error; err != nil {
			t.Fatalf("count uploaders %s: %v", f.row.Sha256, err)
		}
		abs, _ := s.absPath(f.row.RelPath)
		var fileGone bool
		if _, err := os.Stat(abs); errors.Is(err, os.ErrNotExist) {
			fileGone = true
		}
		if i == 0 {
			if rows != 1 || uploaders != 1 || fileGone {
				t.Errorf("failed removal: row=%d uploaders=%d fileGone=%v, want the row, its uploader and the file kept", rows, uploaders, fileGone)
			}
			var kept store.Media
			if err := s.db.First(&kept, "sha256 = ?", f.row.Sha256).Error; err != nil {
				t.Fatalf("reload kept media: %v", err)
			}
			if kept.OrphanedAt == nil || !kept.OrphanedAt.Equal(t0) {
				t.Errorf("kept media orphaned_at = %v, want the mark %v so the next sweep selects it", kept.OrphanedAt, t0)
			}
			continue
		}
		if rows != 0 || uploaders != 0 || !fileGone {
			t.Errorf("reclaimed media: row=%d uploaders=%d fileGone=%v, want all gone", rows, uploaders, fileGone)
		}
	}

	// Once the path can be removed, the next sweep retries the kept row and reclaims it.
	if err := os.RemoveAll(failPath); err != nil {
		t.Fatalf("clear the failing path: %v", err)
	}
	removed, err = s.CollectGarbage(ctx, t0.Add(2*(grace+time.Minute)), grace)
	if err != nil {
		t.Fatalf("retry sweep error = %v", err)
	}
	if removed != 1 {
		t.Fatalf("retry sweep removed %d, want 1", removed)
	}
	var rows, uploaders int64
	s.db.Model(&store.Media{}).Where("sha256 = ?", failing.row.Sha256).Count(&rows)
	s.db.Model(&store.MediaUploader{}).Where("media_sha = ?", failing.row.Sha256).Count(&uploaders)
	if rows != 0 || uploaders != 0 {
		t.Errorf("after the retry: row=%d uploaders=%d, want both gone", rows, uploaders)
	}
}
