//go:build unix

// This whole file only compiles on Unix: the fake command the tests inject is `/bin/sh -c ...`, and checking whether a process really
// exited looks at the zombie state in /proc/<pid>/stat (processGone), neither of which exists on Windows.
// The Windows tree-kill implementation lives in process_windows.go and relies on taskkill, which cannot run on Linux CI; only its
// pid <= 0 guard is covered cross-platform by process_test.go.

package jobs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// newTestRunner opens a temporary SQLite database, migrates the tables, and builds and starts a Runner; ctx is cancelled during cleanup.
// timeout is the subprocess timeout; builder injects the command to execute, so the tests do not depend on a real optimizer.
func newTestRunner(t *testing.T, timeout time.Duration, builder CommandBuilder) (*Runner, *Store, *gorm.DB) {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "jobs.db"))
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
		Timeout: timeout,
		Command: builder,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runner.Start(ctx)
	return runner, st, db
}

// waitForStatus polls until the job reaches the want status (or fails on timeout). The polling interval is very short, so it does not stretch the test.
func waitForStatus(t *testing.T, st *Store, id uint64, want string, limit time.Duration) *store.Job {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		job, err := st.ByID(context.Background(), id)
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if job.Status == want {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	job, _ := st.ByID(context.Background(), id)
	t.Fatalf("job %d did not reach %q within %s (last status %q)", id, want, limit, job.Status)
	return nil
}

// TestEnqueueRequiresKind covers enqueue argument validation.
func TestEnqueueRequiresKind(t *testing.T) {
	runner, _, _ := newTestRunner(t, time.Second, func(context.Context, *store.Job, Reporter) (Command, error) {
		return Command{Name: "/bin/true"}, nil
	})
	if _, err := runner.Enqueue(context.Background(), "  ", nil); !errors.Is(err, ErrKindRequired) {
		t.Fatalf("Enqueue(empty kind) error = %v, want ErrKindRequired", err)
	}
}

// TestOptimiseSingleFlightReturnsConflict is the "single concurrency" case (the first acceptance item):
// while a job is running, a second enqueue should get a sentinel error recognizable as 409.
//
// A real subprocess blocked on stdin (`sh -c 'cat'`) keeps the first job in running, so
// the second Enqueue necessarily hits the single-concurrency gate. stdin comes from a pipe the test holds: closing the write end makes cat exit,
// so there is no fixed wait such as a sleep.
func TestOptimiseSingleFlightReturnsConflict(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer pr.Close()

	runner, st, _ := newTestRunner(t, 10*time.Second, func(context.Context, *store.Job, Reporter) (Command, error) {
		return Command{Name: "/bin/sh", Args: []string{"-c", "cat"}, Stdin: pr}, nil
	})

	first, err := runner.Enqueue(context.Background(), KindOptimize, nil)
	if err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	// Wait for the first job to enter running, to be sure it is really executing and not still sitting in the queue.
	waitForStatus(t, st, first.ID, StatusRunning, 3*time.Second)

	_, err = runner.Enqueue(context.Background(), KindOptimize, nil)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Enqueue error = %v, want ErrAlreadyRunning", err)
	}

	// REST layer mapping: the sentinel error must map to 409 (HTTPStatus is reused when wiring).
	rec := httptest.NewRecorder()
	http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "job conflict", HTTPStatus(err))
	}).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/presets/1/optimize", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("HTTPStatus mapped to %d, want 409", rec.Code)
	}
	t.Logf("second Enqueue -> %v ; HTTPStatus -> %d", err, rec.Code)

	// Release stdin so the first job finishes normally, verifying the state machine and stages of the success path.
	if err := pw.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	done := waitForStatus(t, st, first.ID, StatusSucceeded, 3*time.Second)
	if done.StartedAt == nil || done.FinishedAt == nil {
		t.Fatalf("succeeded job missing started_at/finished_at: %+v", done)
	}
	if done.Stage == nil || *done.Stage != StageWriting {
		t.Fatalf("succeeded job stage = %v, want %q", done.Stage, StageWriting)
	}
	if done.Error != nil {
		t.Fatalf("succeeded job error = %v, want nil", *done.Error)
	}
	t.Logf("job %d status=%s stage=%v started=%v finished=%v", done.ID, done.Status, deref(done.Stage), done.StartedAt != nil, done.FinishedAt != nil)
}

// TestOptimiseTimeoutMarksJobFailed is the "timeout" case (the first half of the second acceptance item):
// a stuck adapter is cancelled after the configured timeout, the job is marked failed with a reason stating the timeout. The command itself sleeps 30s,
// yet the test gets failed within 5s, which shows the timeout mechanism ended the job rather than the command exiting on its own.
// "Kill the processes it spawned as well" is covered separately by the next test, TestOptimiseTimeoutKillsProcessGroup.
func TestOptimiseTimeoutMarksJobFailed(t *testing.T) {
	const timeout = 200 * time.Millisecond
	runner, st, _ := newTestRunner(t, timeout, func(context.Context, *store.Job, Reporter) (Command, error) {
		return Command{Name: "/bin/sh", Args: []string{"-c", "sleep 30"}}, nil
	})

	start := time.Now()
	job, err := runner.Enqueue(context.Background(), KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	failed := waitForStatus(t, st, job.ID, StatusFailed, 5*time.Second)
	elapsed := time.Since(start)

	if elapsed >= 5*time.Second {
		t.Fatalf("job took %s; the timeout did not fire (sleep 30 should have been killed early)", elapsed)
	}
	if failed.Error == nil || !strings.Contains(*failed.Error, ErrTimedOut.Error()) {
		t.Fatalf("failed job error = %v, want it to mention %q", failed.Error, ErrTimedOut.Error())
	}
	if failed.FinishedAt == nil {
		t.Fatalf("failed job missing finished_at")
	}
	t.Logf("job %d status=%s elapsed=%s error=%q", failed.ID, failed.Status, elapsed.Round(time.Millisecond), *failed.Error)
}

// TestOptimiseTimeoutKillsProcessGroup is the "kill the process" case (the second half of the second acceptance item):
// a timeout must kill the whole process group, not just the direct child.
//
// The command is `sh -c 'sleep 30 & echo child=$!; cat'`: the shell and the background sleep share a process group,
// and cat blocks on stdin to simulate a hang. The timeout is set to 200ms and the total test time is far less than sleep's 30s,
// which shows the timeout mechanism really killed the process group rather than waiting for the command to end by itself.
func TestOptimiseTimeoutKillsProcessGroup(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer pr.Close()
	defer pw.Close()

	const timeout = 200 * time.Millisecond
	runner, st, _ := newTestRunner(t, timeout, func(context.Context, *store.Job, Reporter) (Command, error) {
		return Command{
			Name:  "/bin/sh",
			Args:  []string{"-c", "sleep 30 & echo child=$!; cat"},
			Stdin: pr,
		}, nil
	})

	start := time.Now()
	job, err := runner.Enqueue(context.Background(), KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	failed := waitForStatus(t, st, job.ID, StatusFailed, 5*time.Second)
	elapsed := time.Since(start)

	if elapsed >= 5*time.Second {
		t.Fatalf("job took %s; the timeout did not fire (sleep 30 should have been killed early)", elapsed)
	}
	if failed.Error == nil || !strings.Contains(*failed.Error, ErrTimedOut.Error()) {
		t.Fatalf("failed job error = %v, want it to mention %q", failed.Error, ErrTimedOut.Error())
	}
	if failed.FinishedAt == nil {
		t.Fatalf("failed job missing finished_at")
	}
	if failed.LogTail == nil || *failed.LogTail == "" {
		t.Fatalf("failed job did not capture a log tail")
	}

	// Pull the PID of the background child out of the log tail and verify "kill the whole process group": once the shell is SIGKILLed,
	// the sleep it spawned must be gone too (otherwise only the direct child was killed, not the process group).
	m := regexp.MustCompile(`child=(\d+)`).FindStringSubmatch(*failed.LogTail)
	if m == nil {
		t.Fatalf("log tail %q does not contain child=<pid>", *failed.LogTail)
	}
	childPID, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parse child pid %q: %v", m[1], err)
	}
	if !processGone(childPID, 2*time.Second) {
		t.Fatalf("grandchild process %d is still alive: the process group was not killed", childPID)
	}
	t.Logf("job %d status=%s elapsed=%s error=%q log_tail=%q grandchild_pid=%d gone=%v",
		failed.ID, failed.Status, elapsed.Round(time.Millisecond), *failed.Error, *failed.LogTail, childPID, true)
}

// deref dereferences nullable text, returning "<nil>" when nil, so test logs print a readable value.
func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// processGone polls whether a PID has exited.
//
// Why also look at /proc: kill(pid, 0) only returns ESRCH once the process is gone, while a zombie (Z) also
// returns success. In containers PID 1 often does not reap children (act's job container is tail -f /dev/null), and
// killed orphans linger in Z for a long time, so "the process group was killed" would be misjudged as "still alive" in containers.
// A zombie no longer runs and uses no CPU, so it counts as exited too.
func processGone(pid int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if processDead(pid) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// processDead reports whether a PID is dead: kill(pid, 0) returns ESRCH, or the process is already a zombie (Z).
func processDead(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	return procState(pid) == "Z"
}

// procState reads the state character from /proc/<pid>/stat; returns an empty string when it cannot be read (process gone, or no /proc).
// The second field of stat is "(comm)"; the process name may contain spaces or parentheses, so take the fields after the last ')'.
func procState(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	s := string(b)
	i := strings.LastIndex(s, ")")
	if i < 0 {
		return ""
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// TestTailBufferAndLines covers byte truncation and line truncation of the log tail.
func TestTailBufferAndLines(t *testing.T) {
	tb := newTailBuffer(8)
	if _, err := tb.Write([]byte("0123456789abcdef")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := tb.String()
	if len(got) > 8 {
		t.Fatalf("tail size = %d, want <= 8", len(got))
	}
	// Keep only the trailing bytes: should be "89abcdef".
	if got[len(got)-8:] != "89abcdef" {
		t.Fatalf("tail = %q, want suffix %q", got, "89abcdef")
	}

	var b []byte
	for i := 1; i <= 5; i++ {
		b = append(b, []byte(fmt.Sprintf("line%d\n", i))...)
	}
	if got := tailLines(string(b), 2); got != "line4\nline5" {
		t.Fatalf("tailLines(...,2) = %q, want %q", got, "line4\nline5")
	}
	if got := tailLines("only", 2); got != "only" {
		t.Fatalf("tailLines(short) = %q, want %q", got, "only")
	}
}

// TestRecoverStaleJobsAtStartup is acceptance: with a running job row left over from the previous process pre-seeded,
// after building and starting the Runner it must become failed, with the reason recorded as StaleJobReason and the log tail kept,
// and subsequent new enqueues must no longer be blocked by a permanent 409.
func TestRecoverStaleJobsAtStartup(t *testing.T) {
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	st := NewStore(db)
	ctx := context.Background()

	// Pre-seed the leftover job: queued -> running, with a log tail attached (simulating training in progress before the crash).
	stale, err := st.CreateQueued(ctx, KindOptimize, nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateQueued: %v", err)
	}
	if err := st.MarkRunning(ctx, stale.ID, time.Now().UTC()); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	const tail = "training started\nepoch 1\n"
	if err := st.update(ctx, stale.ID, map[string]any{"log_tail": tail}); err != nil {
		t.Fatalf("seed log tail: %v", err)
	}

	runner, err := New(Deps{
		DB:      db,
		Store:   st,
		Timeout: 5 * time.Second,
		Command: func(context.Context, *store.Job, Reporter) (Command, error) {
			return Command{Name: "/bin/true"}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Before recovery: the leftover running row makes enqueue hit the single-concurrency gate (exactly the symptom to fix).
	if _, err := runner.Enqueue(ctx, KindOptimize, nil); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Enqueue before recovery error = %v, want ErrAlreadyRunning", err)
	}

	// Build and start the Runner: the startup hook explicitly reclaims the leftover running job.
	startCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner.Start(startCtx)

	recovered, err := st.ByID(ctx, stale.ID)
	if err != nil {
		t.Fatalf("ByID(stale): %v", err)
	}
	if recovered.Status != StatusFailed {
		t.Fatalf("stale job status = %q, want %q", recovered.Status, StatusFailed)
	}
	if recovered.Error == nil || *recovered.Error != StaleJobReason {
		t.Fatalf("stale job error = %v, want %q", recovered.Error, StaleJobReason)
	}
	if recovered.FinishedAt == nil {
		t.Fatalf("stale job missing finished_at")
	}
	if recovered.LogTail == nil || *recovered.LogTail != tail {
		t.Fatalf("stale job log_tail = %v, want preserved %q", recovered.LogTail, tail)
	}
	t.Logf("recovered stale job %d -> status=%s error=%q log_tail=%q",
		recovered.ID, recovered.Status, *recovered.Error, *recovered.LogTail)

	// After recovery: a new enqueue must succeed and no longer be blocked by a permanent 409.
	fresh, err := runner.Enqueue(ctx, KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue after recovery: %v", err)
	}
	done := waitForStatus(t, st, fresh.ID, StatusSucceeded, 3*time.Second)
	t.Logf("fresh enqueue after recovery -> job %d status=%s", done.ID, done.Status)
}

// TestRecoverStaleExplicitEntry covers the explicit entry point: RecoverStale returns the number of reclaimed rows and does not clear the log tail.
func TestRecoverStaleExplicitEntry(t *testing.T) {
	runner, st, _ := newTestRunner(t, time.Second, func(context.Context, *store.Job, Reporter) (Command, error) {
		return Command{Name: "/bin/true"}, nil
	})
	ctx := context.Background()

	job, err := st.CreateQueued(ctx, KindOptimize, nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateQueued: %v", err)
	}
	if err := st.MarkRunning(ctx, job.ID, time.Now().UTC()); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	n, err := runner.RecoverStale(ctx)
	if err != nil {
		t.Fatalf("RecoverStale: %v", err)
	}
	if n != 1 {
		t.Fatalf("RecoverStale returned %d, want 1", n)
	}
	got, err := st.ByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Status != StatusFailed || got.Error == nil || *got.Error != StaleJobReason {
		t.Fatalf("recovered job = status %q error %v, want failed/%q", got.Status, got.Error, StaleJobReason)
	}
}

// TestRecoverQueuedJobsAtStartup is acceptance: with a queued job row pre-seeded, left over from a previous process that crashed
// "after the insert, before startup" (the in-memory queue dies with the process, so it is never executed), after startup recovery it must become
// failed with the reason "job never started", and subsequent new enqueues must no longer be blocked by a permanent 409.
func TestRecoverQueuedJobsAtStartup(t *testing.T) {
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	st := NewStore(db)
	ctx := context.Background()

	// Create only a queued row, without MarkRunning: this is exactly the leftover a crash between "enqueue persisted" and "worker picked it up" leaves.
	stale, err := st.CreateQueued(ctx, KindOptimize, nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateQueued: %v", err)
	}
	if stale.Status != StatusQueued {
		t.Fatalf("seeded job status = %q, want %q", stale.Status, StatusQueued)
	}

	runner, err := New(Deps{
		DB:      db,
		Store:   st,
		Timeout: 5 * time.Second,
		Command: func(context.Context, *store.Job, Reporter) (Command, error) {
			return Command{Name: "/bin/true"}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Before recovery: the leftover queued row makes Enqueue return a permanent 409, exactly the symptom to fix.
	if _, err := runner.Enqueue(ctx, KindOptimize, nil); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Enqueue before recovery error = %v, want ErrAlreadyRunning", err)
	}

	// The explicit recovery entry point must reclaim queued rows too, and return the number of reclaimed rows.
	n, err := runner.RecoverStale(ctx)
	if err != nil {
		t.Fatalf("RecoverStale: %v", err)
	}
	if n != 1 {
		t.Fatalf("RecoverStale returned %d, want 1", n)
	}
	recovered, err := st.ByID(ctx, stale.ID)
	if err != nil {
		t.Fatalf("ByID(stale): %v", err)
	}
	if recovered.Status != StatusFailed {
		t.Fatalf("queued job status = %q, want %q", recovered.Status, StatusFailed)
	}
	if recovered.Error == nil || *recovered.Error != NeverStartedJobReason {
		t.Fatalf("queued job error = %v, want %q", recovered.Error, NeverStartedJobReason)
	}
	if recovered.FinishedAt == nil {
		t.Fatal("queued job missing finished_at")
	}
	if recovered.StartedAt != nil {
		t.Errorf("queued job started_at = %v, want nil (it never started)", recovered.StartedAt)
	}
	t.Logf("recovered queued job %d -> status=%s error=%q finished=%v",
		recovered.ID, recovered.Status, *recovered.Error, recovered.FinishedAt)

	// After recovery: start the worker; a new enqueue must succeed and be executed, no longer blocked by a permanent 409.
	startCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner.Start(startCtx)
	fresh, err := runner.Enqueue(ctx, KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue after recovery: %v", err)
	}
	done := waitForStatus(t, st, fresh.ID, StatusSucceeded, 3*time.Second)
	t.Logf("fresh enqueue after recovery -> job %d status=%s", done.ID, done.Status)
}
