package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

// TestInviteAcceptRollsBackOnCreateFailure is the counter-case: when the account-creation callback fails the invite must stay usable,
// and the whole flow has no 'used → usable again' intermediate state — claiming and account creation roll back together in the same transaction.
func TestInviteAcceptRollsBackOnCreateFailure(t *testing.T) {
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

	boom := errors.New("simulated user creation failure")
	if _, err := invites.Accept(ctx, inv.Token, time.Now().UTC(), func(tx *gorm.DB) (*User, error) {
		return nil, boom
	}); !errors.Is(err, boom) {
		t.Fatalf("Accept() with a failing create error = %v, want %v", err, boom)
	}

	// after the rollback the token is still usable: used_at and used_by are both empty.
	after, err := invites.ByToken(ctx, inv.Token)
	if err != nil {
		t.Fatalf("ByToken() error = %v", err)
	}
	if after.UsedAt != nil || after.UsedBy != nil {
		t.Fatalf("invite after a failed accept = %+v, want used_at and used_by still nil", after)
	}

	// the same token can still be accepted once, and used_by points at the newly created user.
	u, err := invites.Accept(ctx, inv.Token, time.Now().UTC(), func(tx *gorm.DB) (*User, error) {
		created := &User{Username: "invitee", Email: "invitee@example.com", Role: RoleUser, Status: StatusActive}
		if err := tx.Create(created).Error; err != nil {
			return nil, err
		}
		return created, nil
	})
	if err != nil {
		t.Fatalf("Accept() after rollback error = %v", err)
	}
	used, err := invites.ByToken(ctx, inv.Token)
	if err != nil {
		t.Fatalf("ByToken() error = %v", err)
	}
	if used.UsedAt == nil || used.UsedBy == nil || *used.UsedBy != u.ID {
		t.Fatalf("invite after a successful accept = %+v, want used_at and used_by=%d", used, u.ID)
	}

	// a consumed token cannot be accepted again (one code, one use).
	if _, err := invites.Accept(ctx, inv.Token, time.Now().UTC(), func(tx *gorm.DB) (*User, error) {
		t.Fatal("create callback must not run for a used invite")
		return nil, nil
	}); !errors.Is(err, ErrInviteUsed) {
		t.Fatalf("Accept() on a used invite error = %v, want ErrInviteUsed", err)
	}
}
