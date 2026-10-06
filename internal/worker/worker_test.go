package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 本文件覆盖共享骨架自身的生命周期契约：启动后可停、tick 会触发、Stop 后不再触发、
// ctx 取消能退出，外加错误日志包装与 New 的参数校验。

// discardHandler 丢弃所有日志，避免骨架用例污染测试输出。
type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (discardHandler) WithAttrs([]slog.Attr) slog.Handler        { return discardHandler{} }
func (discardHandler) WithGroup(string) slog.Handler             { return discardHandler{} }

// countHandler 记录日志消息并计数，供断言错误日志文案。
type countHandler struct {
	mu   sync.Mutex
	msgs map[string]int
}

func (h *countHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *countHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.msgs == nil {
		h.msgs = make(map[string]int)
	}
	h.msgs[r.Message]++
	return nil
}

func (h *countHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *countHandler) WithGroup(string) slog.Handler      { return h }

func (h *countHandler) count(msg string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.msgs[msg]
}

// waitFor 轮询等到 cond 为真，或超时判失败。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// counter 是记录调用次数的 RunOnce 替身。
type counter struct{ n atomic.Int64 }

func (c *counter) run(context.Context) error {
	c.n.Add(1)
	return nil
}

// TestLifecycleRunsAndStops 覆盖：启动即跑一轮、tick 会继续触发、重复 Start 幂等、
// Stop 后不再触发。
func TestLifecycleRunsAndStops(t *testing.T) {
	var c counter
	l, err := New(Config{
		RunOnce:     c.run,
		Logger:      slog.New(discardHandler{}),
		FailMessage: "test run failed",
		Interval:    5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	l.Start(context.Background())
	l.Start(context.Background()) // 重复 Start 必须幂等，否则 Stop 只能停掉其中一个 goroutine
	waitFor(t, "the startup run", func() bool { return c.n.Load() >= 1 })
	waitFor(t, "a ticker run", func() bool { return c.n.Load() >= 2 })

	l.Stop()
	stopped := c.n.Load()
	time.Sleep(30 * time.Millisecond)
	if got := c.n.Load(); got != stopped {
		t.Fatalf("RunOnce called %d times after Stop, want no further calls (stuck at %d)", got, stopped)
	}
}

// TestLifecycleContextCancel 覆盖：ctx 取消后 worker 退出，Stop 立即返回（不挂起）。
func TestLifecycleContextCancel(t *testing.T) {
	var c counter
	l, err := New(Config{
		RunOnce:     c.run,
		Logger:      slog.New(discardHandler{}),
		FailMessage: "test run failed",
		Interval:    5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	l.Start(ctx)
	waitFor(t, "the startup run", func() bool { return c.n.Load() >= 1 })

	cancel()
	done := make(chan struct{})
	go func() {
		l.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after the context was cancelled")
	}
}

// TestStopBeforeStart 覆盖：未启动就 Stop 是 no-op，不 panic、不触发 RunOnce、不挂起。
func TestStopBeforeStart(t *testing.T) {
	var c counter
	l, err := New(Config{
		RunOnce:     c.run,
		Logger:      slog.New(discardHandler{}),
		FailMessage: "test run failed",
		Interval:    time.Hour,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	l.Stop()
	l.Stop() // 重复 Stop 同样应为 no-op
	if c.n.Load() != 0 {
		t.Fatalf("Stop before Start ran RunOnce %d times, want 0", c.n.Load())
	}
}

// TestRunOnceErrorIsLogged 覆盖错误日志包装：单轮失败按配置的文案记录，且 worker 不退出。
func TestRunOnceErrorIsLogged(t *testing.T) {
	var c counter
	logs := &countHandler{}
	l, err := New(Config{
		RunOnce: func(ctx context.Context) error {
			if c.n.Add(1) == 1 {
				return errors.New("boom")
			}
			return nil
		},
		Logger:      slog.New(logs),
		FailMessage: "custom failure message",
		Interval:    5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	l.Start(context.Background())
	defer l.Stop()

	waitFor(t, "the error log record", func() bool { return logs.count("custom failure message") >= 1 })
	waitFor(t, "a later run after the failure", func() bool { return c.n.Load() >= 2 })
}

// TestNewValidatesConfig 覆盖构造参数校验：三者缺一不可。
func TestNewValidatesConfig(t *testing.T) {
	okRun := func(context.Context) error { return nil }
	okLogger := slog.New(discardHandler{})
	cases := []struct {
		name string
		cfg  Config
	}{
		{"missing RunOnce", Config{Logger: okLogger, FailMessage: "x"}},
		{"missing Logger", Config{RunOnce: okRun, FailMessage: "x"}},
		{"missing FailMessage", Config{RunOnce: okRun, Logger: okLogger}},
	}
	for _, tc := range cases {
		if _, err := New(tc.cfg); err == nil {
			t.Errorf("New(%s) = nil error, want an error", tc.name)
		}
	}
}
