package answer

import (
	"math"
	"strings"
	"testing"
)

// TestGradeMappingThresholdValidation pins the threshold domain: a stored
// mapping outside it is refused instead of silently changing a rating that
// reviews.rating cannot be reconstructed from later.
func TestGradeMappingThresholdValidation(t *testing.T) {
	refused := []struct {
		name string
		raw  string
	}{
		{"full_threshold above one", `{"full_threshold":2}`},
		{"both thresholds above one", `{"full_threshold":2,"none_threshold":1.5}`},
		{"negative none_threshold", `{"none_threshold":-0.5}`},
		{"none_threshold above one", `{"none_threshold":1.2}`},
		{"full_threshold not above none_threshold", `{"full_threshold":0.5,"none_threshold":0.5}`},
	}
	for _, test := range refused {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseGradeMapping(test.raw); err == nil {
				t.Fatalf("ParseGradeMapping(%s) error = nil, want the threshold refused", test.raw)
			}
		})
	}
}

// TestGradeMappingIntentionalZeroThreshold asserts that a zero threshold is
// distinguishable from an unset one: it survives parse and marshal, and it
// keeps a score of 0 at the "none" rating while a score above it is partial.
func TestGradeMappingIntentionalZeroThreshold(t *testing.T) {
	mapping, err := ParseGradeMapping(`{"full_threshold":0.5,"none_threshold":0}`)
	if err != nil {
		t.Fatalf("ParseGradeMapping: %v", err)
	}
	if mapping.NoneThreshold == nil {
		t.Fatal("none_threshold = nil after parsing an explicit 0, want it kept as set")
	}
	if got := *mapping.NoneThreshold; got != 0 {
		t.Fatalf("none_threshold = %g, want the stored 0", got)
	}
	if got := mapping.RatingFor(0); got != RatingAgain {
		t.Fatalf("RatingFor(0) = %d, want %d", got, RatingAgain)
	}
	if got := mapping.RatingFor(0.25); got != RatingHard {
		t.Fatalf("RatingFor(0.25) = %d, want %d", got, RatingHard)
	}
	if got := mapping.RatingFor(0.5); got != RatingGood {
		t.Fatalf("RatingFor(0.5) = %d, want %d", got, RatingGood)
	}

	raw, err := MarshalGradeMapping(mapping)
	if err != nil {
		t.Fatalf("MarshalGradeMapping: %v", err)
	}
	if !strings.Contains(raw, `"none_threshold":0`) {
		t.Fatalf("stored mapping = %s, want the explicit none_threshold 0 kept", raw)
	}
	roundTripped, err := ParseGradeMapping(raw)
	if err != nil {
		t.Fatalf("ParseGradeMapping after marshal: %v", err)
	}
	if roundTripped.NoneThreshold == nil || *roundTripped.NoneThreshold != 0 {
		t.Fatalf("none_threshold after round trip = %v, want the explicit 0", roundTripped.NoneThreshold)
	}
}

// TestGradeMappingRefusesNonFiniteThreshold asserts a threshold that is not a
// finite number is refused rather than compared with a score.
func TestGradeMappingRefusesNonFiniteThreshold(t *testing.T) {
	for name, value := range map[string]float64{"NaN": math.NaN(), "positive infinity": math.Inf(1)} {
		t.Run(name, func(t *testing.T) {
			mapping := GradeMapping{NoneThreshold: thresholdPtr(value)}
			err := mapping.validate()
			if err == nil {
				t.Fatalf("validate with none_threshold %v error = nil, want it refused", value)
			}
			if !strings.Contains(err.Error(), "finite") {
				t.Fatalf("validate error = %q, want it to name the finite-threshold rule", err)
			}
		})
	}
}

// TestGradeMappingDefaultsStillApply asserts an absent threshold keeps the
// documented default, so the "unset means default" rule still holds.
func TestGradeMappingDefaultsStillApply(t *testing.T) {
	mapping, err := ParseGradeMapping(`{}`)
	if err != nil {
		t.Fatalf("ParseGradeMapping: %v", err)
	}
	if got := mapping.RatingFor(1); got != RatingGood {
		t.Fatalf("RatingFor(1) = %d, want %d", got, RatingGood)
	}
	if got := mapping.RatingFor(0); got != RatingAgain {
		t.Fatalf("RatingFor(0) = %d, want %d", got, RatingAgain)
	}
}
