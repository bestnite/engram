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

// TestSubmitConcurrentFirstReviewIsAtomic 是 M3-10 的验收用例：在真实 PostgreSQL 上
// 并发发起两次同一张全新卡的首评，断言最终恰好一条 reviews 行与一条 card_states 行存活，
// 且输家收到冲突哨兵（ErrVersionConflict）。
//
// 为什么必须用真 PG：SQLite 靠单写连接把写入串行化，两次“读不到状态行”的首评不可能真正
// 并发；PG 下行锁锁不住尚不存在的行，修复前两个事务都能通过版本校验并各写一条 review
// （双写）。用例通过 pgtest 拿一个独立 schema，跑完即丢。
//
// 为什么不能让两个 goroutine 同时开跑：那样赢家往往在输家读状态之前就提交了，输家会从
// 常规版本校验处被拦下，于是既测不出守卫、也复现不了竞态。这里用一次同步把写窗口摆正：
// 赢家先插入但不提交，等输家进入写路径后再提交；输家此时读到的是“没有这一行”（未提交
// 的行对 READ COMMITTED 不可见），INSERT 会在唯一索引上阻塞等待赢家。这正是修复前双双
// 放行的窗口：
//   - 修复前：无守卫的 DO UPDATE 在赢家提交后照常命中，输家也写入 —— 两条 review。
//   - 修复后：DO UPDATE 的 WHERE card_states.version = 0 在赢家提交后不匹配，影响 0 行，
//     输家返回冲突哨兵且不写任何 review。
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

	winnerInserted := make(chan struct{}) // 赢家已写入（未提交）
	loserWriting := make(chan struct{})   // 输家已进入写路径
	var (
		wg                  sync.WaitGroup
		winnerErr, loserErr error
	)

	// 赢家：写入后停住不提交，把窗口留给输家。
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
		// 给输家足够时间读到“没有这一行”并在 INSERT 上阻塞；之后赢家才提交，
		// 让输家的 DO UPDATE 必须自己决定写不写得进去。
		time.Sleep(300 * time.Millisecond)
		winnerErr = tx.Commit().Error
	}()

	// 输家：等赢家写入后开跑，两个事务因此真正重叠。
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
