package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ErrIdentityNotFound means no link was found under the given conditions; upper layers use it to tell this apart from a real database failure.
var ErrIdentityNotFound = errors.New("identity not found")

// IdentityStore wraps GORM access to the identities table.
// The (provider, subject) unique constraint is defined by the model, so a duplicate link errors on Create.
type IdentityStore struct {
	db *gorm.DB
}

// NewIdentityStore constructs the external identity store.
func NewIdentityStore(db *gorm.DB) *IdentityStore { return &IdentityStore{db: db} }

// Create writes one link; when LinkedAt is the zero value it is filled with the current UTC time.
func (s *IdentityStore) Create(ctx context.Context, ident *Identity) error {
	if ident.LinkedAt.IsZero() {
		ident.LinkedAt = time.Now().UTC()
	}
	if err := s.db.WithContext(ctx).Create(ident).Error; err != nil {
		return fmt.Errorf("create identity: %w", err)
	}
	return nil
}

// ByProviderSubject fetches a link by (provider, subject); returns ErrIdentityNotFound when absent.
func (s *IdentityStore) ByProviderSubject(ctx context.Context, provider, subject string) (*Identity, error) {
	var ident Identity
	if err := s.db.WithContext(ctx).
		First(&ident, "provider = ? AND subject = ?", provider, subject).Error; err != nil {
		if IsNotFound(err) {
			return nil, ErrIdentityNotFound
		}
		return nil, fmt.Errorf("load identity: %w", err)
	}
	return &ident, nil
}

// ByID fetches a link by primary key; returns ErrIdentityNotFound when absent.
func (s *IdentityStore) ByID(ctx context.Context, id uint64) (*Identity, error) {
	var ident Identity
	if err := s.db.WithContext(ctx).First(&ident, "id = ?", id).Error; err != nil {
		if IsNotFound(err) {
			return nil, ErrIdentityNotFound
		}
		return nil, fmt.Errorf("load identity: %w", err)
	}
	return &ident, nil
}

// ListForUser returns every external identity linked to an internal user (a user may have several).
// Used by the admin panel (M6) when viewing the link list; ordered by link time ascending so display order is stable.
func (s *IdentityStore) ListForUser(ctx context.Context, userID uint64) ([]Identity, error) {
	var rows []Identity
	if err := s.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("linked_at ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list identities: %w", err)
	}
	return rows, nil
}

// ListAll returns all external identities (with their user), for the admin panel's 'linked identity list'.
// Ordered by user, then link time, so the order is stable across requests; callers join user information by user_id.
func (s *IdentityStore) ListAll(ctx context.Context) ([]Identity, error) {
	var rows []Identity
	if err := s.db.WithContext(ctx).Order("user_id ASC, linked_at ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list identities: %w", err)
	}
	return rows, nil
}

// Delete unlinks one external identity (by primary key). Returns ErrIdentityNotFound when the row does not exist.
//
// Unlinking is an M6 admin panel backend capability: this round provides only storage and tests, with no UI.
// The row is deleted rather than soft-deleted because the identities table has no revoked_at column,
// and adding a column unasked is models.go single-writer work.
func (s *IdentityStore) Delete(ctx context.Context, id uint64) error {
	res := s.db.WithContext(ctx).Delete(&Identity{}, "id = ?", id)
	if res.Error != nil {
		return fmt.Errorf("delete identity: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrIdentityNotFound
	}
	return nil
}
