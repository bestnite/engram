package store

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"
)

// seedReminderUser creates an active user with one due card attached; suspended=true suspends the card.
// dueAt decides whether it is "due" (passing a past time makes it due).
func seedReminderUser(t *testing.T, db *gorm.DB, name, email string, dueAt time.Time, suspended bool) uint64 {
	t.Helper()
	now := time.Now().UTC()
	user := User{Username: name, Email: email, DisplayName: name, Role: RoleUser,
		Status: StatusActive, Locale: "en", Timezone: "UTC", DayCutoffHour: Ptr(4), CreatedAt: now}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	fuzz := true
	preset := Preset{OwnerUserID: user.ID, Name: "default", DesiredRetention: 0.9,
		LearningSteps: "1m,10m", RelearningSteps: "10m", MaximumIntervalDays: 36500,
		EnableFuzz: &fuzz, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&preset).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := Deck{OwnerUserID: user.ID, Name: "Deck", Description: "",
		NewPerDay: 20, ReviewsPerDay: 200, PresetID: preset.ID, CreatedAt: now}
	if err := db.Create(&deck).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	note := Note{DeckID: deck.ID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`,
		TagsJSON: "[]", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&note).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	card := Card{NoteID: note.ID, Template: "forward", CreatedAt: now}
	if err := db.Create(&card).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	due := dueAt.UTC()
	state := CardState{CardID: card.ID, UserID: user.ID, State: "review", DueAt: &due}
	if suspended {
		s := now
		state.SuspendedAt = &s
	}
	if err := db.Create(&state).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}
	return user.ID
}

// TestReminderLogIsUniquePerUserDay verifies the ledger is unique per (user_id, day), and a duplicate record neither errors nor adds a row.
func TestReminderLogIsUniquePerUserDay(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			now := time.Now().UTC()

			first, err := HasReminderBeenSent(ctx, db, 7, "2026-06-01")
			if err != nil {
				t.Fatalf("HasReminderBeenSent(before): %v", err)
			}
			if first {
				t.Fatal("HasReminderBeenSent(before) = true, want false")
			}

			if err := RecordReminderSent(ctx, db, 7, "2026-06-01", now); err != nil {
				t.Fatalf("RecordReminderSent: %v", err)
			}
			// Recording the same day twice: skipped silently (DoNothing), no error.
			if err := RecordReminderSent(ctx, db, 7, "2026-06-01", now); err != nil {
				t.Fatalf("RecordReminderSent(dup): %v", err)
			}

			var rows int64
			if err := db.Model(&ReminderLog{}).Where("user_id = ?", 7).Count(&rows).Error; err != nil {
				t.Fatalf("count reminder_log: %v", err)
			}
			if rows != 1 {
				t.Fatalf("reminder_log rows for user 7 = %d, want 1", rows)
			}

			sent, err := HasReminderBeenSent(ctx, db, 7, "2026-06-01")
			if err != nil {
				t.Fatalf("HasReminderBeenSent(after): %v", err)
			}
			if !sent {
				t.Fatal("HasReminderBeenSent(after) = false, want true")
			}
			other, err := HasReminderBeenSent(ctx, db, 7, "2026-06-02")
			if err != nil {
				t.Fatalf("HasReminderBeenSent(other day): %v", err)
			}
			if other {
				t.Fatal("HasReminderBeenSent(other day) = true, want false")
			}
		})
	}
}

// TestReminderCandidatesOnlyReturnsDueActiveUsers verifies the candidates hold only active users with due cards,
// excluding suspended cards, cards not yet due, buried new cards and disabled accounts. It runs on both drivers: the candidate query's GROUP BY + JOIN is a hotspot for cross-database differences.
func TestReminderCandidatesOnlyReturnsDueActiveUsers(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			now := time.Now().UTC()
			past := now.Add(-24 * time.Hour)
			future := now.Add(72 * time.Hour)

			due := seedReminderUser(t, db, "due", "due@example.com", past, false)
			seedReminderUser(t, db, "suspended", "suspended@example.com", past, true)
			seedReminderUser(t, db, "future", "future@example.com", future, false)
			// A buried new card keeps state=new with a due_at that has passed; the queue treats it as a new card, so no reminder.
			buried := seedReminderUser(t, db, "buried", "buried@example.com", past, false)
			if err := db.Model(&CardState{}).Where("user_id = ?", buried).Update("state", "new").Error; err != nil {
				t.Fatalf("mark state new: %v", err)
			}
			// A disabled user should not be nudged even with due cards.
			disabled := seedReminderUser(t, db, "disabled", "disabled@example.com", past, false)
			if err := db.Model(&User{}).Where("id = ?", disabled).Update("status", StatusDisabled).Error; err != nil {
				t.Fatalf("disable user: %v", err)
			}

			got, err := ReminderCandidates(ctx, db, now)
			if err != nil {
				t.Fatalf("ReminderCandidates: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("ReminderCandidates returned %d candidates, want 1", len(got))
			}
			if got[0].ID != due {
				t.Errorf("candidate id = %d, want %d", got[0].ID, due)
			}
			if got[0].Email != "due@example.com" {
				t.Errorf("candidate email = %q, want due@example.com", got[0].Email)
			}
			if got[0].DueCount != 1 {
				t.Errorf("candidate due_count = %d, want 1", got[0].DueCount)
			}
			if got[0].DayCutoffHour == nil || *got[0].DayCutoffHour != 4 {
				t.Errorf("candidate day_cutoff_hour = %d, want 4", got[0].DayCutoffHour)
			}
		})
	}
}
