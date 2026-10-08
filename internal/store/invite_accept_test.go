package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

// TestInviteAcceptRollsBackOnCreateFailure 是反面用例：建号回调失败时邀请必须保持可用，
// 且整个过程没有“已使用→又变可用”的中间态 —— 占用与建号在同一个事务里一起回滚。
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

	// 回滚后 token 仍可用：used_at 与 used_by 都为空。
	after, err := invites.ByToken(ctx, inv.Token)
	if err != nil {
		t.Fatalf("ByToken() error = %v", err)
	}
	if after.UsedAt != nil || after.UsedBy != nil {
		t.Fatalf("invite after a failed accept = %+v, want used_at and used_by still nil", after)
	}

	// 同一 token 仍能成功接受一次，且 used_by 指向新建用户。
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

	// 已消费的 token 不能再被接受（一码一用）。
	if _, err := invites.Accept(ctx, inv.Token, time.Now().UTC(), func(tx *gorm.DB) (*User, error) {
		t.Fatal("create callback must not run for a used invite")
		return nil, nil
	}); !errors.Is(err, ErrInviteUsed) {
		t.Fatalf("Accept() on a used invite error = %v, want ErrInviteUsed", err)
	}
}
