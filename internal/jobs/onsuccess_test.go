//go:build unix

// Compiles only on Unix: the fake command goes through `/bin/sh -c ...` (same reason as the file header note in jobs_test.go).

package jobs

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// This file is the job-side acceptance for the success hook: after a job success is persisted OnSuccess is called once, and a hook panic does not
// in turn affect the job result — the job-side expression of "a failed notification does not affect the triggering operation" (symmetric with onfailure_test.go).

// succeedingRunner builds a Runner that does not Start automatically, so the hook can be wired before Start.
func succeedingRunner(t *testing.T, hook SuccessFunc) (*Runner, *Store, context.CancelFunc) {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "onsuccess.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	st := NewStore(db)
	runner, err := New(Deps{
		DB:      db,
		Store:   st,
		Timeout: time.Second,
		Command: func(context.Context, *store.Job, Reporter) (Command, error) {
			return Command{Name: "/bin/sh", Args: []string{"-c", "echo ok"}}, nil
		},
		OnSuccess: hook,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runner.Start(ctx)
	return runner, st, cancel
}

// TestSuccessHookInvokedAfterJobSucceeds asserts a successful job calls the OnSuccess hook with the job row.
func TestSuccessHookInvokedAfterJobSucceeds(t *testing.T) {
	jobs := make(chan store.Job, 1)
	runner, st, _ := succeedingRunner(t, func(_ context.Context, job store.Job) {
		jobs <- job
	})

	job, err := runner.Enqueue(context.Background(), KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	done := waitForStatus(t, st, job.ID, StatusSucceeded, 5*time.Second)

	select {
	case got := <-jobs:
		if got.ID != job.ID {
			t.Fatalf("hook job id = %d, want %d", got.ID, job.ID)
		}
		if got.Status != StatusSucceeded {
			t.Errorf("hook saw status %q, want %q (the hook runs after the row is written)", got.Status, StatusSucceeded)
		}
		t.Logf("job %d succeeded (finished_at %v); OnSuccess fired for the same job", done.ID, done.FinishedAt)
	case <-time.After(2 * time.Second):
		t.Fatalf("OnSuccess hook was not invoked for succeeded job %d", job.ID)
	}
}

// TestSuccessHookPanicDoesNotBreakCompletion asserts hook panics are caught: the job still ends up succeeded.
func TestSuccessHookPanicDoesNotBreakCompletion(t *testing.T) {
	runner, st, _ := succeedingRunner(t, func(context.Context, store.Job) {
		panic("hook blew up")
	})

	job, err := runner.Enqueue(context.Background(), KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	done := waitForStatus(t, st, job.ID, StatusSucceeded, 5*time.Second)
	if done.Status != StatusSucceeded {
		t.Fatalf("job status = %q, want succeeded even though the hook panicked", done.Status)
	}
}
