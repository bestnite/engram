package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// SessionStore wraps the sessions table. A session is server-side state: logout, password change and disabling all revoke through it.
type SessionStore struct {
	db *gorm.DB
}

// NewSessionStore builds the session store.
func NewSessionStore(db *gorm.DB) *SessionStore { return &SessionStore{db: db} }

// Create writes one session row.
func (s *SessionStore) Create(ctx context.Context, sess *Session) error {
	if err := s.db.WithContext(ctx).Create(sess).Error; err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// ByID fetches a session; returns gorm.ErrRecordNotFound when absent.
func (s *SessionStore) ByID(ctx context.Context, id string) (*Session, error) {
	var sess Session
	if err := s.db.WithContext(ctx).First(&sess, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &sess, nil
}

// Revoke revokes one session (logout). Calling it again on an already revoked session is idempotent.
func (s *SessionStore) Revoke(ctx context.Context, id string, at time.Time) error {
	if err := s.db.WithContext(ctx).Model(&Session{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", at).Error; err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

// RevokeAllForUser revokes every active session of a user (password change, disabling, forced sign-out).
func (s *SessionStore) RevokeAllForUser(ctx context.Context, userID uint64, at time.Time) error {
	return s.RevokeAllForUserTx(ctx, s.db, userID, at)
}

// RevokeAllForUserTx revokes all of a user's active sessions in bulk inside the transaction given by the caller.
// A password reset needs it to share one transaction with the password write and the API key revocation, so a failure leaves nothing half revoked.
func (s *SessionStore) RevokeAllForUserTx(ctx context.Context, tx *gorm.DB, userID uint64, at time.Time) error {
	if err := tx.WithContext(ctx).Model(&Session{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", at).Error; err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	return nil
}

// RevokeAllForUserExcept revokes all of a user's active sessions except keepID.
// The user's own password change on the settings page uses it: other devices are signed out at once while the current session is kept, so the user is not thrown out right after changing the password.
func (s *SessionStore) RevokeAllForUserExcept(ctx context.Context, userID uint64, keepID string, at time.Time) error {
	return s.RevokeAllForUserExceptTx(ctx, s.db, userID, keepID, at)
}

// RevokeAllForUserExceptTx is RevokeAllForUserExcept inside the caller's transaction.
// The first-password flow needs the hash write and the revocation to share one transaction, so a revocation failure cannot leave a password
// written while other devices stay signed in.
func (s *SessionStore) RevokeAllForUserExceptTx(ctx context.Context, tx *gorm.DB, userID uint64, keepID string, at time.Time) error {
	if err := tx.WithContext(ctx).Model(&Session{}).
		Where("user_id = ? AND revoked_at IS NULL AND id <> ?", userID, keepID).
		Update("revoked_at", at).Error; err != nil {
		return fmt.Errorf("revoke user sessions except current: %w", err)
	}
	return nil
}

// Touch updates the session's last-active time; best effort, a failure should not interrupt the request.
func (s *SessionStore) Touch(ctx context.Context, id string, at time.Time) error {
	return s.db.WithContext(ctx).Model(&Session{}).Where("id = ?", id).
		Update("last_seen_at", at).Error
}

// DeleteExpired removes expired session rows; called by a background job, and the tests use it to verify expiry reclamation.
func (s *SessionStore) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Where("expires_at <= ?", before).Delete(&Session{})
	if res.Error != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// IsNotFound is a narrow wrapper around gorm.ErrRecordNotFound so upper layers need not depend on GORM directly (a minimal consumer-side interface).
func IsNotFound(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }
