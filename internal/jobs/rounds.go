package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/worker"
)

// recordTimeout bounds the history write of one round. The write runs detached from the worker's context, so a
// round that finished just before shutdown is still recorded, and the bound keeps a stuck database from holding the
// worker past its next tick.
const recordTimeout = 10 * time.Second

// RoundRecorder returns the worker.RecordFunc that persists periodic rounds as finished jobs rows. A failed write is
// logged and dropped: the round already happened, and its outcome must not depend on the history table.
func RoundRecorder(st *Store, logger *slog.Logger) worker.RecordFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, round worker.Round) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
		defer cancel()
		if err := st.CreateRound(ctx, round); err != nil {
			logger.Error("record task round failed", "kind", round.Kind, "trigger", round.Trigger, "error", err)
		}
	}
}

// CreateRound writes one periodic round as a terminal jobs row. The row is never queued or running, so the
// optimizer's single-concurrency check (Active) and the restart recovery (RecoverStale) never see it.
func (s *Store) CreateRound(ctx context.Context, round worker.Round) error {
	summary := round.Summary
	if summary == nil {
		summary = worker.Summary{}
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("encode round summary: %w", err)
	}
	started := round.StartedAt.UTC()
	finished := round.FinishedAt.UTC()
	job := store.Job{
		Kind:       round.Kind,
		Trigger:    store.Ptr(round.Trigger),
		Status:     StatusSucceeded,
		ResultJSON: store.Ptr(string(encoded)),
		CreatedAt:  started,
		StartedAt:  &started,
		FinishedAt: &finished,
	}
	if round.Err != nil {
		job.Status = StatusFailed
		job.Error = store.Ptr(round.Err.Error())
	}
	if err := s.db.WithContext(ctx).Create(&job).Error; err != nil {
		return fmt.Errorf("create round job: %w", err)
	}
	return nil
}

// DeleteStale deletes periodic history rows (trigger auto or manual) that finished before the given time, and returns
// the number deleted. Optimizer rows (trigger user, or NULL from before the column existed) are never matched: they
// record a user's parameter change, not a timer round.
func (s *Store) DeleteStale(ctx context.Context, before time.Time) (int64, error) {
	res := s.db.WithContext(ctx).
		Where("trigger IN ? AND finished_at < ?", []string{worker.TriggerAuto, worker.TriggerManual}, before.UTC()).
		Delete(&store.Job{})
	if res.Error != nil {
		return 0, fmt.Errorf("delete stale task rounds: %w", res.Error)
	}
	return res.RowsAffected, nil
}
