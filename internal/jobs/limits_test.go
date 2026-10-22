//go:build unix

// Compiles only on Unix: it reuses the unix-only test scaffolding from jobs_test.go / optimizer_wiring_test.go
// (newWiringDB, okBuilder and others). This file covers two robustness defects: a MarkRunning failure must mark the job
// failed (instead of leaving it in queued and blocking the queue), and reads of the adapter's weight file must have a size cap.

package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// errMarkRunningInjected is the MarkRunning fault reason the test injects, for asserting it appears in the failure text.
var errMarkRunningInjected = errors.New("injected mark running failure")

// injectMarkRunningFailure registers a GORM update callback: it injects an error on the update that "sets the job to running".
// Store is a concrete type (Deps.Store cannot take a fake implementation), so the fault can only be created at the DB layer;
// it intercepts only updates with status running, and other updates such as FinishFailed run as usual.
func injectMarkRunningFailure(db *gorm.DB) {
	db.Callback().Update().Before("gorm:update").Register("jobs_test:fail_mark_running", func(tx *gorm.DB) {
		updates, ok := tx.Statement.Dest.(map[string]any)
		if !ok {
			return
		}
		if status, ok := updates["status"].(string); ok && status == StatusRunning {
			tx.AddError(errMarkRunningInjected)
		}
	})
}

// runJobWithFailingMarkRunning creates a real SQLite database, injects the MarkRunning fault and executes one job synchronously,
// returning the Runner, the Store and the job id. It deliberately does not Start the worker: execute is called directly, to avoid racing a background goroutine.
func runJobWithFailingMarkRunning(t *testing.T) (*Runner, *Store, uint64) {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	st := NewStore(db)
	injectMarkRunningFailure(db)

	runner, err := New(Deps{DB: db, Store: st, Timeout: time.Second, Command: okBuilder})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	job, err := st.CreateQueued(ctx, KindOptimize, nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateQueued: %v", err)
	}
	runner.execute(ctx, job)
	return runner, st, job.ID
}

// TestMarkRunningFailureMarksJobFailed covers the persistence half of the first defect: when MarkRunning fails the job must
// reach the terminal failed state, carrying an English error text that names the reason; before the fix it only logged and returned, leaving the job in queued.
func TestMarkRunningFailureMarksJobFailed(t *testing.T) {
	_, st, id := runJobWithFailingMarkRunning(t)

	got, err := st.ByID(context.Background(), id)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Status != StatusFailed {
		t.Fatalf("job %d status = %q after MarkRunning failed, want %q", id, got.Status, StatusFailed)
	}
	if got.Error == nil || !strings.Contains(*got.Error, "mark job running") {
		t.Fatalf("job %d error = %v, want it to name the failed step (%q)", id, got.Error, "mark job running")
	}
	if got.Error == nil || !strings.Contains(*got.Error, errMarkRunningInjected.Error()) {
		t.Fatalf("job %d error = %v, want it to carry the underlying cause %q", id, got.Error, errMarkRunningInjected.Error())
	}
	if got.FinishedAt == nil {
		t.Fatalf("job %d failed without finished_at", id)
	}
	t.Logf("job %d -> status=%s error=%q finished=%v", id, got.Status, *got.Error, got.FinishedAt != nil)
}

// TestMarkRunningFailureUnblocksQueue covers the blocking half of the first defect: the queued row left behind by a MarkRunning failure
// is counted by Store.Active as an in-flight job, making every later Enqueue return 409 forever. After the fix that row is failed,
// so a new job must be enqueueable. Before the fix Enqueue returned ErrAlreadyRunning (HTTP 409).
func TestMarkRunningFailureUnblocksQueue(t *testing.T) {
	runner, st, id := runJobWithFailingMarkRunning(t)
	ctx := context.Background()

	// First confirm the precondition: that job really did reach a terminal state because of the MarkRunning failure (otherwise this case is meaningless).
	got, err := st.ByID(ctx, id)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Status != StatusFailed {
		t.Fatalf("precondition failed: job %d status = %q, want %q", id, got.Status, StatusFailed)
	}

	if _, err := runner.Enqueue(ctx, KindOptimize, nil); errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Enqueue after a MarkRunning failure = %v; the stuck job still blocks new jobs (HTTP 409)", err)
	} else if err != nil {
		t.Fatalf("Enqueue after a MarkRunning failure: %v", err)
	}
	t.Logf("job %d terminal (%s); a fresh Enqueue is no longer refused", id, got.Status)
}

// testWeightsLimit is the weight file cap as seen by the test (1 MiB). It is deliberately hard-coded rather than referencing the constant in the implementation:
// that way the test still compiles when the cap is not implemented, giving a meaningful red instead of a compile error.
const testWeightsLimit = 1 << 20

// paddedWeightsJSON builds JSON with 21 valid weights whose total byte count is exactly totalBytes (padded with whitespace characters).
// The result is valid JSON, so the pre-fix "read the whole file + decode" succeeds — the red comes from the missing size cap, not from a decode failure.
func paddedWeightsJSON(t *testing.T, totalBytes int) []byte {
	t.Helper()
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < optimizerWeightCount; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("0.5")
	}
	b.WriteByte(']')
	body := b.String()
	if len(body) > totalBytes {
		t.Fatalf("totalBytes %d is smaller than the minimal weights JSON (%d bytes)", totalBytes, len(body))
	}
	pad := strings.Repeat(" ", totalBytes-len(body))
	return []byte("[" + pad + body[1:])
}

// newCompleteTestOptimizer builds an Optimizer whose root points at a temporary directory, and pre-seeds the job directory and
// review-log.jsonl (Complete stats it after reading the weights, and a missing file would fail the happy path early).
func newCompleteTestOptimizer(t *testing.T, job *store.Job) *Optimizer {
	t.Helper()
	db, _ := newWiringDB(t)
	opt, err := NewOptimizer(OptimizerDeps{DB: db})
	if err != nil {
		t.Fatalf("NewOptimizer: %v", err)
	}
	opt.root = t.TempDir()
	dir := opt.jobDir(job)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "review-log.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write review log: %v", err)
	}
	return opt
}

// TestCompleteRejectsOversizedWeights covers the second defect: when the adapter's weights.json exceeds the cap,
// Complete must return an error (naming the cap) rather than reading the whole file into memory. The file is valid JSON, so before the fix it would be
// read whole and decode successfully — the red is precisely "no size cap" itself.
func TestCompleteRejectsOversizedWeights(t *testing.T) {
	job := &store.Job{ID: 11}
	opt := newCompleteTestOptimizer(t, job)
	dir := opt.jobDir(job)

	raw := paddedWeightsJSON(t, testWeightsLimit+1)
	if len(raw) <= testWeightsLimit {
		t.Fatalf("test fixture is %d bytes, want > %d", len(raw), testWeightsLimit)
	}
	if err := os.WriteFile(filepath.Join(dir, "weights.json"), raw, 0o600); err != nil {
		t.Fatalf("write oversized weights: %v", err)
	}

	_, err := opt.Complete(context.Background(), job, Command{}, "")
	if err == nil {
		t.Fatalf("Complete accepted a %d-byte weights.json and returned nil; it must refuse files over %d bytes", len(raw), testWeightsLimit)
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Fatalf("Complete error = %q, want it to mention the size limit", err)
	}
	// The cap value must appear in the error so operators can tell which constraint was triggered.
	if !strings.Contains(err.Error(), "1048576") {
		t.Fatalf("Complete error = %q, want it to name the %d-byte limit", err, testWeightsLimit)
	}
	t.Logf("oversized weights (%d bytes) rejected: %v", len(raw), err)
}

// TestCompleteAcceptsWeightsAtLimit is the positive (and boundary) case of defect 2: a file exactly at the cap can still be read,
// and the 21 weights parse correctly. It guarantees the added cap does not harm normal output and pins down the "the cap is inclusive" semantics.
func TestCompleteAcceptsWeightsAtLimit(t *testing.T) {
	job := &store.Job{ID: 12}
	opt := newCompleteTestOptimizer(t, job)
	dir := opt.jobDir(job)

	raw := paddedWeightsJSON(t, testWeightsLimit)
	if len(raw) != testWeightsLimit {
		t.Fatalf("fixture is %d bytes, want exactly %d", len(raw), testWeightsLimit)
	}
	if err := os.WriteFile(filepath.Join(dir, "weights.json"), raw, 0o600); err != nil {
		t.Fatalf("write weights: %v", err)
	}

	result, err := opt.Complete(context.Background(), job, Command{}, "")
	if err != nil {
		t.Fatalf("Complete rejected a weights.json of exactly %d bytes: %v", testWeightsLimit, err)
	}
	if len(result.Weights) != optimizerWeightCount {
		t.Fatalf("Complete parsed %d weights, want %d", len(result.Weights), optimizerWeightCount)
	}
	t.Logf("weights.json of exactly %d bytes accepted: parsed %d weights", len(raw), len(result.Weights))
}
