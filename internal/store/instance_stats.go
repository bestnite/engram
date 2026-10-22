package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// InstanceStats holds the instance-level counts the admin panel overview page needs.
// All of them are cheap COUNT(*): the overview page is a landing page and must not slow down any section page.
type InstanceStats struct {
	// Users is the total number of accounts; ActiveUsers is those whose status is active (disabled accounts do not count).
	Users       int64
	ActiveUsers int64
	// Decks is the total number of decks; Notes/Cards both exclude soft-deleted rows.
	Decks int64
	Notes int64
	Cards int64
	// DueNow is the instance-level due queue size (same rule as the health page).
	DueNow int64
	// JobsRunning counts queued and running jobs; JobsFailed counts jobs in the failed state.
	JobsRunning int64
	JobsFailed  int64
}

// InstanceSummary collects the counts the overview page needs. Any single query failure fails the whole thing:
// the overview page would rather show an error than treat a failure as 0 (which would make an admin think the instance is empty).
func InstanceSummary(ctx context.Context, db *gorm.DB, now time.Time) (InstanceStats, error) {
	var s InstanceStats
	count := func(model any, query string, args ...any) (int64, error) {
		var n int64
		q := db.WithContext(ctx).Model(model)
		if query != "" {
			q = q.Where(query, args...)
		}
		return n, q.Count(&n).Error
	}

	var err error
	if s.Users, err = count(&User{}, ""); err != nil {
		return InstanceStats{}, fmt.Errorf("instance stats: users: %w", err)
	}
	if s.ActiveUsers, err = count(&User{}, "status = ?", StatusActive); err != nil {
		return InstanceStats{}, fmt.Errorf("instance stats: active users: %w", err)
	}
	if s.Decks, err = count(&Deck{}, ""); err != nil {
		return InstanceStats{}, fmt.Errorf("instance stats: decks: %w", err)
	}
	if s.Notes, err = count(&Note{}, ""); err != nil {
		return InstanceStats{}, fmt.Errorf("instance stats: notes: %w", err)
	}
	if s.Cards, err = count(&Card{}, ""); err != nil {
		return InstanceStats{}, fmt.Errorf("instance stats: cards: %w", err)
	}
	if s.DueNow, err = DueQueueSize(ctx, db, now); err != nil {
		return InstanceStats{}, fmt.Errorf("instance stats: due queue: %w", err)
	}
	// job status values are defined by the Job model (queued | running | succeeded | failed).
	if s.JobsRunning, err = count(&Job{}, "status IN ?", []string{"queued", "running"}); err != nil {
		return InstanceStats{}, fmt.Errorf("instance stats: running jobs: %w", err)
	}
	if s.JobsFailed, err = count(&Job{}, "status = ?", "failed"); err != nil {
		return InstanceStats{}, fmt.Errorf("instance stats: failed jobs: %w", err)
	}
	return s, nil
}
