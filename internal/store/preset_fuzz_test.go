package store

import (
	"context"
	"testing"
)

// TestPresetEnableFuzzPointerSemantics 覆盖 AGENTS.md §2.3 第 9 条：enable_fuzz 是带数据库
// 默认值 true 的布尔列，模型里必须是 *bool，否则 GORM 省略零值、数据库默认值会静默覆盖显式的 false。
// 三条断言：
//  1. 不传该字段（nil）时，落库为数据库默认 true；
//  2. 显式传 false 时，落库真的是 false（PresetStore.Create 的补偿写入删除后依然成立）；
//  3. 包导入路径（package_import.go 的同类补偿写入也已删除）同样把 false 落成 false。
func TestPresetEnableFuzzPointerSemantics(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "preset_fuzz_owner")[0]
			presets := NewPresetStore(db)

			// 1) 不传该字段：EnableFuzz 为 nil，GORM 省略该列，由数据库默认值补齐为 true。
			unset := Preset{
				OwnerUserID: owner, Name: "Unset",
				DesiredRetention: DefaultDesiredRetention, LearningSteps: DefaultLearningSteps,
				RelearningSteps: DefaultRelearningSteps, MaximumIntervalDays: DefaultMaximumIntervalDays,
			}
			if unset.EnableFuzz != nil {
				t.Fatalf("precondition: EnableFuzz = %v, want nil", *unset.EnableFuzz)
			}
			if err := presets.Create(ctx, &unset); err != nil {
				t.Fatalf("Create(unset) error = %v", err)
			}
			gotUnset, err := presets.ByID(ctx, unset.ID)
			if err != nil {
				t.Fatalf("ByID(unset) error = %v", err)
			}
			if gotUnset.EnableFuzz == nil {
				t.Fatal("enable_fuzz = NULL, want true (database default)")
			}
			if !*gotUnset.EnableFuzz || !gotUnset.FuzzEnabled() {
				t.Errorf("enable_fuzz = false, want true (database default)")
			}
			t.Logf("unset (nil) enable_fuzz stored as %v", *gotUnset.EnableFuzz)

			// 2) 显式传 false：必须原样落库为 false，不能被数据库默认值改回 true。
			off := NewPreset(owner, "Off")
			off.EnableFuzz = boolPtr(false)
			if err := presets.Create(ctx, &off); err != nil {
				t.Fatalf("Create(off) error = %v", err)
			}
			gotOff, err := presets.ByID(ctx, off.ID)
			if err != nil {
				t.Fatalf("ByID(off) error = %v", err)
			}
			if gotOff.EnableFuzz == nil {
				t.Fatal("enable_fuzz = NULL, want false (explicit)")
			}
			if *gotOff.EnableFuzz || gotOff.FuzzEnabled() {
				t.Errorf("enable_fuzz = true, want false (explicit)")
			}
			t.Logf("explicit false enable_fuzz stored as %v", *gotOff.EnableFuzz)

			// 3) 包导入路径：createPresetFromPackage 的补偿写入已删除，false 仍须落库为 false。
			importedID, err := createPresetFromPackage(ctx, db, owner, &PackagePreset{
				Name: "Imported", DesiredRetention: DefaultDesiredRetention,
				LearningSteps: DefaultLearningSteps, RelearningSteps: DefaultRelearningSteps,
				MaximumIntervalDays: DefaultMaximumIntervalDays, EnableFuzz: false,
			})
			if err != nil {
				t.Fatalf("createPresetFromPackage error = %v", err)
			}
			gotImported, err := presets.ByID(ctx, importedID)
			if err != nil {
				t.Fatalf("ByID(imported) error = %v", err)
			}
			if gotImported.EnableFuzz == nil || *gotImported.EnableFuzz {
				t.Errorf("imported enable_fuzz = %v, want false", gotImported.EnableFuzz)
			}
			t.Logf("imported enable_fuzz stored as %v", *gotImported.EnableFuzz)
		})
	}
}
