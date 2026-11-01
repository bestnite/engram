package store

import (
	"context"
	"reflect"
	"testing"
)

func TestRepairPresetSchedulerLimits(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := AutoMigrate(context.Background(), db); err != nil {
				t.Fatal(err)
			}
			cases := []struct {
				name, learning, relearning   string
				max                          int
				wantLearning, wantRelearning string
				wantMax                      int
			}{
				{"interval", "2m", "3m", 50000, "2m", "3m", 36500},
				{"negative", "-1m", "-2m", 1000, DefaultLearningSteps, DefaultRelearningSteps, 1000},
				{"nan", "NaNm", "NaNm", 1000, DefaultLearningSteps, DefaultRelearningSteps, 1000},
				{"infinity", "+Infm", "+Infm", 1000, DefaultLearningSteps, DefaultRelearningSteps, 1000},
				{"invalid", "1m,broken", "broken", 1000, DefaultLearningSteps, DefaultRelearningSteps, 1000},
				{"legal", " 2m, ,10m ", "", 36500, " 2m, ,10m ", "", 36500},
			}
			var originals []Preset
			for _, tc := range cases {
				p := NewPreset(1, tc.name)
				p.LearningSteps, p.RelearningSteps, p.MaximumIntervalDays = tc.learning, tc.relearning, tc.max
				if err := db.Create(&p).Error; err != nil {
					t.Fatal(err)
				}
				var stored Preset
				if err := db.First(&stored, p.ID).Error; err != nil {
					t.Fatal(err)
				}
				originals = append(originals, stored)
			}
			for run := 0; run < 2; run++ {
				if err := repairPresetSchedulerLimits(db); err != nil {
					t.Fatal(err)
				}
				for i, tc := range cases {
					var got Preset
					if err := db.First(&got, originals[i].ID).Error; err != nil {
						t.Fatal(err)
					}
					want := originals[i]
					want.LearningSteps, want.RelearningSteps, want.MaximumIntervalDays = tc.wantLearning, tc.wantRelearning, tc.wantMax
					if !reflect.DeepEqual(got, want) {
						t.Errorf("run %d %s: got %+v, want %+v", run+1, tc.name, got, want)
					}
				}
			}
		})
	}
}
