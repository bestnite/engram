package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// optimiseSampleWeights 返回一组与真实优化器同形的 21 维权重；用非整数值保证往返比较
// 不会被浮点取整掩盖差异。
func optimiseSampleWeights() []float64 {
	w := make([]float64, 21)
	for i := range w {
		w[i] = float64(i) + 0.5
	}
	return w
}

// TestOptimisePresetWeightsRoundTrip 是「权重往返」在 store 层的用例：优化器产出的
// 21 维权重写进 preset 后，读回必须逐元素一致，且优化时间与使用条数一并保存。
// 这是「优化完成 → 落库 → 页面/调度器读到同一组权重」这条链路的数据侧契约。
func TestOptimisePresetWeightsRoundTrip(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "optimise_roundtrip")[0]
			presets := NewPresetStore(db)

			p := NewPreset(owner, "RoundTrip")
			if err := presets.Create(ctx, &p); err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			weights := optimiseSampleWeights()
			raw, err := json.Marshal(weights)
			if err != nil {
				t.Fatalf("marshal weights: %v", err)
			}
			encoded := string(raw)
			optimizedAt := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			reviewCount := 512
			p.WeightsJSON = &encoded
			p.WeightsOptimizedAt = &optimizedAt
			p.WeightsReviewCount = &reviewCount
			if err := presets.Update(ctx, owner, &p); err != nil {
				t.Fatalf("Update() error = %v", err)
			}

			got, err := presets.ByID(ctx, p.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if got.WeightsJSON == nil {
				t.Fatal("weights_json is nil after a successful optimize write")
			}
			var decoded []float64
			if err := json.Unmarshal([]byte(*got.WeightsJSON), &decoded); err != nil {
				t.Fatalf("decode weights_json %q: %v", *got.WeightsJSON, err)
			}
			if len(decoded) != 21 {
				t.Fatalf("weights_json has %d elements, want 21", len(decoded))
			}
			for i, want := range weights {
				if decoded[i] != want {
					t.Errorf("weights[%d] = %v, want %v", i, decoded[i], want)
				}
			}
			if got.WeightsOptimizedAt == nil || !got.WeightsOptimizedAt.Equal(optimizedAt) {
				t.Errorf("weights_optimized_at = %v, want %v", got.WeightsOptimizedAt, optimizedAt)
			}
			if got.WeightsReviewCount == nil || *got.WeightsReviewCount != reviewCount {
				t.Errorf("weights_review_count = %v, want %d", got.WeightsReviewCount, reviewCount)
			}
		})
	}
}

// TestOptimiseResetWeightsClearsPreset 是「回退」用例，覆盖点名要求的后端能力：
// 一键回退默认权重后，weights_json / weights_optimized_at / weights_review_count 三列
// 一起归 NULL（只清 weights_json 会留下「已优化」的假象），并验证幂等与 owner 授权。
func TestOptimiseResetWeightsClearsPreset(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owners := seedUsers(t, db, "optimise_reset_owner", "optimise_reset_intruder")
			owner, intruder := owners[0], owners[1]
			presets := NewPresetStore(db)

			p := NewPreset(owner, "Reset")
			if err := presets.Create(ctx, &p); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			weights := `[0.5,1.5,2.5,3.5,4.5,5.5,6.5,0.05,1.0,0.2,0.8,1.5,0.06,0.3,1.6,0.6,1.9,0.5,0.1,0.07,0.15]`
			optimizedAt := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			reviewCount := 700
			p.WeightsJSON = &weights
			p.WeightsOptimizedAt = &optimizedAt
			p.WeightsReviewCount = &reviewCount
			if err := presets.Update(ctx, owner, &p); err != nil {
				t.Fatalf("Update(optimized weights) error = %v", err)
			}

			// 非 owner 不能回退（与 Update 同一授权规则）。
			if err := presets.ResetPresetWeights(ctx, intruder, p.ID); !errors.Is(err, ErrNotOwner) {
				t.Fatalf("ResetPresetWeights(intruder) error = %v, want ErrNotOwner", err)
			}

			if err := presets.ResetPresetWeights(ctx, owner, p.ID); err != nil {
				t.Fatalf("ResetPresetWeights(owner) error = %v", err)
			}
			got, err := presets.ByID(ctx, p.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if got.WeightsJSON != nil {
				t.Errorf("weights_json = %q after revert, want NULL", *got.WeightsJSON)
			}
			if got.WeightsOptimizedAt != nil {
				t.Errorf("weights_optimized_at = %v after revert, want NULL", got.WeightsOptimizedAt)
			}
			if got.WeightsReviewCount != nil {
				t.Errorf("weights_review_count = %d after revert, want NULL", *got.WeightsReviewCount)
			}

			// 幂等：已经是默认的预设再回退一次也成功，不把重复点击当错误。
			if err := presets.ResetPresetWeights(ctx, owner, p.ID); err != nil {
				t.Errorf("ResetPresetWeights(second call) error = %v, want nil", err)
			}

			// 缺少主键时给出哨兵错误。
			if err := presets.ResetPresetWeights(ctx, owner, 0); !errors.Is(err, ErrPresetWeightsIDRequired) {
				t.Errorf("ResetPresetWeights(id=0) error = %v, want ErrPresetWeightsIDRequired", err)
			}
		})
	}
}

// TestOptimizeMinReviewsFloor 是验收 1：门槛读取路径对存量值的四种局面。
// 存量值低于下限（如 100，可能来自直接写库或旧版本）被钳到 300，而不是退回默认 500；
// 恰好等于下限的 300 与下限之上的 500 原样生效；未设置/空值/非数字/非正整数仍退回默认 500
// （那是「值不可用」，与「值合法但太低」的钳制语义不同）。
func TestOptimizeMinReviewsFloor(t *testing.T) {
	// 下限必须低于默认值，否则「钳到下限」与「退回默认」不可区分，这个测试就没有意义。
	if MinOptimizeMinReviews >= DefaultOptimizeMinReviews {
		t.Fatalf("MinOptimizeMinReviews = %d, want < DefaultOptimizeMinReviews = %d", MinOptimizeMinReviews, DefaultOptimizeMinReviews)
	}
	cases := []struct {
		name  string
		value string
		set   bool
		want  int
	}{
		{"stale value below the floor clamps to the floor", "100", true, MinOptimizeMinReviews},
		{"the floor itself passes through", "300", true, MinOptimizeMinReviews},
		{"a value above the floor passes through", "500", true, 500},
		{"unset setting falls back to the default", "", false, DefaultOptimizeMinReviews},
		{"empty value falls back to the default", "", true, DefaultOptimizeMinReviews},
		{"non-numeric value falls back to the default", "abc", true, DefaultOptimizeMinReviews},
		{"zero falls back to the default", "0", true, DefaultOptimizeMinReviews},
	}
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					if err := db.Where("key = ?", SettingKeyOptimizeMinReviews).Delete(&Setting{}).Error; err != nil {
						t.Fatalf("clear setting: %v", err)
					}
					if tc.set {
						if err := PutSetting(ctx, db, SettingKeyOptimizeMinReviews, tc.value, nil, time.Now().UTC()); err != nil {
							t.Fatalf("PutSetting(%q): %v", tc.value, err)
						}
					}
					got, err := OptimizeMinReviews(ctx, db)
					if err != nil {
						t.Fatalf("OptimizeMinReviews: %v", err)
					}
					if got != tc.want {
						t.Errorf("OptimizeMinReviews() with stored %q (set=%v) = %d, want %d", tc.value, tc.set, got, tc.want)
					}
					t.Logf("stored %q -> effective %d (want %d)", tc.value, got, tc.want)
				})
			}
		})
	}
}
