package schedule

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// mustUndo undoes one grade in a self-opened transaction, commits it, and returns the restored state row.
// ExpectedVersion takes the current version in the DB (the undo targets the state it has just produced).
func mustUndo(t *testing.T, db *gorm.DB, cardID, userID uint64, s *Scheduler, now time.Time) store.CardState {
	t.Helper()
	var cur store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, userID).First(&cur).Error; err != nil {
		t.Fatalf("load state before undo: %v", err)
	}
	tx := db.Begin()
	restored, err := Rollback(context.Background(), tx, UndoInput{
		CardID: cardID, UserID: userID, ExpectedVersion: cur.Version, Scheduler: s, Now: now,
	})
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit undo: %v", err)
	}
	return restored
}

// TestSubmitWritesStepIndexSnapshot asserts the submit path writes the pre-grade remaining learning steps into
// reviews.step_index_before (DESC does not define that column but requires it to be fully populated from the very first log).
//
// The snapshot is the "before the grade" value: 0 before the first grade; before the second grade it equals the step_index left by the first.
func TestSubmitWritesStepIndexSnapshot(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	cardID := seedCard(t, db, deckID, "forward", now)
	s := mustScheduler(t, testPreset(t))

	first := mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: 0,
		Scheduler: s, Now: now, Location: time.UTC,
	})
	var firstReview store.Review
	if err := db.First(&firstReview, first.ReviewID).Error; err != nil {
		t.Fatalf("load first review: %v", err)
	}
	if firstReview.StepIndexBefore == nil {
		t.Fatal("first review step_index_before = NULL, want 0 (a new card starts at step 0)")
	}
	if *firstReview.StepIndexBefore != 0 {
		t.Errorf("first review step_index_before = %d, want 0", *firstReview.StepIndexBefore)
	}
	afterFirst := first.State.StepIndex
	if afterFirst == 0 {
		t.Fatalf("first review left step_index = 0; the test needs a non-trivial step to observe")
	}

	second := mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: first.State.Version,
		Scheduler: s, Now: now.Add(time.Minute), Location: time.UTC,
	})
	var secondReview store.Review
	if err := db.First(&secondReview, second.ReviewID).Error; err != nil {
		t.Fatalf("load second review: %v", err)
	}
	if secondReview.StepIndexBefore == nil || *secondReview.StepIndexBefore != afterFirst {
		t.Errorf("second review step_index_before = %v, want %d (the pre-rating value)",
			secondReview.StepIndexBefore, afterFirst)
	}
}

// TestUndoRestoresLearningStepExactly is an acceptance case: a learning card advances two steps,
// and with two consecutive Undos the step_index of each step returns exactly to its pre-grade value.
//
// Before the fix Rollback zeroed step_index, so the first Undo could not restore that middle step —
// which is exactly why the value asserted in this case (non-zero) was chosen.
func TestUndoRestoresLearningStepExactly(t *testing.T) {
	db := newTestDB(t)
	now1 := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now1)
	cardID := seedCard(t, db, deckID, "forward", now1)
	s := mustScheduler(t, testPreset(t)) // learning steps 1m,10m

	// First step: new → learning.
	step1Result := mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: 0,
		Scheduler: s, Now: now1, Location: time.UTC,
	})
	step1 := step1Result.State.StepIndex
	if step1 == 0 {
		t.Fatalf("first rating left step_index = 0; the test needs a non-trivial step value")
	}

	// Second step: learning advances one more step.
	now2 := now1.Add(time.Minute)
	step2Result := mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: step1Result.State.Version,
		Scheduler: s, Now: now2, Location: time.UTC,
	})
	step2 := step2Result.State.StepIndex
	if step1 == step2 {
		t.Fatalf("rating twice did not advance the learning step: step_index = %d both times", step1)
	}

	// First Undo: back to the step_index after the first step, not zeroed.
	restored1 := mustUndo(t, db, cardID, 1, s, now2)
	t.Logf("step_index: after first rating=%d, after second rating=%d, after undo#1=%d",
		step1, step2, restored1.StepIndex)
	if restored1.StepIndex != step1 {
		t.Errorf("first undo step_index = %d, want %d", restored1.StepIndex, step1)
	}
	if restored1.State != step1Result.State.State {
		t.Errorf("first undo state = %q, want %q", restored1.State, step1Result.State.State)
	}

	// Second Undo: back to the original pre-grade value (0 for a brand-new card).
	restored2 := mustUndo(t, db, cardID, 1, s, now2)
	t.Logf("step_index after undo#2=%d (state=%s)", restored2.StepIndex, restored2.State)
	if restored2.StepIndex != 0 {
		t.Errorf("second undo step_index = %d, want 0 (original)", restored2.StepIndex)
	}
	if restored2.State != StateNew.String() {
		t.Errorf("second undo state = %q, want %q", restored2.State, StateNew)
	}
}
