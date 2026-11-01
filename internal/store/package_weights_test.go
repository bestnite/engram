package store

import (
	"testing"

	"gorm.io/gorm"
)

// ---- FSRS weights export / import switches ----

// packageTestWeights is a 21-dimensional non-default weight vector, used only to tell whether it travelled or not.
func packageTestWeights() []float64 {
	w := make([]float64, 21)
	for i := range w {
		w[i] = 0.5 + float64(i)/100
	}
	return w
}

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
