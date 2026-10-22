package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// The five API key scopes; this project's permission model is exactly these five, with no finer grain.
// Values are English constants: scope strings are written into api_keys.scopes and reused by REST/MCP, so they must be stable.
const (
	ScopeRead   = "read"   // read decks, cards, stats
	ScopeWrite  = "write"  // create / update / delete cards and decks
	ScopeReview = "review" // fetch due cards, submit ratings
	// ScopeKeys manages your own API keys (list / create / revoke); self-management in the web UI is not bound by it.
	// It only covers 'your own' keys — managing someone else's keys is admin.
	ScopeKeys  = "keys"
	ScopeAdmin = "admin" // manage all API keys, system settings and users; implies the other four
)

const (
	// APIKeyPlaintextPrefix is the prefix of the plaintext key (fcard_<base64url>).
	APIKeyPlaintextPrefix = "fcard_"
	// apiKeyDisplayChars is how many plaintext chars the display prefix keeps after 'fcard_', e.g. fcard_ab12cd34.
	apiKeyDisplayChars = 8
	// apiKeyRandomBytes is the number of random bytes used to build the plaintext (32 bytes).
	apiKeyRandomBytes = 32
	// defaultAPIKeyScope is the default when a new key is created without scopes (read only by default).
	defaultAPIKeyScope = ScopeRead
)

// apiKeyScopeOrder is the canonical scope order; normalization sorts by it so one scope set has exactly one stored form.
var apiKeyScopeOrder = []string{ScopeRead, ScopeWrite, ScopeReview, ScopeKeys, ScopeAdmin}

var (
	// ErrAPIKeyNameRequired means name was empty when creating the key.
	ErrAPIKeyNameRequired = errors.New("api key name is required")
	// ErrAPIKeyUserRequired means user_id was not supplied when creating the key.
	ErrAPIKeyUserRequired = errors.New("api key user is required")
	// ErrAPIKeyInvalidScope means scopes contained a value outside the five tiers.
	ErrAPIKeyInvalidScope = errors.New("invalid api key scope")
	// ErrAPIKeyNotFound means the key does not exist (no match by plaintext, or it is not the caller's).
	ErrAPIKeyNotFound = errors.New("api key not found")
	// ErrAPIKeyRevoked means the key has been revoked (revoked_at non-null).
	ErrAPIKeyRevoked = errors.New("api key revoked")
	// ErrAPIKeyExpired means the key is past expires_at.
	ErrAPIKeyExpired = errors.New("api key expired")
	// ErrAPIKeyPlaintextRequired means the plaintext string was empty during verification.
	ErrAPIKeyPlaintextRequired = errors.New("api key plaintext is required")
)

// IsValidScope reports whether a value is one of the five.
func IsValidScope(scope string) bool {
	for _, s := range apiKeyScopeOrder {
		if s == scope {
			return true
		}
	}
	return false
}

// ParseScopes splits a comma-separated scopes column into a slice, ignoring blank entries.
func ParseScopes(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// NormalizeScopes validates and normalizes a set of scopes: dedupe, sort by canonical order, join with commas.
// Empty input falls back to the default read; a value outside the five returns ErrAPIKeyInvalidScope.
func NormalizeScopes(scopes []string) (string, error) {
	if len(scopes) == 0 {
		return defaultAPIKeyScope, nil
	}
	seen := make(map[string]bool, len(scopes))
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if !IsValidScope(s) {
			return "", fmt.Errorf("%w: %q", ErrAPIKeyInvalidScope, s)
		}
		seen[s] = true
	}
	ordered := make([]string, 0, len(seen))
	for _, s := range apiKeyScopeOrder {
		if seen[s] {
			ordered = append(ordered, s)
		}
	}
	return strings.Join(ordered, ","), nil
}

// ScopesIncludeAdmin reports whether a scope set tries to grant admin (matched against the raw request values, not normalized).
// Value validity is NormalizeScopes' job; this only answers 'is admin in there'. The web form and the REST
// key-creation entry point share this single check (admin scope may only be granted to admin accounts).
func ScopesIncludeAdmin(scopes []string) bool {
	for _, s := range scopes {
		if strings.TrimSpace(s) == ScopeAdmin {
			return true
		}
	}
	return false
}

// HasScope reports whether the scopes string covers want.
// admin is equivalent to administrator rights, so it implies the other four tiers (including keys); the reverse does not hold,
// and no other two tiers imply each other.
func HasScope(scopes, want string) bool {
	for _, s := range ParseScopes(scopes) {
		if s == want || s == ScopeAdmin {
			return true
		}
	}
	return false
}

// HasScope is the method form of HasScope(scopes, want), handy for checking straight from an *APIKey.
func (k *APIKey) HasScope(want string) bool {
	if k == nil {
		return false
	}
	return HasScope(k.Scopes, want)
}

// HashAPIKey returns the sha256 hex digest of the plaintext; this value is all the DB stores.
func HashAPIKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// displayPrefix cuts out the display prefix (e.g. fcard_ab12cd34); it returns the plaintext unchanged when too short.
func displayPrefix(plaintext string) string {
	keep := len(APIKeyPlaintextPrefix) + apiKeyDisplayChars
	if len(plaintext) <= keep {
		return plaintext
	}
	return plaintext[:keep]
}

// newAPIKeyPlaintext builds the plaintext fcard_<base64url(32 random bytes)>.
func newAPIKeyPlaintext() (string, error) {
	buf := make([]byte, apiKeyRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate api key: %w", err)
	}
	return APIKeyPlaintextPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// ValidateAPIKey is a DB-independent usability check, so table-driven tests can cover it directly.
func ValidateAPIKey(k *APIKey, now time.Time) error {
	if k == nil {
		return ErrAPIKeyNotFound
	}
	if k.RevokedAt != nil {
		return fmt.Errorf("%w: api key %d", ErrAPIKeyRevoked, k.ID)
	}
	if k.ExpiresAt != nil && !k.ExpiresAt.After(now) {
		return fmt.Errorf("%w: api key %d", ErrAPIKeyExpired, k.ID)
	}
	return nil
}

// APIKeyStore wraps the api_keys table; the plaintext shows up only once, in Create's return value, after which only the sha256 remains.
type APIKeyStore struct {
	db *gorm.DB
}

// NewAPIKeyStore constructs the API key store.
func NewAPIKeyStore(db *gorm.DB) *APIKeyStore { return &APIKeyStore{db: db} }

// CreateAPIKeyParams is the input for creating a key; empty Scopes falls back to the default read, ExpiresAt nil means no expiry.
type CreateAPIKeyParams struct {
	UserID    uint64
	Name      string
	Scopes    []string
	ExpiresAt *time.Time
}

// CreatedAPIKey is the creation result: Plaintext is returned only here, callers must show it to the user immediately and never persist it.
type CreatedAPIKey struct {
	Key       *APIKey
	Plaintext string
}

// Create generates a new key and stores it. Only the sha256 and the display prefix are written; the plaintext goes back to the caller via the return value.
func (s *APIKeyStore) Create(ctx context.Context, p CreateAPIKeyParams) (*CreatedAPIKey, error) {
	if p.UserID == 0 {
		return nil, ErrAPIKeyUserRequired
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, ErrAPIKeyNameRequired
	}
	scopes, err := NormalizeScopes(p.Scopes)
	if err != nil {
		return nil, err
	}
	plaintext, err := newAPIKeyPlaintext()
	if err != nil {
		return nil, err
	}
	key := APIKey{
		UserID:    p.UserID,
		Name:      p.Name,
		Prefix:    displayPrefix(plaintext),
		KeyHash:   HashAPIKey(plaintext),
		Scopes:    scopes,
		ExpiresAt: p.ExpiresAt,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.db.WithContext(ctx).Create(&key).Error; err != nil {
		return nil, fmt.Errorf("create api key: %w", err)
	}
	return &CreatedAPIKey{Key: &key, Plaintext: plaintext}, nil
}

// ByID fetches a key by primary key; returns ErrAPIKeyNotFound when it does not exist.
func (s *APIKeyStore) ByID(ctx context.Context, id uint64) (*APIKey, error) {
	var k APIKey
	if err := s.db.WithContext(ctx).First(&k, "id = ?", id).Error; err != nil {
		if IsNotFound(err) {
			return nil, ErrAPIKeyNotFound
		}
		return nil, fmt.Errorf("load api key: %w", err)
	}
	return &k, nil
}

// ListByUser lists all of a user's keys (revoked ones included), newest first; used by the personal settings page and the admin panel.
func (s *APIKeyStore) ListByUser(ctx context.Context, userID uint64) ([]APIKey, error) {
	var keys []APIKey
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").Find(&keys).Error; err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	return keys, nil
}

// Authenticate verifies a key from plaintext: on a hit that is unexpired and unrevoked it returns the row and refreshes last_used_at.
// Revoked / expired keys return ErrAPIKeyRevoked / ErrAPIKeyExpired at once and are never let through.
func (s *APIKeyStore) Authenticate(ctx context.Context, plaintext string, now time.Time) (*APIKey, error) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return nil, ErrAPIKeyPlaintextRequired
	}
	var k APIKey
	if err := s.db.WithContext(ctx).First(&k, "key_hash = ?", HashAPIKey(plaintext)).Error; err != nil {
		if IsNotFound(err) {
			return nil, ErrAPIKeyNotFound
		}
		return nil, fmt.Errorf("load api key by hash: %w", err)
	}
	if err := ValidateAPIKey(&k, now); err != nil {
		return nil, err
	}
	// the last_used_at refresh is best effort: a failed write does not change this auth verdict, but must not hide it either.
	if err := s.TouchLastUsed(ctx, k.ID, now); err == nil {
		at := now.UTC()
		k.LastUsedAt = &at
	}
	return &k, nil
}

// TouchLastUsed updates last_used_at; used on every successful key call.
func (s *APIKeyStore) TouchLastUsed(ctx context.Context, id uint64, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if err := s.db.WithContext(ctx).Model(&APIKey{}).Where("id = ?", id).
		Update("last_used_at", at.UTC()).Error; err != nil {
		return fmt.Errorf("touch api key %d: %w", id, err)
	}
	return nil
}

// Revoke revokes a key owned by actorUserID (writes revoked_at); revocation takes effect immediately.
// A missing key or one owned by someone else both return ErrAPIKeyNotFound, so another user's key existence never leaks.
// Repeated revocation is idempotent (it still hits the same row).
func (s *APIKeyStore) Revoke(ctx context.Context, actorUserID, keyID uint64, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	res := s.db.WithContext(ctx).Model(&APIKey{}).
		Where("id = ? AND user_id = ?", keyID, actorUserID).
		Update("revoked_at", at.UTC())
	if res.Error != nil {
		return fmt.Errorf("revoke api key %d: %w", keyID, res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrAPIKeyNotFound
	}
	return nil
}

// Display state of an API key. Values are English and stable: page copy is mapped from the state by the language pack.
const (
	APIKeyStateActive  = "active"
	APIKeyStateRevoked = "revoked"
	APIKeyStateExpired = "expired"
)

// APIKeyState returns the usability state of one key, for the admin panel overview.
// Revoked is checked before expired: a revoked key no longer cares about expiry, and the state must be unique.
func APIKeyState(k *APIKey, now time.Time) string {
	switch {
	case k == nil:
		return ""
	case k.RevokedAt != nil:
		return APIKeyStateRevoked
	case k.ExpiresAt != nil && !k.ExpiresAt.After(now):
		return APIKeyStateExpired
	default:
		return APIKeyStateActive
	}
}

// ListAll pages through every user's keys (revoked / expired included), newest first, returning the total row count.
// Only for the admin panel overview; the return value is metadata, the DB has no plaintext and no plaintext column to echo back.
func (s *APIKeyStore) ListAll(ctx context.Context, limit, offset int) ([]APIKey, int64, error) {
	var total int64
	if err := s.db.WithContext(ctx).Model(&APIKey{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count api keys: %w", err)
	}
	var keys []APIKey
	if err := s.db.WithContext(ctx).Order("created_at DESC, id DESC").
		Limit(limit).Offset(offset).Find(&keys).Error; err != nil {
		return nil, 0, fmt.Errorf("list api keys: %w", err)
	}
	return keys, total, nil
}

// RevokeByID revokes any user's key by primary key, for the admin panel overview (revoking another's key is an admin action).
// The personal settings page uses Revoke (scoped to the owner); ownership is not checked here, the caller handles authorization.
func (s *APIKeyStore) RevokeByID(ctx context.Context, keyID uint64, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	res := s.db.WithContext(ctx).Model(&APIKey{}).Where("id = ?", keyID).
		Update("revoked_at", at.UTC())
	if res.Error != nil {
		return fmt.Errorf("revoke api key %d: %w", keyID, res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrAPIKeyNotFound
	}
	return nil
}

// RevokeAllForUser voids all of a user's live keys (called on password reset).
// A user changing their own password does not come through here: keys have a lifecycle independent of the password.
func (s *APIKeyStore) RevokeAllForUser(ctx context.Context, userID uint64, at time.Time) error {
	return s.RevokeAllForUserTx(ctx, s.db, userID, at)
}

// RevokeAllForUserTx bulk-revokes all of a user's live keys inside the transaction the caller provides.
// It only hits revoked_at IS NULL rows and is idempotent on repeat (isomorphic to the batch session void);
// it must share the same transaction handle as the password write, otherwise a failure leaves a half-revoke.
func (s *APIKeyStore) RevokeAllForUserTx(ctx context.Context, tx *gorm.DB, userID uint64, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if err := tx.WithContext(ctx).Model(&APIKey{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", at.UTC()).Error; err != nil {
		return fmt.Errorf("revoke user api keys: %w", err)
	}
	return nil
}
