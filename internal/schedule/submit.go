package schedule

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.nite07.com/nite/engram/internal/reviewday"
	"git.nite07.com/nite/engram/internal/store"
)

// ErrVersionConflict means the expected_version held by the caller does not match the one in the DB: a client replay,
// a double-open window, or a grade submitted twice. The caller should map it to HTTP 409.
//
// It is the store's optimistic-lock sentinel, aliased rather than redefined: the scheduler and the note type store
// report the same condition, so callers keep one errors.Is target.
var ErrVersionConflict = store.ErrVersionConflict

// Legal values of grade_source. typed/llm cover answer-based question types and future LLM grading.
const (
	GradeSourceSelf  = "self"
	GradeSourceTyped = "typed"
	GradeSourceLLM   = "llm"
)

// SubmitInput are the inputs for one grade submission.
//
// The transaction boundary is provided by the caller: tx must be a transaction the caller opened (or a bare handle, in which case a single transaction
// is not guaranteed). Submit only reads and writes card_states / reviews through it, and never Begins/Commits itself. This lets a grade submission share a
// transaction with a broader outer operation (AGENTS.md §2.4).
type SubmitInput struct {
	CardID uint64
	UserID uint64
	// Rating is the self-grade, 1–4; out of range is reported as an English error.
	Rating Rating
	// ExpectedVersion is the card_states.version the caller read; a mismatch returns ErrVersionConflict.
	ExpectedVersion int
	// ElapsedMS is the user's answering time (milliseconds), nullable; written to reviews.elapsed_ms.
	ElapsedMS *int
	// An empty GradeSource is treated as self; only self/typed/llm are accepted.
	GradeSource string
	// GradeDetailJSON is the raw grading detail, nullable (the answer written down for answer-based question types or self-graded short answers, or used by future LLM grading).
	GradeDetailJSON *string
	// Scheduler computes the new state, required.
	Scheduler *Scheduler
	// A zero Now falls back to the current time; both the due date and the review day are based on it.
	Now time.Time
	// Location/Timezone/DayCutoffHour determine how review_day is split.
	Location      *time.Location
	Timezone      string
	DayCutoffHour *int
}

// SubmitResult returns the result after submission: the new state row and the written review id.
type SubmitResult struct {
	// State is the new card_states row after it is stored.
	State store.CardState
	// ReviewID is the reviews.id written by this call.
	ReviewID uint64
}

// Submit submits one grade within the given tx: it reads and locks the current state, validates expected_version,
// computes the new state, UPSERTs card_states and INSERTs reviews.
//
// Every write goes through the same tx: when the caller rolls back, the state update and the review row disappear together,
// so "no reviews row is left behind after a transaction failure" holds. When the same expected_version is submitted again,
// the first call already bumped version, so the second returns ErrVersionConflict at the version check without writing any row.
func Submit(ctx context.Context, tx *gorm.DB, in SubmitInput) (SubmitResult, error) {
	if tx == nil {
		return SubmitResult{}, errors.New("schedule: submit review: transaction is required")
	}
	if in.Scheduler == nil {
		return SubmitResult{}, errors.New("schedule: submit review: scheduler is required")
	}
	if in.CardID == 0 || in.UserID == 0 {
		return SubmitResult{}, errors.New("schedule: submit review: card id and user id are required")
	}
	if !in.Rating.Valid() {
		return SubmitResult{}, fmt.Errorf("schedule: submit review: invalid rating %d", int(in.Rating))
	}
	source := in.GradeSource
	if source == "" {
		source = GradeSourceSelf
	}
	switch source {
	case GradeSourceSelf, GradeSourceTyped, GradeSourceLLM:
	default:
		return SubmitResult{}, fmt.Errorf("schedule: submit review: invalid grade source %q", source)
	}

	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	// 1. Fetch the current state and take a row lock (SQLite has no FOR UPDATE; it serialises on a single write connection, see Open).
	cur, err := loadStateForUpdate(ctx, tx, in.CardID, in.UserID)
	if err != nil {
		return SubmitResult{}, err
	}
	stateBefore := StateNew
	version := 0
	if cur != nil {
		stateBefore, err = ParseState(cur.State)
		if err != nil {
			return SubmitResult{}, err
		}
		version = cur.Version
	}

	// 2. Optimistic-lock check: on mismatch return the conflict straight away, never write.
	if version != in.ExpectedVersion {
		return SubmitResult{}, fmt.Errorf("%w: card %d user %d holds version %d, expected %d",
			ErrVersionConflict, in.CardID, in.UserID, version, in.ExpectedVersion)
	}

	// 3. Compute the new state. With no state row, treat it as a brand-new card (state=new).
	base := cur
	if base == nil {
		base = &store.CardState{CardID: in.CardID, UserID: in.UserID, State: StateNew.String()}
	}
	outcome, err := in.Scheduler.Next(base, now, in.Rating)
	if err != nil {
		return SubmitResult{}, err
	}

	// 4. Write card_states (version + 1). Use a conditional upsert guarded by version: a row lock cannot lock
	//    "a row that does not exist yet", so two concurrent first grades would both read version=0 and both pass, so the guard
	//    is pushed down into the DO UPDATE branch of the upsert, making insert and update atomic in one SQL statement.
	newState := stateFromOutcome(base, outcome, now, version+1)
	written, err := upsertState(ctx, tx, &newState, version)
	if err != nil {
		return SubmitResult{}, err
	}
	// The guard stopped this write: the state row's version was concurrently changed after this read.
	// This is exactly the branch the loser of a concurrent first grade reaches — not a single review may be written, return the conflict sentinel.
	if !written {
		return SubmitResult{}, fmt.Errorf("%w: card %d user %d: the state row changed concurrently, expected version %d",
			ErrVersionConflict, in.CardID, in.UserID, in.ExpectedVersion)
	}

	// 5. Write reviews. Every column listed in the schema is populated; it is the only fuel for parameter optimisation.
	review := reviewFromOutcome(in, base, outcome, stateBefore, now)
	if err := tx.WithContext(ctx).Create(&review).Error; err != nil {
		return SubmitResult{}, fmt.Errorf("schedule: submit review: insert review for card %d: %w", in.CardID, err)
	}

	return SubmitResult{State: newState, ReviewID: review.ID}, nil
}

// loadStateForUpdate reads the state row by (card_id, user_id); under PostgreSQL it takes a FOR UPDATE row lock.
// Returns (nil, nil) when not found — a brand-new card having no state row is normal, not an error.
func loadStateForUpdate(ctx context.Context, tx *gorm.DB, cardID, userID uint64) (*store.CardState, error) {
	var st store.CardState
	q := tx.WithContext(ctx).Model(&store.CardState{})
	// SQLite (glebarez) does not support SELECT ... FOR UPDATE; it serialises writes on a single write connection.
	if tx.Dialector.Name() == "postgres" {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := q.Where("card_id = ? AND user_id = ?", cardID, userID).First(&st).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("schedule: load card state (card %d, user %d): %w", cardID, userID, err)
	}
	return &st, nil
}

// stateFromOutcome flattens the scheduling outcome into the card_states row to be stored; version is supplied by the caller.
func stateFromOutcome(base *store.CardState, o Outcome, now time.Time, version int) store.CardState {
	due := o.Due.UTC()
	stability := o.Stability
	difficulty := o.Difficulty
	lastReview := now
	return store.CardState{
		CardID:        base.CardID,
		UserID:        base.UserID,
		State:         o.State.String(),
		DueAt:         &due,
		StepIndex:     o.RemainingSteps,
		Stability:     &stability,
		Difficulty:    &difficulty,
		Reps:          o.Reps,
		Lapses:        o.Lapses,
		ScheduledDays: o.ScheduledDays,
		ElapsedDays:   elapsedDaysSince(base.LastReviewAt, now),
		LastReviewAt:  &lastReview,
		Version:       version,
	}
}

// upsertState writes the state row with GORM's clause.OnConflict (each database generates the correct SQL):
// a row is created on the first grade and updated by (card_id, user_id) afterwards.
//
// expectedVersion is the card_states.version read before this write, used as the guard on the DO UPDATE branch:
// a row lock cannot lock "a row that does not exist yet", so two concurrent first grades would both read version=0 and both pass.
// With `WHERE card_states.version = expectedVersion`, the later write affects 0 rows in the conflict branch because of the version
// mismatch — nothing written means no write happened, and no second read is needed. Returning written=false means
// the guard stopped this write (concurrent conflict); when inserting a new row the guard is not evaluated and it always counts as a successful write.
func upsertState(ctx context.Context, tx *gorm.DB, st *store.CardState, expectedVersion int) (bool, error) {
	cols := []string{
		"state", "due_at", "step_index", "stability", "difficulty",
		"reps", "lapses", "scheduled_days", "elapsed_days", "last_review_at", "version",
	}
	res := tx.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "card_id"}, {Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns(cols),
		// The guard column must carry the table name: the DO UPDATE WHERE references the existing target row.
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Eq{Column: clause.Column{Table: "card_states", Name: "version"}, Value: expectedVersion},
		}},
	}).Create(st)
	if res.Error != nil {
		return false, fmt.Errorf("schedule: upsert card state (card %d, user %d): %w", st.CardID, st.UserID, res.Error)
	}
	return res.RowsAffected > 0, nil
}

// reviewFromOutcome constructs the append-only review log row.
//
// Value semantics: state_before is the state enum before the grade; interval_days / stability / difficulty are
// the new values produced by this grade (for retention and memory-strength statistics); duration_days is the number of days since the previous review
// (i.e. the elapsed days before this grade, nullable — the first grade has no previous one).
func reviewFromOutcome(in SubmitInput, base *store.CardState, o Outcome, stateBefore State, now time.Time) store.Review {
	interval := o.IntervalDays
	stability := o.Stability
	difficulty := o.Difficulty
	loc := resolveLocation(in.Location, in.Timezone)
	cutoff := reviewday.ResolveCutoff(in.DayCutoffHour)
	// Snapshot of the remaining learning steps before the grade. A brand-new card has no state row, so base.StepIndex is 0,
	// which is exactly the "before the grade" value; an existing state row contributes its step_index from before the grade.
	stepIndexBefore := 0
	if base != nil {
		stepIndexBefore = base.StepIndex
	}
	// Snapshot of the due date before the grade: burying only changes due_at and writes no log, so undo can only restore it exactly from this.
	// Copy the value (rather than storing a pointer) so it does not share memory with the state row.
	var dueBefore *time.Time
	if base != nil && base.DueAt != nil {
		d := *base.DueAt
		dueBefore = &d
	}
	row := store.Review{
		CardID:          in.CardID,
		UserID:          in.UserID,
		Rating:          int(in.Rating),
		GradeSource:     gradeSourceOrSelf(in.GradeSource),
		GradeDetailJSON: in.GradeDetailJSON,
		ReviewedAt:      now,
		ReviewDay:       reviewday.Day(now, loc, cutoff),
		ElapsedMS:       in.ElapsedMS,
		DurationDays:    durationDaysSince(base, now),
		StateBefore:     int(stateBefore),
		StepIndexBefore: &stepIndexBefore,
		DueBefore:       dueBefore,
		IntervalDays:    &interval,
		Stability:       &stability,
		Difficulty:      &difficulty,
	}
	return row
}

// durationDaysSince returns the number of days since the previous review (may be fractional); nil when there is no previous review,
// because there is no interval to base it on and writing 0 would pollute the statistics.
func durationDaysSince(base *store.CardState, now time.Time) *float64 {
	if base == nil || base.LastReviewAt == nil {
		return nil
	}
	d := now.Sub(base.LastReviewAt.UTC()).Hours() / 24
	if d < 0 {
		d = 0
	}
	return &d
}

// resolveLocation returns the explicit loc when the caller already holds one, otherwise the user's timezone name resolved
// through reviewday.Location (empty or unknown names fall back to UTC, so a review is never blocked by dirty data).
func resolveLocation(loc *time.Location, tz string) *time.Location {
	if loc != nil {
		return loc
	}
	return reviewday.Location(tz)
}

func gradeSourceOrSelf(source string) string {
	if source == "" {
		return GradeSourceSelf
	}
	return source
}

// elapsedDaysSince returns the whole number of days since the previous review; 0 when there is no previous review.
func elapsedDaysSince(last *time.Time, now time.Time) int {
	if last == nil {
		return 0
	}
	d := now.Sub(last.UTC()).Hours() / 24
	if d < 0 {
		return 0
	}
	return int(d)
}
