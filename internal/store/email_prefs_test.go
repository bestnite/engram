package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// openEmailPrefFile opens a temp-file-based SQLite database and migrates all models.
// A file database (not an in-memory one) is used so it can be closed and reopened, faithfully simulating a 'restart'.
func openEmailPrefFile(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite %s: %v", path, err)
	}
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	return db
}

// closeEmailPrefDB closes the underlying sql.DB so that a later reopen sees the real on-disk state.
func closeEmailPrefDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql.DB: %v", err)
	}
}

// TestEmailPrefsPersistAcrossReopen is direct evidence for the acceptance item 'a user's choices for optional types still apply after a restart':
// write choices → close the database → reopen the same file → the choices read back are identical.
func TestEmailPrefsPersistAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.db")
	ctx := context.Background()
	want := map[string]bool{
		"deck_shared":     false, // B: on by default, turned off by the user
		"review_reminder": true,  // C: off by default, turned on by the user
		"invite":          false,
	}

	db := openEmailPrefFile(t, path)
	if err := NewEmailPrefStore(db).SetChoices(ctx, 42, want, time.Now().UTC()); err != nil {
		t.Fatalf("SetChoices() error = %v", err)
	}
	closeEmailPrefDB(t, db)

	reopened := openEmailPrefFile(t, path)
	got, err := NewEmailPrefStore(reopened).Choices(ctx, 42)
	if err != nil {
		t.Fatalf("Choices() after reopen error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("choices after reopen = %v, want %v", got, want)
	}
	closeEmailPrefDB(t, reopened)
}

// TestEmailPrefsChoicesEmptyWhenUnset asserts that a user who never saved preferences reads an empty map (rather than an error).
func TestEmailPrefsChoicesEmptyWhenUnset(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			got, err := NewEmailPrefStore(db).Choices(context.Background(), 999999)
			if err != nil {
				t.Fatalf("Choices() error = %v", err)
			}
			if len(got) != 0 {
				t.Errorf("Choices() = %v, want an empty map", got)
			}
		})
	}
}

// TestEmailPrefsSetChoicesReplaces asserts that saving overwrites wholesale rather than merging:
// one submission is the user's complete choice at that moment, so old values must be replaced, otherwise a type that was turned off lingers.
func TestEmailPrefsSetChoicesReplaces(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			s := NewEmailPrefStore(db)
			if err := s.SetChoices(ctx, 7, map[string]bool{"deck_shared": false, "invite": false}, time.Now().UTC()); err != nil {
				t.Fatalf("first SetChoices() error = %v", err)
			}
			want := map[string]bool{"review_reminder": true}
			if err := s.SetChoices(ctx, 7, want, time.Now().UTC()); err != nil {
				t.Fatalf("second SetChoices() error = %v", err)
			}
			got, err := s.Choices(ctx, 7)
			if err != nil {
				t.Fatalf("Choices() error = %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("choices after replace = %v, want %v", got, want)
			}
		})
	}
}

// TestEmailPrefsTableRegisteredForMigration asserts that email_prefs is in AllModels():
// AutoMigrate reads from AllModels, and a missing registration silently leaves one table uncreated (AGENTS.md §6.4, guard test).
func TestEmailPrefsTableRegisteredForMigration(t *testing.T) {
	found := false
	for _, model := range AllModels() {
		if _, ok := model.(*EmailPref); ok {
			found = true
		}
	}
	if !found {
		t.Fatal("EmailPref is not registered in AllModels(); AutoMigrate would silently skip email_prefs")
	}
}
