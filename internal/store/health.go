package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// DueQueueSize returns the length of the instance-wide due queue right now (the 'current due queue size' of the health page).
//
// Rules (hard-coded; the test cross-checks with equivalent standalone SQL):
//   - it counts card_states rows, i.e. one due card in one user's queue; the same card due for two users
//     counts twice — this is 'queue length', not 'number of cards'.
//   - only rows that are scheduled and due_at <= now count, and never a state='new' row: burying a new card schedules it,
//     but the queue still takes it as a new card under new_per_day, not as a due one.
//   - cards that are soft-deleted or suspended, and cards whose note is soft-deleted, are excluded, matching the schedule queue's visibility rules.
//
// now is passed in by the caller so tests can pin the time; comparisons are always done in UTC internally.
func DueQueueSize(ctx context.Context, db *gorm.DB, now time.Time) (int64, error) {
	var n int64
	err := db.WithContext(ctx).Table("card_states AS cs").
		Joins("JOIN cards AS c ON c.id = cs.card_id AND c.deleted_at IS NULL AND cs.suspended_at IS NULL").
		Joins("JOIN notes AS n ON n.id = c.note_id AND n.deleted_at IS NULL").
		Where("cs.state <> ? AND cs.due_at IS NOT NULL AND cs.due_at <= ?", "new", now.UTC()).
		Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("count due queue: %w", err)
	}
	return n, nil
}
