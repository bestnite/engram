package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// Stable error values of the account service; the transport layer maps them to stable English codes.
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserDisabled       = errors.New("user is disabled")
	ErrPasswordUnchanged  = errors.New("new password must differ from the current one")
)

// CreateUserInput is the input for creating a local account; empty fields are filled with defaults by the service.
type CreateUserInput struct {
	Username      string
	Email         string
	DisplayName   string
	Password      string
	Role          string
	Locale        string
	Timezone      string
	DayCutoffHour *int
}

// AccountService owns the core local account flows: create, authenticate, change password, disable.
// It holds the UserStore, SessionStore and APIKeyStore, so on a password reset it can revoke sessions and API keys in the same
// transaction; a user-initiated password change revokes sessions only and leaves keys alone.
type AccountService struct {
	users    *store.UserStore
	sessions *store.SessionStore
	keys     *store.APIKeyStore
	hasher   *PasswordHasher
	now      func() time.Time
}

// NewAccountService constructs the account service; no dependency may be missing.
func NewAccountService(users *store.UserStore, sessions *store.SessionStore, keys *store.APIKeyStore, hasher *PasswordHasher) (*AccountService, error) {
	if users == nil || sessions == nil || keys == nil {
		return nil, errors.New("auth: user store, session store and api key store are required")
	}
	if hasher == nil {
		hasher = DefaultPasswordHasher()
	}
	return &AccountService{users: users, sessions: sessions, keys: keys, hasher: hasher,
		now: func() time.Time { return time.Now().UTC() }}, nil
}

// CreateLocalUser validates the password policy, hashes the password and writes the user; the email is case-normalized and the login name is kept as given.
func (s *AccountService) CreateLocalUser(ctx context.Context, in CreateUserInput) (*store.User, error) {
	u, err := s.prepareLocalUser(in)
	if err != nil {
		return nil, err
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

// CreateLocalUserTx creates a local account inside the caller's transaction: invite consumption and account creation therefore share one transaction.
// Validation and hashing go through the same preparation path as CreateLocalUser, so the two account-creation paths cannot drift apart.
func (s *AccountService) CreateLocalUserTx(ctx context.Context, tx *gorm.DB, in CreateUserInput) (*store.User, error) {
	u, err := s.prepareLocalUser(in)
	if err != nil {
		return nil, err
	}
	if err := s.users.CreateTx(ctx, tx, u); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

// prepareLocalUser validates the password policy, hashes the password and normalizes the input into a user ready to persist; it does not touch the database.
func (s *AccountService) prepareLocalUser(in CreateUserInput) (*store.User, error) {
	if err := ValidatePasswordPolicy(in.Password); err != nil {
		return nil, err
	}
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return nil, err
	}
	return newUserFromInput(in, &hash, s.now())
}

// CreateOIDCUser creates a pure OIDC account (password_hash = NULL).
//
// The only difference from CreateLocalUser is the credential: no password policy check and no hash derivation — an OIDC account logs in
// through its bound external identity (onboarding flow, providing account creation). Username and email are still required,
// otherwise the unique constraints and later recovery would lose their basis.
func (s *AccountService) CreateOIDCUser(ctx context.Context, in CreateUserInput) (*store.User, error) {
	u, err := newUserFromInput(in, nil, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

// newUserFromInput normalizes the input into a user ready to persist; a nil passwordHash means no local password.
// Defaults are centralized here so the two account-creation paths cannot drift (AGENTS.md §2.6: one behavior, one implementation).
func newUserFromInput(in CreateUserInput, passwordHash *string, now time.Time) (*store.User, error) {
	username := strings.TrimSpace(in.Username)
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if username == "" || email == "" {
		return nil, errors.New("username and email are required")
	}
	role := in.Role
	if role == "" {
		role = store.RoleUser
	}
	locale := in.Locale
	if locale == "" {
		locale = "zh-CN"
	}
	tz := in.Timezone
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	cutoff := in.DayCutoffHour
	if cutoff != nil && (*cutoff < 0 || *cutoff > 23) {
		return nil, errors.New("day cutoff hour must be between 0 and 23")
	}
	display := strings.TrimSpace(in.DisplayName)
	if display == "" {
		display = username
	}
	return &store.User{
		Username:      username,
		Email:         email,
		PasswordHash:  passwordHash,
		DisplayName:   display,
		Role:          role,
		Status:        store.StatusActive,
		Locale:        locale,
		Timezone:      tz,
		DayCutoffHour: cutoff,
		CreatedAt:     now,
	}, nil
}

// Authenticate checks username/password; disabled users are always rejected, distinguishing no detail beyond "wrong password" versus "account disabled".
func (s *AccountService) Authenticate(ctx context.Context, username, password string) (*store.User, error) {
	u, err := s.users.ByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		if store.IsNotFound(err) {
			// A missing user and a wrong password return the same error; here we additionally run one argon2id with the same parameters,
			// making the two paths indistinguishable by response time and closing the side channel that enumerates usernames by latency.
			// The result must be discarded: the error semantics stay unchanged.
			s.hasher.VerifyDummy(password)
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("load user: %w", err)
	}
	if u.PasswordHash == nil {
		// A pure OIDC account cannot log in with a password; without leveling the cost, that branch would be equally enumerable thanks to its fast return.
		s.hasher.VerifyDummy(password)
		return nil, ErrInvalidCredentials
	}
	ok, err := s.hasher.verify(*u.PasswordHash, password)
	if err != nil {
		return nil, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return nil, ErrInvalidCredentials
	}
	if u.Status != store.StatusActive {
		return nil, ErrUserDisabled
	}
	return u, nil
}

// ChangePassword validates the old password, its strength and that the new one differs, writes the new hash and revokes all of the user's sessions.
func (s *AccountService) ChangePassword(ctx context.Context, userID uint64, oldPassword, newPassword string) error {
	return s.changePassword(ctx, userID, oldPassword, newPassword, "")
}

// ChangePasswordKeepingSession is the same as ChangePassword but keeps the single session keepSessionID:
// when the user changes their password on the settings page, other devices go offline while the current window stays usable (otherwise they would be logged out right after changing it).
// Writing the audit entry is the caller's responsibility (only it knows the request origin).
func (s *AccountService) ChangePasswordKeepingSession(ctx context.Context, userID uint64, keepSessionID, oldPassword, newPassword string) error {
	return s.changePassword(ctx, userID, oldPassword, newPassword, keepSessionID)
}

// changePassword is the single implementation of both password-change paths; an empty keepSessionID means revoke all sessions.
func (s *AccountService) changePassword(ctx context.Context, userID uint64, oldPassword, newPassword, keepSessionID string) error {
	u, err := s.users.ByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	if u.PasswordHash == nil {
		return ErrInvalidCredentials
	}
	ok, err := s.hasher.verify(*u.PasswordHash, oldPassword)
	if err != nil {
		return fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return ErrInvalidCredentials
	}
	if oldPassword == newPassword {
		return ErrPasswordUnchanged
	}
	if err := ValidatePasswordPolicy(newPassword); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return err
	}
	if err := s.users.SetPasswordHash(ctx, userID, hash); err != nil {
		return err
	}
	if keepSessionID != "" {
		return s.sessions.RevokeAllForUserExcept(ctx, userID, keepSessionID, s.now())
	}
	return s.sessions.RevokeAllForUser(ctx, userID, s.now())
}

// DisableUser marks the account disabled and revokes all of its sessions; even if a session row were read back directly, the middleware rejects it because the status is no longer active.
func (s *AccountService) DisableUser(ctx context.Context, userID uint64) error {
	u, err := s.users.ByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	u.Status = store.StatusDisabled
	if err := s.users.Update(ctx, u); err != nil {
		return err
	}
	return s.sessions.RevokeAllForUser(ctx, userID, s.now())
}

// EnableUser re-enables the account; it restores no old sessions (the user has to log in again).
func (s *AccountService) EnableUser(ctx context.Context, userID uint64) error {
	u, err := s.users.ByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	u.Status = store.StatusActive
	return s.users.Update(ctx, u)
}
