package schedule

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// undoOnce 在自建事务里用给定 expected version 撤销一次评分，返回结果与错误（事务按结果提交或回滚）。
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

// TestUndoReplayDoesNotDeleteAnotherReview 是 AUDIT-02 的验收：两次完全相同的撤销请求，
// 第二次因版本已推进而 409，且不删除另一条历史评分（修复前第二次会删掉前一条，只剩 0 条）。
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

	// 第一次撤销：目标版本 = 第二次评分后的版本。
	if _, err := undoOnce(t, db, cardID, 1, second.State.Version, s, now1.Add(time.Minute)); err != nil {
		t.Fatalf("first undo error = %v, want nil", err)
	}
	// 重放同一请求：版本已变，必须以冲突结束且不删任何日志。
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

// TestUndoStaleVersionIsRejected 断言用旧版本发起的撤销被拒：不删评分、状态不变。
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

	// 另一窗口拿的是第一次评分后的版本（已过期）。
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

// TestUndoRestoresBuriedDueBefore 是 AUDIT-04 的验收：先埋藏新卡，第二天评分，再撤销——
// 评分前的到期日必须精确还原为埋藏设的那个值，而不是 NULL（修复前上一条日志不存在，due 归 NULL）。
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
	wantDue := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC) // 切点 4 → 次日 04:00
	if buried.DueAt == nil || !buried.DueAt.Equal(wantDue) {
		t.Fatalf("buried due_at = %v, want %v", buried.DueAt, wantDue)
	}

	// 第二天评分；评分前的到期日应被写进 due_before 快照。
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
