package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/pgtest"
)

// testDatabases returns the test databases available on this machine. SQLite must really run;
// the PostgreSQL path is gated by TEST_PG_DSN (skipped automatically when no usable instance is local, see the convention).
//
// Each call on the PG side gets a brand-new temporary schema (see internal/pgtest): tests on a shared instance
// pollute each other and a rerun is bound to blow up; SQLite builds a fresh database file in t.TempDir(), so this flaw
// only shows up under PG.
func testDatabases(t *testing.T) map[string]*gorm.DB {
	t.Helper()
	out := make(map[string]*gorm.DB)
	sqliteDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	out["sqlite"] = sqliteDB
	if pg, ok := pgtest.Open(t); ok {
		out["postgres"] = pg
	}
	return out
}

// expectedTables are the tables for all models: schema_version, sessions,
// user_totp and totp_recovery_codes, mail_outbox.
var expectedTables = []string{
	"users", "identities", "invites", "settings", "presets", "decks", "notes", "cards",
	"card_states", "reviews", "deck_grants", "media", "media_notes", "media_uploaders",
	"api_keys", "jobs",
	"audit_log", "schema_version", "sessions", "user_totp", "totp_recovery_codes", "mail_outbox",
	"note_types", "media_names", "media_note_types",
}

func TestAutoMigrateCreatesAllTables(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v, want nil", err)
			}
			for _, table := range expectedTables {
				if !db.Migrator().HasTable(table) {
					t.Errorf("table %q missing after AutoMigrate", table)
				}
			}
			if got, want := len(expectedTables), 25; got != want {
				t.Errorf("expected table list has %d entries, want %d", got, want)
			}
		})
	}
}

// fixture holds the related rows the unique constraints under test need.
type fixture struct {
	userA  uint64
	userB  uint64
	deckID uint64
	noteID uint64
}

func seedFixture(t *testing.T, db *gorm.DB) fixture {
	t.Helper()
	now := time.Now().UTC()
	userA := User{Username: "alice", Email: "alice@example.com", DisplayName: "Alice", Role: "user",
		Status: "active", Locale: "zh-CN", Timezone: "Asia/Shanghai", CreatedAt: now}
	userB := User{Username: "bob", Email: "bob@example.com", DisplayName: "Bob", Role: "user",
		Status: "active", Locale: "zh-CN", Timezone: "Asia/Shanghai", CreatedAt: now}
	for _, u := range []*User{&userA, &userB} {
		if err := db.Create(u).Error; err != nil {
			t.Fatalf("create user %s: %v", u.Username, err)
		}
	}
	preset := Preset{OwnerUserID: userA.ID, Name: "default", DesiredRetention: 0.9,
		LearningSteps: "1m,10m", RelearningSteps: "10m", MaximumIntervalDays: 36500,
		EnableFuzz: Ptr(true), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&preset).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := Deck{OwnerUserID: userA.ID, Name: "Deck", Description: "",
		PresetID: preset.ID, CreatedAt: now}
	if err := db.Create(&deck).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	ref := "ref-1"
	note := Note{DeckID: deck.ID, NoteTypeID: Ptr(stockNoteTypeID(t, db, deck.ID, "basic")), FieldsJSON: `{"front":"q","back":"a"}`,
		TagsJSON: "[]", ExternalRef: &ref, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&note).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	return fixture{userA: userA.ID, userB: userB.ID, deckID: deck.ID, noteID: note.ID}
}

func TestUniqueConstraints(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v, want nil", err)
			}
			fx := seedFixture(t, db)
			now := time.Now().UTC()

			t.Run("notes(deck_id, external_ref)", func(t *testing.T) {
				ref := "dup-ref"
				first := Note{DeckID: fx.deckID, NoteTypeID: Ptr(stockNoteTypeID(t, db, fx.deckID, "basic")), FieldsJSON: "{}", TagsJSON: "[]",
					ExternalRef: &ref, CreatedAt: now, UpdatedAt: now}
				if err := db.Create(&first).Error; err != nil {
					t.Fatalf("first insert: %v", err)
				}
				dup := first
				dup.ID = 0
				if err := db.Create(&dup).Error; err == nil {
					t.Error("duplicate (deck_id, external_ref) was accepted, want unique violation")
				}
			})

			t.Run("cards(note_id, template)", func(t *testing.T) {
				first := Card{NoteID: fx.noteID, Template: "forward", CreatedAt: now}
				if err := db.Create(&first).Error; err != nil {
					t.Fatalf("first insert: %v", err)
				}
				dup := first
				dup.ID = 0
				if err := db.Create(&dup).Error; err == nil {
					t.Error("duplicate (note_id, template) was accepted, want unique violation")
				}
			})

			t.Run("identities(provider, subject)", func(t *testing.T) {
				first := Identity{UserID: fx.userA, Provider: "issuer.example.com", Subject: "sub-1", LinkedAt: now}
				if err := db.Create(&first).Error; err != nil {
					t.Fatalf("first insert: %v", err)
				}
				// The same (provider, subject) attached to another user must also be rejected.
				dup := Identity{UserID: fx.userB, Provider: "issuer.example.com", Subject: "sub-1", LinkedAt: now}
				if err := db.Create(&dup).Error; err == nil {
					t.Error("duplicate (provider, subject) was accepted, want unique violation")
				}
			})

			t.Run("media(sha256)", func(t *testing.T) {
				first := Media{Sha256: "deadbeef", RelPath: "de/deadbeef.png", Mime: "image/png",
					Bytes: 12, CreatedAt: now}
				if err := db.Create(&first).Error; err != nil {
					t.Fatalf("first insert: %v", err)
				}
				dup := Media{Sha256: "deadbeef", RelPath: "de/other.png", Mime: "image/png",
					Bytes: 12, CreatedAt: now}
				if err := db.Create(&dup).Error; err == nil {
					t.Error("duplicate sha256 was accepted, want unique violation")
				}
			})
		})
	}
}

func TestLoadSettingsDecodesJSONValues(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "settings.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Setting{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	now := time.Now().UTC()
	rows := []Setting{
		{Key: "http_addr", Value: `"db.example.com:2222"`, UpdatedAt: now},
		{Key: "registration_policy", Value: `"closed"`, UpdatedAt: now},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	got, err := LoadSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("LoadSettings() error = %v", err)
	}
	if got["http_addr"] != "db.example.com:2222" {
		t.Errorf("LoadSettings()[http_addr] = %q, want %q", got["http_addr"], "db.example.com:2222")
	}
}
