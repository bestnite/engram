package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 媒体回收的存储侧：标记孤立媒体、删除孤立超过宽限期的媒体行。文件删除在 internal/media。
//
// 「孤立」= media_notes 里没有任何行指向这份字节。软删除 note 的映射行保留（note 可以恢复），
// 所以被软删除 note 引用的媒体不算孤立；卡组被删除时 note 与映射一起硬删除，媒体随之孤立。
//
// 为什么分两步而不是看到孤立就删：编辑 note 时先删掉图片、稍后再改回来是常见操作；上传与
// 插入卡片之间也有时间差。先记下「从何时起孤立」，只删孤立超过宽限期的，才不会误删。

// orphanCondition 是「没有任何 note 引用」的 SQL 条件，标记、清除与删除共用这一处定义。
const orphanCondition = "NOT EXISTS (SELECT 1 FROM media_notes AS ref WHERE ref.media_sha = media.sha256)"

// MarkOrphanedMedia 给新出现的孤立媒体记下 orphaned_at = now，并清掉重新被引用的媒体的标记。
// 返回新标记与清除的行数。
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

// DeleteOrphanedMedia 删除孤立时刻不晚于 before、且此刻仍然孤立的媒体行（最多 limit 行），
// 连同它们的 media_uploaders 行，返回被删除的行（调用方据 RelPath 删文件）。
//
// 删除语句里再判一次孤立：选出之后、删除之前若有 note 引用了它，这一行就不删。
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
		// 只清理确实删掉了媒体行的 sha：被并发引用而保留的那几行，上传者记录也要留着。
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
