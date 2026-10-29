// Package worker provides the shared lifecycle skeleton for background periodic workers: Start/Stop, a tick loop
// that runs one round immediately on start, and a wrapper that records a single round's error as an English log line.
//
// This package converges only that verbatim-identical skeleton layer; the business phase (what to scan, what to send) is injected by the caller via
// Config.RunOnce, so the package imports no business package and holds no business dependency such as a DB or a mailer.
//
// Extraction background (the verbatim diff conclusion of refactor/converge-c4): the four methods Start / Stop / loop / runAndLog of internal/reminder and
// internal/digest are identical verbatim apart from the receiver name and runAndLog's log text;
// their tick interval (each one's DefaultInterval = 10 minutes) and error handling (log and continue, a failed round does not exit)
// are exactly the same too. The only behavioral difference is the failure log text, so it is injected via Config.FailMessage, keeping each original text verbatim.
// The mail outbox runs on the same skeleton; its extra trigger (a wake-up on every enqueue) is Config.Wake.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// DefaultInterval is the default when no polling interval is specified (10 minutes).
//
// The two class-C workers (reminders, weekly digest) share the same rhythm; the rationale for the default lives in the constant comments of internal/reminder and
// internal/digest, centralized here so neither side has to write its own copy.
const DefaultInterval = 10 * time.Minute

// Config holds Lifecycle's constructor arguments.
type Config struct {
	// RunOnce is one round of business scanning, provided by the caller. Required; New returns an error when it is nil.
	RunOnce func(ctx context.Context) error
	// Logger records single-round failures. Required; New returns an error when it is nil.
	Logger *slog.Logger
	// FailMessage is the English log message for a single-round failure, injected verbatim (see the package comment). Required; New returns an error when it is empty.
	FailMessage string
	// Interval is the polling interval; <=0 means DefaultInterval.
	Interval time.Duration
	// Wake optionally runs a round as soon as it receives, between ticks (the mail outbox uses it so an enqueued mail
	// does not wait for the next poll). nil means ticks only.
	Wake <-chan struct{}
}

// Lifecycle is the start/stop and tick skeleton of a periodic worker. The zero value is not usable; construct it with New.
type Lifecycle struct {
	runOnce     func(ctx context.Context) error
	logger      *slog.Logger
	failMessage string
	interval    time.Duration
	wake        <-chan struct{}

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// New constructs a Lifecycle; it does not start the worker, call Start afterwards.
func New(cfg Config) (*Lifecycle, error) {
	if cfg.RunOnce == nil {
		return nil, errors.New("worker: Config.RunOnce is required")
	}
	if cfg.Logger == nil {
		return nil, errors.New("worker: Config.Logger is required")
	}
	if cfg.FailMessage == "" {
		return nil, errors.New("worker: Config.FailMessage is required")
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Lifecycle{
		runOnce:     cfg.RunOnce,
		logger:      cfg.Logger,
		failMessage: cfg.FailMessage,
		interval:    interval,
		wake:        cfg.Wake,
	}, nil
}

// Start starts the single worker goroutine; repeated calls are idempotent. The worker exits when ctx is cancelled.
func (l *Lifecycle) Start(ctx context.Context) {
	l.mu.Lock()
	if l.started {
		l.mu.Unlock()
		return
	}
	l.started = true
	runCtx, cancel := context.WithCancel(ctx)
	l.cancel = cancel
	done := make(chan struct{})
	l.done = done
	l.mu.Unlock()
	go func() {
		defer close(done)
		l.loop(runCtx)
	}()
}

// Stop gracefully stops the worker: it cancels the context and waits for the current round to finish. Safe to call repeatedly.
func (l *Lifecycle) Stop() {
	l.mu.Lock()
	if !l.started {
		l.mu.Unlock()
		return
	}
	l.started = false
	cancel := l.cancel
	done := l.done
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// loop is the worker's main loop: run one round immediately on start (catching up the window missed while the process was down), then poll on a timer.
func (l *Lifecycle) loop(ctx context.Context) {
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()
	l.runAndLog(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.runAndLog(ctx)
		case <-l.wake:
			// A nil wake channel never receives, so a worker without one only ticks.
			l.runAndLog(ctx)
		}
	}
}

// runAndLog runs one round and records query-level errors as an English log line (the worker must not exit because one round failed).
func (l *Lifecycle) runAndLog(ctx context.Context) {
	if err := l.runOnce(ctx); err != nil {
		l.logger.Error(l.failMessage, "error", err)
	}
}
