package store

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/anki"
)

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
