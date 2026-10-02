package schedule

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"example.com/engram/internal/store"
)

// mustUndo 在自建事务里撤销一次评分并提交，返回恢复后的状态行。
func mustUndo(t *testing.T, db *gorm.DB, cardID, userID uint64, s *Scheduler, now time.Time) store.CardState {
	t.Helper()
	tx := db.Begin()
	restored, err := Rollback(context.Background(), tx, UndoInput{
		CardID: cardID, UserID: userID, Scheduler: s, Now: now,
	})
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit undo: %v", err)
	}
	return restored
}

// TestSubmitWritesStepIndexSnapshot 断言提交路径把评分前的剩余学习步骤写进
// reviews.step_index_before（DESC 未定义该列，M3-9 要求它随第一条日志起就写全）。
//
// 快照是“评分前”的值：首评前是 0；第二评前等于首评后留下的 step_index。
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

// TestUndoRestoresLearningStepExactly 是 M3-9 的验收用例：一张学习卡向前走两步，
// 连续两次 Undo，每一步的 step_index 都精确回到评分前的值。
//
// 修复前 Rollback 会把 step_index 归零，于是第一次 Undo 就还原不出中间那一步 ——
// 本用例断言的值（非 0）正是为此选取的。
func TestUndoRestoresLearningStepExactly(t *testing.T) {
	db := newTestDB(t)
	now1 := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now1)
	cardID := seedCard(t, db, deckID, "forward", now1)
	s := mustScheduler(t, testPreset(t)) // learning steps 1m,10m

	// 第一步：new → learning。
	step1Result := mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: 0,
		Scheduler: s, Now: now1, Location: time.UTC,
	})
	step1 := step1Result.State.StepIndex
	if step1 == 0 {
		t.Fatalf("first rating left step_index = 0; the test needs a non-trivial step value")
	}

	// 第二步：learning 再向前一步。
	now2 := now1.Add(time.Minute)
	step2Result := mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: step1Result.State.Version,
		Scheduler: s, Now: now2, Location: time.UTC,
	})
	step2 := step2Result.State.StepIndex
	if step1 == step2 {
		t.Fatalf("rating twice did not advance the learning step: step_index = %d both times", step1)
	}

	// 第一次 Undo：回到第一步之后的 step_index，而不是被归零。
	restored1 := mustUndo(t, db, cardID, 1, s, now2)
	t.Logf("step_index: after first rating=%d, after second rating=%d, after undo#1=%d",
		step1, step2, restored1.StepIndex)
	if restored1.StepIndex != step1 {
		t.Errorf("first undo step_index = %d, want %d", restored1.StepIndex, step1)
	}
	if restored1.State != step1Result.State.State {
		t.Errorf("first undo state = %q, want %q", restored1.State, step1Result.State.State)
	}

	// 第二次 Undo：回到评分前的原始值（全新卡为 0）。
	restored2 := mustUndo(t, db, cardID, 1, s, now2)
	t.Logf("step_index after undo#2=%d (state=%s)", restored2.StepIndex, restored2.State)
	if restored2.StepIndex != 0 {
		t.Errorf("second undo step_index = %d, want 0 (original)", restored2.StepIndex)
	}
	if restored2.State != StateNew.String() {
		t.Errorf("second undo state = %q, want %q", restored2.State, StateNew)
	}
}
