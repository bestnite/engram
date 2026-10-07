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

// 本文件是成功钩子的作业侧验收：作业成功落库后 OnSuccess 被调用一次，且钩子 panic 不会
// 反过来影响作业结果——「通知失败不影响触发操作」在作业侧的体现（与 onfailure_test.go 对称）。

// succeedingRunner 构造一个不自动 Start 的 Runner，方便在 Start 之前接好钩子。
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

// TestSuccessHookInvokedAfterJobSucceeds 断言成功作业会调用 OnSuccess 钩子并带上作业行。
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

// TestSuccessHookPanicDoesNotBreakCompletion 断言钩子 panic 被兜住：作业仍落到 succeeded。
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
