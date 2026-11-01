package store

import (
	"context"
	"testing"
)

func TestCreatePackagePresetSchedulerLimits(t *testing.T) {
	cases := []struct {
		name, learning, relearning   string
		max                          int
		wantLearning, wantRelearning string
		wantMax                      int
	}{
		{"maximum", "2m", "3m", 50000, "2m", "3m", 36500},
		{"negative learning", "-1m", "3m", 1000, DefaultLearningSteps, "3m", 1000},
		{"invalid learning", "abc", "3m", 1000, DefaultLearningSteps, "3m", 1000},
		{"negative relearning", "2m", "-10m", 1000, "2m", DefaultRelearningSteps, 1000},
		{"invalid relearning", "2m", "abc", 1000, "2m", DefaultRelearningSteps, 1000},
		{"empty steps", "", "", 36500, "", "", 36500},
		{"mixed invalid", "1m,abc", "3m", 1000, DefaultLearningSteps, "3m", 1000},
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, db, "package_preset_limits")[0]
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					pp := &PackagePreset{LearningSteps: tc.learning, RelearningSteps: tc.relearning, MaximumIntervalDays: tc.max}
					p, err := createPresetFromPackage(context.Background(), db, owner, pp, tc.name, false)
					if err != nil {
						t.Fatal(err)
					}
					got, err := NewPresetStore(db).ByID(context.Background(), p.ID)
					if err != nil {
						t.Fatal(err)
					}
					if got.LearningSteps != tc.wantLearning || got.RelearningSteps != tc.wantRelearning || got.MaximumIntervalDays != tc.wantMax {
						t.Fatalf("stored preset: %+v", got)
					}
				})
			}
		})
	}
}
