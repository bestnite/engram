package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

// seedUsers 建出测试用的若干用户，返回其主键；用户名与邮箱都由调用方给出的前缀生成，避免唯一约束冲突。
func seedUsers(t *testing.T, db *gorm.DB, names ...string) []uint64 {
	t.Helper()
	now := time.Now().UTC()
	ids := make([]uint64, 0, len(names))
	for _, name := range names {
		u := User{Username: name, Email: name + "@example.com", DisplayName: name, Role: RoleUser,
			Status: StatusActive, Locale: "zh-CN", Timezone: "Asia/Shanghai", CreatedAt: now}
		if err := db.Create(&u).Error; err != nil {
			t.Fatalf("create user %s: %v", name, err)
		}
		ids = append(ids, u.ID)
	}
	return ids
}

// seedPresetRow 直接写一行预设，供 deck 的 preset_id 引用；字段齐全避免默认值陷阱。
func seedPresetRow(t *testing.T, db *gorm.DB, ownerID uint64) uint64 {
	t.Helper()
	now := time.Now().UTC()
	p := Preset{OwnerUserID: ownerID, Name: "default", DesiredRetention: 0.9,
		LearningSteps: "1m,10m", RelearningSteps: "10m", MaximumIntervalDays: 36500,
		EnableFuzz: Ptr(true), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	return p.ID
}

func TestDeckStoreCRUD(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "deck_owner_a", "deck_owner_b")
			ownerA, ownerB := users[0], users[1]
			presetID := seedPresetRow(t, db, ownerA)
			presetB := seedPresetRow(t, db, ownerB)
			decks := NewDeckStore(db)

			// create：owner 归属由 OwnerUserID 给出，未填可见性时落到默认 private。
			d := &Deck{OwnerUserID: ownerA, Name: "Deck A", PresetID: presetID}
			if err := decks.Create(ctx, d); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if d.ID == 0 {
				t.Fatal("Create() did not populate ID")
			}
			if d.Visibility != DeckVisibilityPrivate {
				t.Errorf("Create() visibility = %q, want %q", d.Visibility, DeckVisibilityPrivate)
			}
			if d.CreatedAt.IsZero() {
				t.Error("Create() did not set CreatedAt")
			}

			// read：ByID 取回同一行。
			got, err := decks.ByID(ctx, d.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if got.Name != "Deck A" || got.OwnerUserID != ownerA {
				t.Errorf("ByID() = %+v, want name Deck A owner %d", got, ownerA)
			}

			// list：只列出该 owner 的卡组。
			if err := decks.Create(ctx, &Deck{OwnerUserID: ownerB, Name: "Deck B", PresetID: presetB}); err != nil {
				t.Fatalf("Create() for owner B error = %v", err)
			}
			owned, err := decks.ListByOwner(ctx, ownerA)
			if err != nil {
				t.Fatalf("ListByOwner() error = %v", err)
			}
			if len(owned) != 1 || owned[0].ID != d.ID {
				t.Errorf("ListByOwner(ownerA) = %+v, want only deck %d", owned, d.ID)
			}

			// update：owner 改设置字段。
			got.Name = "Deck A renamed"
			got.Description = "desc"
			got.Visibility = DeckVisibilityUnlisted
			got.PresetID = presetB
			if err := decks.Update(ctx, ownerA, got); err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			reloaded, err := decks.ByID(ctx, d.ID)
			if err != nil {
				t.Fatalf("ByID() after update error = %v", err)
			}
			if reloaded.Name != "Deck A renamed" || reloaded.Description != "desc" ||
				reloaded.Visibility != DeckVisibilityUnlisted || reloaded.PresetID != presetB {
				t.Errorf("after Update() = %+v, want renamed/desc/unlisted/presetB", reloaded)
			}

			// archive / restore：幂等且状态可往返。
			at := time.Now().UTC().Truncate(time.Second)
			if err := decks.Archive(ctx, ownerA, d.ID, at); err != nil {
				t.Fatalf("Archive() error = %v", err)
			}
			if err := decks.Archive(ctx, ownerA, d.ID, at); err != nil {
				t.Fatalf("Archive() second call error = %v, want idempotent nil", err)
			}
			reloaded, err = decks.ByID(ctx, d.ID)
			if err != nil {
				t.Fatalf("ByID() after archive error = %v", err)
			}
			if reloaded.ArchivedAt == nil || !reloaded.ArchivedAt.Equal(at) {
				t.Errorf("ArchivedAt = %v, want %v", reloaded.ArchivedAt, at)
			}
			if err := decks.Restore(ctx, ownerA, d.ID); err != nil {
				t.Fatalf("Restore() error = %v", err)
			}
			reloaded, err = decks.ByID(ctx, d.ID)
			if err != nil {
				t.Fatalf("ByID() after restore error = %v", err)
			}
			if reloaded.ArchivedAt != nil {
				t.Errorf("ArchivedAt after Restore = %v, want nil", reloaded.ArchivedAt)
			}

			// SetVisibility：owner 单独改可见性。
			if err := decks.SetVisibility(ctx, ownerA, d.ID, DeckVisibilityPublic); err != nil {
				t.Fatalf("SetVisibility() error = %v", err)
			}
			reloaded, err = decks.ByID(ctx, d.ID)
			if err != nil {
				t.Fatalf("ByID() after SetVisibility error = %v", err)
			}
			if reloaded.Visibility != DeckVisibilityPublic {
				t.Errorf("Visibility = %q, want %q", reloaded.Visibility, DeckVisibilityPublic)
			}

			// 校验：非法可见性被拒。
			if err := decks.SetVisibility(ctx, ownerA, d.ID, "secret"); !errors.Is(err, ErrInvalidVisibility) {
				t.Errorf("SetVisibility(invalid) error = %v, want ErrInvalidVisibility", err)
			}
		})
	}
}

// TestDeckNonOwnerCannotModifyBeforeGrants 是 M2-1 的验收用例：
// deck_grants 尚不存在任何行时，非 owner 对卡组的任何修改都必须被拒 —— 这是 M5-1 的地基。
func TestDeckNonOwnerCannotModifyBeforeGrants(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "grant_owner", "grant_stranger")
			owner, stranger := users[0], users[1]
			presetID := seedPresetRow(t, db, owner)
			decks := NewDeckStore(db)

			d := &Deck{OwnerUserID: owner, Name: "Shared", PresetID: presetID}
			if err := decks.Create(ctx, d); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			// 确认前提：此刻授权表里没有任何行。
			var grants int64
			if err := db.Model(&DeckGrant{}).Where("deck_id = ?", d.ID).Count(&grants).Error; err != nil {
				t.Fatalf("count grants: %v", err)
			}
			if grants != 0 {
				t.Fatalf("precondition failed: deck %d already has %d grants", d.ID, grants)
			}

			// 非 owner 改设置被拒。
			attempt := *d
			attempt.Name = "Hijacked"
			attempt.Visibility = DeckVisibilityPublic
			if err := decks.Update(ctx, stranger, &attempt); !errors.Is(err, ErrNotOwner) {
				t.Errorf("non-owner Update() error = %v, want ErrNotOwner", err)
			}
			// 非 owner 归档被拒。
			if err := decks.Archive(ctx, stranger, d.ID, time.Now().UTC()); !errors.Is(err, ErrNotOwner) {
				t.Errorf("non-owner Archive() error = %v, want ErrNotOwner", err)
			}
			// 非 owner 改可见性被拒。
			if err := decks.SetVisibility(ctx, stranger, d.ID, DeckVisibilityPublic); !errors.Is(err, ErrNotOwner) {
				t.Errorf("non-owner SetVisibility() error = %v, want ErrNotOwner", err)
			}
			// 非 owner 恢复（取消归档）也被拒。
			if err := decks.Restore(ctx, stranger, d.ID); !errors.Is(err, ErrNotOwner) {
				t.Errorf("non-owner Restore() error = %v, want ErrNotOwner", err)
			}

			// 数据库里的行必须原封不动。
			after, err := decks.ByID(ctx, d.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if after.Name != "Shared" || after.Visibility != DeckVisibilityPrivate || after.ArchivedAt != nil {
				t.Errorf("deck was modified by a non-owner: %+v", after)
			}
		})
	}
}

// TestDeckCreateValidation 覆盖缺失 preset 与空名两种拒绝路径。
func TestDeckCreateValidation(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "deck_validate_owner")[0]
			decks := NewDeckStore(db)

			if err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: "", PresetID: 1}); !errors.Is(err, ErrDeckNameRequired) {
				t.Errorf("Create(empty name) error = %v, want ErrDeckNameRequired", err)
			}
			if err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: "NoPreset"}); !errors.Is(err, ErrDeckPresetRequired) {
				t.Errorf("Create(no preset) error = %v, want ErrDeckPresetRequired", err)
			}
		})
	}
}
