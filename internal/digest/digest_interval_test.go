package digest

import (
	"context"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// The two sides of the week boundary are different ledger keys, so hour-level collisions must be limited separately, and a skip must not consume the new week's quota.
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

// The minimum interval is not a one-week cooldown: having sent last week's digest four days ago must not block this week's backfill after downtime.
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
