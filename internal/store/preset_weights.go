package store

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/open-spaced-repetition/go-fsrs/v4"
)

// PresetWeights decodes and migrates preset weights, using defaults for NULL, empty, or null values.
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
