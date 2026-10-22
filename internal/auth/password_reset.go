package auth

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// This file is the auth-layer complement of (class-A transactional mail): setting a new password via a one-time reset token.
//
// It differs from ChangePassword in two ways: first, no old password is needed, and the caller is responsible for the security — it must first consume
// a valid, unexpired, use-once reset token; second, a reset is the "prove identity externally after the password is lost" path,
// so besides sessions all of the user's API keys must be revoked too. It still reuses the same password policy and
// the same hasher, and defines no second set of rules.

// SetPasswordFromResetTx sets a new password for a user who has proven control of the mailbox, inside the caller's transaction,
// and revokes all of their sessions and all of their API keys in that same transaction.
//
// The caller's transaction handle must be used: internally this method reuses the three stores s.users/s.sessions/s.keys,
// which are only wrappers around GORM handles, and only passing tx guarantees the writes share one transaction; opening another connection on SQLite's
// single-writer connection hits a lock conflict, and a failure would also leave things half-revoked.
func (s *AccountService) SetPasswordFromResetTx(ctx context.Context, tx *gorm.DB, userID uint64, newPassword string) error {
	if err := ValidatePasswordPolicy(newPassword); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := s.users.SetPasswordHashTx(ctx, tx, userID, hash); err != nil {
		return err
	}
	if err := s.sessions.RevokeAllForUserTx(ctx, tx, userID, s.now()); err != nil {
		return err
	}
	return s.keys.RevokeAllForUserTx(ctx, tx, userID, s.now())
}
