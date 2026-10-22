package reminder

import (
	"context"
	"testing"
	"time"
)

// This file is the acceptance spec for the "minimum send interval" (MinSendInterval):
//   - when the chosen hour precedes the day cutoff (0 with cutoff 4), the tail of the previous review day and the head of the new review day are only hours apart;
//     after the first mail goes out, the second review day in the same calendar day does not send — the interval gate catches the back-to-back double send;
//   - after the interval passes (clock advanced 21 hours from the previous mail) another send is allowed;
//   - the normal every-other-day rhythm (a 19:00 user, two mails 24 hours apart) is unaffected.
//
// Everything uses real SQLite (newTestDB + store.LastReminderSentAt reading the SentAt column); the database is not mocked.

// TestMinSendIntervalBlocksBackToBackDoubleSend is the core acceptance: a 0/ cutoff-4 user no longer gets two mails in one day.
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

	// 06-02 00:00: belongs to the previous review day (06-01), send the first mail.
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 06-02 00:00: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("first send at 06-02 00:00: enqueued %d, want 1", got)
	}

	// 06-02 04:00: the day cutoff; this is the head of the new review day (06-02), only 4 hours apart —
	// the ledger, which counts by review day, cannot stop it; the minimum interval gate must.
	clock = localTimeAt(t, "Asia/Shanghai", 2, 4, 0)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 06-02 04:00: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("back-to-back second review day 4h later: enqueued %d, want 1 (min-interval gate)", got)
	}

	// 06-02 21:00: 21 hours since the first mail (>= 20h), the interval gate lets it through; 06-02 has no send yet, so send the second mail.
	clock = localTimeAt(t, "Asia/Shanghai", 2, 0, 0).Add(21 * time.Hour)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 06-02 21:00: %v", err)
	}
	if got := enq.count(); got != 2 {
		t.Fatalf("after the min interval elapsed (21h): enqueued %d, want 2", got)
	}
}

// TestMinSendIntervalLeavesDailyCadenceIntact verifies: the normal every-other-day rhythm (two mails 24 hours apart) is unaffected by the interval gate.
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

	// Same hour the next day: exactly 24 hours, far above the 20-hour minimum interval, sent as usual.
	clock = localTimeAt(t, "Asia/Shanghai", 3, 19, 0)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce day2 19:00: %v", err)
	}
	if got := enq.count(); got != 2 {
		t.Fatalf("day2 19:00 (24h later): enqueued %d, want 2", got)
	}
}
