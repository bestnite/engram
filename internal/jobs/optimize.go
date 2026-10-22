package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// This file is the server-side gate and the result write-back for parameter optimization in this package.
// The weight fitting itself belongs to the Rust adapter; this only handles "first judge whether there is enough data, then create the job" and
// "write the fit report the adapter produced into the job row's result_json".

// ErrInsufficientReviews is the sentinel error for failing the review-count threshold; callers can test it with errors.Is
// and use ThresholdError to get the exact shortfall for the page hint (showing "N more to go" when insufficient).
var ErrInsufficientReviews = errors.New("not enough reviews to optimize")

// ThresholdError is the detailed result of a threshold rejection: it names the preset, the count so far, the threshold, and the shortfall.
type ThresholdError struct {
	PresetID  uint64
	Reviews   int64
	Min       int
	Shortfall int64
}

// Error outputs the English failure reason and puts the shortfall in the message (AGENTS.md §2.1: logs and errors in English).
func (e *ThresholdError) Error() string {
	return fmt.Sprintf("%v: preset %d has %d reviews, needs %d, short by %d",
		ErrInsufficientReviews, e.PresetID, e.Reviews, e.Min, e.Shortfall)
}

// Is makes errors.Is(err, ErrInsufficientReviews) succeed while keeping the concrete shortfall field.
func (e *ThresholdError) Is(target error) bool { return target == ErrInsufficientReviews }

// EnqueueOptimize is the only enqueue entry point for optimization jobs: it first judges eligibility against the threshold, then reuses the single-concurrency Enqueue.
// With too few reviews it creates no job and enqueues nothing, returning ThresholdError directly so the layer above can show the shortfall to the user.
// ownerUserID is the user the preset belongs to, and thus the owner of the review log; the threshold counts only reviews on decks attached to that preset.
func (r *Runner) EnqueueOptimize(ctx context.Context, ownerUserID, presetID uint64) (*store.Job, error) {
	gate, err := store.GateOptimize(ctx, r.db, ownerUserID, presetID)
	if err != nil {
		return nil, fmt.Errorf("check optimize threshold: %w", err)
	}
	if !gate.Eligible {
		return nil, &ThresholdError{
			PresetID:  presetID,
			Reviews:   gate.Reviews,
			Min:       gate.MinReviews,
			Shortfall: gate.Shortfall,
		}
	}
	target := presetID
	return r.Enqueue(ctx, KindOptimize, &target)
}

// FinishOptimize writes the fit report the adapter produced into the job row's result_json, marks the job succeeded,
// and writes the 21-dimension weights back to the job's target preset (the preset page polls for the real result).
// It can also be called explicitly with no adapter present (for example a test's placeholder path).
//
// Write-back order: write the preset first, then persist the job state. If the preset write-back fails, this function returns an error and the caller marks
// the job failed — avoiding a half-done state where "the job succeeded but the weights never took effect".
func (r *Runner) FinishOptimize(ctx context.Context, jobID uint64, result store.OptimizeResult, tail string) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode optimize result: %w", err)
	}
	if err := r.applyOptimizedWeights(ctx, jobID, result); err != nil {
		return err
	}
	return r.store.FinishSucceeded(ctx, jobID, r.now(), string(raw), tail)
}

// applyOptimizedWeights writes the optimized weights back to the job's target preset. When the job has no target preset, or the target preset
// has been deleted, it skips silently (there is nowhere to write and a retry is pointless); only a genuine write failure returns an error.
func (r *Runner) applyOptimizedWeights(ctx context.Context, jobID uint64, result store.OptimizeResult) error {
	job, err := r.store.ByID(ctx, jobID)
	if err != nil {
		return fmt.Errorf("load job %d: %w", jobID, err)
	}
	if job.Kind != KindOptimize || job.TargetID == nil || *job.TargetID == 0 {
		return nil
	}
	presets := store.NewPresetStore(r.db)
	preset, err := presets.ByID(ctx, *job.TargetID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			r.logger.Warn("optimize target preset no longer exists; skipping weight write-back",
				"job_id", jobID, "preset_id", *job.TargetID)
			return nil
		}
		return fmt.Errorf("load preset %d: %w", *job.TargetID, err)
	}

	weightsJSON, err := json.Marshal(result.Weights)
	if err != nil {
		return fmt.Errorf("encode optimized weights: %w", err)
	}
	encoded := string(weightsJSON)
	optimizedAt := result.OptimizedAt
	if optimizedAt.IsZero() {
		optimizedAt = r.now()
	}
	reviewCount := int(result.ReviewsUsed)
	preset.WeightsJSON = &encoded
	preset.WeightsOptimizedAt = &optimizedAt
	preset.WeightsReviewCount = &reviewCount
	if err := presets.Update(ctx, preset.OwnerUserID, preset); err != nil {
		return fmt.Errorf("store optimized weights on preset %d: %w", *job.TargetID, err)
	}
	return nil
}
