package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/open-spaced-repetition/go-fsrs/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.nite07.com/nite/engram/internal/store"
)

// ErrNothingToUndo means the card has no review log, so Undo has nothing to roll back.
var ErrNothingToUndo = errors.New("schedule: no review to undo")

// UndoInput are the inputs for one undo; tx has the same semantics as in Submit (the caller provides the transaction boundary).
type UndoInput struct {
	CardID uint64
	UserID uint64
	// ExpectedVersion is the card_states.version the caller read (usually the version it just got back after submitting a grade).
	// Undo treats it as the target: a mismatch returns ErrVersionConflict and deletes no log — so a replayed request,
	// a double-open window or a stale request never wrongly deletes another grade.
	ExpectedVersion int
	Scheduler       *Scheduler
	// A zero Now falls back to the current time and is only used for the audit timestamp.
	Now time.Time
}

// Rollback undoes the card's last grade: it uses the last reviews row as the log to drive fsrs.Rollback and restore the state,
// deletes that log row, and writes the restored state back to card_states.
//
// Why the "previous log" is needed: reviews only records the post-grade scheduling result (interval_days/stability/difficulty),
// and the pre-grade FSRS snapshot has no column of its own. The post-grade result of the previous log is exactly this one's pre-grade
// snapshot, so it is used to rebuild ReviewLog's pre-grade fields; that way due_at and interval are restored exactly to the values before this grade.
// step_index (remaining learning steps) is restored exactly from the step_index_before of the log being undone; when that column is
// NULL on an old row, it falls back to the fsrs.Rollback result (which zeroes step_index).
// The pre-grade due date prefers the due_before snapshot of the log being undone: burying only changes due_at and writes no log, so the previous log
// cannot derive the post-bury due date; when that column is NULL on an old row, it falls back to the derivation from the previous log.
//
// Execution order (race-proof under PostgreSQL): lock the state row and check the version first, then read the logs. The opposite order (read logs, then lock the row)
// would let another transaction commit a new grade between the two steps: the undo then reads the new state while still holding the old logs, and the write guard uses
// the new version, so the mismatch between log and state goes undetected.
func Rollback(ctx context.Context, tx *gorm.DB, in UndoInput) (store.CardState, error) {
	if tx == nil {
		return store.CardState{}, errors.New("schedule: undo: transaction is required")
	}
	if in.Scheduler == nil {
		return store.CardState{}, errors.New("schedule: undo: scheduler is required")
	}
	if in.CardID == 0 || in.UserID == 0 {
		return store.CardState{}, errors.New("schedule: undo: card id and user id are required")
	}

	// 1. Lock the state row first; without a state row there is no grade to undo.
	cur, err := loadStateForUpdate(ctx, tx, in.CardID, in.UserID)
	if err != nil {
		return store.CardState{}, err
	}
	if cur == nil {
		return store.CardState{}, fmt.Errorf("%w: card %d user %d has no state row", ErrNothingToUndo, in.CardID, in.UserID)
	}

	// 2. Version guard: replayed, stale or double-open requests are stopped here and no log is deleted.
	if cur.Version != in.ExpectedVersion {
		return store.CardState{}, fmt.Errorf("%w: card %d user %d holds version %d, expected %d",
			ErrVersionConflict, in.CardID, in.UserID, cur.Version, in.ExpectedVersion)
	}

	// 3. Read the logs only after locking the state row: no concurrent grade can be committed now, so logs and state must be consistent.
	// Take the two most recent logs: last is the one being undone, prev provides the pre-grade snapshot (may not exist).
	var logs []store.Review
	if err := tx.WithContext(ctx).Where("card_id = ? AND user_id = ?", in.CardID, in.UserID).
		Order("id DESC").Limit(2).Find(&logs).Error; err != nil {
		return store.CardState{}, fmt.Errorf("schedule: undo: load review log: %w", err)
	}
	if len(logs) == 0 {
		return store.CardState{}, fmt.Errorf("%w: card %d user %d", ErrNothingToUndo, in.CardID, in.UserID)
	}
	last := logs[0]
	var prev *store.Review
	if len(logs) > 1 {
		prev = &logs[1]
	}

	stateBefore, err := ParseState(parseStateInt(last.StateBefore))
	if err != nil {
		return store.CardState{}, err
	}

	card, err := cardFromState(cur)
	if err != nil {
		return store.CardState{}, err
	}
	log, due, lastReview, scheduledDays := rebuildReviewLog(last, prev)
	// The pre-grade due date prefers the snapshot: burying only changes due_at and writes no log, so the previous log cannot derive it.
	if last.DueBefore != nil {
		d := last.DueBefore.UTC()
		due = &d
	}
	restored, err := in.Scheduler.fsrs.Rollback(card, log)
	if err != nil {
		return store.CardState{}, fmt.Errorf("schedule: undo: rollback card %d: %w", in.CardID, err)
	}

	// Assemble the restored state row. state/stability/difficulty/reps/lapses/step_index come from the Rollback result
	// (step_index's snapshot has already been rebuilt by rebuildReviewLog from step_index_before);
	// due/last_review/scheduled_days are rebuilt from the previous log, restoring the due date and interval exactly.
	next := store.CardState{
		CardID:        in.CardID,
		UserID:        in.UserID,
		State:         stateBefore.String(),
		DueAt:         due,
		LastReviewAt:  lastReview,
		ScheduledDays: scheduledDays,
		StepIndex:     restored.RemainingSteps,
		Reps:          int(restored.Reps),
		Lapses:        int(restored.Lapses),
		Version:       cur.Version + 1,
	}
	if stateBefore != StateNew {
		stability := restored.Stability
		difficulty := restored.Difficulty
		next.Stability = &stability
		next.Difficulty = &difficulty
	}
	if next.LastReviewAt != nil {
		next.ElapsedDays = elapsedDaysSince(next.LastReviewAt, nowOr(in.Now))
	}
	written, err := upsertState(ctx, tx, &next, cur.Version)
	if err != nil {
		return store.CardState{}, err
	}
	// A failed guard means the state row was concurrently modified after this read: do not do a partial restore, return the conflict sentinel.
	if !written {
		return store.CardState{}, fmt.Errorf("%w: card %d user %d changed concurrently while undoing",
			ErrVersionConflict, in.CardID, in.UserID)
	}

	// The one exception to reviews being append-only is Undo: delete the row being undone and write an audit note.
	if err := tx.WithContext(ctx).Delete(&store.Review{}, last.ID).Error; err != nil {
		return store.CardState{}, fmt.Errorf("schedule: undo: delete review %d: %w", last.ID, err)
	}
	if err := writeUndoAudit(ctx, tx, in, last); err != nil {
		return store.CardState{}, err
	}
	return next, nil
}

// rebuildReviewLog rebuilds the ReviewLog that fsrs.Rollback needs, from the log being undone and the log before it,
// and returns the pre-grade due date, the last review time and the scheduled days.
func rebuildReviewLog(last store.Review, prev *store.Review) (fsrs.ReviewLog, *time.Time, *time.Time, int) {
	log := fsrs.ReviewLog{
		Rating: fsrs.Rating(last.Rating),
		State:  fsrs.State(last.StateBefore),
		Review: last.ReviewedAt.UTC(),
	}
	// Learning-step cursor: only reviews.step_index_before knows the remaining steps before the grade.
	// Old rows have NULL there; keep 0 — i.e. the old behaviour (step_index zeroed after Undo).
	if last.StepIndexBefore != nil {
		log.RemainingSteps = *last.StepIndexBefore
	}
	if prev == nil {
		// The first grade is being undone: restore to a brand-new card (no due date, no last review).
		return log, nil, nil, 0
	}
	log.Due = prev.ReviewedAt.UTC()
	if prev.Stability != nil {
		log.Stability = *prev.Stability
	}
	if prev.Difficulty != nil {
		log.Difficulty = *prev.Difficulty
	}
	due := prev.ReviewedAt.UTC()
	scheduled := 0
	if prev.IntervalDays != nil {
		scheduled = int(math.Round(*prev.IntervalDays))
		due = addDays(prev.ReviewedAt.UTC(), *prev.IntervalDays)
	}
	log.ScheduledDays = uint64(maxInt(scheduled, 0))
	lastReview := prev.ReviewedAt.UTC()
	return log, &due, &lastReview, scheduled
}

// addDays adds d days to t; rounded to milliseconds because interval_days is REAL and sub-millisecond precision is meaningless.
func addDays(t time.Time, d float64) time.Time {
	return t.Add(time.Duration(math.Round(d*24*60*60*1000)) * time.Millisecond)
}

// parseStateInt turns the integer state from the log into a string so ParseState reuses the same validation.
func parseStateInt(v int) string {
	switch State(v) {
	case StateNew:
		return "new"
	case StateLearning:
		return "learning"
	case StateReview:
		return "review"
	case StateRelearning:
		return "relearning"
	default:
		return fmt.Sprintf("invalid-%d", v)
	}
}

// writeUndoAudit writes one audit_log row in the same transaction, recording which grade was undone.
func writeUndoAudit(ctx context.Context, tx *gorm.DB, in UndoInput, last store.Review) error {
	detail, err := json.Marshal(map[string]any{
		"review_id":    last.ID,
		"rating":       last.Rating,
		"state_before": last.StateBefore,
		"reviewed_at":  last.ReviewedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("schedule: undo: marshal audit detail: %w", err)
	}
	detailStr := string(detail)
	targetType := "card"
	userID := in.UserID
	cardID := in.CardID
	row := store.AuditLog{
		UserID:     &userID,
		Action:     store.ActionReviewUndo,
		TargetType: &targetType,
		TargetID:   &cardID,
		DetailJSON: &detailStr,
		CreatedAt:  nowOr(in.Now).UTC(),
	}
	if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("schedule: undo: write audit log: %w", err)
	}
	return nil
}

// SuspendInput are the inputs for one suspend / unsuspend; tx has the same semantics as in Submit (the caller provides the transaction boundary).
type SuspendInput struct {
	CardID uint64
	UserID uint64
	// Suspended true means suspend, false means unsuspend.
	Suspended bool
	// A zero Now falls back to the current time and is used as the suspend instant.
	Now time.Time
}

// SetSuspended suspends or unsuspends a card, taking effect only for UserID itself (card_states.suspended_at).
//
// Suspension is each user's own study decision: a member suspending a card in a shared deck does not affect other members or the owner.
// No progress figure is touched; a new card with no state row gets one created first (state=new), the same approach as Bury.
// version is bumped: in-flight grade submissions hold the old version and end in a conflict, rather than quietly reviewing a card that was just suspended.
func SetSuspended(ctx context.Context, tx *gorm.DB, in SuspendInput) (store.CardState, error) {
	if tx == nil {
		return store.CardState{}, errors.New("schedule: suspend: transaction is required")
	}
	if in.CardID == 0 || in.UserID == 0 {
		return store.CardState{}, errors.New("schedule: suspend: card id and user id are required")
	}
	cur, err := loadStateForUpdate(ctx, tx, in.CardID, in.UserID)
	if err != nil {
		return store.CardState{}, err
	}
	row := store.CardState{CardID: in.CardID, UserID: in.UserID, State: StateNew.String()}
	version := 0
	if cur != nil {
		row = *cur
		version = cur.Version
	}
	if in.Suspended {
		at := nowOr(in.Now).UTC()
		row.SuspendedAt = &at
	} else {
		row.SuspendedAt = nil
	}
	row.Version = version + 1
	if cur == nil {
		// When a concurrent first write (grade, bury or another suspend) created the row first, this insert fails: treat it as a conflict.
		res := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if res.Error != nil {
			return store.CardState{}, fmt.Errorf("schedule: suspend: create state for card %d: %w", in.CardID, res.Error)
		}
		if res.RowsAffected == 0 {
			return store.CardState{}, fmt.Errorf("%w: card %d user %d gained a state row concurrently while suspending",
				ErrVersionConflict, in.CardID, in.UserID)
		}
		return row, nil
	}
	res := tx.WithContext(ctx).Model(&store.CardState{}).
		Where("card_id = ? AND user_id = ? AND version = ?", in.CardID, in.UserID, version).
		Updates(map[string]any{"suspended_at": row.SuspendedAt, "version": row.Version})
	if res.Error != nil {
		return store.CardState{}, fmt.Errorf("schedule: suspend card %d: %w", in.CardID, res.Error)
	}
	if res.RowsAffected == 0 {
		return store.CardState{}, fmt.Errorf("%w: card %d user %d changed concurrently while suspending",
			ErrVersionConflict, in.CardID, in.UserID)
	}
	return row, nil
}

// BuryInput are the inputs for one "bury for today".
type BuryInput struct {
	CardID uint64
	UserID uint64
	Now    time.Time
	// Location/Timezone/DayCutoffHour determine the boundary of "tomorrow".
	Location      *time.Location
	Timezone      string
	DayCutoffHour *int
}

// Bury buries a card until the next review day begins: it pushes the card's (that user's) due_at to the next cutoff,
// making it disappear from today's queue without touching any progress figure (cutoff rules, the b key bury on the review page).
//
// A new card with no card_states row gets one created first (state=new, due_at=next cutoff); queue construction does not
// take "new card whose due_at is not reached yet", so new cards can be buried too.
func Bury(ctx context.Context, tx *gorm.DB, in BuryInput) (store.CardState, error) {
	if tx == nil {
		return store.CardState{}, errors.New("schedule: bury: transaction is required")
	}
	if in.CardID == 0 || in.UserID == 0 {
		return store.CardState{}, errors.New("schedule: bury: card id and user id are required")
	}
	now := nowOr(in.Now)
	loc := queueLocation(in.Location, in.Timezone)
	due := nextReviewDayStart(now, loc, store.ResolveCutoff(in.DayCutoffHour))

	cur, err := loadStateForUpdate(ctx, tx, in.CardID, in.UserID)
	if err != nil {
		return store.CardState{}, err
	}
	row := store.CardState{
		CardID: in.CardID,
		UserID: in.UserID,
		State:  StateNew.String(),
		DueAt:  &due,
	}
	version := 0
	if cur != nil {
		// Keep all existing progress, only postpone the due date; bump version to void in-flight submissions.
		row = *cur
		row.DueAt = &due
		version = cur.Version
	}
	row.Version = version + 1
	written, err := upsertState(ctx, tx, &row, version)
	if err != nil {
		return store.CardState{}, err
	}
	// A failed guard means the state row was concurrently modified after this read (concurrent first grade or another bury).
	if !written {
		return store.CardState{}, fmt.Errorf("%w: card %d user %d changed concurrently while burying",
			ErrVersionConflict, in.CardID, in.UserID)
	}
	return row, nil
}

// nextReviewDayStart returns the start of "the next review day" (local time = date + 1, hour = cutoff), converted to UTC.
// For example cutoff 4, local 2026-10-02 12:00 → 2026-10-03 04:00 local.
func nextReviewDayStart(now time.Time, loc *time.Location, cutoffHour int) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	day := local.Add(-time.Duration(cutoffHour) * time.Hour)
	next := time.Date(day.Year(), day.Month(), day.Day(), cutoffHour, 0, 0, 0, loc).AddDate(0, 0, 1)
	return next.UTC()
}

func nowOr(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}
