package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// This file is the persistence layer for one-time action tokens (class-A transactional mail).
//
// Why one shared generic table instead of one per token kind: password reset, email verification
// and email-change confirmation have identical lifecycles — the random plaintext appears only in
// the mail link, the DB stores only a digest, a token dies after a single use, and all of them
// expire. One shared table keeps 'store digest only + conditional update WHERE used_at IS NULL' implemented in one place, so a new token kind cannot drift away from it.
//
// Plaintext never touches the DB and never touches the logs: the table holds only the sha256
// digest (token_hash). Building and delivering the link is internal/auth's ActionTokenService job.

// Action token purpose; values are English and stable, fixed once written to the DB (AGENTS.md §2.1).
const (
	// ActionTokenPasswordReset is the password reset token.
	ActionTokenPasswordReset = "password_reset"
	// ActionTokenEmailVerify is the email verification token (confirming the mailbox after signup).
	ActionTokenEmailVerify = "email_verification"
	// ActionTokenEmailChange is the email-change confirmation token; Payload holds the pending new address.
	ActionTokenEmailChange = "email_change"
	// ActionTokenUnsubscribe is the one-click unsubscribe token; Payload holds the optional mail type it names,
	// so a token can only turn off that one type and can never be redirected to another.
	ActionTokenUnsubscribe = "unsubscribe"
)

// Stable error values for token consumption; the transport layer maps them to stable English codes.
var (
	// ErrActionTokenNotFound means the digest does not exist.
	ErrActionTokenNotFound = errors.New("action token not found")
	// ErrActionTokenUsed means the token was already used (single use).
	ErrActionTokenUsed = errors.New("action token already used")
	// ErrActionTokenExpired means the token has expired.
	ErrActionTokenExpired = errors.New("action token expired")
)

// Audit actions; kept in this file so as not to churn the audit_*.go hotspots of other milestones.
const (
	// ActionUserPasswordResetComplete is a user finishing a password reset through the mail link.
	ActionUserPasswordResetComplete = "user.password_reset_complete"
	// ActionUserEmailVerifyRequest is requesting / resending the email verification mail.
	ActionUserEmailVerifyRequest = "user.email_verify_request"
	// ActionUserEmailVerified is a successful email verification.
	ActionUserEmailVerified = "user.email_verified"
	// ActionUserEmailChangeRequest is requesting an email change (a confirmation mail went to the new address).
	ActionUserEmailChangeRequest = "user.email_change_request"
	// ActionUserEmailChanged is a confirmed email change; the address has been updated.
	ActionUserEmailChanged = "user.email_changed"
)

// ActionToken is a one-time action token. The DB stores only token_hash (a sha256 digest),
// the plaintext appears only in the mail link sent to the user.
type ActionToken struct {
	ID uint64 `gorm:"primaryKey" json:"id"`
	// Purpose distinguishes the token use (password_reset / email_verification / email_change).
	Purpose string `gorm:"not null;index:idx_action_tokens_lookup,priority:1" json:"purpose"`
	// UserID is the user the token belongs to.
	UserID uint64 `gorm:"not null;index" json:"user_id"`
	// TokenHash is the sha256 hex digest of the plaintext token; a unique index prevents digest collisions.
	TokenHash string `gorm:"column:token_hash;not null;uniqueIndex" json:"-"`
	// Payload is use-specific extra data; email-change confirmation stores the new address, the others are empty.
	Payload string `gorm:"not null" json:"payload,omitempty"`
	// CreatedAt is the issue time.
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
	// ExpiresAt is the expiry time; once reached the token is rejected even if unused.
	ExpiresAt time.Time `gorm:"not null" json:"expires_at"`
	// UsedAt non-null means consumed; conditional updates rely on it to guarantee single use.
	UsedAt *time.Time `json:"used_at,omitempty"`
}

// TableName pins the table name so pluralization rules cannot drift across GORM versions.
func (ActionToken) TableName() string { return "action_tokens" }

// HashActionToken returns the sha256 hex digest of a plaintext token. This is the only hashing
// implementation; issuing and verifying must both use it so the two algorithms cannot diverge.
func HashActionToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// ActionTokenStore wraps GORM access to the action_tokens table.
type ActionTokenStore struct {
	db *gorm.DB
}

// NewActionTokenStore constructs the action token store.
func NewActionTokenStore(db *gorm.DB) *ActionTokenStore { return &ActionTokenStore{db: db} }

// Create writes a new token; it accepts a digest only, callers must not pass plaintext in.
func (s *ActionTokenStore) Create(ctx context.Context, tok *ActionToken) error {
	if tok == nil {
		return errors.New("store: action token is required")
	}
	if strings.TrimSpace(tok.TokenHash) == "" {
		return errors.New("store: action token hash is required")
	}
	if err := s.db.WithContext(ctx).Create(tok).Error; err != nil {
		return fmt.Errorf("create action token: %w", err)
	}
	return nil
}

// load reads one token and applies the used_at / expires_at checks, returning the row that passed.
// Consume and Peek share it: the validation rules (digest match, single use, validity window) are
// implemented only here, so the two entry points cannot drift apart. The errors are stable sentinels.
func (s *ActionTokenStore) load(ctx context.Context, purpose, plaintext string, now time.Time) (*ActionToken, error) {
	purpose = strings.TrimSpace(purpose)
	plaintext = strings.TrimSpace(plaintext)
	if purpose == "" || plaintext == "" {
		return nil, ErrActionTokenNotFound
	}
	hash := HashActionToken(plaintext)
	var tok ActionToken
	err := s.db.WithContext(ctx).Where("purpose = ? AND token_hash = ?", purpose, hash).First(&tok).Error
	if err != nil {
		if IsNotFound(err) {
			return nil, ErrActionTokenNotFound
		}
		return nil, fmt.Errorf("load action token: %w", err)
	}
	if tok.UsedAt != nil {
		return nil, ErrActionTokenUsed
	}
	if !tok.ExpiresAt.After(now.UTC()) {
		return nil, ErrActionTokenExpired
	}
	return &tok, nil
}

// Peek reads a token without consuming it: validation is identical to Consume, the only difference
// is that used_at is not written. It serves 'show first, confirm later' flows that need no login (the
// one-click unsubscribe page must show the type the token names, yet must not void the token over a single GET / prefetch).
func (s *ActionTokenStore) Peek(ctx context.Context, purpose, plaintext string, now time.Time) (*ActionToken, error) {
	return s.load(ctx, purpose, plaintext, now)
}

// Consume validates and consumes a token: load first does the digest match and the used_at / expires_at
// check, then a conditional update `WHERE id = ? AND used_at IS NULL` atomically marks it used,
// so 'one code, one use' holds under concurrency too. On success it returns the consumed row.
func (s *ActionTokenStore) Consume(ctx context.Context, purpose, plaintext string, now time.Time) (*ActionToken, error) {
	tok, err := s.load(ctx, purpose, plaintext, now)
	if err != nil {
		return nil, err
	}
	now = now.UTC()
	res := s.db.WithContext(ctx).Model(&ActionToken{}).
		Where("id = ? AND used_at IS NULL", tok.ID).
		Update("used_at", now)
	if res.Error != nil {
		return nil, fmt.Errorf("consume action token: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		// another concurrent request consumed it first.
		return nil, ErrActionTokenUsed
	}
	tok.UsedAt = &now
	return tok, nil
}

// DeleteExpired deletes token rows whose expiry is earlier than before, returning the number of rows deleted.
//
// before is given by the caller as 'now − retention': a token lingers after expiry (so 'the link stopped
// working' reports stay investigable) and is only really deleted past the retention window. The read path (load) already rejects expired rows, so deletion only reclaims space and does not affect decisions.
func (s *ActionTokenStore) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Where("expires_at <= ?", before).Delete(&ActionToken{})
	if res.Error != nil {
		return 0, fmt.Errorf("delete expired action tokens: %w", res.Error)
	}
	return res.RowsAffected, nil
}
