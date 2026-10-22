package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// This file is the persistence layer for the 'new device / new IP login alert'.
//
// Only by remembering which (IP, device) combinations a user has logged in from can the system tell whether this login is new.
// A fingerprint is a hash of IP and User-Agent: storing the hash rather than the raw text is enough for dedup, and avoids
// piling up raw user agents in the database long term (data minimization).

// LoginFingerprint records one login fingerprint a user has been seen with.
type LoginFingerprint struct {
	ID uint64 `gorm:"primaryKey" json:"id"`
	// UserID + Fingerprint are unique, forming the basis for deciding 'has this user seen that device/IP'.
	UserID      uint64    `gorm:"not null;uniqueIndex:idx_login_fp_user_fp,priority:1" json:"user_id"`
	Fingerprint string    `gorm:"not null;uniqueIndex:idx_login_fp_user_fp,priority:2" json:"fingerprint"`
	FirstSeenAt time.Time `gorm:"not null" json:"first_seen_at"`
	LastSeenAt  time.Time `gorm:"not null" json:"last_seen_at"`
}

// TableName pins the table name so pluralization rules cannot drift.
func (LoginFingerprint) TableName() string { return "login_fingerprints" }

// LoginFingerprintStore wraps GORM access to the login_fingerprints table.
type LoginFingerprintStore struct {
	db *gorm.DB
}

// NewLoginFingerprintStore constructs the login fingerprint store.
func NewLoginFingerprintStore(db *gorm.DB) *LoginFingerprintStore {
	return &LoginFingerprintStore{db: db}
}

// Touch records one login fingerprint and returns known=true when it has been seen before (not a new device/IP).
//
// Look first, then insert; the insert uses OnConflict DoNothing, so a duplicate insert under concurrency yields RowsAffected=0 and is
// likewise treated as 'known', so two logins in the same second do not each send an alert.
func (s *LoginFingerprintStore) Touch(ctx context.Context, userID uint64, fingerprint string, now time.Time) (bool, error) {
	now = now.UTC()
	var row LoginFingerprint
	err := s.db.WithContext(ctx).
		Where("user_id = ? AND fingerprint = ?", userID, fingerprint).
		First(&row).Error
	if err == nil {
		if uerr := s.db.WithContext(ctx).Model(&LoginFingerprint{}).
			Where("id = ?", row.ID).
			Update("last_seen_at", now).Error; uerr != nil {
			return true, fmt.Errorf("touch login fingerprint: %w", uerr)
		}
		return true, nil
	}
	if !IsNotFound(err) {
		return false, fmt.Errorf("load login fingerprint: %w", err)
	}
	insert := LoginFingerprint{
		UserID:      userID,
		Fingerprint: fingerprint,
		FirstSeenAt: now,
		LastSeenAt:  now,
	}
	res := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&insert)
	if res.Error != nil {
		return false, fmt.Errorf("record login fingerprint: %w", res.Error)
	}
	return res.RowsAffected == 0, nil
}

// DeleteStale deletes fingerprint rows whose last_seen_at is earlier than before, returning the number of rows deleted.
//
// This table grows by 'one row per (IP, device) combination a user has seen', only ever growing. Reclaiming by last-seen time
// means 'has not logged in from this device for a long time' stops counting as a known device — at the cost of that next login being judged new and
// triggering an alert. The retention window therefore cannot be too short (callers pass 90 days): better to keep a bit longer than to falsely alert a normal user about a new device.
func (s *LoginFingerprintStore) DeleteStale(ctx context.Context, before time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Where("last_seen_at <= ?", before).Delete(&LoginFingerprint{})
	if res.Error != nil {
		return 0, fmt.Errorf("delete stale login fingerprints: %w", res.Error)
	}
	return res.RowsAffected, nil
}
