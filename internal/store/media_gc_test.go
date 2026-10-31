package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestMediaGCMarksAndDeletesOnlyStaleOrphans verifies the marking and deleting SQL on both databases:
// media with a mapping is never marked; orphaned media is marked first, and deletion only targets rows orphaned earlier than the cutoff, taking their uploader records with them.
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
			// push old's orphan time far into the past and keep young at now.
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

// TestClearOrphanedMediaBatchesLargeSets verifies that clearing orphan marks works for more shas than one statement may
// bind (SQLite allows 32766 parameters, PostgreSQL 65535), that it clears only the listed marked rows, and that the count
// it returns excludes listed rows that carried no mark.
func TestClearOrphanedMediaBatchesLargeSets(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			if err := AutoMigrate(ctx, db); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			orphaned := now.Add(-8 * 24 * time.Hour)
			sha := func(i int) string { return fmt.Sprintf("%064x", i) }

			const marked, unmarked, absent = 1200, 10, 68800
			rows := make([]Media, 0, marked+unmarked+1)
			for i := 0; i < marked+unmarked+1; i++ {
				row := Media{Sha256: sha(i), RelPath: "x/" + sha(i) + ".png", Mime: "image/png", Bytes: 1, CreatedAt: now}
				// Every row but the unmarked ones carries a mark; the last row is marked but not listed.
				if i < marked || i == marked+unmarked {
					row.OrphanedAt = &orphaned
				}
				rows = append(rows, row)
			}
			if err := db.CreateInBatches(rows, 200).Error; err != nil {
				t.Fatalf("seed media: %v", err)
			}
			listed := make([]string, 0, marked+unmarked+absent)
			for i := 0; i < marked+unmarked; i++ {
				listed = append(listed, sha(i))
			}
			for i := 0; i < absent; i++ {
				listed = append(listed, sha(1_000_000+i))
			}

			cleared, err := clearOrphanedMedia(ctx, db, listed)
			if err != nil {
				t.Fatalf("clearOrphanedMedia(%d shas) error = %v", len(listed), err)
			}
			if cleared != marked {
				t.Fatalf("clearOrphanedMedia() cleared %d rows, want %d", cleared, marked)
			}
			var stillMarked []string
			if err := db.Model(&Media{}).Where("orphaned_at IS NOT NULL").Pluck("sha256", &stillMarked).Error; err != nil {
				t.Fatalf("read marks: %v", err)
			}
			if len(stillMarked) != 1 || stillMarked[0] != sha(marked+unmarked) {
				t.Fatalf("rows still marked = %v, want only the unlisted row %s", stillMarked, sha(marked+unmarked))
			}
		})
	}
}
