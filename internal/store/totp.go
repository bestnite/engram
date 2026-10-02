package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 本文件是 M1-16（TOTP 二次验证）的持久化层。
//
// 为什么不把这两张表并进 models.go：models.go 是单写者热点（AGENTS.md §6.4），
// 本轮与其它泳道并行，故新表定义放在独立文件里，仅在 AutoMigrate 处并集一次。
// 表结构是纯粹的新增（AGENTS.md §2.3 第 5 条允许 AutoMigrate 加表）。
//
// 安全约定：
//   - TOTP secret 只以密文落库（SecretCodec，AES-GCM），明文绝不出现在任何列里；
//   - 恢复码只存 sha256 摘要，明文只在生成时返回一次；
//   - 恢复码一次性由「条件更新 + used_at IS NULL」保证（见 ConsumeRecoveryCode），
//     不依赖读-判断-再写，因此并发下也不会被用两次。

// ErrTOTPRecordNotFound 表示该用户没有 TOTP 记录（或尚未确认）。
var ErrTOTPRecordNotFound = errors.New("totp record not found")

// UserTOTP 是用户的 TOTP 状态；每人至多一行。
// ConfirmedAt 为 NULL 表示「已生成 secret 但尚未用验证码确认」，此时二次验证不生效。
type UserTOTP struct {
	UserID uint64 `gorm:"primaryKey;column:user_id" json:"user_id"`
	// SecretCiphertext 是 SecretCodec 加密后的 secret，带 `v1:` 版本前缀。
	SecretCiphertext string     `gorm:"not null;column:secret_ciphertext" json:"-"`
	ConfirmedAt      *time.Time `json:"confirmed_at,omitempty"`
	CreatedAt        time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt        time.Time  `gorm:"not null" json:"updated_at"`
}

func (UserTOTP) TableName() string { return "user_totp" }

// TOTPRecoveryCode 是一条一次性恢复码；只存摘要。
// UsedAt 非空即已使用，之后同一码再也无法通过校验。
type TOTPRecoveryCode struct {
	ID        uint64     `gorm:"primaryKey" json:"id"`
	UserID    uint64     `gorm:"not null;index" json:"user_id"`
	CodeHash  string     `gorm:"not null;column:code_hash" json:"-"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	CreatedAt time.Time  `gorm:"not null" json:"created_at"`
}

func (TOTPRecoveryCode) TableName() string { return "totp_recovery_codes" }

// TOTPModels 返回 TOTP 相关的全部模型，供 AutoMigrate 并集（见 store.AutoMigrate）。
func TOTPModels() []any { return []any{&UserTOTP{}, &TOTPRecoveryCode{}} }

// TOTPStore 封装 user_totp 与 totp_recovery_codes 的 GORM 访问。
type TOTPStore struct {
	db *gorm.DB
}

// NewTOTPStore 构造 TOTP 存储。
func NewTOTPStore(db *gorm.DB) *TOTPStore { return &TOTPStore{db: db} }

// Get 取用户的 TOTP 行；不存在时返回 gorm.ErrRecordNotFound（用 IsNotFound 判定）。
func (s *TOTPStore) Get(ctx context.Context, userID uint64) (*UserTOTP, error) {
	var row UserTOTP
	if err := s.db.WithContext(ctx).First(&row, "user_id = ?", userID).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// Enabled 报告该用户是否真正启用了 TOTP：只有已确认的行才算启用。
func (s *TOTPStore) Enabled(ctx context.Context, userID uint64) (bool, error) {
	var n int64
	if err := s.db.WithContext(ctx).Model(&UserTOTP{}).
		Where("user_id = ? AND confirmed_at IS NOT NULL", userID).Count(&n).Error; err != nil {
		return false, fmt.Errorf("count totp rows: %w", err)
	}
	return n > 0, nil
}

// UpsertPending 写入（或覆盖）一个未确认的 secret 密文；confirmed_at 一律清空，
// 因此重新开始绑定会把上一次未完成的绑定顶掉。
func (s *TOTPStore) UpsertPending(ctx context.Context, userID uint64, ciphertext string, at time.Time) error {
	row := UserTOTP{UserID: userID, SecretCiphertext: ciphertext, CreatedAt: at.UTC(), UpdatedAt: at.UTC()}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"secret_ciphertext": ciphertext,
			"confirmed_at":      nil,
			"updated_at":        at.UTC(),
		}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("upsert totp secret: %w", err)
	}
	return nil
}

// Confirm 把已存在的待确认行标记为已确认；没有待确认行时返回 ErrTOTPRecordNotFound。
func (s *TOTPStore) Confirm(ctx context.Context, userID uint64, at time.Time) error {
	res := s.db.WithContext(ctx).Model(&UserTOTP{}).
		Where("user_id = ? AND confirmed_at IS NULL", userID).
		Updates(map[string]any{"confirmed_at": at.UTC(), "updated_at": at.UTC()})
	if res.Error != nil {
		return fmt.Errorf("confirm totp: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrTOTPRecordNotFound
	}
	return nil
}

// Delete 关闭 TOTP：在同一事务里删除 secret 行与全部恢复码，二者不会出现半删状态。
func (s *TOTPStore) Delete(ctx context.Context, userID uint64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&TOTPRecoveryCode{}).Error; err != nil {
			return fmt.Errorf("delete recovery codes: %w", err)
		}
		if err := tx.Where("user_id = ?", userID).Delete(&UserTOTP{}).Error; err != nil {
			return fmt.Errorf("delete totp record: %w", err)
		}
		return nil
	})
}

// ReplaceRecoveryCodes 用一批新的摘要替换用户现有的全部恢复码（同一事务）。
// 旧的未使用码因此立即失效——重新生成即作废上一批，这是恢复码唯一合理的语义。
func (s *TOTPStore) ReplaceRecoveryCodes(ctx context.Context, userID uint64, hashes []string, at time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&TOTPRecoveryCode{}).Error; err != nil {
			return fmt.Errorf("clear recovery codes: %w", err)
		}
		if len(hashes) == 0 {
			return nil
		}
		rows := make([]TOTPRecoveryCode, 0, len(hashes))
		for _, h := range hashes {
			rows = append(rows, TOTPRecoveryCode{UserID: userID, CodeHash: h, CreatedAt: at.UTC()})
		}
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("insert recovery codes: %w", err)
		}
		return nil
	})
}

// ConsumeRecoveryCode 尝试把一条恢复码标记为已用。
//
// 一次性由这条 SQL 保证：`used_at IS NULL` 的条件更新即使被并发调用，
// 也只有一个请求能把 RowsAffected 变成 1，其余拿到 0 —— 不需要行锁或读改写。
func (s *TOTPStore) ConsumeRecoveryCode(ctx context.Context, userID uint64, hash string, at time.Time) (bool, error) {
	res := s.db.WithContext(ctx).Model(&TOTPRecoveryCode{}).
		Where("user_id = ? AND code_hash = ? AND used_at IS NULL", userID, hash).
		Update("used_at", at.UTC())
	if res.Error != nil {
		return false, fmt.Errorf("consume recovery code: %w", res.Error)
	}
	return res.RowsAffected > 0, nil
}

// UnusedRecoveryCodeCount 返回用户尚未使用的恢复码数量，供设置页展示余量。
func (s *TOTPStore) UnusedRecoveryCodeCount(ctx context.Context, userID uint64) (int64, error) {
	var n int64
	if err := s.db.WithContext(ctx).Model(&TOTPRecoveryCode{}).
		Where("user_id = ? AND used_at IS NULL", userID).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count recovery codes: %w", err)
	}
	return n, nil
}
