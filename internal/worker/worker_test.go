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

// This file covers the shared skeleton's own lifecycle contract: it can be stopped once started, ticks fire, they do not fire after Stop,
// ctx cancellation makes it exit, plus the error-log wrapper and New's argument validation.

// discardHandler discards all logs so skeleton cases do not pollute test output.
type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (discardHandler) WithAttrs([]slog.Attr) slog.Handler        { return discardHandler{} }
func (discardHandler) WithGroup(string) slog.Handler             { return discardHandler{} }

// countHandler records log messages and counts them, for asserting the error log text.
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

// waitFor polls until cond is true, or fails on timeout.
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

// counter is a RunOnce stand-in that records the number of calls.
type counter struct{ n atomic.Int64 }

func (c *counter) run(context.Context) error {
	c.n.Add(1)
	return nil
}

// TestLifecycleRunsAndStops covers: one round immediately on start, ticks keep firing, repeated Start is idempotent,
// and nothing fires after Stop.
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
	l.Start(context.Background()) // Repeated Start must be idempotent, otherwise Stop can only stop one of the goroutines
	waitFor(t, "the startup run", func() bool { return c.n.Load() >= 1 })
	waitFor(t, "a ticker run", func() bool { return c.n.Load() >= 2 })

	l.Stop()
	stopped := c.n.Load()
	time.Sleep(30 * time.Millisecond)
	if got := c.n.Load(); got != stopped {
		t.Fatalf("RunOnce called %d times after Stop, want no further calls (stuck at %d)", got, stopped)
	}
}

// TestLifecycleWakeRunsBetweenTicks: with a long interval, a signal on Wake still runs a round right away, while a worker
// built without Wake only runs its initial round.
func TestLifecycleWakeRunsBetweenTicks(t *testing.T) {
	cases := []struct {
		name     string
		withWake bool
		want     int64
	}{
		{name: "wake runs a round", withWake: true, want: 2},
		{name: "no wake channel, ticks only", withWake: false, want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c counter
			wake := make(chan struct{}, 1)
			cfg := Config{
				RunOnce:     c.run,
				Logger:      slog.New(discardHandler{}),
				FailMessage: "test run failed",
				Interval:    time.Hour,
			}
			if tc.withWake {
				cfg.Wake = wake
			}
			l, err := New(cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			l.Start(context.Background())
			defer l.Stop()
			waitFor(t, "the startup run", func() bool { return c.n.Load() >= 1 })
			wake <- struct{}{}
			time.Sleep(30 * time.Millisecond)
			if got := c.n.Load(); got != tc.want {
				t.Errorf("rounds = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestLifecycleContextCancel covers: the worker exits after ctx cancellation and Stop returns immediately (without hanging).
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

// TestStopBeforeStart covers: Stop before start is a no-op — no panic, no RunOnce, no hang.
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
	l.Stop() // Repeated Stop should likewise be a no-op
	if c.n.Load() != 0 {
		t.Fatalf("Stop before Start ran RunOnce %d times, want 0", c.n.Load())
	}
}

// TestRunOnceErrorIsLogged covers the error-log wrapper: a failed round is recorded with the configured text, and the worker does not exit.
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

// TestNewValidatesConfig covers constructor argument validation: all three are mandatory.
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
