package digest

import (
	"context"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// 周界前后属于不同账本键，必须另外限制小时级碰撞，且跳过时不能消耗新周额度。
func TestDigestMinIntervalBlocksWeekBoundary(t *testing.T) {
	db := newTestDB(t)
	id := seedUser(t, db, "boundary", "boundary@example.com", "en", "Asia/Shanghai", 4)
	if err := db.Model(&store.User{}).Where("id = ?", id).Update("reminder_hour", 0).Error; err != nil {
		t.Fatal(err)
	}
	setDigestPref(t, db, id, true)
	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 0, 0)
	w := newWorker(t, db, enq, func() time.Time { return clock })
	for _, tc := range []struct {
		offset time.Duration
		want   int
	}{
		{0, 1}, {4 * time.Hour, 1}, {20*time.Hour - time.Nanosecond, 1}, {20 * time.Hour, 2}, {21 * time.Hour, 2},
	} {
		clock = shanghaiAt(t, 0, 0).Add(tc.offset)
		if err := w.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := enq.count(); got != tc.want {
			t.Fatalf("after %v: enqueued %d, want %d", tc.offset, got, tc.want)
		}
	}
}

// 最小间隔不是一周冷却期：四天前发过上一周摘要，不应阻止本周宕机恢复补发。
func TestDigestMinIntervalAllowsRecoveryAfterFourDays(t *testing.T) {
	db := newTestDB(t)
	id := seedUser(t, db, "recovery", "recovery@example.com", "en", "Asia/Shanghai", 4)
	setDigestPref(t, db, id, true)
	clock := shanghaiAt(t, 19, 0)
	if err := store.RecordDigestSent(t.Context(), db, id, "2026-05-25", clock.Add(-4*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	enq := &fakeEnqueuer{configured: true}
	w := newWorker(t, db, enq, func() time.Time { return clock })
	if err := w.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if enq.count() != 1 {
		t.Fatalf("recovery enqueued %d, want 1", enq.count())
	}
}
