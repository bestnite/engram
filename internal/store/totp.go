package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// This file is the persistence layer for (TOTP two-factor authentication).
//
// Why these two tables are not merged into models.go: models.go is a single-writer hotspot (AGENTS.md §6.4),
// and this round runs in parallel with other swimlanes, so the new table definitions live in a separate file and are unioned in only at AutoMigrate.
// The schema change is purely additive (AGENTS.md §2.3 item 5 allows AutoMigrate to add tables).
//
// Security conventions:
//   - the TOTP secret is stored only as ciphertext (SecretCodec, AES-GCM); the plaintext never appears in any column;
//   - recovery codes are stored only as sha256 digests, and the plaintext is returned once at generation;
//   - a recovery code's single use is guaranteed by "a conditional update + used_at IS NULL" (see ConsumeRecoveryCode),
//     not relying on read-check-write, so it cannot be used twice even under concurrency;
//   - replay protection for the 6-digit code is guaranteed by "a conditional update + last_used_step < step" (see AcceptStep),
//     the same technique: under concurrency the same (or an earlier) time step can only be accepted once.

// ErrTOTPRecordNotFound means the user has no TOTP record (or has not confirmed yet).
var ErrTOTPRecordNotFound = errors.New("totp record not found")

// UserTOTP is a user's TOTP state; at most one row per person.
// ConfirmedAt NULL means "a secret was generated but not yet confirmed with a code"; two-factor does not apply in that state.
type UserTOTP struct {
	UserID uint64 `gorm:"primaryKey;column:user_id" json:"user_id"`
	// SecretCiphertext is the secret after SecretCodec encryption, carrying the `v1:` version prefix.
	SecretCiphertext string     `gorm:"not null;column:secret_ciphertext" json:"-"`
	ConfirmedAt      *time.Time `json:"confirmed_at,omitempty"`
	// LastUsedStep is the largest accepted TOTP time step (unix seconds / period); NULL means no code has been accepted yet.
	// A nullable column rather than a NOT NULL default: AutoMigrate is only allowed to add columns (AGENTS.md §2.3 item 5),
	// and existing rows must read as "never used" after the column is added; NULL is exactly that semantics and imposes no constraint on old rows.
	LastUsedStep *int64    `gorm:"column:last_used_step" json:"last_used_step,omitempty"`
	CreatedAt    time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt    time.Time `gorm:"not null" json:"updated_at"`
}

func (UserTOTP) TableName() string { return "user_totp" }

// TOTPRecoveryCode is one single-use recovery code; only the digest is stored.
// A non-null UsedAt means it has been used; after that the same code can never pass validation again.
type TOTPRecoveryCode struct {
	ID        uint64     `gorm:"primaryKey" json:"id"`
	UserID    uint64     `gorm:"not null;index" json:"user_id"`
	CodeHash  string     `gorm:"not null;column:code_hash" json:"-"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	CreatedAt time.Time  `gorm:"not null" json:"created_at"`
}

func (TOTPRecoveryCode) TableName() string { return "totp_recovery_codes" }

// TOTPStore wraps GORM access to user_totp and totp_recovery_codes.
type TOTPStore struct {
	db *gorm.DB
}

// NewTOTPStore builds the TOTP store.
func NewTOTPStore(db *gorm.DB) *TOTPStore { return &TOTPStore{db: db} }

// Get fetches the user's TOTP row; returns gorm.ErrRecordNotFound when absent (test it with IsNotFound).
func (s *TOTPStore) Get(ctx context.Context, userID uint64) (*UserTOTP, error) {
	var row UserTOTP
	if err := s.db.WithContext(ctx).First(&row, "user_id = ?", userID).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// Enabled reports whether the user really has TOTP enabled: only a confirmed row counts as enabled.
func (s *TOTPStore) Enabled(ctx context.Context, userID uint64) (bool, error) {
	var n int64
	if err := s.db.WithContext(ctx).Model(&UserTOTP{}).
		Where("user_id = ? AND confirmed_at IS NOT NULL", userID).Count(&n).Error; err != nil {
		return false, fmt.Errorf("count totp rows: %w", err)
	}
	return n > 0, nil
}

// UpsertPending writes (or overwrites) an unconfirmed secret ciphertext; confirmed_at is always cleared,
// so starting enrollment again displaces the previous unfinished enrollment.
// last_used_step is cleared too: a new secret means a new set of codes, and the largest step the old secret accepted
// must not constrain the new enrollment (otherwise the new secret would be rejected for no apparent reason within the window the old step covers).
func (s *TOTPStore) UpsertPending(ctx context.Context, userID uint64, ciphertext string, at time.Time) error {
	row := UserTOTP{UserID: userID, SecretCiphertext: ciphertext, CreatedAt: at.UTC(), UpdatedAt: at.UTC()}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"secret_ciphertext": ciphertext,
			"confirmed_at":      nil,
			"last_used_step":    nil,
			"updated_at":        at.UTC(),
		}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("upsert totp secret: %w", err)
	}
	return nil
}

// AcceptStep tries to advance "the largest accepted time step" to step: the write succeeds only when step is strictly greater than the recorded step
// (or nothing was recorded before, i.e. NULL).
//
// Replay protection and concurrent single use are guaranteed by this conditional update: under concurrent calls only one request can turn RowsAffected into 1,
// the rest get 0 -- the same technique as ConsumeRecoveryCode, needing no row lock or read-check-write.
// updated_at comes along automatically per the GORM Update callback convention, so it is not set explicitly here.
func (s *TOTPStore) AcceptStep(ctx context.Context, userID uint64, step int64) (bool, error) {
	res := s.db.WithContext(ctx).Model(&UserTOTP{}).
		Where("user_id = ? AND (last_used_step IS NULL OR last_used_step < ?)", userID, step).
		Update("last_used_step", step)
	if res.Error != nil {
		return false, fmt.Errorf("accept totp step: %w", res.Error)
	}
	return res.RowsAffected > 0, nil
}

// Confirm marks an existing pending row as confirmed; returns ErrTOTPRecordNotFound when there is no pending row.
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

// Delete turns TOTP off: the secret row and all recovery codes are deleted in one transaction, so they can never be half deleted.
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

// ReplaceRecoveryCodes replaces all of the user's existing recovery codes with a new batch of digests (in one transaction).
// The old unused codes therefore lapse immediately -- regenerating invalidates the previous batch, the only sensible semantics for recovery codes.
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

// ConsumeRecoveryCode tries to mark one recovery code as used.
//
// Single use is guaranteed by this SQL: even when the `used_at IS NULL` conditional update is called concurrently,
// only one request can turn RowsAffected into 1, the rest get 0 -- no row lock or read-modify-write needed.
func (s *TOTPStore) ConsumeRecoveryCode(ctx context.Context, userID uint64, hash string, at time.Time) (bool, error) {
	res := s.db.WithContext(ctx).Model(&TOTPRecoveryCode{}).
		Where("user_id = ? AND code_hash = ? AND used_at IS NULL", userID, hash).
		Update("used_at", at.UTC())
	if res.Error != nil {
		return false, fmt.Errorf("consume recovery code: %w", res.Error)
	}
	return res.RowsAffected > 0, nil
}

// UnusedRecoveryCodeCount returns the number of recovery codes the user has not used yet, for the settings page to show how many are left.
func (s *TOTPStore) UnusedRecoveryCodeCount(ctx context.Context, userID uint64) (int64, error) {
	var n int64
	if err := s.db.WithContext(ctx).Model(&TOTPRecoveryCode{}).
		Where("user_id = ? AND used_at IS NULL", userID).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count recovery codes: %w", err)
	}
	return n, nil
}
