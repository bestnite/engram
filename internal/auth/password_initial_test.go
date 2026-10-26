package auth

import (
	"context"
	"errors"
	"testing"
)

// TestSetInitialPassword covers the first-password path: it succeeds only while the account has no hash.
func TestSetInitialPassword(t *testing.T) {
	ctx := context.Background()
	e := newLinkEnv(t)
	passwordless, err := e.accounts.CreateOIDCUser(ctx, CreateUserInput{
		Username: "oidc-user", Email: "oidc-user@example.com",
	})
	if err != nil {
		t.Fatalf("CreateOIDCUser: %v", err)
	}
	if err := e.accounts.SetInitialPassword(ctx, passwordless.ID, "Sup3rSecret!", ""); err != nil {
		t.Fatalf("SetInitialPassword() error = %v", err)
	}
	stored, err := e.users.ByID(ctx, passwordless.ID)
	if err != nil || stored.PasswordHash == nil {
		t.Fatalf("stored password hash = %v, err=%v, want set", stored.PasswordHash, err)
	}
	if err := e.accounts.SetInitialPassword(ctx, passwordless.ID, "An0therStrongPass!", ""); !errors.Is(err, ErrPasswordAlreadySet) {
		t.Fatalf("second SetInitialPassword() error = %v, want %v", err, ErrPasswordAlreadySet)
	}

	local := e.seedLocalUser(t, "alice", "alice@example.com")
	if err := e.accounts.SetInitialPassword(ctx, local.ID, "An0therStrongPass!", ""); !errors.Is(err, ErrPasswordAlreadySet) {
		t.Fatalf("SetInitialPassword() on a local account error = %v, want %v", err, ErrPasswordAlreadySet)
	}
}
