package schedule

import (
	"reflect"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

func TestMigratedPresetSchedulerLimits(t *testing.T) {
	db := newTestDB(t)
	weights := `[0.5,1.5,2.5,3.5,4.5,3.5,3.5,0.05,1.0,0.2,0.8,1.5,0.06,0.3,1.6,0.6,1.9,0.5,0.1,0.07,0.15]`
	var ids []uint64
	for _, tc := range []struct {
		name, learning, relearning string
		max                        int
	}{
		{"interval", "2m", "3m", 50000},
		{"negative", "-1m", "-1m", 1000},
		{"nan", "NaNm", "NaNm", 1000},
		{"infinity", "+Infm", "+Infm", 1000},
	} {
		p := store.NewPreset(1, tc.name)
		p.LearningSteps, p.RelearningSteps, p.MaximumIntervalDays, p.WeightsJSON = tc.learning, tc.relearning, tc.max, &weights
		if err := db.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	migration := store.BuiltinMigrations[len(store.BuiltinMigrations)-1]
	if migration.Name != "0020_repair_preset_scheduler_limits" {
		t.Fatalf("unexpected migration %q", migration.Name)
	}
	if err := migration.Up(db); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		var p store.Preset
		if err := db.First(&p, id).Error; err != nil {
			t.Fatal(err)
		}
		t.Run(p.Name, func(t *testing.T) {
			want, err := Parameters(&p)
			if err != nil {
				t.Fatal(err)
			}
			s, err := NewScheduler(&p)
			if err != nil {
				t.Fatal(err)
			}
			got := s.fsrs.Parameters
			if got.MaximumInterval != want.MaximumInterval || !reflect.DeepEqual(got.LearningSteps, want.LearningSteps) || !reflect.DeepEqual(got.RelearningSteps, want.RelearningSteps) || got.W[0] != 0.5 {
				t.Fatalf("scheduler parameters: got %+v want %+v", got, want)
			}
		})
	}
}
