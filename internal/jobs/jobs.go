// Package jobs provides a single-concurrency background job framework: jobs table persistence, subprocess execution (with an injectable command),
// killing the whole process group after a timeout, stage-level progress, and log tail capture.
//
// Design points and rationale:
//   - Single concurrency: only one worker inside the process consumes the queue; on enqueue, if an unfinished job already exists it returns
//     ErrAlreadyRunning, which the REST layer maps to 409 ("409 if a task already exists").
//   - Subprocess execution: training is CPU-intensive and may crash or hang, so running it in a separate process both isolates it and allows timeout cancellation;
//     the optimizer algorithm itself is not a Go library (it is a Rust adapter), so it can only go through a subprocess.
//   - Injectable command: the Runner gets the command to execute from a CommandBuilder; production uses the optimizer
//     adapter (internal/jobs/optimizer.go, which returns a Command that invokes the Rust adapter directly), while tests inject
//     `/bin/sh -c ...`, so they do not depend on a real optimizer.
//   - Injectable result: Complete is optional; after the command exits successfully, Complete parses the command's side effects (for example the
//     21-dimension weight file the adapter writes) and produces an OptimizeResult for FinishOptimize to persist; when not configured it takes the
//     generic "exit code 0 means success" path.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// Job kinds: consistent with the jobs.kind column, English values that stay stable.
const (
	// KindOptimize is the parameter optimization job (currently the only kind).
	KindOptimize = "optimize"
)

// Job state machine: queued -> running -> succeeded | failed.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// Stage names: only stage-level progress, no invented percentages. Values are consistent with the jobs.stage column.
const (
	StageReadLogs = "read_logs"
	StageTraining = "training"
	StageWriting  = "writing"
)

const (
	// StaleJobReason is the failure reason written when reclaiming a leftover running job at startup (English, AGENTS.md §2.1).
	// Semantics: the subprocess necessarily died with the previous process; running is just a fake state left behind by a crash or restart.
	StaleJobReason = "interrupted by restart"

	// NeverStartedJobReason is the failure reason written when reclaiming a leftover queued job at startup.
	// Semantics: enqueue only writes the database and puts the job on an in-memory queue, and the queue dies with the previous process, so these rows are never executed;
	// yet Store.Active counts them as in-flight jobs, making every later Enqueue return 409 forever.
	NeverStartedJobReason = "job never started"

	// DefaultTimeout is the default subprocess timeout; optimizer training may run for a long time, but it must have an upper bound
	DefaultTimeout = 30 * time.Minute
	// DefaultLogTailLines is the maximum number of log tail lines persisted.
	DefaultLogTailLines = 40
	// maxLogTailBytes is the byte cap for the log tail.
	//
	// Rationale: the log tail only serves "poll the UI to see the failure reason", and training logs can be very long; if written without limit into
	// the jobs.log_tail TEXT column, the row would grow with job duration and the status query endpoint would have to move large fields around.
	// Keeping only the last few bytes/lines is enough to locate a failure while keeping each row's size bounded.
	maxLogTailBytes = 16 * 1024
	// queueDepth is the in-memory queue capacity. With single concurrency there is at most one unfinished job, so a capacity of 1 suffices and makes enqueue never block.
	queueDepth = 1
)

var (
	// ErrAlreadyRunning means an unfinished job already exists; callers should return 409 (the single-concurrency sentinel error).
	ErrAlreadyRunning = errors.New("a job is already running")
	// ErrKindRequired means no kind was given on enqueue.
	ErrKindRequired = errors.New("job kind is required")
	// ErrTimedOut means the subprocess was killed after exceeding the configured timeout.
	ErrTimedOut = errors.New("job timed out")
	// ErrNoCommand means no executable command was configured for the job.
	ErrNoCommand = errors.New("no command configured for job")
	// ErrNotRunning means the job to cancel is neither running nor in the cancellable queued state.
	ErrNotRunning = errors.New("job is not running")
)

// CancelReason is the reason an administrator's cancellation writes to jobs.error (English, AGENTS.md §2.1).
// It is kept distinct from timeout/crash reasons so the admin panel can show plainly that "a human cancelled this".
const CancelReason = "cancelled by admin"

// Command describes one subprocess invocation. The fields are injected by the caller, so tests can substitute short commands or the self-referencing binary.
type Command struct {
	Name  string    // Executable path
	Args  []string  // Arguments
	Dir   string    // Working directory; empty means inherit the current process's
	Env   []string  // Environment variables; nil means inherit the current process's
	Stdin io.Reader // Standard input; nil means empty
}

// Reporter lets a command builder report stage-level progress (written to jobs.stage).
type Reporter interface {
	SetStage(stage string)
}

// CommandBuilder builds the command to execute for a job; if it returns an error the job is marked failed directly.
type CommandBuilder func(ctx context.Context, job *store.Job, rep Reporter) (Command, error)

// CompleteFunc parses the command's side effects after a successful exit into an optimization result.
//
// The command itself only executes (process exit code semantics); knowledge such as "which file the adapter writes the weights to" belongs to
// production assembly (Optimizer); after the command succeeds the Runner calls this to obtain the OptimizeResult,
// then hands it to FinishOptimize to write back to the job row and the preset. Returning an error marks the job failed.
type CompleteFunc func(ctx context.Context, job *store.Job, cmd Command, tail string) (store.OptimizeResult, error)

// SuccessFunc is called after the job has been marked succeeded (the class C "parameter optimization finished" notification hook).
//
// Same contract as FailureFunc: side effects only, no error return, and panics are caught — a failed notification never changes
// the job's own result.
type SuccessFunc func(ctx context.Context, job store.Job)

// FailureFunc is called after the job has been marked failed (the class D administrator notification hook).
//
// It runs after the state has been persisted, so an implementation may only perform side effects (for example writing a notification into the mail outbox)
// and must not try to rewrite job state; its return value is ignored, and panics are caught by the Runner —
// a failed notification must never make failure handling itself fail — the same rule as "a failed send must not fail the triggering
// operation"; it runs synchronously on the job worker's ctx. May be nil.
type FailureFunc func(ctx context.Context, job store.Job, reason string)

// Deps are the Runner's explicit dependencies.
type Deps struct {
	// DB is required.
	DB *gorm.DB
	// Store is optional; when empty, New builds one from DB.
	Store *Store
	// Logger is optional; when empty, slog.Default() is used.
	Logger *slog.Logger
	// Timeout is the subprocess timeout; <=0 uses DefaultTimeout.
	Timeout time.Duration
	// Command builds the command for a job; when empty the job fails with ErrNoCommand (production supplies it via Optimizer).
	Command CommandBuilder
	// Complete is optional; after the command exits successfully it parses its side effects and produces the optimization result. When empty, success is judged by exit code alone.
	Complete CompleteFunc
	// OnFailure is optional; called after the job is marked failed (the class D administrator notification hook).
	// When empty it does nothing extra. See the FailureFunc contract.
	OnFailure FailureFunc
	// OnSuccess is optional; called after the job is marked succeeded (the class C "parameter optimization finished" notification hook).
	// When empty it does nothing extra. See the SuccessFunc contract.
	OnSuccess SuccessFunc
	// Now is an injectable clock; when empty the system UTC time is used.
	Now func() time.Time
	// LogTailLines is the maximum number of log tail lines; <=0 uses DefaultLogTailLines.
	LogTailLines int
}

// Runner holds the single-concurrency worker, the job store, and the subprocess execution configuration.
//
// Concurrency model: Enqueue first checks under mu whether an unfinished job exists, then persists and enqueues; a single
// worker goroutine takes jobs off queue and runs them serially. mu makes "check + persist" atomic, so of two concurrent
// Enqueues only one succeeds and the other gets ErrAlreadyRunning.
type Runner struct {
	db           *gorm.DB
	store        *Store
	logger       *slog.Logger
	timeout      time.Duration
	command      CommandBuilder
	complete     CompleteFunc
	onFailure    FailureFunc
	onSuccess    SuccessFunc
	now          func() time.Time
	logTailLines int

	mu      sync.Mutex
	started bool
	queue   chan *store.Job

	// Cancel handle for the running job. It is separate from mu so that Cancel does not contend with Enqueue's lock
	// while execute is running; currentDone is closed before execute returns, and Cancel uses it to wait for the persist to finish.
	runMu         sync.Mutex
	currentID     uint64
	currentCancel context.CancelFunc
	currentDone   chan struct{}
}

// New builds a Runner; it does not start the worker, so Start must be called afterwards.
func New(deps Deps) (*Runner, error) {
	if deps.DB == nil {
		return nil, errors.New("jobs: Deps.DB is required")
	}
	st := deps.Store
	if st == nil {
		st = NewStore(deps.DB)
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	timeout := deps.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	command := deps.Command
	if command == nil {
		command = noCommandBuilder
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	tailLines := deps.LogTailLines
	if tailLines <= 0 {
		tailLines = DefaultLogTailLines
	}
	return &Runner{
		db:           deps.DB,
		store:        st,
		logger:       logger,
		timeout:      timeout,
		command:      command,
		complete:     deps.Complete,
		onFailure:    deps.OnFailure,
		onSuccess:    deps.OnSuccess,
		now:          now,
		logTailLines: tailLines,
		queue:        make(chan *store.Job, queueDepth),
	}, nil
}

// Start starts the single worker goroutine; repeated calls are idempotent. When ctx is cancelled the worker exits
// and the subprocess currently executing is killed (runProcess watches ctx.Done).
//
// Before starting it calls RecoverStale to reclaim unfinished jobs left by the previous process (running and queued):
// otherwise those leftover rows would make Store.Active believe "a job is running" forever and every later Enqueue would keep returning 409. A recovery failure is only logged
// and does not block the worker from starting; the recovery logic itself is an explicit entry point (RecoverStale), not a hidden side effect.
func (r *Runner) Start(ctx context.Context) {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	r.mu.Unlock()
	_, _ = r.RecoverStale(ctx)
	go r.loop(ctx)
}

// RecoverStale is an explicit entry point: it reclaims the unfinished jobs left over from a restart/crash as failed,
// keeping their log_tail for diagnosis. running jobs get StaleJobReason and queued jobs get NeverStartedJobReason
// (different reasons, because only the running batch actually started). It returns the number of reclaimed rows.
// Start calls it once before starting the worker; callers may also call it explicitly when needed.
func (r *Runner) RecoverStale(ctx context.Context) (int64, error) {
	recovered, err := r.store.RecoverStale(ctx, r.now(), StaleJobReason, NeverStartedJobReason)
	if err != nil {
		r.logger.Error("stale job recovery failed", "error", err)
		return 0, err
	}
	if recovered > 0 {
		r.logger.Warn("recovered unfinished jobs left by a previous process",
			"count", recovered,
			"running_reason", StaleJobReason,
			"queued_reason", NeverStartedJobReason)
	}
	return recovered, nil
}

// List returns a page of jobs (with status/stage/log tail) and the total count, for the admin panel to page through.
func (r *Runner) List(ctx context.Context, limit, offset int) ([]store.Job, int64, error) {
	return r.store.List(ctx, limit, offset)
}

// Cancel cancels a job: a running job has its whole process group killed, after which execute persists it
// as failed with error = CancelReason; a still-queued job is marked failed directly (execute
// skips jobs already in a terminal state, so it is never actually run).
//
// Why wait for currentDone: acceptance requires "after cancelling, nothing hangs and a new enqueue succeeds". The single-concurrency check looks at
// unfinished rows in the jobs table, so only once execute has written failed will the next Enqueue stop hitting 409.
func (r *Runner) Cancel(ctx context.Context, id uint64) error {
	r.runMu.Lock()
	if r.currentID == id && r.currentCancel != nil {
		cancel := r.currentCancel
		done := r.currentDone
		r.runMu.Unlock()
		cancel()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.runMu.Unlock()

	// Not running: it may still be waiting in the queue. Persist failed directly; the worker skips it as terminal when it picks it up.
	job, err := r.store.ByID(ctx, id)
	if err != nil {
		return err
	}
	if job.Status == StatusFailed {
		// Already terminal (most likely just cancelled): cancellation is idempotent, so return success.
		return nil
	}
	if job.Status != StatusQueued && job.Status != StatusRunning {
		return ErrNotRunning
	}
	return r.store.FinishFailed(ctx, id, r.now(), CancelReason, "")
}

// SetOnSuccess sets the job success hook; it must be called before Start to avoid racing the worker (same as SetOnFailure).
func (r *Runner) SetOnSuccess(fn SuccessFunc) {
	r.onSuccess = fn
}

// SetOnFailure sets the job failure hook; it must be called before Start to avoid racing the worker.
// Production assembly wires it in cmd/engram to the web layer's administrator notification.
func (r *Runner) SetOnFailure(fn FailureFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onFailure = fn
}

// Enqueue creates a queued job and enqueues it; when an unfinished job already exists it returns ErrAlreadyRunning (HTTP 409).
func (r *Runner) Enqueue(ctx context.Context, kind string, targetID *uint64) (*store.Job, error) {
	if strings.TrimSpace(kind) == "" {
		return nil, ErrKindRequired
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	active, err := r.store.Active(ctx)
	if err != nil {
		return nil, fmt.Errorf("check active job: %w", err)
	}
	if active != nil {
		return nil, fmt.Errorf("%w: job %d is %s", ErrAlreadyRunning, active.ID, active.Status)
	}
	job, err := r.store.CreateQueued(ctx, kind, targetID, r.now())
	if err != nil {
		return nil, err
	}
	r.queue <- job
	return job, nil
}

// loop is the only worker: it consumes the queue serially. Single concurrency is guaranteed jointly by this and Enqueue's mu.
func (r *Runner) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-r.queue:
			r.execute(ctx, job)
		}
	}
}

// execute runs one job and drives the state machine queued -> running -> succeeded | failed.
// Any failing step must end in failed, so a job never stays in running forever.
func (r *Runner) execute(ctx context.Context, job *store.Job) {
	// The worker must not exit as a whole because a single job panicked; log it and mark that job failed.
	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Error("job worker panicked", "job_id", job.ID, "panic", rec)
			r.fail(ctx, job.ID, fmt.Sprintf("job worker panicked: %v", rec), "")
		}
	}()

	// Cancelled while queued: Cancel already marked the row failed, so skip it here and do not start a subprocess.
	if current, err := r.store.ByID(ctx, job.ID); err == nil && current.Status == StatusFailed {
		return
	}

	// Build a separately cancellable context for this run: an administrator cancellation affects only this job and does not involve the worker's ctx.
	runCtx, cancelRun := context.WithCancel(ctx)
	done := make(chan struct{})
	r.registerRun(job.ID, cancelRun, done)
	defer func() {
		r.unregisterRun(job.ID)
		close(done)
	}()

	startedAt := r.now()
	if err := r.store.MarkRunning(ctx, job.ID, startedAt); err != nil {
		// A failed transition to running must also reach a terminal state: staying in queued would make Store.Active count it as in-flight,
		// making every later Enqueue return 409 forever until the process restarts and RecoverStale reclaims it (same root symptom).
		// The error text names the failing step, so jobs.error pinpoints it at a glance.
		r.logger.Error("mark job running failed", "job_id", job.ID, "error", err)
		r.fail(ctx, job.ID, fmt.Sprintf("mark job running: %v", err), "")
		return
	}
	r.setStage(ctx, job.ID, StageReadLogs)

	cmd, err := r.command(ctx, job, jobReporter{ctx: ctx, runner: r, id: job.ID})
	if err != nil {
		r.fail(ctx, job.ID, fmt.Sprintf("build command: %v", err), "")
		return
	}
	if strings.TrimSpace(cmd.Name) == "" {
		r.fail(ctx, job.ID, ErrNoCommand.Error(), "")
		return
	}

	r.setStage(ctx, job.ID, StageTraining)
	out, runErr := runProcess(runCtx, cmd, r.timeout, maxLogTailBytes)
	tail := tailLines(out, r.logTailLines)
	if runErr != nil {
		// Administrator cancellation must be distinguishable from a timeout or an ordinary failure: the former writes CancelReason so the panel shows at a glance who did it.
		msg := runErr.Error()
		if !errors.Is(runErr, ErrTimedOut) && runCtx.Err() != nil && ctx.Err() == nil {
			msg = CancelReason
		}
		r.fail(ctx, job.ID, msg, tail)
		return
	}

	// A successful command return moves into the write-back phase: jobs configured with Complete (optimization jobs) have it parse
	// the weights the adapter wrote and hand them to FinishOptimize; other jobs still follow "exit code 0 means success".
	r.setStage(ctx, job.ID, StageWriting)
	if r.complete != nil {
		result, err := r.complete(runCtx, job, cmd, tail)
		if err != nil {
			r.fail(ctx, job.ID, fmt.Sprintf("parse job output: %v", err), tail)
			return
		}
		if err := r.FinishOptimize(ctx, job.ID, result, tail); err != nil {
			r.logger.Error("finish optimize failed", "job_id", job.ID, "error", err)
			r.fail(ctx, job.ID, fmt.Sprintf("finish optimize: %v", err), tail)
			return
		}
		r.notifySuccess(ctx, job.ID)
		return
	}
	if err := r.store.FinishSucceeded(ctx, job.ID, r.now(), "", tail); err != nil {
		r.logger.Error("finish job succeeded failed", "job_id", job.ID, "error", err)
		return
	}
	r.notifySuccess(ctx, job.ID)
}

// notifySuccess calls the OnSuccess hook after the job is persisted as succeeded; it returns immediately when not configured.
// Same discipline as notifyFailure: hook panics are caught, and a failed notification does not affect the job result.
func (r *Runner) notifySuccess(ctx context.Context, id uint64) {
	fn := r.onSuccess
	if fn == nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Error("job success hook panicked", "job_id", id, "panic", rec)
		}
	}()
	job, err := r.store.ByID(ctx, id)
	if err != nil {
		r.logger.Error("load succeeded job for notification failed", "job_id", id, "error", err)
		return
	}
	fn(ctx, *job)
}

// fail marks the job failed and records the error and log tail; the failure reason is written in English (AGENTS.md §2.1).
// The OnFailure hook is called only after the state is persisted successfully: the hook performs side effects only and never changes this result.
func (r *Runner) fail(ctx context.Context, id uint64, msg, tail string) {
	if err := r.store.FinishFailed(ctx, id, r.now(), msg, tail); err != nil {
		r.logger.Error("finish job failed failed", "job_id", id, "error", err)
		return
	}
	r.notifyFailure(ctx, id, msg)
}

// notifyFailure calls the OnFailure hook after the job failure is persisted; it returns immediately when no hook is configured.
// Hook panics are caught: a failed notification must never in turn break job failure handling
func (r *Runner) notifyFailure(ctx context.Context, id uint64, reason string) {
	fn := r.onFailure
	if fn == nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Error("job failure hook panicked", "job_id", id, "panic", rec)
		}
	}()
	job, err := r.store.ByID(ctx, id)
	if err != nil {
		r.logger.Error("load failed job for notification failed", "job_id", id, "error", err)
		return
	}
	fn(ctx, *job, reason)
}

// registerRun records the cancel handle of the currently running job; Cancel uses it to find the process group to kill.
func (r *Runner) registerRun(id uint64, cancel context.CancelFunc, done chan struct{}) {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	r.currentID = id
	r.currentCancel = cancel
	r.currentDone = done
}

// unregisterRun clears the cancel handle; it only clears when the current record still belongs to that job, so the next job's state is not deleted by mistake.
func (r *Runner) unregisterRun(id uint64) {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	if r.currentID != id {
		return
	}
	r.currentID = 0
	r.currentCancel = nil
	r.currentDone = nil
}

// setStage updates the stage; failures are only logged, so a failed progress write never interrupts the job.
func (r *Runner) setStage(ctx context.Context, id uint64, stage string) {
	if err := r.store.SetStage(ctx, id, stage); err != nil {
		r.logger.Error("set job stage failed", "job_id", id, "stage", stage, "error", err)
	}
}

// jobReporter forwards Reporter.SetStage to the Runner, for CommandBuilder to report stages.
type jobReporter struct {
	ctx    context.Context
	runner *Runner
	id     uint64
}

func (j jobReporter) SetStage(stage string) { j.runner.setStage(j.ctx, j.id, stage) }

// noCommandBuilder is the fallback when no CommandBuilder is configured: it fails the job with ErrNoCommand directly.
//
// It deliberately no longer "re-execs itself with optimize --job": that self-referencing path depends on a CLI subcommand that has been removed
// and would leave jobs ending in production as "command not implemented". Production assembly must explicitly pass Optimizer.CommandBuilder()
// to talk to the real adapter; this only fails loudly when the assembly is missing.
func noCommandBuilder(context.Context, *store.Job, Reporter) (Command, error) {
	return Command{}, ErrNoCommand
}

// HTTPStatus maps the jobs package's sentinel errors to HTTP status codes: duplicate enqueue -> 409, everything else -> 500.
// The meaning of 409 is defined in exactly one place here, and the REST layer reuses it, so the two cannot drift.
func HTTPStatus(err error) int {
	if errors.Is(err, ErrAlreadyRunning) {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// runProcess starts the subprocess and waits for it to finish.
//
// The subprocess is put into its own process group (setProcessGroup), and on timeout or ctx cancellation the whole tree is killed
// (killProcessGroup): killing only the direct child would leave the processes it spawned behind (for example a background training
// process under a shell), which keep burning CPU and make the job "look finished while it is still running" — exactly why
// "kill the whole process group" is required. The platform implementations of the two actions live in process_unix.go and process_windows.go.
func runProcess(ctx context.Context, c Command, timeout time.Duration, maxBytes int) (string, error) {
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	cmd.Stdin = c.Stdin
	setProcessGroup(cmd)

	tail := newTailBuffer(maxBytes)
	cmd.Stdout = tail
	cmd.Stderr = tail

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start subprocess: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return tail.String(), err
	case <-timer.C:
		killProcessGroup(cmd.Process.Pid)
		<-done
		return tail.String(), fmt.Errorf("%w after %s", ErrTimedOut, timeout)
	case <-ctx.Done():
		killProcessGroup(cmd.Process.Pid)
		<-done
		return tail.String(), fmt.Errorf("job canceled: %w", ctx.Err())
	}
}

// tailBuffer is an io.Writer that keeps only the last max bytes; concurrent writes (stdout/stderr) are protected by mu.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newTailBuffer(max int) *tailBuffer {
	if max <= 0 {
		max = maxLogTailBytes
	}
	return &tailBuffer{max: max}
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// tailLines keeps only the last n lines; returns the input unchanged when n <= 0.
func tailLines(s string, n int) string {
	if n <= 0 || s == "" {
		return s
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
