package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// legacyUserTOTP 是 user_totp 的旧形态（没有 last_used_step）。
// 用它建表可以忠实模拟「已有数据的旧库」，验证 AutoMigrate 只加列、不重建、不丢行。
type legacyUserTOTP struct {
	UserID           uint64 `gorm:"primaryKey;column:user_id"`
	SecretCiphertext string `gorm:"not null;column:secret_ciphertext"`
	ConfirmedAt      *time.Time
	CreatedAt        time.Time `gorm:"not null"`
	UpdatedAt        time.Time `gorm:"not null"`
}

func (legacyUserTOTP) TableName() string { return "user_totp" }

// TestAutoMigrateAddsTOTPStepToExistingRows 断言新增的可空列能加在已有数据的库上：
// 旧行保留、新列为 NULL（= 尚未接受过任何验证码），并且随后可用 AcceptStep 前移。
func TestAutoMigrateAddsTOTPStepToExistingRows(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// 旧版本建表并写入一行。
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

	// 新版本的 AutoMigrate：只允许加列（AGENTS.md §2.3 第 5 条）。
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

	// 新列在旧行上立刻可用：第一次接受把 NULL 前移到具体步。
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

// TestAcceptStepRejectsStepsAtOrBelowTheRecordedOne 断言条件更新的严格单调语义：
// 只有更大的步能写入，重复写入同一步或更小的步都拿不到 RowsAffected。
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
		{step: 100, want: false}, // 相等：重放
		{step: 99, want: false},  // 更早的旧步
		{step: 101, want: true},  // 严格更大
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
