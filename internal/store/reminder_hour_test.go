package store

import (
	"context"
	"testing"
	"time"
)

// TestUserReminderHourRoundTrip 验证 users.reminder_hour 是**可空**列，三种状态互不混淆：
//   - NULL：从未设置 → 读回 nil（调用方回落到全局默认）；
//   - 显式 0：午夜是合法取值 → 读回 0，绝不能被当成 NULL；
//   - 显式非零：读回原值。
//
// 在 SQLite 与 PostgreSQL（TEST_PG_DSN 设置时）上跑同一份断言：两库都必须能把 NULL 与 0 分开。
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
					DayCutoffHour: 4, CreatedAt: now, ReminderHour: hour,
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

			// NULL：未设置。
			unset := reload(newUser("unset", nil))
			if unset.ReminderHour != nil {
				t.Fatalf("unset reminder_hour = %d, want NULL", *unset.ReminderHour)
			}

			// 显式 0：午夜，必须与 NULL 区分。
			midnight := reload(newUser("midnight", Ptr(0)))
			if midnight.ReminderHour == nil {
				t.Fatal("explicit 0 reminder_hour read back as NULL: midnight and unset are conflated")
			}
			if *midnight.ReminderHour != 0 {
				t.Fatalf("explicit 0 reminder_hour = %d, want 0", *midnight.ReminderHour)
			}

			// 改成非零后读回原值。
			midnight.ReminderHour = Ptr(19)
			if err := users.Update(ctx, midnight); err != nil {
				t.Fatalf("update to 19: %v", err)
			}
			if got := *reload(midnight).ReminderHour; got != 19 {
				t.Fatalf("after update reminder_hour = %d, want 19", got)
			}

			// 再改回未设置：UPDATE 必须把列写成 NULL，而不是残留旧值。
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
