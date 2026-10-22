package store

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// UserStore wraps GORM access to the users table; concrete types are preferred and interfaces are left to consumers to define as needed (AGENTS.md §2.4).
type UserStore struct {
	db *gorm.DB
}

// NewUserStore builds the user store.
func NewUserStore(db *gorm.DB) *UserStore { return &UserStore{db: db} }

// Create writes a new user; the caller translates a unique-constraint conflict into a stable error code.
func (s *UserStore) Create(ctx context.Context, u *User) error {
	return s.CreateTx(ctx, s.db, u)
}

// CreateTx writes a new user in the transaction given by the caller, so claiming the invite and creating the account share one transaction boundary.
func (s *UserStore) CreateTx(ctx context.Context, tx *gorm.DB, u *User) error {
	if err := tx.WithContext(ctx).Create(u).Error; err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

// ByID fetches a user by primary key; returns gorm.ErrRecordNotFound when absent.
func (s *UserStore) ByID(ctx context.Context, id uint64) (*User, error) {
	var u User
	if err := s.db.WithContext(ctx).First(&u, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// ByUsername fetches a user by login name (used by the login path).
func (s *UserStore) ByUsername(ctx context.Context, username string) (*User, error) {
	var u User
	if err := s.db.WithContext(ctx).First(&u, "username = ?", username).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// ByEmail fetches a user by email (used by OIDC auto-linking and admin lookups).
func (s *UserStore) ByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	if err := s.db.WithContext(ctx).First(&u, "email = ?", email).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// Update saves all of the user's fields; Save generates the right upsert on both databases.
func (s *UserStore) Update(ctx context.Context, u *User) error {
	if err := s.db.WithContext(ctx).Save(u).Error; err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	return nil
}

// SetPasswordHash changes only the password hash column, so a whole-row overwrite cannot lose a concurrent write.
func (s *UserStore) SetPasswordHash(ctx context.Context, id uint64, hash string) error {
	return s.SetPasswordHashTx(ctx, s.db, id, hash)
}

// SetPasswordHashTx changes the password hash column inside the transaction given by the caller.
// A password reset needs it: the password write and the session/API key revocation in the same transaction must either all take effect or none.
func (s *UserStore) SetPasswordHashTx(ctx context.Context, tx *gorm.DB, id uint64, hash string) error {
	if err := tx.WithContext(ctx).Model(&User{}).Where("id = ?", id).
		Update("password_hash", hash).Error; err != nil {
		return fmt.Errorf("set password hash: %w", err)
	}
	return nil
}

// ByIDTx fetches a user by primary key inside the transaction given by the caller; a password reset checks existence in the transaction, avoiding another connection.
func (s *UserStore) ByIDTx(ctx context.Context, tx *gorm.DB, id uint64) (*User, error) {
	var u User
	if err := tx.WithContext(ctx).First(&u, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// SetStatus changes only the account status column (active | disabled).
func (s *UserStore) SetStatus(ctx context.Context, id uint64, status string) error {
	if err := s.db.WithContext(ctx).Model(&User{}).Where("id = ?", id).
		Update("status", status).Error; err != nil {
		return fmt.Errorf("set user status: %w", err)
	}
	return nil
}

// CountActiveAdmins counts the admins who can still sign in; it prevents disabling the last admin (the onboarding page relies on it).
func (s *UserStore) CountActiveAdmins(ctx context.Context) (int64, error) {
	var n int64
	if err := s.db.WithContext(ctx).Model(&User{}).
		Where("role = ? AND status = ?", RoleAdmin, StatusActive).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count active admins: %w", err)
	}
	return n, nil
}
