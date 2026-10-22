package store

import (
	"context"
	"errors"
	"sync"
	"testing"

	"gorm.io/gorm"
)

// TestPresetDefaultsAppliedAndRoundTrip is the acceptance case:
// the documented defaults must reach the database, and every changeable parameter must read back unchanged.
func TestPresetDefaultsAppliedAndRoundTrip(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "preset_owner")[0]
			presets := NewPresetStore(db)

			// Defaults: supplied at construction and must match after Create.
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

			// Round trip: after each parameter is set to a non-default value (fuzz turned off included), reading back must match.
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

			// An empty learning_steps is a meaningful value (learning steps disabled) and must be preserved as is.
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

			// The list is filtered by owner.
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

// TestPresetNonOwnerCannotModify asserts a non-owner cannot modify a preset.
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

// TestPresetValidationRejectsZeroValues asserts a directly constructed zero-value preset is not silently persisted.
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

// TestEnsureDefaultPresetIdempotentAndConcurrent is the acceptance for the default-preset backfill:
// repeated and concurrent calls must both yield exactly one default preset, and neither may error.
//
// The concurrent part covers "two requests backfilling for the same user at once": the presets table has no (owner_user_id, name) unique constraint,
// so EnsureDefaultPreset relies on an in-process mutex plus a re-query inside the transaction to land exactly one row (see the notes in store/preset.go).
func TestEnsureDefaultPresetIdempotentAndConcurrent(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "preset_ensure_owner")[0]

			// Sequential repeat: the second call must reuse the first row and add nothing.
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

			// Concurrent: N backfills for the same user run at once; none may error and no two rows may appear.
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

// mustListPresets reads all of a user's presets and fails the test on error.
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

			// 1. Default preset protection
			if err := presets.Delete(ctx, ownerA, defaultP.ID); !errors.Is(err, ErrDefaultPresetCannotDelete) {
				t.Errorf("Delete(default) error = %v, want ErrDefaultPresetCannotDelete", err)
			}

			// A custom preset
			custom := NewPreset(ownerA, "Custom Preset")
			if err := presets.Create(ctx, &custom); err != nil {
				t.Fatalf("Create() custom preset error = %v", err)
			}

			// 2. Someone else cannot delete it
			if err := presets.Delete(ctx, ownerB, custom.ID); !errors.Is(err, ErrNotOwner) {
				t.Errorf("Delete() by non-owner error = %v, want ErrNotOwner", err)
			}

			// 3. A preset referenced by a deck cannot be deleted
			decks := NewDeckStore(db)
			d := &Deck{OwnerUserID: ownerA, Name: "Deck using custom", PresetID: custom.ID}
			if err := decks.Create(ctx, d); err != nil {
				t.Fatalf("Create() deck error = %v", err)
			}
			if err := presets.Delete(ctx, ownerA, custom.ID); !errors.Is(err, ErrPresetInUse) {
				t.Errorf("Delete() in-use preset error = %v, want ErrPresetInUse", err)
			}

			// Once that deck is deleted, the preset can be deleted successfully
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

// presetNames extracts the preset names, used only in failure messages.
func presetNames(presets []Preset) []string {
	names := make([]string, 0, len(presets))
	for i := range presets {
		names = append(names, presets[i].Name)
	}
	return names
}

// TestPresetDefaultCannotBeRenamed asserts the default preset's scheduling parameters may change but renaming is rejected:
// its identity is the DefaultPresetName literal, and renaming would make a later backfill create another preset under that name.
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

			// The rename is rejected and the name in the database is unchanged.
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

			// With the name passed back unchanged, the parameters are still editable.
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
