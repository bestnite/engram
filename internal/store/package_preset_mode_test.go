package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/anki"
)

func TestAnkiImportPresetRefusals(t *testing.T) {
	raw, err := os.ReadFile(ankiFixturePath("stock.anki21.apkg"))
	if err != nil {
		t.Fatal(err)
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			users := seedUsers(t, db, "preset_refusal_owner", "preset_refusal_other")
			ownerPreset := seedPresetRow(t, db, users[0])
			otherPreset := seedPresetRow(t, db, users[1])
			deckID := seedPresetDeck(t, db, users[0])
			type selection struct {
				target, mode, preset string
				weights              bool
			}
			var cases []selection
			for _, mode := range []string{PackagePresetReuse, PackagePresetCopy} {
				cases = append(cases,
					selection{"new_deck", mode, "none", false},
					selection{"new_deck", mode, "other", false},
					selection{"new_deck", mode, "owner", true},
					selection{"into_deck", mode, "owner", false},
					selection{"replace_deck", mode, "owner", false},
				)
			}
			for _, target := range []string{"new_deck", "into_deck", "replace_deck"} {
				cases = append(cases, selection{target, PackagePresetNew, "owner", false})
			}
			cases = append(cases, selection{"new_deck", "unknown", "none", false},
				selection{"into_deck", "", "owner", false}, selection{"replace_deck", "", "owner", false})
			models := []any{&Deck{}, &Preset{}, &Note{}, &Card{}}
			counts := func(t *testing.T) []int64 {
				t.Helper()
				result := make([]int64, len(models))
				for i, model := range models {
					if err := db.Model(model).Count(&result[i]).Error; err != nil {
						t.Fatal(err)
					}
				}
				return result
			}
			for _, tc := range cases {
				name := fmt.Sprintf("target=%s/mode=%q/preset=%s/weights=%t", tc.target, tc.mode, tc.preset, tc.weights)
				t.Run(name, func(t *testing.T) {
					target := tc.target
					if target != PackageTargetNewDeck {
						target = fmt.Sprintf("%s:%d", target, deckID)
					}
					ids := map[string]uint64{"none": 0, "owner": ownerPreset, "other": otherPreset}
					opts := PackageImportOptions{Target: target, PresetMode: tc.mode, PresetID: ids[tc.preset], ApplyWeights: tc.weights, MediaRoot: t.TempDir()}
					before := counts(t)
					report, err := NewDeckStore(db).ImportPackage(context.Background(), users[0], bytes.NewReader(raw), opts)
					var pe *PackageError
					if !errors.As(err, &pe) || pe.Code != CodePresetSelectionInvalid {
						t.Errorf("report=%+v error=%v, want preset selection PackageError", report, err)
					}
					if after := counts(t); !reflect.DeepEqual(before, after) {
						t.Errorf("deck, preset, note, card counts: before=%v after=%v", before, after)
					}
				})
			}
		})
	}
}

func TestAnkiImportPresetModes(t *testing.T) {
	raw, err := os.ReadFile(ankiFixturePath("stock.anki21.apkg"))
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := anki.ReadWithLimits(bytes.NewReader(raw), int64(len(raw)), DefaultPackageImportLimits().ankiLimits())
	if err != nil {
		t.Fatal(err)
	}
	plan := planAnkiDecks(pkg)
	cfg := ankiTopConfig(pkg, plan.TopName)
	if cfg == nil {
		t.Fatal("fixture has no deck configuration")
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, db, "anki_preset_modes")[0]
			source := NewPreset(owner, "Chosen")
			source.DesiredRetention = .57
			source.WeightsJSON = Ptr("[1,2,3]")
			source.CreatedAt = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			source.UpdatedAt = source.CreatedAt
			if err := db.Create(&source).Error; err != nil {
				t.Fatal(err)
			}
			var before Preset
			if err := db.First(&before, source.ID).Error; err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			for _, mode := range []string{"new", "reuse", "copy"} {
				t.Run(mode, func(t *testing.T) {
					opts := PackageImportOptions{PresetMode: mode, MediaRoot: t.TempDir(), Now: func() time.Time { return now }}
					if mode != "new" {
						opts.PresetID = source.ID
					}
					report, err := NewDeckStore(db).ImportPackage(context.Background(), owner, bytes.NewReader(raw), opts)
					if err != nil {
						t.Fatal(err)
					}
					var deck Deck
					if err := db.First(&deck, report.DeckID).Error; err != nil {
						t.Fatal(err)
					}
					var p, after Preset
					if err := db.First(&p, deck.PresetID).Error; err != nil {
						t.Fatal(err)
					}
					if err := db.First(&after, source.ID).Error; err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(before, after) {
						t.Fatal("source changed")
					}
					if report.PresetMode != mode || report.PresetName != p.Name {
						t.Fatalf("report: %+v", report)
					}
					switch mode {
					case "new":
						if p.ID == source.ID || p.LearningSteps != ankiSteps(cfg.LearnSteps) || p.RelearningSteps != ankiSteps(cfg.RelearnSteps) || (cfg.MaximumReviewInterval > 0 && p.MaximumIntervalDays != cfg.MaximumReviewInterval) {
							t.Fatalf("package preset: %+v", p)
						}
					case "reuse":
						if p.ID != source.ID {
							t.Fatal("not reused")
						}
					case "copy":
						if p.ID == source.ID || !p.CreatedAt.Equal(now) || !p.UpdatedAt.Equal(now) {
							t.Fatalf("copy identity/timestamps: %+v", p)
						}
						p.ID, p.PublicID, p.Name, p.CreatedAt, p.UpdatedAt = before.ID, before.PublicID, before.Name, before.CreatedAt, before.UpdatedAt
						if !reflect.DeepEqual(p, before) {
							t.Fatal("copy settings differ")
						}
					}
				})
			}
		})
	}
}
