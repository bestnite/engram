//go:build unix

// The shutdown regression test runs a real `/bin/sh -c ...` subprocess and checks that its background child exited
// through /proc/<pid>/stat (processGone), neither of which exists on Windows. The Windows build only has to compile
// process_windows.go, which this file does not touch.

package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestStopLeavesRunningJobTerminal covers shutdown while an optimisation is running. Cancelling the process context
// (as a signal does) and calling Stop must kill the whole process group, persist a terminal row with finished_at, and
// leave nothing for the next start's RecoverStale to reclaim.
//
// The terminal write happens after the context was cancelled, so the failed row also proves that execute persists it
// with a context that is not cancelled; without that, FinishFailed would fail with "context canceled" and the row
// would stay running.
func TestStopLeavesRunningJobTerminal(t *testing.T) {
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	st := NewStore(db)

	// The shell spawns a background child in the same process group and waits for it. The child pid goes to a file,
	// so "the process group is gone" can be checked independently of the job row.
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	script := fmt.Sprintf("sleep 30 & echo $! > %s; wait", pidFile)
	runner, err := New(Deps{
		DB:      db,
		Store:   st,
		Timeout: 30 * time.Second,
		Command: func(context.Context, *store.Job, Reporter) (Command, error) {
			return Command{Name: "/bin/sh", Args: []string{"-c", script}}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// This is the process context a SIGTERM cancels; Stop must turn it into a finished row, not an abandoned one.
	procCtx, cancelProcess := context.WithCancel(context.Background())
	defer cancelProcess()
	runner.Start(procCtx)

	job, err := runner.Enqueue(context.Background(), KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	waitForStatus(t, st, job.ID, StatusRunning, 5*time.Second)
	childPID := waitForPIDFile(t, pidFile, 5*time.Second)

	cancelProcess()
	stopped := make(chan struct{})
	go func() {
		runner.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return within 10s while a job was running")
	}
	// Stop is idempotent: a second call must return too.
	runner.Stop()

	terminal, err := st.ByID(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if terminal.Status != StatusFailed {
		t.Fatalf("job status after shutdown = %q, want %q (a cancelled shutdown must not leave it running)", terminal.Status, StatusFailed)
	}
	if terminal.FinishedAt == nil {
		t.Fatalf("failed job missing finished_at; the terminal write did not survive the cancelled context")
	}
	if terminal.Error == nil || strings.TrimSpace(*terminal.Error) == "" {
		t.Fatalf("failed job error = %v, want a non-empty reason", terminal.Error)
	}
	if !processGone(childPID, 2*time.Second) {
		t.Fatalf("subprocess %d is still alive after Stop: the process group was not killed", childPID)
	}

	// The next process must have nothing to reclaim: the row was already written terminal by this process.
	fresh, err := New(Deps{
		DB:      db,
		Store:   st,
		Timeout: time.Second,
		Command: func(context.Context, *store.Job, Reporter) (Command, error) {
			return Command{Name: "/bin/true"}, nil
		},
	})
	if err != nil {
		t.Fatalf("New (next start): %v", err)
	}
	recovered, err := fresh.RecoverStale(context.Background())
	if err != nil {
		t.Fatalf("RecoverStale: %v", err)
	}
	if recovered != 0 {
		t.Fatalf("next start reclaimed %d rows, want 0", recovered)
	}
	t.Logf("job %d status=%s finished=%v error=%q child_pid=%d gone=%v reclaimed=%d",
		terminal.ID, terminal.Status, terminal.FinishedAt != nil, *terminal.Error, childPID, true, recovered)
}

// TestStopWithoutStartIsSafe covers the no-worker case: Stop must be a no-op when Start was never called.
func TestStopWithoutStartIsSafe(t *testing.T) {
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	runner, err := New(Deps{DB: db, Store: NewStore(db)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runner.Stop()
	runner.Stop()
}

// waitForPIDFile polls until the command has written its background child pid to path.
func waitForPIDFile(t *testing.T, path string, limit time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(limit)
	var lastContent string
	var lastErr error
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err != nil {
			lastErr = err
			time.Sleep(5 * time.Millisecond)
			continue
		}
		lastContent = strings.TrimSpace(string(b))
		if pid, perr := strconv.Atoi(lastContent); perr == nil && pid > 0 {
			return pid
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("pid file %s did not contain a pid within %s (content %q, read error %v)", path, limit, lastContent, lastErr)
	return 0
}
