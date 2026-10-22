package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// The storage side of media reclamation: mark orphaned media, delete media rows orphaned past the grace period. File deletion lives in internal/media.
//
// 'Orphaned' = no row in media_notes points at these bytes. Mapping rows of soft-deleted notes are kept (a note can be restored),
// so media referenced by a soft-deleted note is not orphaned; when a deck is deleted its notes and mappings are hard-deleted together, and the media becomes orphaned.
//
// Why two steps instead of deleting as soon as something looks orphaned: removing an image while editing a note and adding it back later is common, and there is also a
// time gap between upload and card insertion. Recording 'since when it has been orphaned' and deleting only past the grace period avoids deleting by mistake.

// orphanCondition is the SQL condition for 'no note references it', shared by marking, clearing and deleting.
const orphanCondition = "NOT EXISTS (SELECT 1 FROM media_notes AS ref WHERE ref.media_sha = media.sha256)"

// MarkOrphanedMedia stamps orphaned_at = now on newly orphaned media and clears the mark on media that got referenced again.
// It returns the number of rows newly marked and cleared.
func MarkOrphanedMedia(ctx context.Context, db *gorm.DB, now time.Time) (marked, cleared int64, err error) {
	res := db.WithContext(ctx).Model(&Media{}).
		Where("orphaned_at IS NULL").Where(orphanCondition).
		Update("orphaned_at", now.UTC())
	if res.Error != nil {
		return 0, 0, fmt.Errorf("store: mark orphaned media: %w", res.Error)
	}
	marked = res.RowsAffected
	res = db.WithContext(ctx).Model(&Media{}).
		Where("orphaned_at IS NOT NULL").Where("NOT ("+orphanCondition+")").
		Update("orphaned_at", nil)
	if res.Error != nil {
		return marked, 0, fmt.Errorf("store: clear orphan marks: %w", res.Error)
	}
	return marked, res.RowsAffected, nil
}

// DeleteOrphanedMedia deletes media rows orphaned no later than before and still orphaned right now (at most limit rows),
// together with their media_uploaders rows, returning the deleted rows (callers delete files based on RelPath).
//
// The delete statement checks orphanhood again: if a note referenced it between selection and deletion, that row is not deleted.
func DeleteOrphanedMedia(ctx context.Context, db *gorm.DB, before time.Time, limit int) ([]Media, error) {
	var rows []Media
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("orphaned_at IS NOT NULL AND orphaned_at <= ?", before.UTC()).
			Where(orphanCondition).Order("sha256 ASC").Limit(limit).Find(&rows).Error; err != nil {
			return fmt.Errorf("store: select orphaned media: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		shas := make([]string, 0, len(rows))
		for _, m := range rows {
			shas = append(shas, m.Sha256)
		}
		if err := tx.Where("sha256 IN ?", shas).Where(orphanCondition).Delete(&Media{}).Error; err != nil {
			return fmt.Errorf("store: delete orphaned media: %w", err)
		}
		// only shas whose media rows were actually deleted are cleaned: rows kept because of a concurrent reference must also keep their uploader records.
		var kept []string
		if err := tx.Model(&Media{}).Where("sha256 IN ?", shas).Pluck("sha256", &kept).Error; err != nil {
			return fmt.Errorf("store: reload kept media: %w", err)
		}
		keptSet := make(map[string]bool, len(kept))
		for _, sha := range kept {
			keptSet[sha] = true
		}
		deleted := rows[:0]
		gone := make([]string, 0, len(rows))
		for _, m := range rows {
			if !keptSet[m.Sha256] {
				deleted = append(deleted, m)
				gone = append(gone, m.Sha256)
			}
		}
		rows = deleted
		if len(gone) == 0 {
			return nil
		}
		if err := tx.Where("media_sha IN ?", gone).Delete(&MediaUploader{}).Error; err != nil {
			return fmt.Errorf("store: delete uploaders of orphaned media: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}
