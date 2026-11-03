// Package worker provides the shared lifecycle skeleton for background periodic workers: Start/Stop, a tick loop
// that runs one round immediately on start, a wrapper that records a single round's error as an English log line,
// the in-memory state of the latest round, a manual trigger, and the registry the admin panel reads.
//
// This package converges only that verbatim-identical skeleton layer; the business phase (what to scan, what to send) is injected by the caller via
// Config.RunOnce, so the package imports no business package and holds no business dependency such as a DB or a mailer.
// Persisting a round is likewise injected: the Registry is built with a record function, which cmd/engram wires to the jobs table.
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

// Periodic task kinds. They are stable English identifiers: the jobs table stores them in jobs.kind and the admin
// API exposes them, so a rename would orphan the recorded history.
const (
	// KindRetention deletes expired sessions, action tokens, invites, deck share invites, stale login fingerprints
	// and old periodic history rows.
	KindRetention = "retention"
	// KindMediaGC reclaims media no note references once its grace period has passed.
	KindMediaGC = "media_gc"
	// KindReminder sends the review due reminder.
	KindReminder = "reminder"
	// KindDigest sends the weekly study digest.
	KindDigest = "digest"
	// KindMailDelivery delivers the mail outbox.
	KindMailDelivery = "mail_delivery"
)

// Kinds lists every periodic task kind in the order the admin panel shows them.
var Kinds = []string{KindRetention, KindMediaGC, KindReminder, KindDigest, KindMailDelivery}

// IsKind reports whether kind names a periodic task.
func IsKind(kind string) bool {
	for _, k := range Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Triggers: what started a round.
const (
	// TriggerAuto is a round started by the timer or by a Wake signal.
	TriggerAuto = "auto"
	// TriggerManual is a round an administrator started with RunNow.
	TriggerManual = "manual"
)

var (
	// ErrBusy means a round is running or a manual round is already pending, so RunNow queued nothing.
	ErrBusy = errors.New("worker: a round is already running or pending")
	// ErrNotStarted means the worker has not been started (or has been stopped), so no loop would pick the request up.
	ErrNotStarted = errors.New("worker: not started")
)

// Summary counts the actions one round performed, keyed by a stable English name (rows deleted, mails queued, mails
// sent). A value never counts rows that were only scanned: "the round did work" is defined as some value above zero,
// and the history depends on that definition.
type Summary map[string]int64

// DidWork reports whether any action count is above zero.
func (s Summary) DidWork() bool {
	for _, v := range s {
		if v > 0 {
			return true
		}
	}
	return false
}

// Round is one finished round.
type Round struct {
	Kind       string
	Trigger    string
	StartedAt  time.Time
	FinishedAt time.Time
	Summary    Summary
	Err        error
}

// Recorded reports whether the round belongs in the persisted history: a round that did work, failed, or was
// started by hand. A periodic worker ticks every few minutes and most rounds find nothing to do; recording those
// would bury the rounds that matter, and the latest round of each task is visible in Status anyway.
func (r Round) Recorded() bool {
	return r.Trigger == TriggerManual || r.Err != nil || r.Summary.DidWork()
}

// Status is a snapshot of a lifecycle's in-memory state.
type Status struct {
	Kind     string
	Interval time.Duration
	// Running is true while a round executes; Pending is true while a manual round waits to start.
	Running bool
	Pending bool
	// NextRunAt is when the next timer round is due; zero when the worker is not started.
	NextRunAt time.Time
	// Last is the latest finished round since the process started, nil before the first one finishes.
	Last *Round
}

// RecordFunc persists a round that Round.Recorded selects. It must not fail the round: an implementation logs its
// own write errors.
type RecordFunc func(ctx context.Context, round Round)

// Config holds Lifecycle's constructor arguments.
type Config struct {
	// Kind names the task, one of Kinds. Required; New returns an error when it is empty.
	Kind string
	// RunOnce is one round of business work, provided by the caller. It returns what the round did (see Summary)
	// and a round-level error. Required; New returns an error when it is nil.
	RunOnce func(ctx context.Context) (Summary, error)
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
	kind        string
	runOnce     func(ctx context.Context) (Summary, error)
	logger      *slog.Logger
	failMessage string
	interval    time.Duration
	wake        <-chan struct{}
	// manual carries RunNow requests; its capacity of one together with pending keeps at most one request queued.
	manual chan struct{}

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
	running bool
	pending bool
	nextRun time.Time
	last    *Round
	record  RecordFunc
}

// New constructs a Lifecycle; it does not start the worker, call Start afterwards.
func New(cfg Config) (*Lifecycle, error) {
	if cfg.Kind == "" {
		return nil, errors.New("worker: Config.Kind is required")
	}
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
		kind:        cfg.Kind,
		runOnce:     cfg.RunOnce,
		logger:      cfg.Logger,
		failMessage: cfg.FailMessage,
		interval:    interval,
		wake:        cfg.Wake,
		manual:      make(chan struct{}, 1),
	}, nil
}

// Kind returns the task kind the lifecycle was built with.
func (l *Lifecycle) Kind() string { return l.kind }

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
	// A request the stopped loop never picked up must not run on a later Start as if it had just been made.
	l.mu.Lock()
	l.pending = false
	l.nextRun = time.Time{}
	select {
	case <-l.manual:
	default:
	}
	l.mu.Unlock()
}

// RunNow asks the loop to run one round with TriggerManual and returns without waiting for it. The round applies
// exactly the rules of a timer round. It returns ErrBusy when a round is running or a manual round is already
// pending, and ErrNotStarted when the loop is not running.
func (l *Lifecycle) RunNow() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.started {
		return ErrNotStarted
	}
	if l.running || l.pending {
		return ErrBusy
	}
	l.pending = true
	// pending was false, so the buffered channel is empty and this send cannot block.
	l.manual <- struct{}{}
	return nil
}

// Status returns a snapshot of the lifecycle's in-memory state.
func (l *Lifecycle) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := Status{
		Kind:      l.kind,
		Interval:  l.interval,
		Running:   l.running,
		Pending:   l.pending,
		NextRunAt: l.nextRun,
	}
	if l.last != nil {
		last := *l.last
		st.Last = &last
	}
	return st
}

// setRecorder installs the record function; the Registry calls it when the lifecycle is added.
func (l *Lifecycle) setRecorder(fn RecordFunc) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.record = fn
}

// loop is the worker's main loop: run one round immediately on start (catching up the window missed while the process was down), then poll on a timer.
func (l *Lifecycle) loop(ctx context.Context) {
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()
	l.scheduleNext()
	l.runAndLog(ctx, TriggerAuto)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.scheduleNext()
			l.runAndLog(ctx, TriggerAuto)
		case <-l.wake:
			// A nil wake channel never receives, so a worker without one only ticks.
			l.runAndLog(ctx, TriggerAuto)
		case <-l.manual:
			l.runAndLog(ctx, TriggerManual)
		}
	}
}

// scheduleNext records when the timer fires next, for Status.
func (l *Lifecycle) scheduleNext() {
	l.mu.Lock()
	l.nextRun = time.Now().UTC().Add(l.interval)
	l.mu.Unlock()
}

// runAndLog runs one round, records query-level errors as an English log line (the worker must not exit because one
// round failed), keeps the round as the latest one, and hands it to the record function when it belongs in history.
func (l *Lifecycle) runAndLog(ctx context.Context, trigger string) {
	l.mu.Lock()
	l.running = true
	if trigger == TriggerManual {
		l.pending = false
	}
	l.mu.Unlock()

	started := time.Now().UTC()
	summary, err := l.runOnce(ctx)
	round := Round{
		Kind:       l.kind,
		Trigger:    trigger,
		StartedAt:  started,
		FinishedAt: time.Now().UTC(),
		Summary:    summary,
		Err:        err,
	}
	if summary == nil {
		round.Summary = Summary{}
	}
	if err != nil {
		l.logger.Error(l.failMessage, "error", err)
	}

	l.mu.Lock()
	l.running = false
	l.last = &round
	record := l.record
	l.mu.Unlock()

	// A round cut short by shutdown failed because the process is stopping, not because of the task; recording it
	// would leave a failure in the history on every restart.
	if record == nil || ctx.Err() != nil || !round.Recorded() {
		return
	}
	record(ctx, round)
}
