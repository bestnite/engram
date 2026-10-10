package mcp

import (
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestPackageWeightsToolOptions 断言 export_deck 的 include_weights 与 import_deck 的 apply_weights
// 都到达 service 层：缺省时不带权重、不采用权重，与 REST 的缺省一致。
func TestPackageWeightsToolOptions(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "weights")
	deck := seedDeck(t, db, u.ID)
	if err := db.Model(&store.Preset{}).Where("id = ?", deck.PresetID).Updates(map[string]any{
		"weights_json":         `[0.5,0.51,0.52,0.53,0.54,0.55,0.56,0.57,0.58,0.59,0.6,0.61,0.62,0.63,0.64,0.65,0.66,0.67,0.68,0.69,0.7]`,
		"weights_review_count": 42,
	}).Error; err != nil {
		t.Fatalf("seed weights: %v", err)
	}
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)

	exportCases := []struct {
		name        string
		args        map[string]any
		wantWeights bool
	}{
		{"default omits weights", map[string]any{"deck_id": deck.PublicID}, false},
		{"include_weights exports them", map[string]any{"deck_id": deck.PublicID, "include_weights": true}, true},
	}
	var weightedDoc map[string]any
	for _, tc := range exportCases {
		t.Run("export "+tc.name, func(t *testing.T) {
			doc, isErr, text := callTool(t, cs, "export_deck", tc.args)
			if isErr {
				t.Fatalf("export_deck error: %s", text)
			}
			preset, _ := doc["preset.json"].(map[string]any)
			weights, _ := preset["weights"].([]any)
			if (len(weights) == 21) != tc.wantWeights {
				t.Fatalf("preset.json weights = %v, want present=%v", preset["weights"], tc.wantWeights)
			}
			manifest, _ := doc["manifest.json"].(map[string]any)
			if manifest["include_weights"] != tc.wantWeights {
				t.Fatalf("manifest.include_weights = %v, want %v", manifest["include_weights"], tc.wantWeights)
			}
			if tc.wantWeights {
				weightedDoc = doc
			}
		})
	}
	if weightedDoc == nil {
		t.Fatal("no weighted package document to import")
	}

	importCases := []struct {
		name          string
		apply         *bool
		wantApplied   bool
		wantDiscarded bool
	}{
		{"default keeps the defaults", nil, false, true},
		{"apply_weights applies them", store.Ptr(true), true, false},
	}
	for _, tc := range importCases {
		t.Run("import "+tc.name, func(t *testing.T) {
			args := map[string]any{"package": weightedDoc, "dry_run": true, "target": "new_deck"}
			if tc.apply != nil {
				args["apply_weights"] = *tc.apply
			}
			report, isErr, text := callTool(t, cs, "import_deck", args)
			if isErr {
				t.Fatalf("import_deck error: %s", text)
			}
			if report["weights_applied"] != tc.wantApplied || report["weights_discarded"] != tc.wantDiscarded {
				t.Fatalf("weights_applied/discarded = %v/%v, want %v/%v",
					report["weights_applied"], report["weights_discarded"], tc.wantApplied, tc.wantDiscarded)
			}
		})
	}
}
