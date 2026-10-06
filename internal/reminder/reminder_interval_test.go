package reminder

import (
	"context"
	"testing"
	"time"
)

// 本文件是「最小发送间隔」（MinSendInterval）的验收：
//   - 所选小时早于日切点时（0 点 / 切点 4 点），前一个复习日尾与新复习日头只隔几小时，
//     第一封发出后，同一自然日里第二个复习日不再发——间隔门把贴身双发拦住；
//   - 间隔过后（时钟从上一封起推进 21 小时）可以再发；
//   - 正常的隔日节律（19 点用户，两封相隔 24 小时）不受影响。
//
// 全部用真实 SQLite（newTestDB + store.LastReminderSentAt 读 SentAt 列），不 mock 数据库。

// TestMinSendIntervalConstant 钉住阈值取值：改小它就会放行贴身双发，是本文件的负向对照锚点。
func TestMinSendIntervalConstant(t *testing.T) {
	if MinSendInterval != 20*time.Hour {
		t.Fatalf("MinSendInterval = %v, want 20h", MinSendInterval)
	}
}

// TestMinSendIntervalBlocksBackToBackDoubleSend 是核心验收：0 点/切点 4 点的用户不再一天两封。
func TestMinSendIntervalBlocksBackToBackDoubleSend(t *testing.T) {
	db := newTestDB(t)
	userID := seedUserWithDueCard(t, db, "midnight", "midnight@example.com", "en", "Asia/Shanghai", 4,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	enableReminder(t, db, userID)
	setUserSendHour(t, db, userID, intHour(0))

	enq := &fakeEnqueuer{configured: true}
	clock := localTimeAt(t, "Asia/Shanghai", 2, 0, 0)
	r := newReminder(t, db, enq, func() time.Time { return clock })
	ctx := context.Background()

	// 06-02 00:00：属于前一个复习日（06-01），发第一封。
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 06-02 00:00: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("first send at 06-02 00:00: enqueued %d, want 1", got)
	}

	// 06-02 04:00：日切点，这里是新复习日（06-02）的头部，只隔 4 小时——
	// 台账按复习日计拦不住它，必须由最小间隔门拦住。
	clock = localTimeAt(t, "Asia/Shanghai", 2, 4, 0)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 06-02 04:00: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("back-to-back second review day 4h later: enqueued %d, want 1 (min-interval gate)", got)
	}

	// 06-02 21:00：距第一封 21 小时（>= 20h），间隔门放行；06-02 尚未发过，发第二封。
	clock = localTimeAt(t, "Asia/Shanghai", 2, 0, 0).Add(21 * time.Hour)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 06-02 21:00: %v", err)
	}
	if got := enq.count(); got != 2 {
		t.Fatalf("after the min interval elapsed (21h): enqueued %d, want 2", got)
	}
}

// TestMinSendIntervalLeavesDailyCadenceIntact 验收：正常的隔日节律（两封相隔 24 小时）不受间隔门影响。
func TestMinSendIntervalLeavesDailyCadenceIntact(t *testing.T) {
	db := newTestDB(t)
	userID := seedUserWithDueCard(t, db, "evening", "evening@example.com", "en", "Asia/Shanghai", 4,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	enableReminder(t, db, userID)
	setUserSendHour(t, db, userID, intHour(19))

	enq := &fakeEnqueuer{configured: true}
	clock := localTimeAt(t, "Asia/Shanghai", 2, 19, 0)
	r := newReminder(t, db, enq, func() time.Time { return clock })
	ctx := context.Background()

	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce day1 19:00: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("day1 19:00: enqueued %d, want 1", got)
	}

	// 次日同一小时：正好 24 小时，远大于 20 小时的最小间隔，照常发。
	clock = localTimeAt(t, "Asia/Shanghai", 3, 19, 0)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce day2 19:00: %v", err)
	}
	if got := enq.count(); got != 2 {
		t.Fatalf("day2 19:00 (24h later): enqueued %d, want 2", got)
	}
}
