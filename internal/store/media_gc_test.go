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

// TestMediaGCSameNameInAnotherDeckDoesNotKeepOrphan is the acceptance for the deck scope of the note-type
// reference: media names are keyed by (deck_id, name), so a template reference in deck B must not keep
// deck A's already-unreferenced file that happens to carry the same name.
func TestMediaGCSameNameInAnotherDeckDoesNotKeepOrphan(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			if err := AutoMigrate(ctx, db); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			owner := seedUsers(t, db, "gc_collision_owner")[0]
			deckA := seedPresetDeck(t, db, owner)
			deckB := seedPresetDeck(t, db, owner)
			noteTypeB := stockNoteTypeID(t, db, deckB, "basic")

			const name = "_font.woff2"
			shas := map[string]string{"a": strings.Repeat("a", 64), "b": strings.Repeat("b", 64)}
			for _, sha := range shas {
				if err := db.Create(&Media{Sha256: sha, RelPath: sha[:2] + "/" + sha + ".woff2", Mime: "font/woff2", Bytes: 1, CreatedAt: now}).Error; err != nil {
					t.Fatalf("create media %s: %v", sha, err)
				}
			}
			for _, row := range []MediaName{
				{DeckID: deckA, Name: name, Sha256: shas["a"]},
				{DeckID: deckB, Name: name, Sha256: shas["b"]},
			} {
				if err := db.Create(&row).Error; err != nil {
					t.Fatalf("create media name %+v: %v", row, err)
				}
			}
			// Deck B's note type references the name; nothing references it in deck A.
			if err := db.Create(&MediaNoteType{NoteTypeID: noteTypeB, MediaName: name}).Error; err != nil {
				t.Fatalf("create media note type: %v", err)
			}

			marked, _, err := MarkOrphanedMedia(ctx, db, now)
			if err != nil {
				t.Fatalf("MarkOrphanedMedia() error = %v", err)
			}
			if marked != 1 {
				t.Fatalf("MarkOrphanedMedia() marked %d rows, want 1: another deck's same-name reference kept deck A's file alive", marked)
			}

			if err := db.Model(&Media{}).Where("sha256 = ?", shas["a"]).Update("orphaned_at", now.Add(-30*24*time.Hour)).Error; err != nil {
				t.Fatalf("age orphan mark: %v", err)
			}
			deleted, err := DeleteOrphanedMedia(ctx, db, now.Add(-7*24*time.Hour), 10)
			if err != nil {
				t.Fatalf("DeleteOrphanedMedia() error = %v", err)
			}
			if len(deleted) != 1 || deleted[0].Sha256 != shas["a"] {
				t.Fatalf("deleted = %+v, want only deck A's unreferenced file", deleted)
			}
			var keptB int64
			if err := db.Model(&Media{}).Where("sha256 = ?", shas["b"]).Count(&keptB).Error; err != nil {
				t.Fatalf("count deck B's media: %v", err)
			}
			if keptB != 1 {
				t.Fatalf("deck B's referenced media rows = %d, want 1", keptB)
			}
		})
	}
}
