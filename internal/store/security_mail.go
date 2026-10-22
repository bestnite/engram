package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// This file is the user-column write extension that (category A transactional security mail) needs: email verification time and email updates.
//
// It lives in a separate file rather than being stuffed into user.go / models.go: those two are single-writer hotspots for other milestones
// (AGENTS.md §6.4) and should not be touched while work is running in parallel. Only new methods are added here; no existing behaviour changes.
// No users column is added either, deliberately -- email_verified_at already exists on the User model, so no migration is needed.

// SetEmailVerifiedAt writes only the email_verified_at column, so a whole-row overwrite cannot lose a concurrent write.
// at nil clears the verification mark (after an email change the new address is not verified yet).
func (s *UserStore) SetEmailVerifiedAt(ctx context.Context, id uint64, at *time.Time) error {
	var value any
	if at != nil {
		u := at.UTC()
		value = u
	}
	if err := s.db.WithContext(ctx).Model(&User{}).Where("id = ?", id).
		Update("email_verified_at", value).Error; err != nil {
		return fmt.Errorf("set email verified at: %w", err)
	}
	return nil
}

// SetEmail changes only the email column. A unique-constraint conflict on email is returned as is, for the caller to translate into a stable error code.
// Changing the email means the new address passed confirmation-link verification, so email_verified_at is set to at in the same transaction.
func (s *UserStore) SetEmail(ctx context.Context, id uint64, email string, at time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).Where("id = ?", id).
			Update("email", email).Error; err != nil {
			return fmt.Errorf("set email: %w", err)
		}
		if err := tx.Model(&User{}).Where("id = ?", id).
			Update("email_verified_at", at.UTC()).Error; err != nil {
			return fmt.Errorf("set email verified at: %w", err)
		}
		return nil
	})
}
