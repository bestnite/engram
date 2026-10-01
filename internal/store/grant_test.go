package store

import (
	"context"
	"testing"
)

// TestGrantStoreLifecycle 覆盖 deck_grants 的授予 / 改角色 / 查询 / 撤销（M5-1）。
func TestGrantStoreLifecycle(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "grant_owner", "grant_member")
			owner, member := users[0], users[1]
			presetID := seedPresetRow(t, db, owner)
			d := Deck{OwnerUserID: owner, Name: "shared", PresetID: presetID}
			if err := NewDeckStore(db).Create(ctx, &d); err != nil {
				t.Fatalf("create deck: %v", err)
			}
			gs := NewGrantStore(db)

			// 没有授权行时返回空串（“无授权”不是错误）。
			if role, err := gs.Role(ctx, d.ID, member); err != nil || role != "" {
				t.Fatalf("Role(before grant) = (%q,%v), want (\"\",nil)", role, err)
			}

			// 非法角色被拒，不会落库。
			if err := gs.Grant(ctx, d.ID, member, "admin", nil); err == nil {
				t.Fatalf("Grant(invalid role) succeeded, want error")
			}
			if role, _ := gs.Role(ctx, d.ID, member); role != "" {
				t.Fatalf("invalid role was persisted: %q", role)
			}

			// 授予 reader。
			if err := gs.Grant(ctx, d.ID, member, RoleReader, Ptr(owner)); err != nil {
				t.Fatalf("grant reader: %v", err)
			}
			if role, err := gs.Role(ctx, d.ID, member); err != nil || role != RoleReader {
				t.Fatalf("Role(after grant) = (%q,%v), want (%q,nil)", role, err, RoleReader)
			}

			// 改角色是原地更新，不产生第二行。
			if err := gs.Grant(ctx, d.ID, member, RoleEditor, Ptr(owner)); err != nil {
				t.Fatalf("change role: %v", err)
			}
			if role, _ := gs.Role(ctx, d.ID, member); role != RoleEditor {
				t.Fatalf("Role(after change) = %q, want %q", role, RoleEditor)
			}
			rows, err := gs.ListByDeck(ctx, d.ID)
			if err != nil || len(rows) != 1 {
				t.Fatalf("ListByDeck = (%d rows,%v), want (1,nil)", len(rows), err)
			}

			ids, err := gs.DeckIDsForUser(ctx, member)
			if err != nil || len(ids) != 1 || ids[0] != d.ID {
				t.Fatalf("DeckIDsForUser = (%v,%v), want [%d]", ids, err, d.ID)
			}

			// 撤销即删除整行；下一次查询立刻为空（§5“立即生效”）。
			if err := gs.Revoke(ctx, d.ID, member); err != nil {
				t.Fatalf("revoke: %v", err)
			}
			if role, _ := gs.Role(ctx, d.ID, member); role != "" {
				t.Fatalf("Role(after revoke) = %q, want \"\"", role)
			}
			if rows, _ := gs.ListByDeck(ctx, d.ID); len(rows) != 0 {
				t.Fatalf("ListByDeck after revoke = %d rows, want 0", len(rows))
			}
		})
	}
}

// TestRoleAllows 断言 §5 权限表的“至少”语义：owner > editor > reader，未知角色一律无权。
func TestRoleAllows(t *testing.T) {
	cases := []struct {
		have, want string
		ok         bool
	}{
		{RoleOwner, RoleOwner, true},
		{RoleOwner, RoleEditor, true},
		{RoleOwner, RoleReader, true},
		{RoleEditor, RoleOwner, false},
		{RoleEditor, RoleEditor, true},
		{RoleEditor, RoleReader, true},
		{RoleReader, RoleOwner, false},
		{RoleReader, RoleEditor, false},
		{RoleReader, RoleReader, true},
		{"", RoleReader, false},
		{RoleOwner, "", false},
		{RoleOwner, "admin", false},
		{"root", RoleReader, false},
	}
	for _, c := range cases {
		if got := RoleAllows(c.have, c.want); got != c.ok {
			t.Errorf("RoleAllows(%q,%q) = %v, want %v", c.have, c.want, got, c.ok)
		}
	}
	for _, role := range []string{RoleOwner, RoleEditor, RoleReader} {
		if !ValidRole(role) {
			t.Errorf("ValidRole(%q) = false, want true", role)
		}
	}
	for _, role := range []string{"", "admin", "root"} {
		if ValidRole(role) {
			t.Errorf("ValidRole(%q) = true, want false", role)
		}
	}
}
