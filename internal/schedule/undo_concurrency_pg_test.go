package schedule

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/pgtest"
	"git.nite07.com/nite/engram/internal/store"
)

// TestUndoConcurrentSameVersionIsAtomic is the PostgreSQL acceptance for AUDIT-03: two windows concurrently
// undo the same card with the same target version, exactly one succeeds and the other receives the conflict sentinel; state, grade log and audit record
// stay consistent (one fewer grade, one more audit entry, the state version advanced by exactly one step).
//
// Why real PG is required: Rollback relies on the card_states row lock (FOR UPDATE) to serialise "read state + read logs +
// write state + delete log". SQLite serialises on a single write connection, hiding the race window of "read logs before locking the state".
// The fixed order is "lock the state and check the version first, then read the logs", so the row lock serialises the two transactions: the winner deletes the log and
// advances the version to 3, and the loser, once it gets the lock, reads version 3 ≠ expected 2, returns ErrVersionConflict and writes nothing.
func TestUndoConcurrentSameVersionIsAtomic(t *testing.T) {
	db, ok := pgtest.Open(t)
	if !ok {
		t.Skipf("%s not set: skipping the PostgreSQL concurrency case", pgtest.EnvDSN)
	}
	if err := db.AutoMigrate(store.AllModels()...); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	cardID := seedCard(t, db, deckID, "forward", now)
	s := mustScheduler(t, testPreset(t))

	first := mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: 0,
		Scheduler: s, Now: now, Location: time.UTC,
	})
	second := mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: first.State.Version,
		Scheduler: s, Now: now.Add(time.Minute), Location: time.UTC,
	})
	target := second.State.Version

	start := make(chan struct{})
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		errs      []error
		succeeded int
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			tx := db.Begin()
			if tx.Error != nil {
				mu.Lock()
				errs = append(errs, tx.Error)
				mu.Unlock()
				return
			}
			if _, err := Rollback(context.Background(), tx, UndoInput{
				CardID: cardID, UserID: 1, ExpectedVersion: target, Scheduler: s, Now: now.Add(time.Minute),
			}); err != nil {
				_ = tx.Rollback().Error
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return
			}
			if err := tx.Commit().Error; err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return
			}
			mu.Lock()
			succeeded++
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	if succeeded != 1 {
		t.Fatalf("concurrent undos succeeded = %d, want exactly 1 (errors: %v)", succeeded, errs)
	}
	conflicts := 0
	for _, err := range errs {
		if errors.Is(err, ErrVersionConflict) {
			conflicts++
		} else if err != nil {
			t.Errorf("unexpected concurrent undo error = %v", err)
		}
	}
	if conflicts != 1 {
		t.Errorf("version conflicts = %d, want 1 (errors: %v)", conflicts, errs)
	}

	var reviews int64
	if err := db.Model(&store.Review{}).Where("card_id = ? AND user_id = ?", cardID, 1).Count(&reviews).Error; err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if reviews != 1 {
		t.Errorf("reviews = %d, want 1 (exactly one undo applied)", reviews)
	}
	var audits int64
	if err := db.Model(&store.AuditLog{}).Where("action = ?", store.ActionReviewUndo).Count(&audits).Error; err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if audits != 1 {
		t.Errorf("review.undo audit rows = %d, want 1", audits)
	}
	var st store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, 1).First(&st).Error; err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.Version != target+1 {
		t.Errorf("state version = %d, want %d (advanced exactly once)", st.Version, target+1)
	}
}
