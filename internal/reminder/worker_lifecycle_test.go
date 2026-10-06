package reminder

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/i18n"
)

// 本文件钉住后台 worker 的**生命周期契约**：启动即跑一轮、tick 会继续触发、Stop 后不再
// 触发、ctx 取消后能退出。它先于 internal/worker 骨架抽取落笔，抽完必须仍然原样绿。
//
// 观测手段：走「SMTP 未配置」路径把每一轮压缩成一条 Info 日志——不查库、不发信，
// 日志条数即 RunOnce 的调用次数，正好用来数 tick。

// reminderSkippedLog 是 SMTP 未配置时每轮留下的英文日志（与生产代码逐字一致）。
const reminderSkippedLog = "review reminder skipped: smtp not configured"

// recordingHandler 捕获日志消息并计数，供生命周期用例数 RunOnce 的调用次数。
type recordingHandler struct {
	mu   sync.Mutex
	msgs map[string]int
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.msgs == nil {
		h.msgs = make(map[string]int)
	}
	h.msgs[r.Message]++
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordingHandler) count(msg string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.msgs[msg]
}

// waitForCount 轮询等到某条日志至少出现 want 次，或超时判失败。
func waitForCount(t *testing.T, h *recordingHandler, msg string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.count(msg) >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q x%d, got x%d", msg, want, h.count(msg))
}

// TestWorkerLifecycle 验证：Start 后启动即跑一轮，随后 tick 继续触发，Stop 后不再触发。
func TestWorkerLifecycle(t *testing.T) {
	db := newTestDB(t)
	logs := &recordingHandler{}
	translator, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	// SMTP 未配置：每轮只记一条 Info，不查库不发信，适合毫秒级轮询。
	r, err := New(Deps{
		DB: db, Outbox: &fakeEnqueuer{configured: false}, Translator: translator,
		Logger: slog.New(logs), Interval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("reminder.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)

	waitForCount(t, logs, reminderSkippedLog, 1) // 启动即跑一轮
	waitForCount(t, logs, reminderSkippedLog, 2) // tick 会继续触发

	r.Stop()
	stopped := logs.count(reminderSkippedLog)
	time.Sleep(30 * time.Millisecond)
	if got := logs.count(reminderSkippedLog); got != stopped {
		t.Fatalf("ran %d times after Stop, want no further runs (stuck at %d)", got, stopped)
	}
}

// TestWorkerLifecycleContextCancel 验证：父上下文取消后 worker 退出，Stop 立即返回（不挂起）。
func TestWorkerLifecycleContextCancel(t *testing.T) {
	db := newTestDB(t)
	logs := &recordingHandler{}
	translator, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	r, err := New(Deps{
		DB: db, Outbox: &fakeEnqueuer{configured: false}, Translator: translator,
		Logger: slog.New(logs), Interval: time.Hour,
	})
	if err != nil {
		t.Fatalf("reminder.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	waitForCount(t, logs, reminderSkippedLog, 1)

	cancel() // 父上下文取消：worker 应自行退出
	done := make(chan struct{})
	go func() {
		r.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after the context was cancelled")
	}
}
