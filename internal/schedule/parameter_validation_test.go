package schedule

import (
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestNewSchedulerRefusesParametersTheLibraryWouldReplace pins that a preset the go-fsrs library would reject is
// refused with an error naming the parameter, instead of being replaced wholesale by the library's defaults.
//
// The library's NewFSRS returns no error: it clips the weights and, when the parameters still fail validation,
// swaps in DefaultParam(). A scheduler built from a preset with retention 0 or a negative step would therefore
// schedule and record reviews from 0.9 / 1m,10m while the owner believes otherwise.
func TestNewSchedulerRefusesParametersTheLibraryWouldReplace(t *testing.T) {
	base := func() *store.Preset {
		p := store.NewPreset(1, "Test")
		return &p
	}

	cases := []struct {
		name   string
		mutate func(*store.Preset)
		want   string
	}{
		{"retention zero", func(p *store.Preset) { p.DesiredRetention = 0 }, "desired retention"},
		{"retention above one", func(p *store.Preset) { p.DesiredRetention = 1.5 }, "desired retention"},
		{"maximum interval zero", func(p *store.Preset) { p.MaximumIntervalDays = 0 }, "maximum interval"},
		{"maximum interval above the library ceiling", func(p *store.Preset) { p.MaximumIntervalDays = 36501 }, "maximum interval"},
		{"negative learning step", func(p *store.Preset) { p.LearningSteps = "1m,-5m" }, "learning step"},
		{"negative relearning step", func(p *store.Preset) { p.RelearningSteps = "-10m" }, "relearning step"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			p := base()
			test.mutate(p)
			_, err := NewScheduler(p)
			if err == nil {
				t.Fatalf("NewScheduler() error = nil, want the parameter the library would replace refused")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewScheduler() error = %v, want it to name %q", err, test.want)
			}
		})
	}

	// Control: the documented defaults still build a scheduler, so only bad values are refused.
	if _, err := NewScheduler(base()); err != nil {
		t.Fatalf("NewScheduler(defaults) error = %v, want nil", err)
	}
}
