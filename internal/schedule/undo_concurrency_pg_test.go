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

// TestUndoConcurrentSameVersionIsAtomic 是 AUDIT-03 的 PostgreSQL 验收：两个窗口用同一
// 目标版本并发撤销同一张卡，恰好一个成功、另一个收到冲突哨兵；状态、评分日志与审计记录
// 三者一致（评分只少一条、审计只多一条、状态版本只前进一步）。
//
// 为什么必须用真 PG：Rollback 依赖 card_states 的行锁（FOR UPDATE）把「读状态 + 读日志 +
// 写状态 + 删日志」串行化。SQLite 靠单写连接串行化，掩盖了「先读日志再锁状态」时的竞态窗口。
// 修复后顺序为「先锁状态并校验版本、再读日志」，两个事务因此被行锁排成先后：赢家删日志并把
// 版本推进到 3，输家拿到锁后读到版本 3 ≠ 期望 2，返回 ErrVersionConflict 且什么都不写。
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
