//go:build unix

package jobs

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// The optimize finish is one transaction: the preset's weights and the job's terminal row commit together.
// Each case below makes one of those writes fail inside the database and checks that the failure is not
// half-applied and that the row ends terminal instead of staying running.
func TestOptimizeFinishIsAtomic(t *testing.T) {
	const (
		failTerminal = `CREATE TRIGGER probe_fail_terminal BEFORE UPDATE ON jobs
			WHEN NEW.status = 'succeeded'
			BEGIN SELECT RAISE(ABORT, 'injected terminal write failure'); END;`
		failPreset = `CREATE TRIGGER probe_fail_preset BEFORE UPDATE ON presets
			BEGIN SELECT RAISE(ABORT, 'injected preset write failure'); END;`
	)

	cases := []struct {
		name    string
		trigger string
		// complete selects the optimization write-back path; false exercises the generic exit-code path.
		complete bool
	}{
		{name: "a failing terminal write rolls the preset back", trigger: failTerminal, complete: true},
		{name: "a failing write-back leaves the job terminal", trigger: failPreset, complete: true},
		{name: "a failed generic finish ends the job", trigger: failTerminal, complete: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "jobs.db"))
			if err != nil {
				t.Fatalf("open sqlite: %v", err)
			}
			if err := store.AutoMigrate(context.Background(), db); err != nil {
				t.Fatalf("AutoMigrate: %v", err)
			}
			ctx := context.Background()

			preset := store.NewPreset(1, "optimize preset")
			if err := db.Create(&preset).Error; err != nil {
				t.Fatalf("create preset: %v", err)
			}
			oldWeights := `["old"]`
			if err := db.Model(&preset).Update("weights_json", oldWeights).Error; err != nil {
				t.Fatalf("seed old weights: %v", err)
			}

			st := NewStore(db)
			target := preset.ID
			job, err := st.CreateQueued(ctx, KindOptimize, &target, time.Now().UTC())
			if err != nil {
				t.Fatalf("create job: %v", err)
			}

			if err := db.Exec(tc.trigger).Error; err != nil {
				t.Fatalf("install trigger: %v", err)
			}

			deps := Deps{
				DB:    db,
				Store: st,
				Command: func(context.Context, *store.Job, Reporter) (Command, error) {
					return Command{Name: "/bin/true"}, nil
				},
			}
			if tc.complete {
				deps.Complete = func(context.Context, *store.Job, Command, string) (store.OptimizeResult, error) {
					return store.OptimizeResult{ReviewsUsed: 500, Weights: []float64{0.4, 0.6}}, nil
				}
			}
			runner, err := New(deps)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			runner.execute(ctx, job)

			got, err := st.ByID(ctx, job.ID)
			if err != nil {
				t.Fatalf("ByID: %v", err)
			}
			if got.Status != StatusFailed {
				t.Errorf("job status = %q, want %q (the failed write must not leave the job non-terminal)", got.Status, StatusFailed)
			}
			if got.FinishedAt == nil {
				t.Error("finished_at is NULL, want the terminal write to have landed")
			}
			if got.ResultJSON != nil {
				t.Errorf("result_json = %q, want NULL: the job did not succeed", *got.ResultJSON)
			}

			stored, err := store.NewPresetStore(db).ByID(ctx, preset.ID)
			if err != nil {
				t.Fatalf("load preset: %v", err)
			}
			gotWeights := "<nil>"
			if stored.WeightsJSON != nil {
				gotWeights = *stored.WeightsJSON
			}
			if gotWeights != oldWeights {
				t.Errorf("preset weights = %q, want the old %q: a rolled-back finish must not apply them", gotWeights, oldWeights)
			}

			active, err := st.Active(ctx)
			if err != nil {
				t.Fatalf("Active: %v", err)
			}
			if active != nil {
				t.Errorf("Active() = job %d %q, want none: the row must be terminal", active.ID, active.Status)
			}
		})
	}
}

// A terminal write that keeps failing must not be retried forever, but the first failure already has to end
// the row as failed. This pins the error text that tells the operator which step failed.
func TestOptimizeFinishFailureNamesTheStep(t *testing.T) {
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	ctx := context.Background()
	st := NewStore(db)
	job, err := st.CreateQueued(ctx, KindOptimize, nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := db.Exec(`CREATE TRIGGER probe_fail_terminal BEFORE UPDATE ON jobs
		WHEN NEW.status = 'succeeded'
		BEGIN SELECT RAISE(ABORT, 'injected terminal write failure'); END;`).Error; err != nil {
		t.Fatalf("install trigger: %v", err)
	}

	runner, err := New(Deps{
		DB:    db,
		Store: st,
		Command: func(context.Context, *store.Job, Reporter) (Command, error) {
			return Command{Name: "/bin/true"}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runner.execute(ctx, job)

	got, err := st.ByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Status != StatusFailed || got.Error == nil || *got.Error == "" {
		t.Fatalf("status/error = %q/%v, want failed with the failing step named", got.Status, got.Error)
	}
	if want := "finish job succeeded"; !strings.Contains(*got.Error, want) {
		t.Errorf("error = %q, want it to name %q", *got.Error, want)
	}
}
