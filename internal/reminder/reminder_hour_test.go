package reminder

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// This file is the acceptance spec (supplementary) for "per-user configurable send time":
//   - user sets 19:00 → only sends at local 19:00, the 7am tick does not send;
//   - unset (NULL) → falls back to the global default 19:00;
//   - explicitly set to 0 → sends at midnight, and its behaviour is distinguishable from "unset";
//   - only one send per review day (no repeats across ticks);
//   - the send instant is interpreted in **the user's timezone**.
//
// Everything uses real SQLite (newTestDB), writes go through store.UserStore, reads go through the candidate query; the database is not mocked.

// intHour returns an hour pointer: an explicitly set send hour must be a pointer, since 0 is a valid value (midnight).
func intHour(h int) *int { return &h }

// setUserSendHour writes users.reminder_hour directly in the DB; nil means unset (falls back to the global default).
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

// localTimeAt returns the UTC instant for the tz-local time "2026-06-DD hh:mm".
func localTimeAt(t *testing.T, tz string, day, hour, min int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", tz, err)
	}
	return time.Date(2026, 6, day, hour, min, 0, 0, loc).UTC()
}

// TestUserSendHourGatesDelivery is the core acceptance: sends only once the local hour is due; unset (NULL) falls back to the global default 19:00.
//
// Both variants walk the same ladder (07:00 no send → 18:59 no send → 19:00 send), differing only in whether users.reminder_hour
// is an explicit 19 or NULL, so they are merged into one table instead of copying the same ladder twice.
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
			// nil writes nothing to the DB: users.reminder_hour stays NULL, which is exactly the "unset" branch.
			if tc.hour != nil {
				setUserSendHour(t, db, userID, tc.hour)
			}

			enq := &fakeEnqueuer{configured: true}
			clock := localTimeAt(t, "Asia/Shanghai", 2, 7, 0)
			r := newReminder(t, db, enq, func() time.Time { return clock })
			ctx := context.Background()

			// 07:00: earlier than the send time, no send.
			if err := r.RunOnce(ctx); err != nil {
				t.Fatalf("RunOnce at 07:00: %v", err)
			}
			if got := enq.count(); got != 0 {
				t.Fatalf("tick at local 07:00 before the chosen hour: enqueued %d, want 0", got)
			}

			// 18:59: still not due.
			clock = localTimeAt(t, "Asia/Shanghai", 2, 18, 59)
			if err := r.RunOnce(ctx); err != nil {
				t.Fatalf("RunOnce at 18:59: %v", err)
			}
			if got := enq.count(); got != 0 {
				t.Fatalf("tick at local 18:59 before the chosen hour: enqueued %d, want 0", got)
			}

			// 19:00: due, send one.
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

// TestExplicitMidnightIsDistinctFromUnset verifies two things:
//  1. resolution layer: nil → default 19, explicit 0 → 0 (0 is not treated as "unset");
//  2. behaviour layer: on the same midnight tick, the user with explicit 0 gets mail while the unset user does not — 0 and NULL really are separate.
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
	// Shanghai local 2026-06-02 00:00 (midnight).
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

// TestSendHourOncePerReviewDayAcrossTicks verifies: within one review day, multiple ticks after the hour is due send only one mail;
// the next mail goes out only after crossing into a new review day and the hour being due again.
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

	// Later tick within the same review day (cutoff 4): the ledger already has an entry, so no repeat.
	clock = localTimeAt(t, "Asia/Shanghai", 2, 23, 30)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 06-02 23:30: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("second tick same review day: enqueued %d, want 1", got)
	}

	// Early morning of the new review day: not yet 19:00, no send.
	clock = localTimeAt(t, "Asia/Shanghai", 3, 7, 0)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 06-03 07:00: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("next review day before the chosen hour: enqueued %d, want 1", got)
	}

	// The new review day reaches the hour: send the second mail.
	clock = localTimeAt(t, "Asia/Shanghai", 3, 19, 0)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 06-03 19:00: %v", err)
	}
	if got := enq.count(); got != 2 {
		t.Fatalf("next review day at the chosen hour: enqueued %d, want 2", got)
	}
}

// TestSendHourFollowsUserTimezone verifies: the send instant is interpreted in each user's own timezone.
// At the same UTC instant, a Shanghai user who has reached 19:00 gets mail while a New York user who has not does not.
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
	// UTC 2026-06-02 11:00 = Shanghai 19:00 (due), New York 07:00 (not due).
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

	// UTC 2026-06-02 23:00 = Shanghai 07:00 next day (not due), New York 19:00 (due).
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
