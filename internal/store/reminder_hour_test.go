package store

import (
	"context"
	"testing"
	"time"
)

// TestUserReminderHourRoundTrip verifies users.reminder_hour is a **nullable** column whose three states stay distinct:
//   - NULL: never set -> reads back nil (the caller falls back to the global default);
//   - an explicit 0: midnight is a legal value -> reads back 0 and must never be taken for NULL;
//   - an explicit non-zero: reads back the original value.
//
// The same assertions run on SQLite and PostgreSQL (when TEST_PG_DSN is set): both databases must tell NULL from 0.
func TestUserReminderHourRoundTrip(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := NewUserStore(db)
			now := time.Now().UTC()

			newUser := func(name string, hour *int) *User {
				u := &User{
					Username: name, Email: name + "@example.com", DisplayName: name,
					Role: RoleUser, Status: StatusActive, Locale: "en", Timezone: "UTC",
					DayCutoffHour: Ptr(4), CreatedAt: now, ReminderHour: hour,
				}
				if err := users.Create(ctx, u); err != nil {
					t.Fatalf("create user %s: %v", name, err)
				}
				return u
			}
			reload := func(u *User) *User {
				got, err := users.ByID(ctx, u.ID)
				if err != nil {
					t.Fatalf("reload user %s: %v", u.Username, err)
				}
				return got
			}

			// NULL: not set.
			unset := reload(newUser("unset", nil))
			if unset.ReminderHour != nil {
				t.Fatalf("unset reminder_hour = %d, want NULL", *unset.ReminderHour)
			}

			// Explicit 0: midnight, must be distinguished from NULL.
			midnight := reload(newUser("midnight", Ptr(0)))
			if midnight.ReminderHour == nil {
				t.Fatal("explicit 0 reminder_hour read back as NULL: midnight and unset are conflated")
			}
			if *midnight.ReminderHour != 0 {
				t.Fatalf("explicit 0 reminder_hour = %d, want 0", *midnight.ReminderHour)
			}

			// After changing to a non-zero value it reads back the original.
			midnight.ReminderHour = Ptr(19)
			if err := users.Update(ctx, midnight); err != nil {
				t.Fatalf("update to 19: %v", err)
			}
			if got := *reload(midnight).ReminderHour; got != 19 {
				t.Fatalf("after update reminder_hour = %d, want 19", got)
			}

			// Changing back to unset: UPDATE must write NULL into the column rather than leaving the old value behind.
			midnight.ReminderHour = nil
			if err := users.Update(ctx, midnight); err != nil {
				t.Fatalf("update back to NULL: %v", err)
			}
			if got := reload(midnight).ReminderHour; got != nil {
				t.Fatalf("after clearing reminder_hour = %d, want NULL", *got)
			}
		})
	}
}
