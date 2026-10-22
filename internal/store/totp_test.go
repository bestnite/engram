package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// legacyUserTOTP is the old shape of user_totp (without last_used_step).
// Creating the table with it faithfully simulates "an old database that already holds data", verifying AutoMigrate only adds a column, rebuilds nothing and loses no rows.
type legacyUserTOTP struct {
	UserID           uint64 `gorm:"primaryKey;column:user_id"`
	SecretCiphertext string `gorm:"not null;column:secret_ciphertext"`
	ConfirmedAt      *time.Time
	CreatedAt        time.Time `gorm:"not null"`
	UpdatedAt        time.Time `gorm:"not null"`
}

func (legacyUserTOTP) TableName() string { return "user_totp" }

// TestAutoMigrateAddsTOTPStepToExistingRows asserts the new nullable column can be added to a database that already holds data:
// old rows are kept, the new column is NULL (= no code accepted yet), and AcceptStep can advance it afterwards.
func TestAutoMigrateAddsTOTPStepToExistingRows(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// Create the table as the old version did and insert one row.
	if err := db.AutoMigrate(&legacyUserTOTP{}); err != nil {
		t.Fatalf("create legacy user_totp: %v", err)
	}
	now := time.Now().UTC()
	row := legacyUserTOTP{UserID: 7, SecretCiphertext: "v1:legacy-ciphertext", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	if db.Migrator().HasColumn(&UserTOTP{}, "last_used_step") {
		t.Fatal("legacy table unexpectedly already has last_used_step")
	}

	// The new version's AutoMigrate: it may only add columns (AGENTS.md §2.3 item 5).
	if err := AutoMigrate(ctx, db); err != nil {
		t.Fatalf("AutoMigrate() on a legacy database error = %v", err)
	}
	if !db.Migrator().HasColumn(&UserTOTP{}, "last_used_step") {
		t.Fatal("AutoMigrate did not add the last_used_step column")
	}

	var got UserTOTP
	if err := db.First(&got, "user_id = ?", 7).Error; err != nil {
		t.Fatalf("legacy row vanished after AutoMigrate: %v", err)
	}
	if got.SecretCiphertext != "v1:legacy-ciphertext" {
		t.Errorf("legacy row mutated: SecretCiphertext = %q", got.SecretCiphertext)
	}
	if got.LastUsedStep != nil {
		t.Errorf("existing row got last_used_step = %v, want NULL (never accepted a code)", *got.LastUsedStep)
	}

	// The new column works on the old row right away: the first acceptance advances NULL to a concrete step.
	ok, err := NewTOTPStore(db).AcceptStep(ctx, 7, 58500000)
	if err != nil || !ok {
		t.Fatalf("AcceptStep() on a legacy row = (%v, %v), want (true, nil)", ok, err)
	}
	if err := db.First(&got, "user_id = ?", 7).Error; err != nil {
		t.Fatalf("reload row: %v", err)
	}
	if got.LastUsedStep == nil || *got.LastUsedStep != 58500000 {
		t.Errorf("LastUsedStep = %v, want 58500000", got.LastUsedStep)
	}
}

// TestAcceptStepRejectsStepsAtOrBelowTheRecordedOne asserts the strictly monotonic semantics of the conditional update:
// only a larger step can be written; writing the same step again or a smaller one yields no RowsAffected.
func TestAcceptStepRejectsStepsAtOrBelowTheRecordedOne(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "steps.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := AutoMigrate(ctx, db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&UserTOTP{UserID: 1, SecretCiphertext: "v1:x", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create totp row: %v", err)
	}
	st := NewTOTPStore(db)
	for _, tc := range []struct {
		step int64
		want bool
	}{
		{step: 100, want: true},  // NULL → 100
		{step: 100, want: false}, // equal: replay
		{step: 99, want: false},  // an older, earlier step
		{step: 101, want: true},  // strictly greater
	} {
		got, err := st.AcceptStep(ctx, 1, tc.step)
		if err != nil {
			t.Fatalf("AcceptStep(%d) error = %v", tc.step, err)
		}
		if got != tc.want {
			t.Errorf("AcceptStep(%d) = %v, want %v", tc.step, got, tc.want)
		}
	}
}
