package store

import (
	"fmt"
	"math"

	"gorm.io/gorm"
)

// backfillReviewScheduledDays fills reviews.scheduled_days from interval_days for rows written before
// the snapshot existed.
//
// The scheduler stores a learning step's whole days with a floor, so floor(interval_days) is what those
// rows would have recorded; rounding would turn a 1.5-day step into 2. Rows without an interval stay
// NULL, and undo treats a NULL snapshot as 0.
//
// Rows are read in primary-key batches, so memory use is independent of the review count. The
// scheduled_days IS NULL predicate makes a second run a no-op.
func backfillReviewScheduledDays(tx *gorm.DB) error {
	if !tx.Migrator().HasColumn("reviews", "scheduled_days") {
		return nil
	}
	const batchSize = 500
	var last uint64
	for {
		var batch []Review
		if err := tx.Select("id", "interval_days").
			Where("id > ? AND scheduled_days IS NULL AND interval_days IS NOT NULL", last).
			Order("id ASC").Limit(batchSize).Find(&batch).Error; err != nil {
			return fmt.Errorf("backfill review scheduled days: scan reviews after id %d: %w", last, err)
		}
		if len(batch) == 0 {
			return nil
		}
		for _, r := range batch {
			last = r.ID
			days := 0
			if r.IntervalDays != nil {
				days = int(math.Floor(*r.IntervalDays))
			}
			if days < 0 {
				days = 0
			}
			if err := tx.Model(&Review{}).Where("id = ?", r.ID).
				UpdateColumn("scheduled_days", days).Error; err != nil {
				return fmt.Errorf("backfill review scheduled days: review %d: %w", r.ID, err)
			}
		}
	}
}
