package store

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

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
					report := importPackageOK(t, db, users[0], raw, PackageImportOptions{PresetMode: mode, PresetID: source.ID})
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
			cases := []PackageImportOptions{{PresetMode: "bad"}, {PresetMode: "reuse"}, {PresetMode: "copy"}, {PresetMode: "reuse", PresetID: foreign.ID}, {PresetMode: "copy", PresetID: foreign.ID}, {PresetMode: "reuse", PresetID: source.ID, ApplyWeights: true}, {PresetMode: "copy", PresetID: source.ID, ApplyWeights: true}, {PresetMode: "reuse", PresetID: source.ID, Target: "into_deck:1"}}
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
		})
	}
}

func fmtOptions(o PackageImportOptions) string { b, _ := json.Marshal(o); return string(b) }
