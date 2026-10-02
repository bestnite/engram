package schedule

import (
	"context"
	"errors"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestSubmitWritesStateAndReviewInOneTransaction 断言一次提交同时写出 card_states 与
// reviews 两行，且 reviews 的每个 §2.2 字段都被写全（AGENTS.md §2.3 第 2 条）。
func TestSubmitWritesStateAndReviewInOneTransaction(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	cardID := seedCard(t, db, deckID, "forward", now)
	s := mustScheduler(t, testPreset(t))
	elapsed := 4200

	tx := db.Begin()
	res, err := Submit(ctx, tx, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: 0,
		ElapsedMS: &elapsed, Scheduler: s, Now: now, Location: time.UTC,
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit: %v", err)
	}

	var st store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, 1).First(&st).Error; err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.Version != 1 {
		t.Errorf("state version = %d, want 1 (optimistic lock advanced)", st.Version)
	}
	if st.State != StateLearning.String() {
		t.Errorf("state = %q, want %q (new+Good stays learning)", st.State, StateLearning)
	}
	if st.DueAt == nil || !st.DueAt.After(now) {
		t.Errorf("state due_at = %v, want a future time", st.DueAt)
	}
	if st.Reps != 1 || st.Lapses != 0 {
		t.Errorf("state reps/lapses = %d/%d, want 1/0", st.Reps, st.Lapses)
	}

	var rv store.Review
	if err := db.First(&rv, res.ReviewID).Error; err != nil {
		t.Fatalf("load review %d: %v", res.ReviewID, err)
	}
	if rv.CardID != cardID || rv.UserID != 1 {
		t.Errorf("review target = (%d,%d), want (%d,1)", rv.CardID, rv.UserID, cardID)
	}
	if rv.Rating != int(Good) {
		t.Errorf("review rating = %d, want %d", rv.Rating, int(Good))
	}
	if rv.GradeSource != GradeSourceSelf {
		t.Errorf("review grade_source = %q, want %q", rv.GradeSource, GradeSourceSelf)
	}
	if rv.StateBefore != int(StateNew) {
		t.Errorf("review state_before = %d, want %d (new card)", rv.StateBefore, int(StateNew))
	}
	if !rv.ReviewedAt.Equal(now) {
		t.Errorf("review reviewed_at = %v, want %v", rv.ReviewedAt, now)
	}
	if rv.ReviewDay != "2026-10-02" {
		t.Errorf("review review_day = %q, want 2026-10-02", rv.ReviewDay)
	}
	if rv.ElapsedMS == nil || *rv.ElapsedMS != elapsed {
		t.Errorf("review elapsed_ms = %v, want %d", rv.ElapsedMS, elapsed)
	}
	if rv.IntervalDays == nil || rv.Stability == nil || rv.Difficulty == nil {
		t.Errorf("review interval/stability/difficulty must all be written: %v/%v/%v",
			rv.IntervalDays, rv.Stability, rv.Difficulty)
	}
	if rv.DurationDays != nil {
		t.Errorf("review duration_days = %v, want nil (first review has no previous)", *rv.DurationDays)
	}
}

// TestSubmitDuplicateReturnsVersionConflict 断言重复提交同一 expected_version 时，
// 第二次返回 ErrVersionConflict，且不改变任何一行（DESIGN.md §3.4、M3-3 验收）。
func TestSubmitDuplicateReturnsVersionConflict(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	cardID := seedCard(t, db, deckID, "forward", now)
	s := mustScheduler(t, testPreset(t))
	in := SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: 0,
		Scheduler: s, Now: now, Location: time.UTC,
	}

	tx := db.Begin()
	if _, err := Submit(ctx, tx, in); err != nil {
		t.Fatalf("first Submit() error = %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit first: %v", err)
	}

	var before store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, 1).First(&before).Error; err != nil {
		t.Fatalf("load state before: %v", err)
	}
	var reviewsBefore int64
	if err := db.Model(&store.Review{}).Count(&reviewsBefore).Error; err != nil {
		t.Fatalf("count reviews before: %v", err)
	}

	// 重放：同一个 expected_version（0），此时库里已是 1 → 必须 409。
	tx2 := db.Begin()
	_, err := Submit(ctx, tx2, in)
	tx2.Rollback()
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("duplicate Submit() error = %v, want ErrVersionConflict", err)
	}

	var after store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, 1).First(&after).Error; err != nil {
		t.Fatalf("load state after: %v", err)
	}
	var reviewsAfter int64
	if err := db.Model(&store.Review{}).Count(&reviewsAfter).Error; err != nil {
		t.Fatalf("count reviews after: %v", err)
	}
	if reviewsAfter != reviewsBefore {
		t.Errorf("review count = %d, want %d (a conflicting submit must not write)", reviewsAfter, reviewsBefore)
	}
	if after.Version != before.Version || after.State != before.State ||
		after.Reps != before.Reps || !after.DueAt.Equal(*before.DueAt) {
		t.Errorf("state changed on conflict: before=%+v after=%+v", before, after)
	}
}

// TestSubmitRollbackLeavesNoReviewRow 断言状态更新与 review 行共用调用方的事务：
// 事务回滚后两行都不存在（M3-3 验收：“事务失败后不留 reviews 行”）。
func TestSubmitRollbackLeavesNoReviewRow(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	cardID := seedCard(t, db, deckID, "forward", now)
	s := mustScheduler(t, testPreset(t))

	tx := db.Begin()
	if _, err := Submit(ctx, tx, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Easy, ExpectedVersion: 0,
		Scheduler: s, Now: now, Location: time.UTC,
	}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	// 模拟外层事务失败：不 commit，直接回滚。
	if err := tx.Rollback().Error; err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var reviews int64
	if err := db.Model(&store.Review{}).Count(&reviews).Error; err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if reviews != 0 {
		t.Errorf("reviews = %d, want 0 after rollback", reviews)
	}
	var states int64
	if err := db.Model(&store.CardState{}).Count(&states).Error; err != nil {
		t.Fatalf("count states: %v", err)
	}
	if states != 0 {
		t.Errorf("card_states = %d, want 0 after rollback", states)
	}
}

// TestSubmitRejectsInvalidInput 覆盖入参校验的拒绝路径。
func TestSubmitRejectsInvalidInput(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	cardID := seedCard(t, db, deckID, "forward", now)
	s := mustScheduler(t, testPreset(t))

	// nil 事务单独验证：Submit 必须拒绝没有事务边界的调用。
	if _, err := Submit(ctx, nil, SubmitInput{CardID: cardID, UserID: 1, Rating: Good, Scheduler: s}); err == nil {
		t.Error("Submit(nil tx) error = nil, want error")
	}
	cases := []struct {
		name string
		in   SubmitInput
	}{
		{"nil scheduler", SubmitInput{CardID: cardID, UserID: 1, Rating: Good}},
		{"zero card", SubmitInput{UserID: 1, Rating: Good, Scheduler: s}},
		{"bad rating", SubmitInput{CardID: cardID, UserID: 1, Rating: 9, Scheduler: s}},
		{"bad grade source", SubmitInput{CardID: cardID, UserID: 1, Rating: Good, GradeSource: "robot", Scheduler: s}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := db.Begin()
			defer tx.Rollback()
			if _, err := Submit(ctx, tx, tc.in); err == nil {
				t.Errorf("Submit(%s) error = nil, want error", tc.name)
			}
		})
	}
}
