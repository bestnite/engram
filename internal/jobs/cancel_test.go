package jobs

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"example.com/flashcard/internal/store"
)

// fileSize 返回文件当前字节数；不存在时返回 0。
func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// waitFileNonEmpty 轮询到文件出现且非空，证明子进程确实在产出（而不是只改了状态字段）。
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

// TestCancelRunningJobKillsProcessAndUnblocksQueue 是 M6-6 的核心验收：
// 用一个真实的长时间子进程让作业停在 running，然后取消它，必须做到
//  1. Cancel 立即返回（不阻塞）；
//  2. 作业落库为 failed 且 error = CancelReason；
//  3. 子进程确实被杀（marker 文件不再增长，证明整组进程消失而非只改状态）；
//  4. 取消后新作业能成功入队并跑完（单并发门已释放）。
func TestCancelRunningJobKillsProcessAndUnblocksQueue(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ticks")

	// 第一次调用返回一个不断写 marker 的真实长命令；之后返回秒退命令，用于验证新入队。
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

	// 子进程必须真的消失：marker 在取消后不再增长。
	sizeAfterCancel := fileSize(marker)
	time.Sleep(400 * time.Millisecond)
	if grew := fileSize(marker); grew != sizeAfterCancel {
		t.Errorf("subprocess kept running after cancel: marker grew %d -> %d", sizeAfterCancel, grew)
	}

	// 取消后单并发门已释放：新作业能入队并成功完成。
	second, err := runner.Enqueue(context.Background(), KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue after cancel: %v (single-flight gate not released)", err)
	}
	done := waitForStatus(t, st, second.ID, StatusSucceeded, 5*time.Second)
	if done.Status != StatusSucceeded {
		t.Errorf("second job status = %q, want %q", done.Status, StatusSucceeded)
	}
}
