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

// TestSubmitConcurrentFirstReviewIsAtomic is an acceptance case: on a real PostgreSQL,
// fire two concurrent first grades of the same brand-new card and assert that exactly one reviews row and one card_states row survive,
// and the loser receives the conflict sentinel (ErrVersionConflict).
//
// Why real PG is required: SQLite serialises writes on a single write connection, so two first grades that "cannot read a state row" can never really
// be concurrent; under PG a row lock cannot lock a row that does not exist yet, and before the fix both transactions could pass the version check and each write one review
// (a double write). The case gets its own schema via pgtest and drops it when done.
//
// Why both goroutines must not simply start at once: the winner would usually commit before the loser reads the state, and the loser would be stopped by
// the ordinary version check, so neither the guard nor the race would be exercised. A single synchronisation here lines up the write window:
// the winner inserts first without committing, then commits only after the loser enters the write path; by then the loser reads "no such row" (an uncommitted
// row is invisible under READ COMMITTED) and the INSERT blocks on the unique index waiting for the winner. This is exactly the window where both used to
// pass:
//   - before the fix: the guardless DO UPDATE still matched after the winner committed, so the loser wrote too — two reviews.
//   - after the fix: the DO UPDATE's WHERE card_states.version = 0 no longer matches once the winner has committed, affecting 0 rows, so
//     the loser returns the conflict sentinel and writes no review at all.
func TestSubmitConcurrentFirstReviewIsAtomic(t *testing.T) {
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
	in := SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: 0,
		Scheduler: s, Now: now, Location: time.UTC,
	}

	winnerInserted := make(chan struct{}) // the winner has written (not committed)
	loserWriting := make(chan struct{})   // the loser has entered the write path
	var (
		wg                  sync.WaitGroup
		winnerErr, loserErr error
	)

	// Winner: stops after writing without committing, leaving the window to the loser.
	wg.Add(1)
	go func() {
		defer wg.Done()
		tx := db.Begin()
		if tx.Error != nil {
			winnerErr = tx.Error
			return
		}
		if _, err := Submit(context.Background(), tx, in); err != nil {
			winnerErr = err
			_ = tx.Rollback().Error
			return
		}
		close(winnerInserted)
		<-loserWriting
		// Give the loser enough time to read "no such row" and block on the INSERT; only then does the winner commit,
		// forcing the loser's DO UPDATE to decide for itself whether the write goes through.
		time.Sleep(300 * time.Millisecond)
		winnerErr = tx.Commit().Error
	}()

	// Loser: starts after the winner has written, so the two transactions genuinely overlap.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-winnerInserted
		tx := db.Begin()
		if tx.Error != nil {
			loserErr = tx.Error
			return
		}
		close(loserWriting)
		if _, err := Submit(context.Background(), tx, in); err != nil {
			loserErr = err
			_ = tx.Rollback().Error
			return
		}
		loserErr = tx.Commit().Error
	}()
	wg.Wait()

	if winnerErr != nil {
		t.Fatalf("winner error = %v", winnerErr)
	}
	if !errors.Is(loserErr, ErrVersionConflict) {
		t.Fatalf("loser error = %v, want ErrVersionConflict", loserErr)
	}

	var states int64
	if err := db.Model(&store.CardState{}).Where("card_id = ? AND user_id = ?", cardID, 1).Count(&states).Error; err != nil {
		t.Fatalf("count card_states: %v", err)
	}
	var reviews int64
	if err := db.Model(&store.Review{}).Where("card_id = ? AND user_id = ?", cardID, 1).Count(&reviews).Error; err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	t.Logf("after concurrent first submissions: card_states=%d reviews=%d", states, reviews)
	if states != 1 {
		t.Errorf("card_states rows = %d, want exactly 1", states)
	}
	if reviews != 1 {
		t.Errorf("reviews rows = %d, want exactly 1", reviews)
	}

	var st store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, 1).First(&st).Error; err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.Version != 1 {
		t.Errorf("state version = %d, want 1 (exactly one submission applied)", st.Version)
	}
	if st.Reps != 1 {
		t.Errorf("state reps = %d, want 1 (exactly one submission applied)", st.Reps)
	}
}
