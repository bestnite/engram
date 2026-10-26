package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestIdentityStoreDeleteForUser covers the transactional last-credential guard: a passwordless account keeps one sign-in method,
// and an identity that is already gone reports not-found on the second delete.
func TestIdentityStoreDeleteForUser(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := NewUserStore(db)
			identities := NewIdentityStore(db)
			now := time.Now().UTC()

			passwordless := &User{Username: "oidc", Email: "oidc@example.com", DisplayName: "OIDC",
				Role: RoleUser, Status: StatusActive, Locale: "zh-CN", Timezone: "UTC", CreatedAt: now}
			if err := users.Create(ctx, passwordless); err != nil {
				t.Fatalf("create passwordless user: %v", err)
			}
			first := &Identity{UserID: passwordless.ID, Provider: "issuer.example.com", Subject: "sub-1"}
			second := &Identity{UserID: passwordless.ID, Provider: "issuer.example.com", Subject: "sub-2"}
			for _, ident := range []*Identity{first, second} {
				if err := identities.Create(ctx, ident); err != nil {
					t.Fatalf("create identity: %v", err)
				}
			}

			// With two identities the first unlink succeeds; the second would leave zero credentials and is refused.
			if err := identities.DeleteForUser(ctx, first.ID, passwordless.ID); err != nil {
				t.Fatalf("first DeleteForUser() error = %v", err)
			}
			if err := identities.DeleteForUser(ctx, second.ID, passwordless.ID); !errors.Is(err, ErrLastCredential) {
				t.Fatalf("last-credential DeleteForUser() error = %v, want %v", err, ErrLastCredential)
			}
			rows, err := identities.ListForUser(ctx, passwordless.ID)
			if err != nil || len(rows) != 1 {
				t.Fatalf("passwordless identities = %d (err %v), want 1", len(rows), err)
			}

			// A local account with a password may remove its last identity, and deleting the same row again reports not-found.
			hash := "$argon2id$v=19$m=1024,t=1,p=1$c2FsdA$aGFzaA"
			local := &User{Username: "local", Email: "local@example.com", DisplayName: "Local", PasswordHash: &hash,
				Role: RoleUser, Status: StatusActive, Locale: "zh-CN", Timezone: "UTC", CreatedAt: now}
			if err := users.Create(ctx, local); err != nil {
				t.Fatalf("create local user: %v", err)
			}
			only := &Identity{UserID: local.ID, Provider: "issuer.example.com", Subject: "sub-local"}
			if err := identities.Create(ctx, only); err != nil {
				t.Fatalf("create local identity: %v", err)
			}
			if err := identities.DeleteForUser(ctx, only.ID, local.ID); err != nil {
				t.Fatalf("DeleteForUser() with a password error = %v", err)
			}
			if err := identities.DeleteForUser(ctx, only.ID, local.ID); !errors.Is(err, ErrIdentityNotFound) {
				t.Fatalf("second DeleteForUser() error = %v, want %v", err, ErrIdentityNotFound)
			}
		})
	}
}
