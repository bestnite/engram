package reminder

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是「每用户可设发送时间」的验收（补充规格）：
//   - 用户设定 19 点 → 本地 19 点才发，凌晨 7 点的 tick 不发；
//   - 未设置（NULL）→ 走全局默认 19 点；
//   - 显式设为 0 点 → 午夜发，且与「未设置」行为可区分；
//   - 同一个复习日只发一次（跨 tick 不重复）；
//   - 发送时刻按**用户时区**解释。
//
// 全部用真实 SQLite（newTestDB），写入走 store.UserStore，读取走候选查询，不 mock 数据库。

// intHour 返回小时指针：显式设置发送小时必须用指针，0 是合法值（午夜）。
func intHour(h int) *int { return &h }

// setUserSendHour 直接改库里的 users.reminder_hour；nil 表示未设置（回落到全局默认）。
func setUserSendHour(t *testing.T, db *gorm.DB, userID uint64, hour *int) {
	t.Helper()
	users := store.NewUserStore(db)
	u, err := users.ByID(context.Background(), userID)
	if err != nil {
		t.Fatalf("load user %d: %v", userID, err)
	}
	u.ReminderHour = hour
	if err := users.Update(context.Background(), u); err != nil {
		t.Fatalf("update user %d: %v", userID, err)
	}
}

// localTimeAt 返回 tz 本地时间「2026-06-DD hh:mm」对应的 UTC 时刻。
func localTimeAt(t *testing.T, tz string, day, hour, min int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", tz, err)
	}
	return time.Date(2026, 6, day, hour, min, 0, 0, loc).UTC()
}

// TestUserSendHourGatesDelivery 是核心验收：只有本地到点才发，未设置（NULL）回落全局默认 19 点。
//
// 两档走的是同一段阶梯（07:00 不发 → 18:59 不发 → 19:00 发），差别只在 users.reminder_hour
// 是显式 19 还是 NULL，所以合成一张表跑，不把同一段阶梯抄两遍。
func TestUserSendHourGatesDelivery(t *testing.T) {
	if DefaultSendHour != 19 {
		t.Fatalf("DefaultSendHour = %d, want 19", DefaultSendHour)
	}
	cases := []struct {
		name string
		hour *int
	}{
		{name: "explicit 19", hour: intHour(19)},
		{name: "unset falls back to the default 19", hour: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			userID := seedUserWithDueCard(t, db, "alice", "alice@example.com", "en", "Asia/Shanghai", 4,
				time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
			enableReminder(t, db, userID)
			// nil 不写库：users.reminder_hour 保持 NULL，走的正是「未设置」分支。
			if tc.hour != nil {
				setUserSendHour(t, db, userID, tc.hour)
			}

			enq := &fakeEnqueuer{configured: true}
			clock := localTimeAt(t, "Asia/Shanghai", 2, 7, 0)
			r := newReminder(t, db, enq, func() time.Time { return clock })
			ctx := context.Background()

			// 07:00：早于发送时间，不发。
			if err := r.RunOnce(ctx); err != nil {
				t.Fatalf("RunOnce at 07:00: %v", err)
			}
			if got := enq.count(); got != 0 {
				t.Fatalf("tick at local 07:00 before the chosen hour: enqueued %d, want 0", got)
			}

			// 18:59：仍未到点。
			clock = localTimeAt(t, "Asia/Shanghai", 2, 18, 59)
			if err := r.RunOnce(ctx); err != nil {
				t.Fatalf("RunOnce at 18:59: %v", err)
			}
			if got := enq.count(); got != 0 {
				t.Fatalf("tick at local 18:59 before the chosen hour: enqueued %d, want 0", got)
			}

			// 19:00：到点，发一封。
			clock = localTimeAt(t, "Asia/Shanghai", 2, 19, 0)
			if err := r.RunOnce(ctx); err != nil {
				t.Fatalf("RunOnce at 19:00: %v", err)
			}
			if got := enq.count(); got != 1 {
				t.Fatalf("tick at local 19:00 at the chosen hour: enqueued %d, want 1", got)
			}
		})
	}
}

// TestExplicitMidnightIsDistinctFromUnset 验收两件事：
//  1. 解析层：nil → 默认 19，显式 0 → 0（0 不被当成「未设置」）；
//  2. 行为层：同一个午夜 tick，显式 0 点的用户发信，未设置的用户不发——0 与 NULL 真的分开。
func TestExplicitMidnightIsDistinctFromUnset(t *testing.T) {
	if got := SendHour(nil); got != DefaultSendHour {
		t.Fatalf("SendHour(nil) = %d, want the default %d", got, DefaultSendHour)
	}
	if got := SendHour(intHour(0)); got != 0 {
		t.Fatalf("SendHour(explicit 0) = %d, want 0", got)
	}

	db := newTestDB(t)
	due := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	midnight := seedUserWithDueCard(t, db, "carol", "carol@example.com", "en", "Asia/Shanghai", 4, due)
	enableReminder(t, db, midnight)
	setUserSendHour(t, db, midnight, intHour(0))

	unset := seedUserWithDueCard(t, db, "dave", "dave@example.com", "en", "Asia/Shanghai", 4, due)
	enableReminder(t, db, unset)

	enq := &fakeEnqueuer{configured: true}
	// 上海本地 2026-06-02 00:00（午夜）。
	clock := localTimeAt(t, "Asia/Shanghai", 2, 0, 0)
	r := newReminder(t, db, enq, func() time.Time { return clock })

	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce at midnight: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("midnight tick: enqueued %d, want 1 (only the explicit-0 user)", got)
	}
	if got := enq.last().To; got != "carol@example.com" {
		t.Fatalf("midnight tick went to %q, want carol@example.com (the explicit-0 user)", got)
	}
}

// TestSendHourOncePerReviewDayAcrossTicks 验收：同一复习日内在到点后多轮 tick 只发一封；
// 跨到新的复习日、且再次到点后才发下一封。
func TestSendHourOncePerReviewDayAcrossTicks(t *testing.T) {
	db := newTestDB(t)
	userID := seedUserWithDueCard(t, db, "erin", "erin@example.com", "en", "Asia/Shanghai", 4,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	enableReminder(t, db, userID)
	setUserSendHour(t, db, userID, intHour(19))

	enq := &fakeEnqueuer{configured: true}
	clock := localTimeAt(t, "Asia/Shanghai", 2, 19, 5)
	r := newReminder(t, db, enq, func() time.Time { return clock })
	ctx := context.Background()

	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 06-02 19:05: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("first tick: enqueued %d, want 1", got)
	}

	// 同一复习日（切点 4）内的后续 tick：台账已存在，不重复。
	clock = localTimeAt(t, "Asia/Shanghai", 2, 23, 30)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 06-02 23:30: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("second tick same review day: enqueued %d, want 1", got)
	}

	// 新复习日的凌晨：未到 19 点，不发。
	clock = localTimeAt(t, "Asia/Shanghai", 3, 7, 0)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 06-03 07:00: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("next review day before the chosen hour: enqueued %d, want 1", got)
	}

	// 新复习日到点：发第二封。
	clock = localTimeAt(t, "Asia/Shanghai", 3, 19, 0)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 06-03 19:00: %v", err)
	}
	if got := enq.count(); got != 2 {
		t.Fatalf("next review day at the chosen hour: enqueued %d, want 2", got)
	}
}

// TestSendHourFollowsUserTimezone 验收：发送时刻按用户各自时区解释。
// 同一个 UTC 时刻，上海用户已到 19 点则发，纽约用户还差着则不发。
func TestSendHourFollowsUserTimezone(t *testing.T) {
	db := newTestDB(t)
	due := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	shanghai := seedUserWithDueCard(t, db, "sh", "sh@example.com", "en", "Asia/Shanghai", 4, due)
	enableReminder(t, db, shanghai)
	setUserSendHour(t, db, shanghai, intHour(19))
	newYork := seedUserWithDueCard(t, db, "ny", "ny@example.com", "en", "America/New_York", 4, due)
	enableReminder(t, db, newYork)
	setUserSendHour(t, db, newYork, intHour(19))

	enq := &fakeEnqueuer{configured: true}
	// UTC 2026-06-02 11:00 = 上海 19:00（到点），纽约 07:00（未到）。
	clock := time.Date(2026, 6, 2, 11, 0, 0, 0, time.UTC)
	r := newReminder(t, db, enq, func() time.Time { return clock })

	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce at 11:00 UTC: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("11:00 UTC: enqueued %d, want 1 (only the Shanghai user)", got)
	}
	if got := enq.last().To; got != "sh@example.com" {
		t.Fatalf("11:00 UTC went to %q, want sh@example.com", got)
	}

	// UTC 2026-06-02 23:00 = 上海次日 07:00（未到），纽约 19:00（到点）。
	clock = time.Date(2026, 6, 2, 23, 0, 0, 0, time.UTC)
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce at 23:00 UTC: %v", err)
	}
	if got := enq.count(); got != 2 {
		t.Fatalf("23:00 UTC: enqueued %d, want 2 (the New York user now at 19:00)", got)
	}
	if got := enq.last().To; got != "ny@example.com" {
		t.Fatalf("23:00 UTC went to %q, want ny@example.com", got)
	}
}
