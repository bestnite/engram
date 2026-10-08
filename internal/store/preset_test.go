package store

import (
	"context"
	"errors"
	"sync"
	"testing"

	"gorm.io/gorm"
)

// TestPresetDefaultsAppliedAndRoundTrip 是验收用例：
// 文档化默认值必须落到库里，且每个可改参数都能原样读回。
func TestPresetDefaultsAppliedAndRoundTrip(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "preset_owner")[0]
			presets := NewPresetStore(db)

			// 默认值：构造时给出，Create 后必须一致。
			p := NewPreset(owner, "Default")
			if err := presets.Create(ctx, &p); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if p.ID == 0 {
				t.Fatal("Create() did not populate ID")
			}
			got, err := presets.ByID(ctx, p.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if got.DesiredRetention != DefaultDesiredRetention {
				t.Errorf("desired_retention = %v, want %v", got.DesiredRetention, DefaultDesiredRetention)
			}
			if got.LearningSteps != DefaultLearningSteps {
				t.Errorf("learning_steps = %q, want %q", got.LearningSteps, DefaultLearningSteps)
			}
			if got.RelearningSteps != DefaultRelearningSteps {
				t.Errorf("relearning_steps = %q, want %q", got.RelearningSteps, DefaultRelearningSteps)
			}
			if got.MaximumIntervalDays != DefaultMaximumIntervalDays {
				t.Errorf("maximum_interval_days = %d, want %d", got.MaximumIntervalDays, DefaultMaximumIntervalDays)
			}
			if !got.FuzzEnabled() {
				t.Errorf("enable_fuzz = false, want true (default on)")
			}
			if got.WeightsJSON != nil {
				t.Errorf("weights_json = %v, want nil (default weights)", *got.WeightsJSON)
			}

			// 往返：每个参数改成非默认值（含把 fuzz 关掉）后读回必须一致。
			got.DesiredRetention = 0.85
			got.LearningSteps = "2m,20m"
			got.RelearningSteps = "15m"
			got.MaximumIntervalDays = 100
			got.EnableFuzz = Ptr(false)
			if err := presets.Update(ctx, owner, got); err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			again, err := presets.ByID(ctx, p.ID)
			if err != nil {
				t.Fatalf("ByID() after update error = %v", err)
			}
			if again.DesiredRetention != 0.85 || again.LearningSteps != "2m,20m" ||
				again.RelearningSteps != "15m" || again.MaximumIntervalDays != 100 || again.FuzzEnabled() {
				t.Errorf("round-trip mismatch: %+v", again)
			}

			// 空 learning_steps 是有意义的取值（关闭学习步骤），必须原样保留。
			empty := NewPreset(owner, "NoSteps")
			empty.LearningSteps = ""
			if err := presets.Create(ctx, &empty); err != nil {
				t.Fatalf("Create(empty steps) error = %v", err)
			}
			gotEmpty, err := presets.ByID(ctx, empty.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if gotEmpty.LearningSteps != "" {
				t.Errorf("learning_steps = %q, want empty (learning steps disabled)", gotEmpty.LearningSteps)
			}

			// 列表按 owner 过滤。
			listed, err := presets.ListByOwner(ctx, owner)
			if err != nil {
				t.Fatalf("ListByOwner() error = %v", err)
			}
			if len(listed) != 2 {
				t.Errorf("ListByOwner() returned %d rows, want 2", len(listed))
			}
		})
	}
}

// TestPresetNonOwnerCannotModify 断言非 owner 不能改预设。
func TestPresetNonOwnerCannotModify(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "preset_owner_x", "preset_stranger_x")
			owner, stranger := users[0], users[1]
			presets := NewPresetStore(db)

			p := NewPreset(owner, "Mine")
			if err := presets.Create(ctx, &p); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			attempt := p
			attempt.DesiredRetention = 0.5
			if err := presets.Update(ctx, stranger, &attempt); !errors.Is(err, ErrNotOwner) {
				t.Errorf("non-owner Update() error = %v, want ErrNotOwner", err)
			}
			after, err := presets.ByID(ctx, p.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if after.DesiredRetention != DefaultDesiredRetention {
				t.Errorf("preset was modified by a non-owner: desired_retention = %v", after.DesiredRetention)
			}
		})
	}
}

// TestPresetValidationRejectsZeroValues 断言直接构造的零值预设不会被静默写库。
func TestPresetValidationRejectsZeroValues(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "preset_validate_owner")[0]
			presets := NewPresetStore(db)

			bare := Preset{OwnerUserID: owner, Name: "Bare"}
			if err := presets.Create(ctx, &bare); !errors.Is(err, ErrInvalidDesiredRetention) {
				t.Errorf("Create(bare) error = %v, want ErrInvalidDesiredRetention", err)
			}

			noName := NewPreset(owner, "")
			if err := presets.Create(ctx, &noName); !errors.Is(err, ErrPresetNameRequired) {
				t.Errorf("Create(no name) error = %v, want ErrPresetNameRequired", err)
			}

			tooHigh := NewPreset(owner, "TooHigh")
			tooHigh.DesiredRetention = 1.5
			if err := presets.Create(ctx, &tooHigh); !errors.Is(err, ErrInvalidDesiredRetention) {
				t.Errorf("Create(retention 1.5) error = %v, want ErrInvalidDesiredRetention", err)
			}

			noInterval := NewPreset(owner, "NoInterval")
			noInterval.MaximumIntervalDays = 0
			if err := presets.Create(ctx, &noInterval); !errors.Is(err, ErrInvalidMaximumInterval) {
				t.Errorf("Create(interval 0) error = %v, want ErrInvalidMaximumInterval", err)
			}
		})
	}
}

// TestEnsureDefaultPresetIdempotentAndConcurrent 是默认预设补齐的验收：
// 重复调用与并发调用都只能得到一条默认预设，且都不报错。
//
// 并发部分覆盖「两个请求同时为同一用户补齐」：presets 表没有 (owner_user_id, name) 唯一约束，
// EnsureDefaultPreset 用进程内互斥 + 事务内再查保证只落一条（见 store/preset.go 的说明）。
func TestEnsureDefaultPresetIdempotentAndConcurrent(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "preset_ensure_owner")[0]

			// 顺序重复：第二次必须复用第一条，不新增。
			first, err := EnsureDefaultPreset(ctx, db, owner)
			if err != nil {
				t.Fatalf("EnsureDefaultPreset() error = %v", err)
			}
			if len(first) != 1 || first[0].Name != DefaultPresetName {
				t.Fatalf("first ensure = %v, want exactly one %q", presetNames(first), DefaultPresetName)
			}
			second, err := EnsureDefaultPreset(ctx, db, owner)
			if err != nil {
				t.Fatalf("EnsureDefaultPreset() second call error = %v", err)
			}
			if len(second) != 1 || second[0].ID != first[0].ID {
				t.Fatalf("second ensure = %v, want the same single preset id %d", presetNames(second), first[0].ID)
			}

			// 并发：同一用户的 N 个补齐同时进行，不得报错也不得建出两条。
			other := seedUsers(t, db, "preset_ensure_concurrent")[0]
			const n = 8
			errs := make([]error, n)
			var wg sync.WaitGroup
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, errs[i] = EnsureDefaultPreset(ctx, db, other)
				}(i)
			}
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					t.Errorf("concurrent EnsureDefaultPreset #%d error = %v", i, err)
				}
			}
			got := mustListPresets(t, db, other)
			if len(got) != 1 {
				t.Fatalf("concurrent ensure produced %d presets (%v), want 1", len(got), presetNames(got))
			}
		})
	}
}

// mustListPresets 读某用户的全部预设，失败即 Fatal。
func mustListPresets(t *testing.T, db *gorm.DB, ownerID uint64) []Preset {
	t.Helper()
	presets, err := NewPresetStore(db).ListByOwner(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("ListByOwner() error = %v", err)
	}
	return presets
}

func TestPresetStoreDelete(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "preset_del_a", "preset_del_b")
			ownerA, ownerB := users[0], users[1]
			presets := NewPresetStore(db)

			defaultPresets, err := EnsureDefaultPreset(ctx, db, ownerA)
			if err != nil {
				t.Fatalf("EnsureDefaultPreset error = %v", err)
			}
			defaultP := defaultPresets[0]

			// 1. 默认预设保护
			if err := presets.Delete(ctx, ownerA, defaultP.ID); !errors.Is(err, ErrDefaultPresetCannotDelete) {
				t.Errorf("Delete(default) error = %v, want ErrDefaultPresetCannotDelete", err)
			}

			// 自定义预设
			custom := NewPreset(ownerA, "Custom Preset")
			if err := presets.Create(ctx, &custom); err != nil {
				t.Fatalf("Create() custom preset error = %v", err)
			}

			// 2. 他人不能删除
			if err := presets.Delete(ctx, ownerB, custom.ID); !errors.Is(err, ErrNotOwner) {
				t.Errorf("Delete() by non-owner error = %v, want ErrNotOwner", err)
			}

			// 3. 被卡组引用的预设不能删除
			decks := NewDeckStore(db)
			d := &Deck{OwnerUserID: ownerA, Name: "Deck using custom", PresetID: custom.ID}
			if err := decks.Create(ctx, d); err != nil {
				t.Fatalf("Create() deck error = %v", err)
			}
			if err := presets.Delete(ctx, ownerA, custom.ID); !errors.Is(err, ErrPresetInUse) {
				t.Errorf("Delete() in-use preset error = %v, want ErrPresetInUse", err)
			}

			// 删除该卡组后，该预设即可成功删除
			if err := decks.Delete(ctx, ownerA, d.ID); err != nil {
				t.Fatalf("Delete deck error = %v", err)
			}
			if err := presets.Delete(ctx, ownerA, custom.ID); err != nil {
				t.Fatalf("Delete() unused preset error = %v", err)
			}
			if _, err := presets.ByID(ctx, custom.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Errorf("ByID() after Delete error = %v, want ErrRecordNotFound", err)
			}
		})
	}
}

// presetNames 取预设名列表，仅用于失败信息。
func presetNames(presets []Preset) []string {
	names := make([]string, 0, len(presets))
	for i := range presets {
		names = append(names, presets[i].Name)
	}
	return names
}

// TestPresetDefaultCannotBeRenamed 断言默认预设可以改调度参数、但改名被拒：
// 它的身份就是 DefaultPresetName 这个字面量，改名会让后续补齐再建一条同名预设。
func TestPresetDefaultCannotBeRenamed(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "preset_rename")[0]
			presets := NewPresetStore(db)

			list, err := EnsureDefaultPreset(ctx, db, owner)
			if err != nil {
				t.Fatalf("EnsureDefaultPreset error = %v", err)
			}
			def := DefaultPreset(list)
			if def == nil {
				t.Fatalf("no default preset after EnsureDefaultPreset (names %v)", presetNames(list))
			}

			// 改名被拒，且库里的名字没变。
			renamed := *def
			renamed.Name = "Mine"
			if err := presets.Update(ctx, owner, &renamed); !errors.Is(err, ErrDefaultPresetCannotRename) {
				t.Errorf("Update(rename default) error = %v, want ErrDefaultPresetCannotRename", err)
			}
			after, err := presets.ByID(ctx, def.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if after.Name != DefaultPresetName {
				t.Errorf("default preset name after rejected rename = %q, want %q", after.Name, DefaultPresetName)
			}

			// 名字原样回传时，参数仍然可改。
			edited := *def
			edited.DesiredRetention = 0.8
			if err := presets.Update(ctx, owner, &edited); err != nil {
				t.Fatalf("Update(default params) error = %v", err)
			}
			got, err := presets.ByID(ctx, def.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if got.Name != DefaultPresetName || got.DesiredRetention != 0.8 {
				t.Errorf("default preset after param edit = %+v, want name %q and retention 0.8",
					got, DefaultPresetName)
			}
		})
	}
}
