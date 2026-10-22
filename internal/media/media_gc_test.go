package media

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

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
		m, err := s.Save(ctx, bytes.NewReader(body), SaveOptions{DeclaredMime: "image/png", CreatedBy: &uploader})
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
