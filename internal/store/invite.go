package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Stable sentinel values for invites; upper layers translate them uniformly into 403 and localized messages.
var (
	// ErrInviteNotFound means the token does not exist (including already revoked: revocation is implemented as deleting the row).
	ErrInviteNotFound = errors.New("invite not found")
	// ErrInviteUsed means the token was already used; one-time semantics forbid reuse.
	ErrInviteUsed = errors.New("invite already used")
	// ErrInviteExpired means the token is past expires_at.
	ErrInviteExpired = errors.New("invite expired")
	// ErrInviteEmailMismatch means the signup email does not match the email the invite is limited to.
	ErrInviteEmailMismatch = errors.New("invite is restricted to another email")
)

// InviteStore wraps the invites table; tokens are one-time, may be limited to an email, and may have an expiry.
//
// On 'revocation': the invites table has no revoked_at column, so revocation is implemented as deleting the row.
// A revoked token is refused the same way as 'does not exist' in ByToken/MarkUsed. If revocation auditing is ever needed,
// a revoked_at column should be added (which needs the models.go owner's schedule); this implementation does not add columns unasked.
type InviteStore struct {
	db *gorm.DB
}

// NewInviteStore constructs the invite store.
func NewInviteStore(db *gorm.DB) *InviteStore { return &InviteStore{db: db} }

// newInviteToken generates the URL-safe encoding of a 32-byte random token (same spec as share links).
func newInviteToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate invite token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Create writes one invite. When token is empty it is generated; a nil Email means no email restriction;
// a nil ExpiresAt means no expiry. When CreatedAt is empty it is filled with the current UTC time.
func (s *InviteStore) Create(ctx context.Context, inv *Invite) error {
	if inv.Token == "" {
		token, err := newInviteToken()
		if err != nil {
			return err
		}
		inv.Token = token
	}
	if inv.Role == "" {
		// string defaults are supplied by the store layer on the Go side (models.go package comment).
		inv.Role = RoleUser
	}
	if inv.CreatedAt.IsZero() {
		inv.CreatedAt = time.Now().UTC()
	}
	if err := s.db.WithContext(ctx).Create(inv).Error; err != nil {
		return fmt.Errorf("create invite: %w", err)
	}
	return nil
}

// ByToken fetches an invite by token; returns ErrInviteNotFound when absent.
func (s *InviteStore) ByToken(ctx context.Context, token string) (*Invite, error) {
	var inv Invite
	if err := s.db.WithContext(ctx).First(&inv, "token = ?", token).Error; err != nil {
		if IsNotFound(err) {
			return nil, ErrInviteNotFound
		}
		return nil, fmt.Errorf("load invite: %w", err)
	}
	return &inv, nil
}

// List returns all invites (used and expired included), newest first; for the M6 admin panel to show usage.
func (s *InviteStore) List(ctx context.Context) ([]Invite, error) {
	var invites []Invite
	if err := s.db.WithContext(ctx).Order("created_at DESC, id DESC").Find(&invites).Error; err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	return invites, nil
}

// Validate checks whether the token is usable right now and verifies the signup email against the invite's restriction.
// Used / expired / not found (including revoked) / email mismatch each return their own sentinel value.
func (s *InviteStore) Validate(ctx context.Context, token, email string, now time.Time) (*Invite, error) {
	inv, err := s.ByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	return inv, UsableInvite(inv, email, now)
}

// UsableInvite is a DB-independent usability check, so table-driven tests can cover it directly.
func UsableInvite(inv *Invite, email string, now time.Time) error {
	if inv.UsedAt != nil {
		return ErrInviteUsed
	}
	if inv.ExpiresAt != nil && !inv.ExpiresAt.After(now) {
		return ErrInviteExpired
	}
	if inv.Email != nil && strings.ToLower(strings.TrimSpace(*inv.Email)) != strings.ToLower(strings.TrimSpace(email)) {
		return ErrInviteEmailMismatch
	}
	return nil
}

// Accept consumes the invite and runs the account-creation callback in a single transaction.
//
// The old 'MarkUsed first, Release if creation fails' approach left an observable intermediate state between the two steps (showing used,
// then flipping back to usable). Here claiming and account creation share one transaction: either both commit or both roll back, and no half-finished state is visible outside.
// Claiming is still a conditional update (used_at IS NULL), so under concurrency only one transaction can claim the same token — one code, one use.
//
// create returns an error on a failed account creation, which triggers the whole rollback and leaves the token usable; on success used_by and used_at are persisted together.
func (s *InviteStore) Accept(ctx context.Context, token string, now time.Time, create func(tx *gorm.DB) (*User, error)) (*User, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var u *User
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&Invite{}).
			Where("token = ? AND used_at IS NULL", token).
			Update("used_at", now)
		if res.Error != nil {
			return fmt.Errorf("claim invite: %w", res.Error)
		}
		if res.RowsAffected != 1 {
			// under concurrency it was already claimed by someone else, or the token never existed / was already used.
			return ErrInviteUsed
		}
		created, cerr := create(tx)
		if cerr != nil {
			return cerr
		}
		u = created
		if err := tx.Model(&Invite{}).Where("token = ?", token).
			Update("used_by", u.ID).Error; err != nil {
			return fmt.Errorf("set invite used_by: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

// MarkUsed atomically marks the token used: only rows where used_at is still NULL are updated.
// Returning claimed=false means another party claimed it first under concurrency, and the caller must abandon account creation — this is
// the enforcement point for 'one invite creates only one user'.
func (s *InviteStore) MarkUsed(ctx context.Context, token string, at time.Time) (bool, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	res := s.db.WithContext(ctx).Model(&Invite{}).
		Where("token = ? AND used_at IS NULL", token).
		Update("used_at", at)
	if res.Error != nil {
		return false, fmt.Errorf("mark invite used: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// Release rolls back one MarkUsed: it clears used_at when account creation failed, so the invite stays usable.
func (s *InviteStore) Release(ctx context.Context, token string) error {
	if err := s.db.WithContext(ctx).Model(&Invite{}).
		Where("token = ?", token).
		Update("used_at", nil).Error; err != nil {
		return fmt.Errorf("release invite: %w", err)
	}
	return nil
}

// Revoke revokes an invite: deletes the whole row. Returns ErrInviteNotFound when the row does not exist.
func (s *InviteStore) Revoke(ctx context.Context, id uint64) error {
	res := s.db.WithContext(ctx).Delete(&Invite{}, "id = ?", id)
	if res.Error != nil {
		return fmt.Errorf("revoke invite: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrInviteNotFound
	}
	return nil
}

// DeleteExpired deletes expired invite rows, returning the number of rows deleted.
//
// **Only rows with a non-null expires_at that has passed are deleted**: expires_at is nullable, and NULL means 'this invite never expires',
// so it must not be cleaned up as expired. Likewise, a used (used_at non-null) but unexpired invite is kept — it is the evidence of 'who used
// this invite', and is reclaimed once it naturally expires.
func (s *InviteStore) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	res := s.db.WithContext(ctx).
		Where("expires_at IS NOT NULL AND expires_at <= ?", before).
		Delete(&Invite{})
	if res.Error != nil {
		return 0, fmt.Errorf("delete expired invites: %w", res.Error)
	}
	return res.RowsAffected, nil
}
