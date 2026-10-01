package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

// newInviteTestDB 建一个已迁移的全新 SQLite 库文件；邀请测试只需 invites 表，但迁移全模型更贴近生产。
func newInviteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbs := testDatabases(t)
	return dbs["sqlite"]
}

// TestInviteCreateAndList 断言创建后能取回，且 token 自动生成、角色默认 user。
func TestInviteCreateAndList(t *testing.T) {
	db := newInviteTestDB(t)
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	ctx := context.Background()
	invites := NewInviteStore(db)

	email := "Invitee@Example.com"
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

// TestUsableInvite 是纯判定矩阵：已用、已过期、邮箱不符分别被拒。
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
		{name: "email restricted match", inv: &Invite{Email: &email}, email: "Invitee@Example.com"},
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

// TestInviteMarkUsedIsOneShot 断言一次邀请只能被消费一次（并发语义的强制点）。
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
	// 第二次必须失败，且 token 不再可用。
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

// TestInviteRevokeDeletesToken 断言撤销后 token 与“不存在”同路被拒。
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
	// 再次撤销已不存在的行同样报 not found，而不是静默成功。
	if err := invites.Revoke(ctx, inv.ID); !errors.Is(err, ErrInviteNotFound) {
		t.Errorf("Revoke(again) error = %v, want ErrInviteNotFound", err)
	}
}

// TestInviteReleaseRestoresToken 断言建号失败后的回滚让邀请恢复可用。
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
