package store

import (
	"context"
	"errors"
	"testing"
)

// TestPresetDefaultsAppliedAndRoundTrip 是 M2-2 的验收用例：
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

			// 空 learning_steps 是有意义的取值（关闭学习步骤，DESIGN.md §3.2），必须原样保留。
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
