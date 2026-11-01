package store

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/open-spaced-repetition/go-fsrs/v4"
)

func importPresetWeights(owner uint64, weights []float64, apply bool, report *PackageImportReport) *string {
	if !apply || len(weights) == 0 {
		return nil
	}
	if _, err := fsrs.MigrateWeights(weights); err != nil {
		report.WeightsInvalid = true
		slog.Warn("package weights rejected", "user_id", owner, "error", err)
		return nil
	}
	// MigrateWeights accepts only finite float64 values, which JSON can encode.
	raw, _ := json.Marshal(weights)
	text := string(raw)
	return &text
}

// PresetWeights decodes and migrates preset weights, using defaults for NULL, an empty
// string, a whitespace-only string, or null. It lives in store so progress import and
// the scheduler share one rule: imported memory states must use the weights the scheduler
// continues with, and schedule already depends on store.
func PresetWeights(raw *string) (fsrs.Weights, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return fsrs.DefaultWeights(), nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "null" {
		return fsrs.DefaultWeights(), nil
	}
	var arr []float64
	if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
		return fsrs.Weights{}, fmt.Errorf("store: decode preset weights: %w", err)
	}
	w, err := fsrs.MigrateWeights(arr)
	if err != nil {
		return fsrs.Weights{}, fmt.Errorf("store: migrate preset weights: %w", err)
	}
	return w, nil
}
