//go:build unix

// 只在 Unix 上编译：假命令走 `/bin/sh -c ...`（理由同 jobs_test.go 的文件头说明）。

package jobs

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是作业侧验收：作业失败落库后，OnFailure 钩子被调用一次，
// 且钩子 panic 不会反过来影响作业失败处理（「发信失败不影响触发操作」在作业侧的体现）。

// failingRunner 构造一个不自动 Start 的 Runner，方便在 Start 之前接好钩子。
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

// TestFailureHookInvokedAfterJobFails 断言失败作业会调用 OnFailure 钩子，并带上失败原因。
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

// TestFailureHookPanicDoesNotBreakFailureHandling 断言钩子 panic 被兜住：作业仍落到 failed，
// worker 不崩，后续作业仍能执行。
func TestFailureHookPanicDoesNotBreakFailureHandling(t *testing.T) {
	runner, st, _ := failingRunner(t, func(context.Context, store.Job, string) {
		panic("notification exploded")
	})

	job, err := runner.Enqueue(context.Background(), KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	waitForStatus(t, st, job.ID, StatusFailed, 5*time.Second)

	// worker 仍然存活：第二个作业同样能入队并失败。
	second, err := runner.Enqueue(context.Background(), KindOptimize, nil)
	if err != nil {
		t.Fatalf("second Enqueue after hook panic: %v", err)
	}
	waitForStatus(t, st, second.ID, StatusFailed, 5*time.Second)
	t.Logf("hook panic contained; jobs %d and %d both reached failed", job.ID, second.ID)
}
