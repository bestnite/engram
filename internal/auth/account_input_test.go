package auth

import (
	"context"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

func TestValidEmail(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"alice@example.com", true},
		{"a.b+tag@example.org", true},
		{"", false},
		{"not-an-address", false},
		{"Alice <alice@example.com>", false}, // a display-name form would be stored verbatim and never match a sign-in
		{"alice@example.com, bob@example.com", false},
	}
	for _, tc := range cases {
		if got := ValidEmail(tc.in); got != tc.want {
			t.Errorf("ValidEmail(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestAccountInputCode(t *testing.T) {
	good := "correct-horse-battery-staple"
	cases := []struct {
		name, username, email, password, want string
	}{
		{"acceptable", "alice", "alice@example.com", good, ""},
		{"missing username", "", "alice@example.com", good, "username_required"},
		{"missing email", "alice", "", good, "email_required"},
		{"malformed email", "alice", "alice", good, "email_invalid"},
		{"display-name email", "alice", "Alice <alice@example.com>", good, "email_invalid"},
		{"missing password", "alice", "alice@example.com", "", "password_required"},
		{"short password", "alice", "alice@example.com", "short", "password_too_short"},
		{"long password", "alice", "alice@example.com", strings.Repeat("x", MaxPasswordLength+1), "password_too_long"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AccountInputCode(tc.username, tc.email, tc.password); got != tc.want {
				t.Errorf("AccountInputCode() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCreateUserNormalisesAndValidatesEmail: account creation stores the canonical address and refuses a malformed one
// even when a caller skipped AccountInputCode, so no creation path can store an address a user could never sign in with.
func TestCreateUserNormalisesAndValidatesEmail(t *testing.T) {
	cases := []struct {
		name, email, wantStored string
		wantErr                 bool
	}{
		{name: "mixed case and spaces are normalised", email: "  Alice@Example.COM ", wantStored: "alice@example.com"},
		{name: "display-name form is refused", email: "Alice <alice@example.com>", wantErr: true},
		{name: "malformed address is refused", email: "alice", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			u, err := env.accounts.CreateLocalUser(context.Background(), CreateUserInput{
				Username: "alice", Email: tc.email, Password: "correct-horse-battery-staple",
			})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("CreateLocalUser(%q) succeeded, want an error", tc.email)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateLocalUser: %v", err)
			}
			if u.Email != tc.wantStored {
				t.Errorf("stored email = %q, want %q", u.Email, tc.wantStored)
			}
			// Lookups normalise too, so the address can be found however it is typed.
			found, err := env.users.ByEmail(context.Background(), " ALICE@example.com")
			if err != nil || found.ID != u.ID {
				t.Errorf("ByEmail with another spelling = %v, %v; want user %d", found, err, u.ID)
			}
		})
	}
	if got := store.NormalizeEmail("  Bob@Example.COM "); got != "bob@example.com" {
		t.Errorf("NormalizeEmail = %q, want bob@example.com", got)
	}
}
