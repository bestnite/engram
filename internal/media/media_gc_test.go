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

// TestCollectGarbage 覆盖媒体回收的完整时间线（真库 + 真文件）：
//   - 被引用的媒体（映射行存在，含软删除 note 的映射）永不回收；
//   - 无人引用的媒体第一轮只被标记，宽限期内不删；
//   - 宽限期过后删行、删文件、删上传者记录；
//   - 标记后又被引用的媒体清除标记、不删；重新上传的媒体清除标记、宽限期重算。
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
	// 宽限期内：被标记的媒体又被引用 / 又被上传。
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

	// 重新上传的媒体在第二轮被重新标记（t1），所以要再过一个宽限期才删。
	var again store.Media
	if err := s.db.First(&again, "sha256 = ?", reUploaded.Sha256).Error; err != nil {
		t.Fatalf("reload re-uploaded media: %v", err)
	}
	if again.OrphanedAt == nil || !again.OrphanedAt.Equal(t1) {
		t.Errorf("re-uploaded media orphaned_at = %v, want %v", again.OrphanedAt, t1)
	}
}
