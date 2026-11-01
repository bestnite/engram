package store

import (
	"bytes"
	"context"
	"log/slog"
	"reflect"
	"strings"
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
				retention, wantRetention     float64
			}{
				{"interval", "2m", "3m", 50000, "2m", "3m", 36500, .9, .9},
				{"negative interval", "2m", "3m", -5, "2m", "3m", 36500, .9, .9},
				{"zero interval", "2m", "3m", 0, "2m", "3m", 36500, .9, .9},
				{"zero retention", "2m", "3m", 1000, "2m", "3m", 1000, 0, .9},
				{"negative retention", "2m", "3m", 1000, "2m", "3m", 1000, -.1, .9},
				{"high retention", "2m", "3m", 1000, "2m", "3m", 1000, 1.5, .9},
				{"negative steps", "-1m", "-2m", 1000, DefaultLearningSteps, DefaultRelearningSteps, 1000, .9, .9},
				{"nan", "NaNm", "NaNm", 1000, DefaultLearningSteps, DefaultRelearningSteps, 1000, .9, .9},
				{"infinity", "+Infm", "+Infm", 1000, DefaultLearningSteps, DefaultRelearningSteps, 1000, .9, .9},
				{"over the step cap", "36501d", "36501d", 1000, DefaultLearningSteps, DefaultRelearningSteps, 1000, .9, .9},
				{"invalid", "1m,broken", "broken", 1000, DefaultLearningSteps, DefaultRelearningSteps, 1000, .9, .9},
				{"legal", " 2m, ,10m ", "", 36500, " 2m, ,10m ", "", 36500, 1, 1},
			}
			var originals []Preset
			for _, tc := range cases {
				p := NewPreset(1, tc.name)
				p.LearningSteps, p.RelearningSteps, p.MaximumIntervalDays = tc.learning, tc.relearning, tc.max
				if err := db.Create(&p).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&Preset{}).Where("id = ?", p.ID).UpdateColumns(map[string]any{"maximum_interval_days": tc.max, "desired_retention": tc.retention}).Error; err != nil {
					t.Fatal(err)
				}
				var stored Preset
				if err := db.First(&stored, p.ID).Error; err != nil {
					t.Fatal(err)
				}
				originals = append(originals, stored)
			}
			for run := 0; run < 2; run++ {
				var logs bytes.Buffer
				previous := slog.Default()
				slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
				if err := repairPresetSchedulerLimits(db); err != nil {
					slog.SetDefault(previous)
					t.Fatal(err)
				}
				slog.SetDefault(previous)
				if run == 1 && !strings.Contains(logs.String(), `"rows_changed":0`) {
					t.Errorf("second run log = %s, want zero rows changed", logs.String())
				}
				for i, tc := range cases {
					var got Preset
					if err := db.First(&got, originals[i].ID).Error; err != nil {
						t.Fatal(err)
					}
					want := originals[i]
					want.LearningSteps, want.RelearningSteps, want.MaximumIntervalDays = tc.wantLearning, tc.wantRelearning, tc.wantMax
					want.DesiredRetention = tc.wantRetention
					if !reflect.DeepEqual(got, want) {
						t.Errorf("run %d %s: got %+v, want %+v", run+1, tc.name, got, want)
					}
				}
			}
		})
	}
}
