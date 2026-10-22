package store

import (
	"context"
	"testing"
	"time"
)

// TestLastReminderSentAtPicksMostRecent verifies the timestamp source of the minimum-interval gate: the newest SentAt across review days,
// and ok=false when nothing was ever sent. It runs on both drivers: this is the only basis the sender has for "how long since the last send".
func TestLastReminderSentAtPicksMostRecent(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()

			// Nothing ever sent: ok=false, not a zero time.
			if _, ok, err := LastReminderSentAt(ctx, db, 42); err != nil {
				t.Fatalf("LastReminderSentAt(before): %v", err)
			} else if ok {
				t.Fatal("LastReminderSentAt(before) ok = true, want false")
			}

			older := time.Date(2026, 6, 1, 19, 0, 0, 0, time.UTC)
			newer := older.Add(20 * time.Hour)
			// Write the newer day first and the older day second, to confirm it takes the maximum by sent_at, not by insertion order.
			if err := RecordReminderSent(ctx, db, 42, "2026-06-02", newer); err != nil {
				t.Fatalf("RecordReminderSent(newer): %v", err)
			}
			if err := RecordReminderSent(ctx, db, 42, "2026-06-01", older); err != nil {
				t.Fatalf("RecordReminderSent(older): %v", err)
			}
			// Another user's row must not be counted for this user.
			if err := RecordReminderSent(ctx, db, 43, "2026-06-02", newer.Add(time.Hour)); err != nil {
				t.Fatalf("RecordReminderSent(other user): %v", err)
			}

			got, ok, err := LastReminderSentAt(ctx, db, 42)
			if err != nil {
				t.Fatalf("LastReminderSentAt: %v", err)
			}
			if !ok {
				t.Fatal("LastReminderSentAt ok = false, want true")
			}
			if !got.Equal(newer) {
				t.Fatalf("LastReminderSentAt = %v, want %v (the most recent sent_at)", got, newer)
			}
		})
	}
}
