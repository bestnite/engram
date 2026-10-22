package store

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

// The default, midnight and an ordinary hour must stay consistent through persistence and optimizer output.
func TestNullableCutoffRoundTripAndOptimizer(t *testing.T) {
	db := newSQLite(t)
	if err := AutoMigrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	for i, hour := range []*int{nil, Ptr(0), Ptr(4), Ptr(23)} {
		u := User{Username: []string{"unset", "midnight", "default", "late"}[i], Email: []string{"unset@example.com", "midnight@example.com", "default@example.com", "late@example.com"}[i], DisplayName: "test", Role: RoleUser, Status: StatusActive, Locale: "en", Timezone: "UTC", DayCutoffHour: hour, CreatedAt: time.Now().UTC()}
		s := NewUserStore(db)
		if err := s.Create(t.Context(), &u); err != nil {
			t.Fatal(err)
		}
		got, err := s.ByID(t.Context(), u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if hour == nil {
			if got.DayCutoffHour != nil {
				t.Fatal("unset cutoff was replaced by a database default")
			}
		} else if got.DayCutoffHour == nil || *got.DayCutoffHour != *hour {
			t.Fatal("explicit cutoff lost")
		}
		// The log content stays as it is; the export only uses the currently effective setting and does not rewrite historical review days.
		presetID, cards := seedPresetCards(t, db, u.ID, 1)
		r := Review{CardID: cards[0], UserID: u.ID, Rating: 3, StateBefore: 2, GradeSource: "self", ReviewedAt: time.Now().UTC(), ReviewDay: "2020-01-01"}
		if err := db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if err := NewReviewStore(db).ExportOptimizerLog(t.Context(), u.ID, presetID, &b); err != nil {
			t.Fatal(err)
		}
		var log OptimizerReviewLog
		if err := json.Unmarshal(bytes.TrimSpace(b.Bytes()), &log); err != nil {
			t.Fatal(err)
		}
		want := []int{4, 0, 4, 23}[i]
		if log.DayStart != want {
			t.Fatalf("export day_start=%d, want %d", log.DayStart, want)
		}
		if err := db.First(&r, r.ID).Error; err != nil {
			t.Fatal(err)
		}
		if r.ReviewDay != "2020-01-01" {
			t.Fatal("historical review day changed")
		}
	}
}

// The migration only changes the constraint: it does not turn the old 0 into 4 and keeps every user index and all existing data.
func TestNullableCutoffMigrationPreservesValues(t *testing.T) {
	db := newSQLite(t)
	if err := AutoMigrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	type oldColumn struct {
		DayCutoffHour int `gorm:"column:day_cutoff_hour;not null;default:4"`
	}
	// The Table argument keeps the test-only struct from deriving another table name.
	if err := db.Table("users").Migrator().AlterColumn(&oldColumn{}, "DayCutoffHour"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"zero", "four"} {
		u := User{Username: name, Email: name + "@example.com", DisplayName: name, Role: RoleUser, Status: StatusActive, Locale: "en", Timezone: "UTC", DayCutoffHour: Ptr(4), CreatedAt: time.Now().UTC()}
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&User{}).Where("username = ?", "zero").Update("day_cutoff_hour", 0).Error; err != nil {
		t.Fatal(err)
	}
	// External indexes and triggers must survive the migration; a driver rebuilding the table drops them by default.
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE cutoff_changes (user_id integer)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TRIGGER cutoff_audit AFTER UPDATE OF day_cutoff_hour ON users BEGIN INSERT INTO cutoff_changes (user_id) VALUES (NEW.id); END").Error; err != nil {
		t.Fatal(err)
	}
	// AutoMigrate itself must not change the old constraint; DDL changes must be auditable as versioned migrations.
	if err := AutoMigrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&User{}).Where("username = ?", "zero").Update("day_cutoff_hour", nil).Error; err == nil {
		t.Fatal("AutoMigrate relaxed the old non-null constraint")
	}
	applied, err := Sync(context.Background(), db, BuiltinMigrations)
	if err != nil {
		t.Fatal(err)
	}
	if applied != len(BuiltinMigrations) {
		t.Fatalf("applied=%d", applied)
	}
	users, err := NewUserStore(db).ByUsername(t.Context(), "zero")
	if err != nil {
		t.Fatal(err)
	}
	if users.DayCutoffHour == nil || *users.DayCutoffHour != 0 {
		t.Fatal("migration rewrote legacy midnight")
	}
	four, err := NewUserStore(db).ByUsername(t.Context(), "four")
	if err != nil {
		t.Fatal(err)
	}
	if four.DayCutoffHour == nil || *four.DayCutoffHour != 4 {
		t.Fatal("migration rewrote four")
	}
	if err := db.Model(&User{}).Where("id = ?", users.ID).Update("day_cutoff_hour", nil).Error; err != nil {
		t.Fatal(err)
	}
	var changes int64
	if err := db.Table("cutoff_changes").Count(&changes).Error; err != nil || changes != 1 {
		t.Fatalf("trigger did not survive: %d %v", changes, err)
	}
	if !db.Migrator().HasIndex(&User{}, "idx_users_username") {
		t.Fatal("migration lost username index")
	}
	if applied, err := Sync(t.Context(), db, BuiltinMigrations); err != nil || applied != 0 {
		t.Fatalf("second sync=%d %v", applied, err)
	}
}
