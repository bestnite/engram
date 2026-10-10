package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
)

// ---- FSRS 权重的导出 / 导入开关 ----

// packageTestWeights 是一组 21 维的非默认权重，只用来判断「带出去了没有」。
func packageTestWeights() []float64 {
	w := make([]float64, 21)
	for i := range w {
		w[i] = 0.5 + float64(i)/100
	}
	return w
}

// seedWeightedPackageDeck 建一个源卡组，并把它的预设改成带权重与优化元数据的状态。
func seedWeightedPackageDeck(t *testing.T, db *gorm.DB, owner uint64) uint64 {
	t.Helper()
	deckID, _ := seedPackageDeck(t, db, owner)
	var d Deck
	if err := db.First(&d, deckID).Error; err != nil {
		t.Fatalf("load deck: %v", err)
	}
	raw, err := json.Marshal(packageTestWeights())
	if err != nil {
		t.Fatalf("marshal weights: %v", err)
	}
	optimizedAt := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	if err := db.Model(&Preset{}).Where("id = ?", d.PresetID).Updates(map[string]any{
		"weights_json":         string(raw),
		"weights_optimized_at": optimizedAt,
		"weights_review_count": 1234,
	}).Error; err != nil {
		t.Fatalf("seed weights: %v", err)
	}
	return deckID
}

// deckPreset 读出卡组当前绑定的预设。
func deckPreset(t *testing.T, db *gorm.DB, deckID uint64) Preset {
	t.Helper()
	var d Deck
	if err := db.First(&d, deckID).Error; err != nil {
		t.Fatalf("load deck %d: %v", deckID, err)
	}
	var p Preset
	if err := db.First(&p, d.PresetID).Error; err != nil {
		t.Fatalf("load preset %d: %v", d.PresetID, err)
	}
	return p
}

// TestExportPackageWeightsOption 断言权重默认不导出，且关闭时三项权重字段一起为 null。
func TestExportPackageWeightsOption(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, db, "pkg_w_export")[0]
			weighted := seedWeightedPackageDeck(t, db, owner)
			plain := seedPresetDeck(t, db, owner)

			cases := []struct {
				name        string
				deckID      uint64
				opts        PackageOptions
				wantWeights bool
			}{
				{"default omits weights", weighted, PackageOptions{}, false},
				{"include_weights exports them", weighted, PackageOptions{IncludeWeights: true}, true},
				{"preset on default weights stays null", plain, PackageOptions{IncludeWeights: true}, false},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					pkg, err := NewDeckStore(db).ExportPackage(context.Background(), owner, tc.deckID, tc.opts)
					if err != nil {
						t.Fatalf("ExportPackage: %v", err)
					}
					p := pkg.Preset
					if pkg.Manifest.IncludeWeights != tc.wantWeights {
						t.Fatalf("manifest.include_weights = %v, want %v", pkg.Manifest.IncludeWeights, tc.wantWeights)
					}
					// 其余调度参数不受开关影响，始终随包。
					if p.DesiredRetention <= 0 || p.LearningSteps == "" {
						t.Fatalf("scheduling parameters missing: %+v", p)
					}
					if !tc.wantWeights {
						if p.Weights != nil || p.WeightsOptimizedAt != nil || p.WeightsReviewCount != nil {
							t.Fatalf("weight fields = %v / %v / %v, want all null", p.Weights, p.WeightsOptimizedAt, p.WeightsReviewCount)
						}
						return
					}
					if fmt.Sprint(p.Weights) != fmt.Sprint(packageTestWeights()) {
						t.Fatalf("weights = %v, want %v", p.Weights, packageTestWeights())
					}
					if p.WeightsOptimizedAt == nil || *p.WeightsOptimizedAt != "2026-09-01T08:00:00Z" {
						t.Fatalf("weights_optimized_at = %v, want 2026-09-01T08:00:00Z", p.WeightsOptimizedAt)
					}
					if p.WeightsReviewCount == nil || *p.WeightsReviewCount != 1234 {
						t.Fatalf("weights_review_count = %v, want 1234", p.WeightsReviewCount)
					}
				})
			}
		})
	}
}

// TestImportPackageApplyWeights 断言包内权重默认不采用，只有 new_deck + apply_weights 才写进新预设，
// 合并到已有卡组时目标预设保持不变。
func TestImportPackageApplyWeights(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, src, "pkg_w_src")[0]
			weightedDeck := seedWeightedPackageDeck(t, src, owner)
			withWeights := exportZip(t, src, owner, weightedDeck, PackageOptions{IncludeWeights: true})
			withoutWeights := exportZip(t, src, owner, weightedDeck, PackageOptions{})

			importer := seedUsers(t, dst, "pkg_w_dst")[0]
			existing := seedPresetDeck(t, dst, importer)
			existingBefore := deckPreset(t, dst, existing)

			cases := []struct {
				name          string
				raw           []byte
				target        string
				apply         bool
				wantApplied   bool
				wantDiscarded bool
			}{
				{"new deck ignores weights by default", withWeights, PackageTargetNewDeck, false, false, true},
				{"new deck applies weights when asked", withWeights, PackageTargetNewDeck, true, true, false},
				{"into deck never touches the target preset", withWeights, fmt.Sprintf("into_deck:%d", existing), true, false, true},
				{"package without weights reports neither", withoutWeights, PackageTargetNewDeck, true, false, false},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					report, err := NewDeckStore(dst).ImportPackage(context.Background(), importer, bytes.NewReader(tc.raw), PackageImportOptions{
						Target: tc.target, ApplyWeights: tc.apply,
					})
					if err != nil {
						t.Fatalf("ImportPackage: %v", err)
					}
					if report.WeightsApplied != tc.wantApplied || report.WeightsDiscarded != tc.wantDiscarded {
						t.Fatalf("weights_applied/discarded = %v/%v, want %v/%v",
							report.WeightsApplied, report.WeightsDiscarded, tc.wantApplied, tc.wantDiscarded)
					}
					got := deckPreset(t, dst, report.DeckID)
					if tc.target != PackageTargetNewDeck {
						// 按 JSON 比较：EnableFuzz 是指针，直接打印比的是地址。
						before, _ := json.Marshal(existingBefore)
						after, _ := json.Marshal(got)
						if !bytes.Equal(before, after) {
							t.Fatalf("target preset changed:\n before=%+v\n after=%+v", existingBefore, got)
						}
						return
					}
					if !tc.wantApplied {
						if got.WeightsJSON != nil || got.WeightsOptimizedAt != nil || got.WeightsReviewCount != nil {
							t.Fatalf("new preset weight fields = %v / %v / %v, want all null", got.WeightsJSON, got.WeightsOptimizedAt, got.WeightsReviewCount)
						}
						return
					}
					var w []float64
					if got.WeightsJSON == nil || json.Unmarshal([]byte(*got.WeightsJSON), &w) != nil || fmt.Sprint(w) != fmt.Sprint(packageTestWeights()) {
						t.Fatalf("new preset weights = %v, want %v", got.WeightsJSON, packageTestWeights())
					}
					if got.WeightsReviewCount == nil || *got.WeightsReviewCount != 1234 {
						t.Fatalf("new preset weights_review_count = %v, want 1234", got.WeightsReviewCount)
					}
					if got.WeightsOptimizedAt == nil || !got.WeightsOptimizedAt.Equal(time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)) {
						t.Fatalf("new preset weights_optimized_at = %v, want 2026-09-01T08:00:00Z", got.WeightsOptimizedAt)
					}
				})
			}
		})
	}
}
