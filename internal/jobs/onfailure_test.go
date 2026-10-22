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

// This file is the job-side acceptance for the failure hook: after a job failure is persisted, the OnFailure hook is called once,
// and a hook panic does not in turn affect job failure handling (the job-side expression of "a failed notification does not affect the triggering operation").

// failingRunner builds a Runner that does not Start automatically, so the hook can be wired before Start.
func failingRunner(t *testing.T, hook FailureFunc) (*Runner, *Store, context.CancelFunc) {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "onfailure.db"))
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
			return Command{Name: "/bin/sh", Args: []string{"-c", "exit 3"}}, nil
		},
		OnFailure: hook,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runner.Start(ctx)
	return runner, st, cancel
}

// TestFailureHookInvokedAfterJobFails asserts a failed job calls the OnFailure hook, with the failure reason.
func TestFailureHookInvokedAfterJobFails(t *testing.T) {
	type call struct {
		id     uint64
		reason string
	}
	calls := make(chan call, 1)
	runner, st, _ := failingRunner(t, func(_ context.Context, job store.Job, reason string) {
		calls <- call{id: job.ID, reason: reason}
	})

	job, err := runner.Enqueue(context.Background(), KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	failed := waitForStatus(t, st, job.ID, StatusFailed, 5*time.Second)

	select {
	case c := <-calls:
		if c.id != job.ID {
			t.Fatalf("hook job id = %d, want %d", c.id, job.ID)
		}
		if c.reason == "" {
			t.Fatalf("hook reason is empty; want the failure reason")
		}
		t.Logf("job %d failed (%q); OnFailure hook fired with reason %q", job.ID, *failed.Error, c.reason)
	case <-time.After(2 * time.Second):
		t.Fatalf("OnFailure hook was not invoked for failed job %d", job.ID)
	}
}

// TestFailureHookPanicDoesNotBreakFailureHandling asserts hook panics are caught: the job still ends up failed,
// the worker does not crash, and later jobs still run.
func TestFailureHookPanicDoesNotBreakFailureHandling(t *testing.T) {
	runner, st, _ := failingRunner(t, func(context.Context, store.Job, string) {
		panic("notification exploded")
	})

	job, err := runner.Enqueue(context.Background(), KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	waitForStatus(t, st, job.ID, StatusFailed, 5*time.Second)

	// The worker is still alive: the second job is likewise enqueued and fails.
	second, err := runner.Enqueue(context.Background(), KindOptimize, nil)
	if err != nil {
		t.Fatalf("second Enqueue after hook panic: %v", err)
	}
	waitForStatus(t, st, second.ID, StatusFailed, 5*time.Second)
	t.Logf("hook panic contained; jobs %d and %d both reached failed", job.ID, second.ID)
}
