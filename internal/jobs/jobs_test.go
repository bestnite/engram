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

	"example.com/flashcard/internal/store"
)

// newTestRunner 打开临时 SQLite 库、迁移表、构造并启动 Runner；ctx 在清理时取消。
// timeout 是子进程超时；builder 注入要执行的命令，因此测试不依赖真实优化器。
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

// waitForStatus 轮询到作业到达 want 状态（或超时失败）。轮询间隔很短，不拖测试时长。
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

// TestEnqueueRequiresKind 覆盖入队参数校验。
func TestEnqueueRequiresKind(t *testing.T) {
	runner, _, _ := newTestRunner(t, time.Second, func(context.Context, *store.Job, Reporter) (Command, error) {
		return Command{Name: "/bin/true"}, nil
	})
	if _, err := runner.Enqueue(context.Background(), "  ", nil); !errors.Is(err, ErrKindRequired) {
		t.Fatalf("Enqueue(empty kind) error = %v, want ErrKindRequired", err)
	}
}

// TestSingleFlightReturnsConflict 是 M9-1 的第一条验收：作业在跑时第二次入队应得到
// 可识别为 409 的哨兵错误。
//
// 用一个阻塞在 stdin 上的真实子进程（`sh -c 'cat'`）让第一个作业停在 running，
// 此时第二个 Enqueue 必然撞上单并发门。stdin 由测试持有的管道提供：关闭写端 cat 即退出，
// 因此不依赖 sleep 之类的固定等待。
func TestSingleFlightReturnsConflict(t *testing.T) {
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
	// 等第一个作业进入 running，确保它确实在执行而不是还躺在队列里。
	waitForStatus(t, st, first.ID, StatusRunning, 3*time.Second)

	_, err = runner.Enqueue(context.Background(), KindOptimize, nil)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Enqueue error = %v, want ErrAlreadyRunning", err)
	}

	// REST 层映射：哨兵错误必须映射成 409（M9-4 接线时复用 HTTPStatus）。
	rec := httptest.NewRecorder()
	http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "job conflict", HTTPStatus(err))
	}).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/presets/1/optimize", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("HTTPStatus mapped to %d, want 409", rec.Code)
	}
	t.Logf("second Enqueue -> %v ; HTTPStatus -> %d", err, rec.Code)

	// 释放 stdin 让第一个作业正常结束，验证成功路径的状态机与阶段。
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

// TestTimeoutKillsProcessGroupAndFailsJob 是 M9-1 的第二条验收：卡死的适配器在超时后
// 被杀死（连同它派生的子进程），作业标记为 failed。
//
// 命令是 `sh -c 'sleep 30 & echo child=$!; cat'`：shell 与后台 sleep 同属一个进程组，
// cat 阻塞在 stdin 上模拟挂死。超时设为 200ms，测试总耗时远小于 sleep 的 30s，
// 说明确实是超时机制杀掉了进程组，而不是等命令自然结束。
func TestTimeoutKillsProcessGroupAndFailsJob(t *testing.T) {
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

	// 从日志尾巴里取出后台子进程的 PID，验证「杀整个进程组」：shell 被 SIGKILL 后，
	// 它派生的 sleep 也必须消失（否则只是杀了直接子进程，没杀进程组）。
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

// deref 解引用可空文本，nil 时返回 "<nil>"，供测试日志打印可读值。
func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// processGone 轮询判断 PID 是否已消失（kill(pid, 0) 返回 ESRCH）。
func processGone(pid int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestTailBufferAndLines 覆盖日志尾巴的字节截断与行截断。
func TestTailBufferAndLines(t *testing.T) {
	tb := newTailBuffer(8)
	if _, err := tb.Write([]byte("0123456789abcdef")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := tb.String()
	if len(got) > 8 {
		t.Fatalf("tail size = %d, want <= 8", len(got))
	}
	// 只保留末尾字节：应为 "89abcdef"。
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

// TestRecoverStaleJobsAtStartup 是 M9-6 的验收：预置一行上次进程遗留的 running 作业，
// 构造并启动 Runner 后它必须变为 failed、原因记录为 StaleJobReason、日志尾巴保留，
// 且随后新入队不再被永久 409 阻塞。
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

	// 预置残留作业：queued -> running，并带上日志尾巴（模拟崩溃前正在训练）。
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

	// 恢复前：残留的 running 行让入队撞上单并发门（这正是 M9-6 要修的症状）。
	if _, err := runner.Enqueue(ctx, KindOptimize, nil); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Enqueue before recovery error = %v, want ErrAlreadyRunning", err)
	}

	// 构造并启动 Runner：启动钩子显式回收残留 running 作业。
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

	// 恢复后：新入队必须成功，不再被永久 409 阻塞。
	fresh, err := runner.Enqueue(ctx, KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue after recovery: %v", err)
	}
	done := waitForStatus(t, st, fresh.ID, StatusSucceeded, 3*time.Second)
	t.Logf("fresh enqueue after recovery -> job %d status=%s", done.ID, done.Status)
}

// TestRecoverStaleExplicitEntry 覆盖显式入口：RecoverStale 返回回收行数，且不清空日志尾巴。
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

// TestRecoverQueuedJobsAtStartup 是 M9-8 的验收：预置一行上次进程在「insert 之后、启动之前」
// 崩溃遗留的 queued 作业（内存队列随进程消失，它永远不会被执行），启动恢复后它必须变为
// failed、原因是「作业从未启动」，且随后新入队不再被永久 409 阻塞。
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

	// 只建一行 queued，不做 MarkRunning：这正是崩溃在「入队已写库、worker 还没取走」之间留下的残留。
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

	// 恢复前：残留 queued 行让 Enqueue 永久 409，这正是 M9-8 要修的症状。
	if _, err := runner.Enqueue(ctx, KindOptimize, nil); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Enqueue before recovery error = %v, want ErrAlreadyRunning", err)
	}

	// 显式恢复入口必须同时回收 queued 行，并返回回收行数。
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

	// 恢复后：启动 worker，新入队必须成功并被执行，不再被永久 409 阻塞。
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
