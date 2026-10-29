package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// This file is the one-time token service used by class-A transactional mail: issuing and consuming.
//
// Security constraints:
//   - the plaintext is random and URL-safe, appears only in email links, and is never persisted nor logged;
//   - only the sha256 digest is stored (store.HashActionToken);
//   - one-time: consumption goes through a conditional update `WHERE used_at IS NULL` (store.ActionTokenStore.Consume);
//   - there is an explicit expiry.
//
// The token purpose constants and TTLs are centralized here; callers do not invent their own durations.

const (
	// PasswordResetTTL is the validity of a password reset link; the short window shrinks the abuse surface if the link leaks.
	PasswordResetTTL = 1 * time.Hour
	// EmailVerifyTTL is the validity of an email verification link.
	EmailVerifyTTL = 24 * time.Hour
	// EmailChangeTTL is the validity of an email change confirmation link.
	EmailChangeTTL = 24 * time.Hour
)

// ActionTokenService issues and consumes one-time action tokens.
type ActionTokenService struct {
	tokens *store.ActionTokenStore
	now    func() time.Time
	rand   func([]byte) error
}

// NewActionTokenService constructs the token service; store is required.
func NewActionTokenService(tokens *store.ActionTokenStore) (*ActionTokenService, error) {
	if tokens == nil {
		return nil, errors.New("auth: action token store is required")
	}
	return &ActionTokenService{
		tokens: tokens,
		now:    func() time.Time { return time.Now().UTC() },
		rand:   func(b []byte) error { _, err := rand.Read(b); return err },
	}, nil
}

// Issue generates a random token, persists the digest and returns the plaintext (only for splicing into an email link).
// payload is purpose-specific extra data (the new email for email change confirmation); all other purposes pass an empty string.
func (s *ActionTokenService) Issue(ctx context.Context, userID uint64, purpose, payload string, ttl time.Duration) (string, error) {
	buf := make([]byte, 32)
	if err := s.rand(buf); err != nil {
		return "", fmt.Errorf("generate action token: %w", err)
	}
	plaintext := base64.RawURLEncoding.EncodeToString(buf)
	now := s.now()
	row := &store.ActionToken{
		Purpose:   purpose,
		UserID:    userID,
		TokenHash: store.HashActionToken(plaintext),
		Payload:   payload,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	if err := s.tokens.Create(ctx, row); err != nil {
		return "", err
	}
	return plaintext, nil
}

// Consume validates and one-time consumes a token, returning the consumed row (including UserID and Payload).
// The errors are the store's stable sentinels: ErrActionTokenNotFound / Used / Expired.
func (s *ActionTokenService) Consume(ctx context.Context, purpose, plaintext string) (*store.ActionToken, error) {
	return s.tokens.Consume(ctx, purpose, plaintext, s.now())
}

// Peek reads a token without consuming it; the error sentinels are the same as Consume. Used by "show first, then confirm" flows.
func (s *ActionTokenService) Peek(ctx context.Context, purpose, plaintext string) (*store.ActionToken, error) {
	return s.tokens.Peek(ctx, purpose, plaintext, s.now())
}
