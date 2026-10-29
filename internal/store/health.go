package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// DueQueueSize returns the length of the instance-wide due queue right now (the 'current due queue size' of the health page).
//
// It counts card_states rows, i.e. one due card in one user's queue: the same card due for two users counts twice, because
// this is 'queue length', not 'number of cards'. A row counts when store.WhereDue holds for it on a live, unsuspended card
// (LiveCardStates), the same predicates the review queue uses. Daily caps and paused decks are per-user study choices and
// do not shrink this operational number.
//
// now is passed in by the caller so tests can pin the time; comparisons are always done in UTC internally.
func DueQueueSize(ctx context.Context, db *gorm.DB, now time.Time) (int64, error) {
	var n int64
	if err := WhereDue(LiveCardStates(db.WithContext(ctx)), now.UTC()).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count due queue: %w", err)
	}
	return n, nil
}
