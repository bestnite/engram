package schedule

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/open-spaced-repetition/go-fsrs/v4"
	"gorm.io/gorm"

	"example.com/engram/internal/store"
)

// optimiseDB 打开临时 SQLite 并迁移全部表，供本文件的「落库 → 调度器」用例使用。
// 不依赖外部网络，也不依赖真实优化器二进制。
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

// optimiseOwner 建一个可用于归属 preset 的用户，返回其主键。
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

// optimiseWeights21 返回一组 21 维权重，各元素互不相同且非默认值，便于断言确实是写回的那组。
func optimiseWeights21() []float64 {
	w := make([]float64, 21)
	for i := range w {
		w[i] = float64(i) + 0.25
	}
	return w
}

// TestOptimiseWeightRoundTrip 是 M9-6「权重往返」的端到端用例：优化写回的权重落库后，
// 调度器 NewScheduler 读到的就是同一组权重（而不是默认权重）。
// 这条链路串起 store（落库）与 schedule（消费），正是「优化完成即生效」的数据契约。
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
	// 调度器确实采用了存库那组权重：与默认权重不同即证明走了 weights_json 分支
	// （逐元素精确往返由 internal/store 的 TestOptimisePresetWeightsRoundTrip 断言；
	// 这里不比较具体数值，因为 NewFSRS 会对权重做边界裁剪，下游看到的不是原样数组）。
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

// TestOptimiseRevertRestoresDefaultWeights 是 M9-6「回退」的端到端用例：一键回退后，
// 调度器重新落到 fsrs.DefaultWeights()，三列权重元数据一并归 NULL。
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
