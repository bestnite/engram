//go:build unix

// Compiles only on Unix: the fake command goes through `/bin/sh -c ...` (same reason as the file header note in jobs_test.go).

package jobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// fileSize returns the file's current byte count; 0 when it does not exist.
func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// waitFileNonEmpty polls until the file exists and is non-empty, proving the subprocess is really producing output (not just changing a status field).
func waitFileNonEmpty(t *testing.T, path string, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if fileSize(path) > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("file %s stayed empty within %s; the subprocess never produced output", path, limit)
}

// TestCancelRunningJobKillsProcessAndUnblocksQueue is the core acceptance:
// a real long-running subprocess keeps the job in running; cancelling it must achieve
//  1. Cancel returns immediately (does not block);
//  2. the job is persisted as failed with error = CancelReason;
//  3. the subprocess really is killed (the marker file stops growing, proving the whole process group disappeared rather than just a status change);
//  4. after cancelling, a new job can be enqueued successfully and run to completion (the single-concurrency gate has been released).
func TestCancelRunningJobKillsProcessAndUnblocksQueue(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ticks")

	// The first call returns a real long command that keeps writing the marker; afterwards return a fast-exiting command, for verifying the new enqueue.
	var calls int32
	builder := func(_ context.Context, _ *store.Job, _ Reporter) (Command, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return Command{
				Name: "/bin/sh",
				Args: []string{"-c", "while true; do echo tick >> " + marker + "; sleep 0.05; done"},
			}, nil
		}
		return Command{Name: "/bin/true"}, nil
	}

	runner, st, _ := newTestRunner(t, time.Minute, builder)

	job, err := runner.Enqueue(context.Background(), KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	waitForStatus(t, st, job.ID, StatusRunning, 5*time.Second)
	waitFileNonEmpty(t, marker, 5*time.Second)

	start := time.Now()
	if err := runner.Cancel(context.Background(), job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Cancel blocked for %s; it must return once the job is terminal", d)
	}

	got, err := st.ByID(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Status != StatusFailed {
		t.Errorf("cancelled job status = %q, want %q", got.Status, StatusFailed)
	}
	if got.Error == nil || *got.Error != CancelReason {
		t.Errorf("cancelled job error = %v, want %q", got.Error, CancelReason)
	}

	// The subprocess must really be gone: the marker stops growing after the cancellation.
	sizeAfterCancel := fileSize(marker)
	time.Sleep(400 * time.Millisecond)
	if grew := fileSize(marker); grew != sizeAfterCancel {
		t.Errorf("subprocess kept running after cancel: marker grew %d -> %d", sizeAfterCancel, grew)
	}

	// The single-concurrency gate has been released after the cancellation: a new job can be enqueued and complete successfully.
	second, err := runner.Enqueue(context.Background(), KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue after cancel: %v (single-flight gate not released)", err)
	}
	done := waitForStatus(t, st, second.ID, StatusSucceeded, 5*time.Second)
	if done.Status != StatusSucceeded {
		t.Errorf("second job status = %q, want %q", done.Status, StatusSucceeded)
	}
}

// TestCancelQueuedJobAtPickupNeverRunsCommand covers the queued-to-running race: the
// worker has read the queued row and is between that read and MarkRunning when Cancel
// marks the row failed. The subprocess must never start, and the terminal row must keep
// the cancellation reason instead of being flipped to running and then succeeded.
func TestCancelQueuedJobAtPickupNeverRunsCommand(t *testing.T) {
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	st := NewStore(db)

	var ran atomic.Bool
	runner, err := New(Deps{
		DB:      db,
		Store:   st,
		Timeout: 5 * time.Second,
		Command: func(context.Context, *store.Job, Reporter) (Command, error) {
			ran.Store(true)
			return Command{Name: "/bin/true"}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Block the worker's first job read by primary key after its row was scanned. The
	// read is the last step before the queued-to-running transition, so cancelling while
	// it is held reproduces the interleaving deterministically.
	var stalled atomic.Bool
	reached := make(chan struct{})
	release := make(chan struct{})
	hook := func(tx *gorm.DB) {
		sql := tx.Statement.SQL.String()
		if !strings.Contains(sql, "id = ") {
			return
		}
		if stalled.CompareAndSwap(false, true) {
			close(reached)
			<-release
		}
	}
	if err := db.Callback().Query().After("gorm:query").Register("test:queued_pickup", hook); err != nil {
		t.Fatalf("register query hook: %v", err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove("test:queued_pickup") })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runner.Start(ctx)

	job, err := runner.Enqueue(context.Background(), KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	select {
	case <-reached:
	case <-time.After(3 * time.Second):
		t.Fatalf("the worker never read the queued job")
	}

	if err := runner.Cancel(context.Background(), job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	close(release)

	// Give the worker time to resume past the read and reach its transition. A command
	// that ran would set the flag, and a running transition would rewrite the row.
	time.Sleep(300 * time.Millisecond)
	if ran.Load() {
		t.Errorf("the subprocess ran after the job was cancelled while queued")
	}
	got, err := st.ByID(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Status != StatusFailed {
		t.Errorf("status after cancelling the queued job = %q, want %q", got.Status, StatusFailed)
	}
	if got.Error == nil || *got.Error != CancelReason {
		t.Errorf("cancelled job error = %v, want %q", got.Error, CancelReason)
	}
	if got.StartedAt != nil {
		t.Errorf("cancelled job started_at = %v, want NULL", got.StartedAt)
	}
}
