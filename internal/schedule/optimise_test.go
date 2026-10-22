package schedule

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/open-spaced-repetition/go-fsrs/v4"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// optimiseDB opens a temporary SQLite and migrates all tables, for this file's "persist → scheduler" cases.
// It depends neither on an external network nor on the real optimiser binary.
func optimiseDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "optimise.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return db
}

// optimiseOwner creates a user that can own a preset, returning its primary key.
func optimiseOwner(t *testing.T, db *gorm.DB) uint64 {
	t.Helper()
	u := store.User{
		Username: "optimise_owner", Email: "optimise_owner@example.com", DisplayName: "Optimise Owner",
		Role: store.RoleUser, Status: store.StatusActive, Locale: "zh-CN", Timezone: "Asia/Shanghai",
		CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u.ID
}

// optimiseWeights21 returns a set of 21-dimensional weights whose elements are all distinct and non-default, so it is easy to assert that exactly that set was written back.
func optimiseWeights21() []float64 {
	w := make([]float64, 21)
	for i := range w {
		w[i] = float64(i) + 0.25
	}
	return w
}

// TestOptimiseWeightRoundTrip is the end-to-end case for the "weight round trip": after the weights written back by optimisation are persisted,
// the scheduler built by NewScheduler reads exactly the same weights (not the defaults).
// This chain links store (persist) and schedule (consume), which is exactly the data contract of "takes effect as soon as optimisation finishes".
func TestOptimiseWeightRoundTrip(t *testing.T) {
	db := optimiseDB(t)
	ctx := context.Background()
	owner := optimiseOwner(t, db)
	presets := store.NewPresetStore(db)

	p := store.NewPreset(owner, "RoundTrip")
	if err := presets.Create(ctx, &p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	weights := optimiseWeights21()
	raw, err := json.Marshal(weights)
	if err != nil {
		t.Fatalf("marshal weights: %v", err)
	}
	encoded := string(raw)
	optimizedAt := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	reviewCount := 640
	p.WeightsJSON = &encoded
	p.WeightsOptimizedAt = &optimizedAt
	p.WeightsReviewCount = &reviewCount
	if err := presets.Update(ctx, owner, &p); err != nil {
		t.Fatalf("Update(weights): %v", err)
	}

	got, err := presets.ByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	s := mustScheduler(t, got)
	if len(s.fsrs.W) != 21 {
		t.Fatalf("scheduler has %d weights, want 21", len(s.fsrs.W))
	}
	// The scheduler really did adopt the stored weight set: differing from the defaults proves the weights_json branch was taken
	// (the exact element-by-element round trip is asserted by internal/store's TestOptimisePresetWeightsRoundTrip;
	// specific values are not compared here because NewFSRS clamps the weights at their bounds, so downstream does not see the array verbatim).
	defaults := fsrs.DefaultWeights()
	sameAsDefault := true
	for i := range defaults {
		if s.fsrs.W[i] != defaults[i] {
			sameAsDefault = false
			break
		}
	}
	if sameAsDefault {
		t.Errorf("scheduler fell back to the default weights; the stored weights_json was not used")
	}
}

// TestOptimiseRevertRestoresDefaultWeights is the end-to-end case for "revert": after a one-click revert,
// the scheduler falls back to fsrs.DefaultWeights(), and the three weight metadata columns all go NULL.
func TestOptimiseRevertRestoresDefaultWeights(t *testing.T) {
	db := optimiseDB(t)
	ctx := context.Background()
	owner := optimiseOwner(t, db)
	presets := store.NewPresetStore(db)

	p := store.NewPreset(owner, "Revert")
	if err := presets.Create(ctx, &p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	weights := `[0.5,1.5,2.5,3.5,4.5,5.5,6.5,0.05,1.0,0.2,0.8,1.5,0.06,0.3,1.6,0.6,1.9,0.5,0.1,0.07,0.15]`
	optimizedAt := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	reviewCount := 900
	p.WeightsJSON = &weights
	p.WeightsOptimizedAt = &optimizedAt
	p.WeightsReviewCount = &reviewCount
	if err := presets.Update(ctx, owner, &p); err != nil {
		t.Fatalf("Update(weights): %v", err)
	}

	optimized, err := presets.ByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got := mustScheduler(t, optimized).fsrs.W[0]; got != 0.5 {
		t.Fatalf("before revert W[0] = %v, want 0.5 (the optimized value)", got)
	}

	if err := presets.ResetPresetWeights(ctx, owner, p.ID); err != nil {
		t.Fatalf("ResetPresetWeights: %v", err)
	}
	reverted, err := presets.ByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("ByID(after revert): %v", err)
	}
	if reverted.WeightsJSON != nil || reverted.WeightsOptimizedAt != nil || reverted.WeightsReviewCount != nil {
		t.Fatalf("revert left weight metadata behind: json=%v at=%v count=%v",
			reverted.WeightsJSON, reverted.WeightsOptimizedAt, reverted.WeightsReviewCount)
	}

	s := mustScheduler(t, reverted)
	defaults := fsrs.DefaultWeights()
	for i := range defaults {
		if s.fsrs.W[i] != defaults[i] {
			t.Errorf("after revert W[%d] = %v, want default %v", i, s.fsrs.W[i], defaults[i])
		}
	}
}
