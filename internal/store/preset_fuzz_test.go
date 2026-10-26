package store

import (
	"context"
	"testing"
)

// TestPresetEnableFuzzPointerSemantics covers AGENTS.md §2.3 item 9: enable_fuzz is a boolean column with a database
// default of true, so the model must use *bool; otherwise GORM omits the zero value and the database default silently overrides an explicit false.
// Three assertions:
//  1. when the field is not passed (nil), the stored value is the database default true;
//  2. when false is passed explicitly, false really is stored (still true now that PresetStore.Create's compensatory write is gone);
//  3. the package import path (whose equivalent compensatory write in package_import.go is also gone) likewise stores false as false.
func TestPresetEnableFuzzPointerSemantics(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "preset_fuzz_owner")[0]
			presets := NewPresetStore(db)

			// 1) Field not passed: EnableFuzz is nil, GORM omits the column, and the database default fills it in as true.
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

			// 2) false passed explicitly: it must be stored as false, not turned back into true by the database default.
			off := NewPreset(owner, "Off")
			off.EnableFuzz = Ptr(false)
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

			// 3) Package import path: createPresetFromPackage's compensatory write is gone, and false must still be stored as false.
			importedID, err := createPresetFromPackage(ctx, db, owner, &PackagePreset{
				Name: "Imported", DesiredRetention: DefaultDesiredRetention,
				LearningSteps: DefaultLearningSteps, RelearningSteps: DefaultRelearningSteps,
				MaximumIntervalDays: DefaultMaximumIntervalDays, EnableFuzz: false,
			}, "Imported deck", false)
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
