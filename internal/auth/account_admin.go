package auth

import (
	"context"
	"crypto/rand"
	"fmt"

	"gorm.io/gorm"
)

// Account operations needed for user management in the admin panel.
// Kept in a separate file to avoid touching account.go (the single-writer hotspot for other milestones).

// tempPasswordAlphabet is the temporary-password character set: the confusable 0/O/1/l/I are removed.
const tempPasswordAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"

// tempPasswordLength is the temporary-password length; it is greater than MinPasswordLength, so random characters never hit the weak-password wordlist.
const tempPasswordLength = 16

// GenerateTempPassword generates a random temporary password that satisfies the password policy.
func GenerateTempPassword() (string, error) {
	buf := make([]byte, tempPasswordLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate temp password: %w", err)
	}
	out := make([]byte, tempPasswordLength)
	for i, b := range buf {
		out[i] = tempPasswordAlphabet[int(b)%len(tempPasswordAlphabet)]
	}
	return string(out), nil
}

// ResetPasswordTx lets an administrator generate a temporary password for the given user: it writes the new hash inside the caller's transaction
// and, in that same transaction, revokes all of the user's sessions and all of their API keys.
// The returned plaintext is handed to the caller for a single display at this moment and is never written to logs or persisted.
//
// The caller's transaction handle must be used: the existence check and all three writes have to go through tx, otherwise opening another connection on SQLite hits a lock conflict,
// and if any write fails things are left half-revoked.
func (s *AccountService) ResetPasswordTx(ctx context.Context, tx *gorm.DB, userID uint64) (string, error) {
	if _, err := s.users.ByIDTx(ctx, tx, userID); err != nil {
		return "", fmt.Errorf("load user: %w", err)
	}
	temp, err := GenerateTempPassword()
	if err != nil {
		return "", err
	}
	if err := ValidatePasswordPolicy(temp); err != nil {
		// Cannot happen in theory; if it does, the alphabet/length has been broken, and we must fail explicitly rather than write out a weak password.
		return "", fmt.Errorf("temp password failed policy: %w", err)
	}
	hash, err := s.hasher.Hash(temp)
	if err != nil {
		return "", err
	}
	if err := s.users.SetPasswordHashTx(ctx, tx, userID, hash); err != nil {
		return "", err
	}
	if err := s.sessions.RevokeAllForUserTx(ctx, tx, userID, s.now()); err != nil {
		return temp, err
	}
	if err := s.keys.RevokeAllForUserTx(ctx, tx, userID, s.now()); err != nil {
		return temp, err
	}
	return temp, nil
}

// ForceLogout revokes all current sessions of the given user (forced logout) without changing the password or the status.
func (s *AccountService) ForceLogout(ctx context.Context, userID uint64) error {
	if _, err := s.users.ByID(ctx, userID); err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	return s.sessions.RevokeAllForUser(ctx, userID, s.now())
}
