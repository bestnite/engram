package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func TestImportPresetModes(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			users := seedUsers(t, db, "preset_mode_owner", "preset_mode_other")
			source := NewPreset(users[0], DefaultPresetName)
			source.DesiredRetention = .83
			if err := db.Create(&source).Error; err != nil {
				t.Fatal(err)
			}
			foreign := NewPreset(users[1], "Foreign")
			if err := db.Create(&foreign).Error; err != nil {
				t.Fatal(err)
			}
			raw := manifestZip(t, "Preset test", "")
			for _, mode := range []string{"new", "reuse", "copy"} {
				t.Run(mode, func(t *testing.T) {
					var before Preset
					if err := db.First(&before, source.ID).Error; err != nil {
						t.Fatal(err)
					}
					beforeJSON, _ := json.Marshal(before)
					opts := PackageImportOptions{PresetMode: mode}
					if mode != "new" {
						opts.PresetID = source.ID
					}
					report := importPackageOK(t, db, users[0], raw, opts)
					var deck Deck
					if err := db.First(&deck, report.DeckID).Error; err != nil {
						t.Fatal(err)
					}
					var got Preset
					if err := db.First(&got, deck.PresetID).Error; err != nil {
						t.Fatal(err)
					}
					if report.PresetMode != mode || report.PresetName != got.Name {
						t.Fatalf("report = %+v, preset = %+v", report, got)
					}
					var after Preset
					if err := db.First(&after, source.ID).Error; err != nil {
						t.Fatal(err)
					}
					afterJSON, _ := json.Marshal(after)
					if !bytes.Equal(beforeJSON, afterJSON) {
						t.Fatal("source changed")
					}
					switch mode {
					case "new":
						if got.ID == source.ID || got.DesiredRetention == source.DesiredRetention {
							t.Fatalf("new preset = %+v", got)
						}
					case "reuse":
						if got.ID != source.ID {
							t.Fatal("not reused")
						}
					case "copy":
						if got.ID == source.ID || got.Name != "Default (copy)" {
							t.Fatalf("copy = %+v", got)
						}
						got.ID, got.PublicID, got.Name = before.ID, before.PublicID, before.Name
						got.CreatedAt, got.UpdatedAt = before.CreatedAt, before.UpdatedAt
						if !reflect.DeepEqual(got, before) {
							t.Fatalf("copy differs: %+v vs %+v", got, before)
						}
						var defaults int64
						db.Model(&Preset{}).Where("owner_user_id = ? AND name = ?", users[0], DefaultPresetName).Count(&defaults)
						if defaults != 1 {
							t.Fatalf("defaults = %d", defaults)
						}
					}
				})
			}
			existing := importPackageOK(t, db, users[0], manifestZip(t, "Existing", ""), PackageImportOptions{})
			intoTarget := fmt.Sprintf("into_deck:%d", existing.DeckID)
			replaceTarget := fmt.Sprintf("replace_deck:%d", existing.DeckID)
			cases := []PackageImportOptions{{PresetMode: "new", PresetID: source.ID}, {PresetMode: "", PresetID: source.ID}, {PresetMode: "bad"}, {PresetMode: "reuse"}, {PresetMode: "copy"}, {PresetMode: "reuse", PresetID: foreign.ID}, {PresetMode: "copy", PresetID: foreign.ID}, {PresetMode: "reuse", PresetID: source.ID, ApplyWeights: true}, {PresetMode: "copy", PresetID: source.ID, ApplyWeights: true}, {PresetMode: "reuse", PresetID: source.ID, Target: "into_deck:1"},
				{Target: intoTarget, PresetID: source.ID}, {Target: intoTarget, PresetMode: "new", PresetID: source.ID},
				{Target: replaceTarget, PresetID: source.ID}, {Target: replaceTarget, PresetMode: "new", PresetID: source.ID},
			}
			for _, opts := range cases {
				t.Run(fmtOptions(opts), func(t *testing.T) {
					var decks, presets, decksAfter, presetsAfter int64
					db.Model(&Deck{}).Count(&decks)
					db.Model(&Preset{}).Count(&presets)
					pe := importPackageRejected(t, db, users[0], raw, opts)
					if pe.Code != "" {
						t.Fatalf("code = %q", pe.Code)
					}
					db.Model(&Deck{}).Count(&decksAfter)
					db.Model(&Preset{}).Count(&presetsAfter)
					if decks != decksAfter || presets != presetsAfter {
						t.Fatal("database changed")
					}
				})
			}
			for _, mode := range []string{"", "new"} {
				t.Run("into_deck_mode_"+mode, func(t *testing.T) {
					report := importPackageOK(t, db, users[0], raw, PackageImportOptions{Target: intoTarget, PresetMode: mode})
					encoded, err := json.Marshal(report)
					if err != nil {
						t.Fatal(err)
					}
					if bytes.Contains(encoded, []byte(`"preset_mode"`)) || bytes.Contains(encoded, []byte(`"preset_name"`)) {
						t.Fatalf("unexpected preset fields: %s", encoded)
					}
				})
			}
		})
	}
}

func fmtOptions(o PackageImportOptions) string { b, _ := json.Marshal(o); return string(b) }

func TestImportReportPresetByTarget(t *testing.T) {
	apkg, err := os.ReadFile(ankiFixturePath("stock.anki21.apkg"))
	if err != nil {
		t.Fatal(err)
	}
	formats := []struct {
		name string
		raw  []byte
	}{{"edeck", manifestZip(t, "Report", "")}, {"apkg", apkg}}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, db, "report_preset_owner")[0]
			for _, format := range formats {
				for _, target := range []string{"new_deck", "into_deck", "replace_deck"} {
					t.Run(format.name+"/"+target, func(t *testing.T) {
						existing := importPackageOK(t, db, owner, manifestZip(t, "Target", ""), PackageImportOptions{})
						opts := PackageImportOptions{Target: target, MediaRoot: t.TempDir()}
						if target != "new_deck" {
							opts.Target = fmt.Sprintf("%s:%d", target, existing.DeckID)
						}
						report := importPackageOK(t, db, owner, format.raw, opts)
						if target == "new_deck" {
							var deck Deck
							var preset Preset
							if err := db.First(&deck, report.DeckID).Error; err != nil {
								t.Fatal(err)
							}
							if err := db.First(&preset, deck.PresetID).Error; err != nil {
								t.Fatal(err)
							}
							if report.PresetMode != "new" || report.PresetName != preset.Name || preset.Name == "" {
								t.Fatalf("report: %+v preset: %+v", report, preset)
							}
						} else if report.PresetMode != "" || report.PresetName != "" {
							t.Fatalf("report: %+v", report)
						}
					})
				}
			}
		})
	}
}
