package schedule

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// mustSubmit 在自建事务里提交一次评分并提交事务，返回结果。
func mustSubmit(t *testing.T, db *gorm.DB, in SubmitInput) SubmitResult {
	t.Helper()
	tx := db.Begin()
	res, err := Submit(context.Background(), tx, in)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit: %v", err)
	}
	return res
}

// TestUndoRestoresPreviousDueAndInterval 断言 Undo 精确恢复上一次评分之后的到期日与间隔
// 做法：评分两次，快照第一次之后的状态，Undo 后逐字段比对。
func TestUndoRestoresPreviousDueAndInterval(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now1 := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now1)
	cardID := seedCard(t, db, deckID, "forward", now1)
	s := mustScheduler(t, testPreset(t))

	mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: 0,
		Scheduler: s, Now: now1, Location: time.UTC,
	})
	var afterFirst store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, 1).First(&afterFirst).Error; err != nil {
		t.Fatalf("load state after first review: %v", err)
	}

	now2 := now1.Add(15 * time.Minute)
	mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: afterFirst.Version,
		Scheduler: s, Now: now2, Location: time.UTC,
	})

	tx := db.Begin()
	restored, err := Rollback(ctx, tx, UndoInput{CardID: cardID, UserID: 1, Scheduler: s, Now: now2})
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit undo: %v", err)
	}

	if restored.State != afterFirst.State {
		t.Errorf("undo state = %q, want %q", restored.State, afterFirst.State)
	}
	if restored.DueAt == nil || afterFirst.DueAt == nil {
		t.Fatalf("undo due_at = %v, want %v", restored.DueAt, afterFirst.DueAt)
	}
	if restored.DueAt.UnixMilli() != afterFirst.DueAt.UnixMilli() {
		t.Errorf("undo due_at = %v, want %v (exact restore)", restored.DueAt, afterFirst.DueAt)
	}
	if restored.ScheduledDays != afterFirst.ScheduledDays {
		t.Errorf("undo scheduled_days = %d, want %d", restored.ScheduledDays, afterFirst.ScheduledDays)
	}
	if restored.Reps != afterFirst.Reps || restored.Lapses != afterFirst.Lapses {
		t.Errorf("undo reps/lapses = %d/%d, want %d/%d",
			restored.Reps, restored.Lapses, afterFirst.Reps, afterFirst.Lapses)
	}
	if restored.Stability == nil || afterFirst.Stability == nil || *restored.Stability != *afterFirst.Stability {
		t.Errorf("undo stability = %v, want %v", restored.Stability, afterFirst.Stability)
	}
	if restored.Difficulty == nil || afterFirst.Difficulty == nil || *restored.Difficulty != *afterFirst.Difficulty {
		t.Errorf("undo difficulty = %v, want %v", restored.Difficulty, afterFirst.Difficulty)
	}
	if restored.LastReviewAt == nil || !restored.LastReviewAt.Equal(*afterFirst.LastReviewAt) {
		t.Errorf("undo last_review_at = %v, want %v", restored.LastReviewAt, afterFirst.LastReviewAt)
	}

	// 被撤销的那条日志必须被删除，只剩第一次的日志。
	var logs int64
	if err := db.Model(&store.Review{}).Where("card_id = ? AND user_id = ?", cardID, 1).Count(&logs).Error; err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if logs != 1 {
		t.Errorf("reviews = %d, want 1 after undo", logs)
	}
	// 写一条 review.undo 审计。
	var audits int64
	if err := db.Model(&store.AuditLog{}).Where("action = ?", store.ActionReviewUndo).Count(&audits).Error; err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if audits != 1 {
		t.Errorf("review.undo audit rows = %d, want 1", audits)
	}
}

// TestUndoFirstReviewReturnsCardToNew 断言撤销首次评分把卡恢复为全新状态（无到期日、无日志）。
func TestUndoFirstReviewReturnsCardToNew(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	cardID := seedCard(t, db, deckID, "forward", now)
	s := mustScheduler(t, testPreset(t))

	mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: 0,
		Scheduler: s, Now: now, Location: time.UTC,
	})

	tx := db.Begin()
	restored, err := Rollback(ctx, tx, UndoInput{CardID: cardID, UserID: 1, Scheduler: s, Now: now})
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit: %v", err)
	}
	if restored.State != StateNew.String() {
		t.Errorf("undo state = %q, want %q", restored.State, StateNew)
	}
	if restored.DueAt != nil {
		t.Errorf("undo due_at = %v, want nil", restored.DueAt)
	}
	if restored.Stability != nil || restored.Difficulty != nil {
		t.Errorf("undo stability/difficulty = %v/%v, want nil", restored.Stability, restored.Difficulty)
	}
	var logs int64
	if err := db.Model(&store.Review{}).Count(&logs).Error; err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if logs != 0 {
		t.Errorf("reviews = %d, want 0", logs)
	}
}

// TestUndoWithoutLogReturnsError 断言没有日志时 Undo 报英文错误而不是静默成功。
func TestUndoWithoutLogReturnsError(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	cardID := seedCard(t, db, deckID, "forward", now)
	seedState(t, db, 1, cardID, "review", now, 5, 5, &now)
	s := mustScheduler(t, testPreset(t))

	tx := db.Begin()
	defer tx.Rollback()
	_, err := Rollback(context.Background(), tx, UndoInput{CardID: cardID, UserID: 1, Scheduler: s, Now: now})
	if !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("Rollback() error = %v, want ErrNothingToUndo", err)
	}
}

// TestSuspendIsPerUser 断言暂停只对暂停者生效：用户 1 暂停后这张卡从他的队列消失，
// 同一张卡仍在用户 2 的队列里（反面：卡片级暂停会让所有人都看不到）；取消暂停后回到用户 1 的队列，
// 进度数值与到期日不变。还没有状态行的新卡也能被暂停。
func TestSuspendIsPerUser(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	cardID := seedCard(t, db, deckID, "forward", now)
	seedState(t, db, 1, cardID, "review", now.Add(-time.Hour), 5, 5, &now)
	seedState(t, db, 2, cardID, "review", now.Add(-time.Hour), 5, 5, &now)
	fresh := seedCard(t, db, deckID, "forward", now)

	s := mustScheduler(t, testPreset(t))
	queue := func(user uint64) map[uint64]bool {
		t.Helper()
		items, err := NewQueueBuilder(db, store.NewDeckStore(db), s).Build(ctx, user,
			QueueOptions{DeckIDs: []uint64{deckID}, Now: now, Location: time.UTC, NewPerDayOverride: new(int)})
		if err != nil {
			t.Fatalf("Build(%d) error = %v", user, err)
		}
		out := map[uint64]bool{}
		for _, it := range items {
			out[it.CardID] = true
		}
		return out
	}
	set := func(card uint64, suspended bool) store.CardState {
		t.Helper()
		var st store.CardState
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			st, err = SetSuspended(ctx, tx, SuspendInput{CardID: card, UserID: 1, Suspended: suspended, Now: now})
			return err
		})
		if err != nil {
			t.Fatalf("SetSuspended(%d, %v) error = %v", card, suspended, err)
		}
		return st
	}

	st := set(cardID, true)
	if st.SuspendedAt == nil || st.State != "review" || st.Stability == nil || *st.Stability != 5 {
		t.Fatalf("suspended state = %+v, want suspended_at set and progress untouched", st)
	}
	if queue(1)[cardID] {
		t.Error("user 1 still sees the card after suspending it")
	}
	if !queue(2)[cardID] {
		t.Error("user 2 lost the card because user 1 suspended it")
	}
	if st := set(fresh, true); st.State != "new" || st.SuspendedAt == nil {
		t.Errorf("suspending a new card = %+v, want a new state row with suspended_at", st)
	}
	if queue(1)[fresh] {
		t.Error("user 1 still sees the suspended new card")
	}

	st = set(cardID, false)
	if st.SuspendedAt != nil || st.DueAt == nil || !st.DueAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("unsuspended state = %+v, want suspended_at cleared and due_at unchanged", st)
	}
	if !queue(1)[cardID] {
		t.Error("user 1 does not see the card after unsuspending it")
	}
}

// TestBuryDefersCardToNextReviewDay 断言埋藏把到期日推到下一个复习日，且复习卡与新卡
// 都因此在本日队列里消失。
func TestBuryDefersCardToNextReviewDay(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)

	reviewCard := seedCard(t, db, deckID, "forward", now)
	seedState(t, db, 1, reviewCard, "review", now.Add(-time.Hour), 5, 5, &now)
	newCard := seedCard(t, db, deckID, "reverse", now)

	buried, err := Bury(ctx, db, BuryInput{CardID: reviewCard, UserID: 1, Now: now, Location: time.UTC})
	if err != nil {
		t.Fatalf("Bury(review card) error = %v", err)
	}
	wantDue := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC) // 切点 4 → 次日 04:00
	if buried.DueAt == nil || !buried.DueAt.Equal(wantDue) {
		t.Errorf("buried due_at = %v, want %v", buried.DueAt, wantDue)
	}
	if _, err := Bury(ctx, db, BuryInput{CardID: newCard, UserID: 1, Now: now, Location: time.UTC}); err != nil {
		t.Fatalf("Bury(new card) error = %v", err)
	}

	s := mustScheduler(t, testPreset(t))
	items, err := NewQueueBuilder(db, store.NewDeckStore(db), s).Build(ctx, 1, QueueOptions{
		DeckID: deckID, Now: now, Location: time.UTC, NewPerDay: 50, ReviewsPerDay: 200,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(items) != 0 {
		t.Errorf("Build() = %+v, want empty (both cards buried)", items)
	}
}
