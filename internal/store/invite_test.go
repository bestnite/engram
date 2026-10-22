package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

// newInviteTestDB creates a freshly migrated SQLite file; invite tests only need the invites table, but migrating all models is closer to production.
func newInviteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbs := testDatabases(t)
	return dbs["sqlite"]
}

// TestInviteCreateAndList asserts that a created invite can be fetched back, with the token auto-generated and the role defaulting to user.
func TestInviteCreateAndList(t *testing.T) {
	db := newInviteTestDB(t)
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	ctx := context.Background()
	invites := NewInviteStore(db)

	email := "Invitee@example.com"
	expires := time.Now().UTC().Add(24 * time.Hour)
	inv := &Invite{Email: &email, ExpiresAt: &expires}
	if err := invites.Create(ctx, inv); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if inv.Token == "" {
		t.Fatal("Create() did not generate a token")
	}
	if inv.Role != RoleUser {
		t.Errorf("Create() role = %q, want %q", inv.Role, RoleUser)
	}

	got, err := invites.ByToken(ctx, inv.Token)
	if err != nil {
		t.Fatalf("ByToken() error = %v", err)
	}
	if got.ID != inv.ID || got.Email == nil || *got.Email != email {
		t.Errorf("ByToken() = %+v, want id %d with restricted email", got, inv.ID)
	}

	list, err := invites.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 1 || list[0].ID != inv.ID {
		t.Errorf("List() = %+v, want exactly invite %d", list, inv.ID)
	}
}

// TestUsableInvite is a pure decision matrix: used, expired and email-mismatch are each rejected.
func TestUsableInvite(t *testing.T) {
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	used := now.Add(-time.Minute)
	email := "invitee@example.com"

	cases := []struct {
		name    string
		inv     *Invite
		email   string
		wantErr error
	}{
		{name: "fresh invite", inv: &Invite{ExpiresAt: &future}, email: "anyone@example.com"},
		{name: "no expiry means usable", inv: &Invite{}, email: "anyone@example.com"},
		{name: "used invite rejected", inv: &Invite{UsedAt: &used}, email: "anyone@example.com", wantErr: ErrInviteUsed},
		{name: "expired invite rejected", inv: &Invite{ExpiresAt: &past}, email: "anyone@example.com", wantErr: ErrInviteExpired},
		{name: "email restricted match", inv: &Invite{Email: &email}, email: "Invitee@example.com"},
		{name: "email restricted mismatch", inv: &Invite{Email: &email}, email: "someone@example.com", wantErr: ErrInviteEmailMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := UsableInvite(tc.inv, tc.email, now)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("UsableInvite() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("UsableInvite() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestInviteMarkUsedIsOneShot asserts that an invite can be consumed only once (the enforcement point for the concurrency semantics).
func TestInviteMarkUsedIsOneShot(t *testing.T) {
	db := newInviteTestDB(t)
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	ctx := context.Background()
	invites := NewInviteStore(db)

	inv := &Invite{}
	if err := invites.Create(ctx, inv); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	claimed, err := invites.MarkUsed(ctx, inv.Token, time.Now().UTC())
	if err != nil {
		t.Fatalf("MarkUsed() error = %v", err)
	}
	if !claimed {
		t.Fatal("first MarkUsed() claimed = false, want true")
	}
	// the second attempt must fail, and the token is no longer usable.
	again, err := invites.MarkUsed(ctx, inv.Token, time.Now().UTC())
	if err != nil {
		t.Fatalf("second MarkUsed() error = %v", err)
	}
	if again {
		t.Error("second MarkUsed() claimed = true, want false")
	}
	got, err := invites.ByToken(ctx, inv.Token)
	if err != nil {
		t.Fatalf("ByToken() error = %v", err)
	}
	if err := UsableInvite(got, "anyone@example.com", time.Now().UTC()); !errors.Is(err, ErrInviteUsed) {
		t.Errorf("UsableInvite(after use) = %v, want ErrInviteUsed", err)
	}
}

// TestInviteRevokeDeletesToken asserts that a revoked token is refused the same way as 'does not exist'.
func TestInviteRevokeDeletesToken(t *testing.T) {
	db := newInviteTestDB(t)
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	ctx := context.Background()
	invites := NewInviteStore(db)

	inv := &Invite{}
	if err := invites.Create(ctx, inv); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := invites.Revoke(ctx, inv.ID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if _, err := invites.ByToken(ctx, inv.Token); !errors.Is(err, ErrInviteNotFound) {
		t.Errorf("ByToken(revoked) error = %v, want ErrInviteNotFound", err)
	}
	// revoking an already missing row likewise reports not found instead of silently succeeding.
	if err := invites.Revoke(ctx, inv.ID); !errors.Is(err, ErrInviteNotFound) {
		t.Errorf("Revoke(again) error = %v, want ErrInviteNotFound", err)
	}
}

// TestInviteReleaseRestoresToken asserts that the rollback after a failed account creation makes the invite usable again.
func TestInviteReleaseRestoresToken(t *testing.T) {
	db := newInviteTestDB(t)
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	ctx := context.Background()
	invites := NewInviteStore(db)

	inv := &Invite{}
	if err := invites.Create(ctx, inv); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if claimed, err := invites.MarkUsed(ctx, inv.Token, time.Now().UTC()); err != nil || !claimed {
		t.Fatalf("MarkUsed() = (%v, %v), want (true, nil)", claimed, err)
	}
	if err := invites.Release(ctx, inv.Token); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	rendered, err := invites.ByToken(ctx, inv.Token)
	if err != nil {
		t.Fatalf("ByToken() error = %v", err)
	}
	if err := UsableInvite(rendered, "anyone@example.com", time.Now().UTC()); err != nil {
		t.Errorf("UsableInvite(after release) = %v, want nil", err)
	}
}
