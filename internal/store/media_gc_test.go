package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestMediaGCMarksAndDeletesOnlyStaleOrphans 在两库上验证标记与删除的 SQL：
// 有映射的媒体从不被标记；孤立媒体先标记，删除只针对孤立时刻早于界限的行，并带走上传者记录。
func TestMediaGCMarksAndDeletesOnlyStaleOrphans(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			if err := AutoMigrate(ctx, db); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			shas := map[string]string{"kept": strings.Repeat("1", 64), "old": strings.Repeat("2", 64), "young": strings.Repeat("3", 64)}
			for name, sha := range shas {
				if err := db.Create(&Media{Sha256: sha, RelPath: sha[:2] + "/" + sha + ".png", Mime: "image/png", Bytes: 1, CreatedAt: now}).Error; err != nil {
					t.Fatalf("create media %s: %v", name, err)
				}
				if err := RecordMediaUploader(ctx, db, sha, 9); err != nil {
					t.Fatalf("record uploader %s: %v", name, err)
				}
			}
			if err := db.Create(&MediaNote{MediaSha: shas["kept"], NoteID: 1, CreatedAt: now}).Error; err != nil {
				t.Fatalf("create mapping: %v", err)
			}

			marked, cleared, err := MarkOrphanedMedia(ctx, db, now)
			if err != nil || marked != 2 || cleared != 0 {
				t.Fatalf("MarkOrphanedMedia() = %d, %d, %v; want 2, 0, nil", marked, cleared, err)
			}
			// 把 old 的孤立时刻推到很早以前，young 保持 now。
			if err := db.Model(&Media{}).Where("sha256 = ?", shas["old"]).Update("orphaned_at", now.Add(-30*24*time.Hour)).Error; err != nil {
				t.Fatalf("age orphan mark: %v", err)
			}

			deleted, err := DeleteOrphanedMedia(ctx, db, now.Add(-7*24*time.Hour), 10)
			if err != nil {
				t.Fatalf("DeleteOrphanedMedia() error = %v", err)
			}
			if len(deleted) != 1 || deleted[0].Sha256 != shas["old"] {
				t.Fatalf("deleted = %+v, want only the old orphan", deleted)
			}
			for name, sha := range shas {
				var media, uploaders int64
				db.Model(&Media{}).Where("sha256 = ?", sha).Count(&media)
				db.Model(&MediaUploader{}).Where("media_sha = ?", sha).Count(&uploaders)
				want := int64(1)
				if name == "old" {
					want = 0
				}
				if media != want || uploaders != want {
					t.Errorf("%s: media=%d uploaders=%d, want %d each", name, media, uploaders, want)
				}
			}
		})
	}
}
