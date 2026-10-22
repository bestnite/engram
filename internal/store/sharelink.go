package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ErrShareLinkNotFound means the share link does not exist, is revoked or is expired.
//
// The three cases are deliberately merged into one sentinel: a revoked/expired link must return the same result as "never existed"
// (a security requirement; the acceptance "a revoked or expired link returns 404"),
// otherwise the caller leaks the fact that "this token was once valid".
var ErrShareLinkNotFound = errors.New("share link not found")

// ShareLinkDigest returns the hex sha256 digest of a plaintext token.
//
// Only the digest is stored: even if the database leaks, an attacker gets no usable link.
// The plaintext is returned only once, at creation, the same way API keys are handled.
func ShareLinkDigest(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// NewShareLinkToken generates a share link's plaintext token: the URL-safe encoding of 32 bytes of cryptographic randomness.
// RawURLEncoding avoids needing to escape '=', '+' and '/', so the token can go straight into a URL path.
func NewShareLinkToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate share link token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ShareLinkInput is the input for creating a share link.
// PasswordHash is an already encoded argon2id hash (produced by the auth layer); the store only persists it
// and does not take a dependency on internal/auth (auth depends on store, so the reverse would make a cycle).
type ShareLinkInput struct {
	DeckID       uint64
	PasswordHash *string
	ExpiresAt    *time.Time
	CreatedBy    uint64
}

// ShareLinkStore wraps the share_links table: create (optional password and expiry), list, revoke one/all, resolve by plaintext.
//
// Revoking writes revoked_at instead of deleting the row: keeping share link history makes it auditable "who revoked what and when".
// The resolve path filters revoked_at / expires_at uniformly, so revocation takes effect as immediately as expiry.
type ShareLinkStore struct {
	db *gorm.DB
}

// NewShareLinkStore builds the share link store.
func NewShareLinkStore(db *gorm.DB) *ShareLinkStore { return &ShareLinkStore{db: db} }

// Create generates the plaintext token, stores the digest and returns the plaintext (the one and only chance at the plaintext; the caller must show it to the user at once).
// A share link is always reader: it only serves registration-free read-only browsing and takes no caller-supplied role.
func (s *ShareLinkStore) Create(ctx context.Context, in ShareLinkInput) (string, *ShareLink, error) {
	if in.DeckID == 0 || in.CreatedBy == 0 {
		return "", nil, errors.New("share link: deck_id and created_by are required")
	}
	plaintext, err := NewShareLinkToken()
	if err != nil {
		return "", nil, err
	}
	link := &ShareLink{
		Token:        ShareLinkDigest(plaintext),
		DeckID:       in.DeckID,
		Role:         RoleReader,
		PasswordHash: in.PasswordHash,
		ExpiresAt:    in.ExpiresAt,
		CreatedBy:    in.CreatedBy,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.db.WithContext(ctx).Create(link).Error; err != nil {
		return "", nil, fmt.Errorf("create share link: %w", err)
	}
	return plaintext, link, nil
}

// ListByDeck lists all of a deck's share links newest first (revoked/expired ones included, so the state differences can be shown).
func (s *ShareLinkStore) ListByDeck(ctx context.Context, deckID uint64) ([]ShareLink, error) {
	var links []ShareLink
	if err := s.db.WithContext(ctx).
		Where("deck_id = ?", deckID).Order("created_at DESC, token ASC").Find(&links).Error; err != nil {
		return nil, fmt.Errorf("list share links: %w", err)
	}
	return links, nil
}

// Revoke revokes one link (located by digest). It is an idempotent no-op when already revoked or absent.
// The token digest is used as the identifier because the table has no auto-increment id column, and the digest itself is not a secret (the plaintext cannot be derived from it).
func (s *ShareLinkStore) Revoke(ctx context.Context, deckID uint64, digest string) error {
	if digest == "" {
		return errors.New("share link: token is required")
	}
	if err := s.db.WithContext(ctx).Model(&ShareLink{}).
		Where("deck_id = ? AND token = ? AND revoked_at IS NULL", deckID, digest).
		Update("revoked_at", time.Now().UTC()).Error; err != nil {
		return fmt.Errorf("revoke share link: %w", err)
	}
	return nil
}

// RevokeAll revokes all of the deck's active links at once ("revoke everything in one go").
// It returns the number actually revoked, for auditing and test assertions.
func (s *ShareLinkStore) RevokeAll(ctx context.Context, deckID uint64) (int64, error) {
	now := time.Now().UTC()
	res := s.db.WithContext(ctx).Model(&ShareLink{}).
		Where("deck_id = ? AND revoked_at IS NULL", deckID).
		Update("revoked_at", now)
	if res.Error != nil {
		return 0, fmt.Errorf("revoke all share links: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// Resolve finds the active link for a plaintext token; absent, revoked and expired all return ErrShareLinkNotFound.
//
// Revocation and expiry converge here: the read path has this single decision point, so no new entry point can miss the filter.
func (s *ShareLinkStore) Resolve(ctx context.Context, plaintext string, now time.Time) (*ShareLink, error) {
	if plaintext == "" {
		return nil, ErrShareLinkNotFound
	}
	var link ShareLink
	err := s.db.WithContext(ctx).Where("token = ?", ShareLinkDigest(plaintext)).First(&link).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrShareLinkNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("resolve share link: %w", err)
	}
	if link.RevokedAt != nil {
		return nil, ErrShareLinkNotFound
	}
	if link.ExpiresAt != nil && !now.Before(*link.ExpiresAt) {
		return nil, ErrShareLinkNotFound
	}
	return &link, nil
}

// Active reports whether the link is currently valid (not revoked and not expired); the sharing admin page uses it to show the state.
func (l ShareLink) Active(now time.Time) bool {
	if l.RevokedAt != nil {
		return false
	}
	if l.ExpiresAt != nil && !now.Before(*l.ExpiresAt) {
		return false
	}
	return true
}
