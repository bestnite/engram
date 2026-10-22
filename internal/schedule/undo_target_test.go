package schedule

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// undoOnce undoes one grade in a self-opened transaction with the given expected version, returning the result and error (the transaction commits or rolls back depending on the result).
func undoOnce(t *testing.T, db *gorm.DB, cardID, userID uint64, expectedVersion int, s *Scheduler, now time.Time) (store.CardState, error) {
	t.Helper()
	tx := db.Begin()
	restored, err := Rollback(context.Background(), tx, UndoInput{
		CardID: cardID, UserID: userID, ExpectedVersion: expectedVersion, Scheduler: s, Now: now,
	})
	if err != nil {
		_ = tx.Rollback().Error
		return store.CardState{}, err
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit undo: %v", err)
	}
	return restored, nil
}

// TestUndoReplayDoesNotDeleteAnotherReview is the AUDIT-02 acceptance: with two identical undo requests,
// the second gets a 409 because the version has advanced, and it does not delete another historical grade (before the fix the second would delete the previous one, leaving 0).
func TestUndoReplayDoesNotDeleteAnotherReview(t *testing.T) {
	db := newTestDB(t)
	now1 := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now1)
	cardID := seedCard(t, db, deckID, "forward", now1)
	s := mustScheduler(t, testPreset(t))

	first := mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: 0,
		Scheduler: s, Now: now1, Location: time.UTC,
	})
	second := mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: first.State.Version,
		Scheduler: s, Now: now1.Add(time.Minute), Location: time.UTC,
	})

	// First undo: target version = the version after the second grade.
	if _, err := undoOnce(t, db, cardID, 1, second.State.Version, s, now1.Add(time.Minute)); err != nil {
		t.Fatalf("first undo error = %v, want nil", err)
	}
	// Replaying the same request: the version has changed, so it must end in a conflict and delete no log.
	_, err := undoOnce(t, db, cardID, 1, second.State.Version, s, now1.Add(time.Minute))
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("replayed undo error = %v, want ErrVersionConflict", err)
	}
	var logs int64
	if err := db.Model(&store.Review{}).Where("card_id = ? AND user_id = ?", cardID, 1).Count(&logs).Error; err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if logs != 1 {
		t.Errorf("reviews = %d, want 1 (a replayed undo must not remove another review)", logs)
	}
}

// TestUndoStaleVersionIsRejected asserts an undo issued with a stale version is rejected: no grade deleted, state unchanged.
func TestUndoStaleVersionIsRejected(t *testing.T) {
	db := newTestDB(t)
	now1 := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now1)
	cardID := seedCard(t, db, deckID, "forward", now1)
	s := mustScheduler(t, testPreset(t))

	first := mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: 0,
		Scheduler: s, Now: now1, Location: time.UTC,
	})
	second := mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: first.State.Version,
		Scheduler: s, Now: now1.Add(time.Minute), Location: time.UTC,
	})

	// The other window holds the version after the first grade (already stale).
	_, err := undoOnce(t, db, cardID, 1, first.State.Version, s, now1.Add(time.Minute))
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale undo error = %v, want ErrVersionConflict", err)
	}
	var logs int64
	if err := db.Model(&store.Review{}).Where("card_id = ? AND user_id = ?", cardID, 1).Count(&logs).Error; err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if logs != 2 {
		t.Errorf("reviews = %d, want 2 (a stale undo must not delete anything)", logs)
	}
	var st store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, 1).First(&st).Error; err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.Version != second.State.Version {
		t.Errorf("state version = %d, want %d (unchanged)", st.Version, second.State.Version)
	}
}

// TestUndoRestoresBuriedDueBefore is the AUDIT-04 acceptance: bury a new card, grade it the next day, then undo —
// the pre-grade due date must be restored exactly to the value the bury set, not NULL (before the fix the previous log did not exist and due became NULL).
func TestUndoRestoresBuriedDueBefore(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	day1 := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, day1)
	cardID := seedCard(t, db, deckID, "forward", day1)
	s := mustScheduler(t, testPreset(t))

	buried, err := Bury(ctx, db, BuryInput{CardID: cardID, UserID: 1, Now: day1, Location: time.UTC})
	if err != nil {
		t.Fatalf("Bury() error = %v", err)
	}
	wantDue := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC) // cutoff 4 → 04:00 the next day
	if buried.DueAt == nil || !buried.DueAt.Equal(wantDue) {
		t.Fatalf("buried due_at = %v, want %v", buried.DueAt, wantDue)
	}

	// Grade it the next day; the pre-grade due date should be written into the due_before snapshot.
	day2 := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	reviewed := mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: buried.Version,
		Scheduler: s, Now: day2, Location: time.UTC,
	})
	var log store.Review
	if err := db.First(&log, reviewed.ReviewID).Error; err != nil {
		t.Fatalf("load review: %v", err)
	}
	if log.DueBefore == nil || !log.DueBefore.Equal(wantDue) {
		t.Fatalf("review due_before = %v, want %v", log.DueBefore, wantDue)
	}

	restored, err := undoOnce(t, db, cardID, 1, reviewed.State.Version, s, day2)
	if err != nil {
		t.Fatalf("undo error = %v", err)
	}
	if restored.DueAt == nil {
		t.Fatalf("undo due_at = nil, want %v (pre-review due must be restored, not NULL)", wantDue)
	}
	if !restored.DueAt.Equal(wantDue) {
		t.Errorf("undo due_at = %v, want %v (exact pre-review restore)", restored.DueAt, wantDue)
	}
}
