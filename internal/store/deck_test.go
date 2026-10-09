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

			// create：owner 归属由 OwnerUserID 给出。
			d := &Deck{OwnerUserID: ownerA, Name: "Deck A", PresetID: presetID}
			if err := decks.Create(ctx, d); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if d.ID == 0 {
				t.Fatal("Create() did not populate ID")
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

			// update：owner 只改名称与描述；预设不在可改字段内（换预设走 SetPreset / SetStudySettings）。
			got.Name = "Deck A renamed"
			got.Description = "desc"
			got.PresetID = presetB
			if err := decks.Update(ctx, ownerA, got.ID, &got.Name, &got.Description); err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			reloaded, err := decks.ByID(ctx, d.ID)
			if err != nil {
				t.Fatalf("ByID() after update error = %v", err)
			}
			if reloaded.Name != "Deck A renamed" || reloaded.Description != "desc" {
				t.Errorf("after Update() = %+v, want renamed/desc", reloaded)
			}
			if reloaded.PresetID != d.PresetID {
				t.Errorf("after Update() preset = %d, want %d (a metadata update must not change the preset)", reloaded.PresetID, d.PresetID)
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

			// delete：非 owner 删除被拒。
			if err := decks.Delete(ctx, ownerB, d.ID); !errors.Is(err, ErrNotOwner) {
				t.Errorf("Delete() by non-owner error = %v, want ErrNotOwner", err)
			}
			// owner 删除成功，随后 ByID 返回记录未找到。
			if err := decks.Delete(ctx, ownerA, d.ID); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			if _, err := decks.ByID(ctx, d.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Errorf("ByID() after Delete() error = %v, want ErrRecordNotFound", err)
			}
		})
	}
}

// TestDeckNonOwnerCannotModifyBeforeGrants 是验收用例：
// deck_grants 尚不存在任何行时，非 owner 对卡组的任何修改都必须被拒 —— 这是地基。
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
			if err := decks.Update(ctx, stranger, attempt.ID, &attempt.Name, &attempt.Description); !errors.Is(err, ErrNotOwner) {
				t.Errorf("non-owner Update() error = %v, want ErrNotOwner", err)
			}
			// 非 owner 归档被拒。
			if err := decks.Archive(ctx, stranger, d.ID, time.Now().UTC()); !errors.Is(err, ErrNotOwner) {
				t.Errorf("non-owner Archive() error = %v, want ErrNotOwner", err)
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
			if after.Name != "Shared" || after.ArchivedAt != nil {
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

// TestDeckSetPresetRequiresOwnership 是「卡组切换调度」的验收：owner 可以在自己名下的预设
// 之间切换，但换成别人的预设必须被拒——卡组可读就意味着它的排程参数可读。
func TestDeckSetPresetRequiresOwnership(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "preset_owner", "preset_intruder")
			owner, intruder := users[0], users[1]
			first := seedPresetRow(t, db, owner)
			second := seedPresetRow(t, db, owner)
			foreign := seedPresetRow(t, db, intruder)
			decks := NewDeckStore(db)
			d := &Deck{OwnerUserID: owner, Name: "Switchable", PresetID: first}
			if err := decks.Create(ctx, d); err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			// 自己名下的另一个预设：切换成功并落库。
			if err := decks.SetPreset(ctx, owner, d.ID, second); err != nil {
				t.Fatalf("SetPreset(own preset) error = %v", err)
			}
			if got, err := decks.ByID(ctx, d.ID); err != nil {
				t.Fatalf("ByID() error = %v", err)
			} else if got.PresetID != second {
				t.Errorf("preset_id = %d, want %d", got.PresetID, second)
			}

			// 别人的预设：拒绝。
			if err := decks.SetPreset(ctx, owner, d.ID, foreign); !errors.Is(err, ErrDeckPresetInvalid) {
				t.Errorf("SetPreset(foreign preset) error = %v, want ErrDeckPresetInvalid", err)
			}
			// 非 owner：拒绝。
			if err := decks.SetPreset(ctx, intruder, d.ID, foreign); !errors.Is(err, ErrNotOwner) {
				t.Errorf("SetPreset(non-owner) error = %v, want ErrNotOwner", err)
			}
			// 两次被拒都不得留下痕迹。
			if got, err := decks.ByID(ctx, d.ID); err != nil {
				t.Fatalf("ByID() after rejected writes error = %v", err)
			} else if got.PresetID != second {
				t.Errorf("preset_id after rejected writes = %d, want %d (unchanged)", got.PresetID, second)
			}
		})
	}
}
