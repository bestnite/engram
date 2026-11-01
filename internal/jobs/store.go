package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// Store wraps the jobs table; the model reuses internal/store.Job to avoid duplicate definitions (AGENTS.md §2.4:
// one model serves the business layer, GORM and JSON at the same time).
type Store struct {
	db *gorm.DB
}

// NewStore builds the job store.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// CreateQueued writes a queued job row and returns it.
func (s *Store) CreateQueued(ctx context.Context, kind string, targetID *uint64, at time.Time) (*store.Job, error) {
	job := store.Job{
		Kind:      kind,
		TargetID:  targetID,
		Status:    StatusQueued,
		CreatedAt: at.UTC(),
	}
	if err := s.db.WithContext(ctx).Create(&job).Error; err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}
	return &job, nil
}

// ByID reads a job by primary key.
func (s *Store) ByID(ctx context.Context, id uint64) (*store.Job, error) {
	return s.byID(s.db.WithContext(ctx), id)
}

// byID reads a job through the caller's handle, so a terminal write inside a transaction sees its own snapshot.
func (s *Store) byID(db *gorm.DB, id uint64) (*store.Job, error) {
	var job store.Job
	if err := db.First(&job, "id = ?", id).Error; err != nil {
		return nil, fmt.Errorf("load job %d: %w", id, err)
	}
	return &job, nil
}

// ByPublicID reads a job by its public id; an empty string counts as not found, and never matches an old row whose public_id is empty.
func (s *Store) ByPublicID(ctx context.Context, publicID string) (*store.Job, error) {
	if publicID == "" {
		return nil, fmt.Errorf("load job %q: %w", publicID, gorm.ErrRecordNotFound)
	}
	var job store.Job
	if err := s.db.WithContext(ctx).First(&job, "public_id = ?", publicID).Error; err != nil {
		return nil, fmt.Errorf("load job %q: %w", publicID, err)
	}
	return &job, nil
}

// Active returns the currently unfinished job (queued or running); (nil, nil) when there is none.
// The single-concurrency decision depends on it: as long as an unfinished job exists, a new enqueue must be refused.
func (s *Store) Active(ctx context.Context) (*store.Job, error) {
	var job store.Job
	err := s.db.WithContext(ctx).
		Where("status IN ?", []string{StatusQueued, StatusRunning}).
		Order("id DESC").First(&job).Error
	if err != nil {
		if store.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("find active job: %w", err)
	}
	return &job, nil
}

// RecoverStale marks the unfinished jobs left over from the previous process as failed across the board (reclaiming running, and additionally queued):
// it writes finished_at and the corresponding failure reason but does not clear log_tail, preserving the scene for diagnosis. It returns the total number of reclaimed rows.
//
// The two kinds of leftover need different reasons and must be written separately: a running subprocess really did start (reason = interrupted by restart),
// whereas a queued job only entered an in-memory queue that died with the process and never started (reason = job never started).
func (s *Store) RecoverStale(ctx context.Context, at time.Time, runningReason, queuedReason string) (int64, error) {
	steps := []struct {
		status string
		reason string
	}{
		{status: StatusRunning, reason: runningReason},
		{status: StatusQueued, reason: queuedReason},
	}
	var total int64
	for _, step := range steps {
		res := s.db.WithContext(ctx).Model(&store.Job{}).
			Where("status = ?", step.status).
			Updates(map[string]any{
				"status":      StatusFailed,
				"error":       step.reason,
				"finished_at": at.UTC(),
			})
		if res.Error != nil {
			return total, fmt.Errorf("recover %s jobs: %w", step.status, res.Error)
		}
		total += res.RowsAffected
	}
	return total, nil
}

// List returns a page of jobs and the total count in descending id order, for the admin panel to page through.
// It must be limited: the jobs table keeps growing with every optimization, and rendering everything at once would drag the page down.
func (s *Store) List(ctx context.Context, limit, offset int) ([]store.Job, int64, error) {
	if limit <= 0 {
		limit = 20
	}
	var total int64
	if err := s.db.WithContext(ctx).Model(&store.Job{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count jobs: %w", err)
	}
	var jobs []store.Job
	if err := s.db.WithContext(ctx).
		Order("id DESC").
		Limit(limit).
		Offset(offset).
		Find(&jobs).Error; err != nil {
		return nil, 0, fmt.Errorf("list jobs: %w", err)
	}
	return jobs, total, nil
}

// ErrJobNotQueued reports that a job had already left the queued state when a transition
// written only for a queued row ran. The caller must not start the job in that case.
var ErrJobNotQueued = errors.New("job is no longer queued")

// MarkRunning sets the job to running and records started_at. The update is conditional on
// the row still being queued: a job cancelled while the worker was reading it is already
// failed, and must not be flipped back to running and executed.
func (s *Store) MarkRunning(ctx context.Context, id uint64, at time.Time) error {
	res := s.db.WithContext(ctx).Model(&store.Job{}).
		Where("id = ? AND status = ?", id, StatusQueued).
		Updates(map[string]any{
			"status":     StatusRunning,
			"started_at": at.UTC(),
		})
	if res.Error != nil {
		return fmt.Errorf("update job %d: %w", id, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("update job %d: %w", id, ErrJobNotQueued)
	}
	return nil
}

// FinishQueuedFailed marks a queued job failed, conditional on the row still being queued.
// A job that moved to running between the caller's read and this write is left to the
// running cancellation path instead of being reported as cancelled here.
func (s *Store) FinishQueuedFailed(ctx context.Context, id uint64, at time.Time, msg string) error {
	res := s.db.WithContext(ctx).Model(&store.Job{}).
		Where("id = ? AND status = ?", id, StatusQueued).
		Updates(map[string]any{
			"status":      StatusFailed,
			"finished_at": at.UTC(),
			"error":       msg,
		})
	if res.Error != nil {
		return fmt.Errorf("update job %d: %w", id, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("update job %d: %w", id, ErrJobNotQueued)
	}
	return nil
}

// SetStage updates stage-level progress.
func (s *Store) SetStage(ctx context.Context, id uint64, stage string) error {
	return s.update(ctx, id, map[string]any{"stage": stage})
}

// FinishSucceeded sets the job to succeeded: it writes finished_at, the log tail and the result, and clears error.
func (s *Store) FinishSucceeded(ctx context.Context, id uint64, at time.Time, resultJSON, tail string) error {
	return s.finishSucceeded(ctx, s.db, id, at, resultJSON, tail)
}

// finishSucceeded is FinishSucceeded on the caller's handle, so the optimize write-back commits the preset and
// this terminal row together.
func (s *Store) finishSucceeded(ctx context.Context, db *gorm.DB, id uint64, at time.Time, resultJSON, tail string) error {
	updates := map[string]any{
		"status":      StatusSucceeded,
		"finished_at": at.UTC(),
		"log_tail":    nullableString(tail),
		"result_json": nullableString(resultJSON),
		"error":       nil,
	}
	return s.updateDB(ctx, db, id, updates)
}

// FinishFailed sets the job to failed: it writes finished_at, the failure reason and the log tail.
func (s *Store) FinishFailed(ctx context.Context, id uint64, at time.Time, msg, tail string) error {
	updates := map[string]any{
		"status":      StatusFailed,
		"finished_at": at.UTC(),
		"error":       msg,
		"log_tail":    nullableString(tail),
	}
	return s.update(ctx, id, updates)
}

// update updates several columns by id; a missing row returns an error, so state is never silently lost.
func (s *Store) update(ctx context.Context, id uint64, updates map[string]any) error {
	return s.updateDB(ctx, s.db, id, updates)
}

// updateDB is update on the caller's handle, for writes that must commit with another table's write.
func (s *Store) updateDB(ctx context.Context, db *gorm.DB, id uint64, updates map[string]any) error {
	res := db.WithContext(ctx).Model(&store.Job{}).Where("id = ?", id).Updates(updates)
	if res.Error != nil {
		return fmt.Errorf("update job %d: %w", id, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("update job %d: %w", id, gorm.ErrRecordNotFound)
	}
	return nil
}

// nullableString turns an empty string into nil, so nullable TEXT columns store NULL instead of an empty string.
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
