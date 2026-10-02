package store

import (
	"context"
	"testing"

	"example.com/engram/internal/cardtype"
)

// TestPresetGradeMappingRoundTrip 是 M3-6 的存储验收：分数→评分档位映射存进 preset
// 的新列后能原样读回；未配置（NULL）时回退到内置默认映射。
func TestPresetGradeMappingRoundTrip(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "grade_map_owner")[0]
			presets := NewPresetStore(db)

			// 默认：列为 NULL，解析出内置默认映射。
			p := NewPreset(owner, "Default map")
			if err := presets.Create(ctx, &p); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			got, err := presets.ByID(ctx, p.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if got.GradeMappingJSON != nil {
				t.Errorf("grade_mapping_json = %v, want nil (use built-in default)", *got.GradeMappingJSON)
			}
			m, err := got.GradeMapping()
			if err != nil {
				t.Fatalf("GradeMapping() error = %v", err)
			}
			if m.RatingFor(1) != cardtype.RatingGood || m.RatingFor(0.5) != cardtype.RatingHard || m.RatingFor(0) != cardtype.RatingAgain {
				t.Errorf("default mapping = %+v, want Good/Hard/Again", m)
			}

			// 自定义：写回后读出的映射必须生效。
			custom := cardtype.GradeMapping{Full: cardtype.RatingEasy, Partial: cardtype.RatingGood, None: cardtype.RatingAgain}
			if err := got.SetGradeMapping(custom); err != nil {
				t.Fatalf("SetGradeMapping() error = %v", err)
			}
			if err := presets.Update(ctx, owner, got); err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			again, err := presets.ByID(ctx, p.ID)
			if err != nil {
				t.Fatalf("ByID() after update error = %v", err)
			}
			if again.GradeMappingJSON == nil {
				t.Fatal("grade_mapping_json = nil after SetGradeMapping, want a JSON string")
			}
			back, err := again.GradeMapping()
			if err != nil {
				t.Fatalf("GradeMapping() after update error = %v", err)
			}
			if back.RatingFor(1) != cardtype.RatingEasy || back.RatingFor(0.5) != cardtype.RatingGood {
				t.Errorf("round-trip mapping = %+v, want Easy/Good", back)
			}
		})
	}
}
