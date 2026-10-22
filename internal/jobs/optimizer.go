package jobs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the production wiring: it plugs the Rust adapter (tools/optimizer) into the job pipeline.
//
// Data flow: the web layer creates a job with EnqueueOptimize -> the Runner executes the command built by Optimizer.CommandBuilder
// (the Go side first exports that user's review log via ExportOptimizerLog, then execs the adapter to read the log and write the weight file)
// -> the Runner calls Optimizer.Complete to parse the weights -> FinishOptimize writes back to the job and to the preset.
//
// Adapter contract (tools/optimizer/src/main.rs, stable): `optimizer <review-log.jsonl> [--out PATH]`,
// which on success writes the 21-element weight JSON to --out, sends diagnostics to stderr, and exits with code 0/2/3/4.

// optimizerBinaryName is the adapter binary's file name (consistent with Cargo's bin name).
const optimizerBinaryName = "optimizer"

// optimizerWeightCount is the FSRS weight dimension, used to fend off incomplete results caused by upstream API changes.
const optimizerWeightCount = 21

// maxAdapterWeightsBytes is the read cap for the adapter weight file (weights.json), 1 MiB.
// Rationale: the weights are a JSON array of a fixed 21 float64s whose normal output is only a few hundred bytes; when the adapter misbehaves and produces a huge file,
// os.ReadFile would read the whole thing into memory before decoding, leaving memory use unbounded. 1 MiB easily holds any reasonable formatting of 21 doubles
// (whitespace included); anything beyond it counts as abnormal output and fails the job rather than being read in full first.
const maxAdapterWeightsBytes = 1 << 20

// ErrOptimizerNotFound means the adapter binary does not exist; the error message names the expected path so operators can locate it.
var ErrOptimizerNotFound = errors.New("optimizer adapter not found")

// OptimizerDeps are the Optimizer's explicit dependencies.
type OptimizerDeps struct {
	// DB is required: used to export the review log and to resolve the owner of the job's target preset.
	DB *gorm.DB
	// Logger is optional; when empty, slog.Default() is used.
	Logger *slog.Logger
	// Binary is the configured adapter path (config.KeyOptimizerPath); when empty it is resolved by ResolveOptimizerBinary.
	Binary string
	// Executable is for test injection only: a stand-in when resolving "next to the service binary"; when empty, os.Executable() is used.
	Executable string
	// Now is an injectable clock; when empty the system UTC time is used.
	Now func() time.Time
}

// Optimizer turns an optimization job into a command builder and result parser: "export the log -> run the adapter -> parse the weights".
// It is stateless (apart from the in-process working directory); production builds it once when assembling main.go, for the single-concurrency worker to use.
type Optimizer struct {
	db     *gorm.DB
	logger *slog.Logger
	binary string
	now    func() time.Time
	// root is this process's working root: each job keeps its review-log.jsonl and weights.json in its own subdirectory.
	// The process id takes part in naming, so several instances on one machine (or concurrent tests) do not step on each other; the directory is deleted after the job ends.
	root string
}

// NewOptimizer builds the production optimizer wiring; it starts no goroutine.
func NewOptimizer(deps OptimizerDeps) (*Optimizer, error) {
	if deps.DB == nil {
		return nil, errors.New("jobs: OptimizerDeps.DB is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Optimizer{
		db:     deps.DB,
		logger: logger,
		binary: ResolveOptimizerBinary(deps.Binary, deps.Executable),
		now:    now,
		root:   filepath.Join(os.TempDir(), fmt.Sprintf("engram-optimize-%d", os.Getpid())),
	}, nil
}

// Binary returns the adapter path that will actually be executed, for the startup log to show the "effective value".
func (o *Optimizer) Binary() string { return o.binary }

// ResolveOptimizerBinary resolves the adapter path. Order:
//  1. configured is non-empty: use it as-is (respect an administrator's explicit choice; if it is missing the job fails and names that path);
//  2. the optimizer next to the service binary (release form: the adapter ships in the same directory as the main program); when executable is empty, os.Executable() is used;
//  3. the repository build artifact tools/optimizer/target/release/optimizer (development/test form).
//
// When none exists it returns the path from step 2, so the failure message points at "where it should have been".
func ResolveOptimizerBinary(configured, executable string) string {
	if strings.TrimSpace(configured) != "" {
		return configured
	}
	if executable == "" {
		if p, err := os.Executable(); err == nil {
			executable = p
		}
	}
	beside := filepath.Join(filepath.Dir(executable), optimizerBinaryName)
	if fileExists(beside) {
		return beside
	}
	for _, cand := range repoOptimizerCandidates() {
		if fileExists(cand) {
			return cand
		}
	}
	return beside
}

// repoOptimizerCandidates are the candidate paths in the development/test form (relative to the current working directory).
// `go test`'s working directory is the package directory (internal/jobs), so `../../` points at the repository root;
// when the service is started from the repository root the first entry hits.
func repoOptimizerCandidates() []string {
	rel := filepath.Join("tools", optimizerBinaryName, "target", "release", optimizerBinaryName)
	return []string{rel, filepath.Join("..", "..", rel)}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// CommandBuilder returns the CommandBuilder for optimization jobs: export the review log -> exec the adapter.
// When the adapter is missing it returns ErrOptimizerNotFound with the path in the message, and the job fails accordingly.
func (o *Optimizer) CommandBuilder() CommandBuilder {
	return func(ctx context.Context, job *store.Job, rep Reporter) (Command, error) {
		if !fileExists(o.binary) {
			return Command{}, fmt.Errorf("%w: %s", ErrOptimizerNotFound, o.binary)
		}
		ownerID, err := o.ownerUserID(ctx, job)
		if err != nil {
			return Command{}, err
		}
		dir, err := o.prepareWorkDir(job)
		if err != nil {
			return Command{}, err
		}

		rep.SetStage(StageReadLogs)
		logPath := filepath.Join(dir, "review-log.jsonl")
		f, err := os.Create(logPath)
		if err != nil {
			return Command{}, fmt.Errorf("create review log: %w", err)
		}
		exportErr := store.NewReviewStore(o.db).ExportOptimizerLog(ctx, ownerID, *job.TargetID, f)
		closeErr := f.Close()
		if exportErr != nil {
			return Command{}, fmt.Errorf("export review log: %w", exportErr)
		}
		if closeErr != nil {
			return Command{}, fmt.Errorf("close review log: %w", closeErr)
		}

		rep.SetStage(StageTraining)
		return Command{
			Name: o.binary,
			Args: []string{logPath, "--out", filepath.Join(dir, "weights.json")},
			Dir:  filepath.Dir(o.binary),
		}, nil
	}
}

// Complete parses the weight file the adapter wrote, computes the fit metrics before and after optimization, and produces the result for FinishOptimize.
//
// Fit metrics: using the same review log the adapter trained on, replay it once under the preset's current (old) weights and once under
// the new weights the adapter produced, yielding FitBefore/FitAfter, so the page can show a real "improved / not improved".
// The old weights must be read from the preset before FinishOptimize writes back — this function runs before the write-back, so it reads exactly the old values.
// When the metrics cannot be computed (no target preset, unreadable log, no predictable item at all) it does not fail: the weights themselves are still valid,
// and leaving zero values means "unavailable", so the page hides the conclusion instead of slandering it as "not improved".
func (o *Optimizer) Complete(ctx context.Context, job *store.Job, _ Command, _ string) (store.OptimizeResult, error) {
	dir := o.jobDir(job)
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			o.logger.Warn("remove optimizer work dir failed", "job_id", job.ID, "dir", dir, "error", err)
		}
	}()

	raw, err := readLimitedFile(filepath.Join(dir, "weights.json"), maxAdapterWeightsBytes)
	if err != nil {
		return store.OptimizeResult{}, fmt.Errorf("read adapter weights: %w", err)
	}
	var weights []float64
	if err := json.Unmarshal(bytes.TrimSpace(raw), &weights); err != nil {
		return store.OptimizeResult{}, fmt.Errorf("decode adapter weights: %w", err)
	}
	if len(weights) != optimizerWeightCount {
		return store.OptimizeResult{}, fmt.Errorf("adapter wrote %d weights, want %d", len(weights), optimizerWeightCount)
	}

	used, err := countLines(filepath.Join(dir, "review-log.jsonl"))
	if err != nil {
		return store.OptimizeResult{}, fmt.Errorf("count exported reviews: %w", err)
	}
	result := store.OptimizeResult{
		ReviewsUsed: used,
		Weights:     weights,
		OptimizedAt: o.now(),
	}
	before, after, fitErr := o.fitMetrics(ctx, job, weights)
	if fitErr != nil {
		// Metrics are a bonus for the page, not a condition of job success: record the reason and leave zero values so the page hides the conclusion.
		o.logger.Warn("compute optimizer fit metrics failed", "job_id", job.ID, "error", fitErr)
	} else {
		result.FitBefore = before
		result.FitAfter = after
	}
	return result, nil
}

// fitMetrics evaluates the fit once with the preset's old weights and once with the new ones, on the review log the adapter trained on.
// The log and the preset are both read before the job working directory is deleted (Complete's defer does the deletion).
func (o *Optimizer) fitMetrics(ctx context.Context, job *store.Job, newWeights []float64) (store.FitMetrics, store.FitMetrics, error) {
	if job.TargetID == nil || *job.TargetID == 0 {
		return store.FitMetrics{}, store.FitMetrics{}, errors.New("optimize job has no target preset; cannot load the old weights")
	}
	// The old weights come from the preset: FinishOptimize writes the new weights only after this function, so what is read here is the pre-optimization value.
	preset, err := store.NewPresetStore(o.db).ByID(ctx, *job.TargetID)
	if err != nil {
		return store.FitMetrics{}, store.FitMetrics{}, fmt.Errorf("load preset %d for fit metrics: %w", *job.TargetID, err)
	}
	logs, err := readOptimizerLog(filepath.Join(o.jobDir(job), "review-log.jsonl"))
	if err != nil {
		return store.FitMetrics{}, store.FitMetrics{}, err
	}
	before, after, err := schedule.CompareFit(preset, newWeights, logs)
	if err != nil {
		return store.FitMetrics{}, store.FitMetrics{}, fmt.Errorf("compare fit: %w", err)
	}
	return store.FitMetrics{LogLoss: before.LogLoss, RMSE: before.RMSE, Items: before.Items},
		store.FitMetrics{LogLoss: after.LogLoss, RMSE: after.RMSE, Items: after.Items}, nil
}

// readOptimizerLog reads back review-log.jsonl from the job directory (the very copy the adapter just trained on).
func readOptimizerLog(path string) ([]store.OptimizerReviewLog, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open optimizer review log: %w", err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	var logs []store.OptimizerReviewLog
	for {
		var row store.OptimizerReviewLog
		if err := dec.Decode(&row); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode optimizer review log: %w", err)
		}
		logs = append(logs, row)
	}
	return logs, nil
}

// ownerUserID looks up the owner of the review log from the job's target preset (the job only carries preset_id; the log takes that user's reviews on decks attached to this preset).
func (o *Optimizer) ownerUserID(ctx context.Context, job *store.Job) (uint64, error) {
	if job.TargetID == nil || *job.TargetID == 0 {
		return 0, errors.New("optimize job has no target preset; cannot locate the owner's review log")
	}
	preset, err := store.NewPresetStore(o.db).ByID(ctx, *job.TargetID)
	if err != nil {
		return 0, fmt.Errorf("load preset %d for optimize job: %w", *job.TargetID, err)
	}
	return preset.OwnerUserID, nil
}

// jobDir returns a job's working directory (without creating it).
func (o *Optimizer) jobDir(job *store.Job) string {
	return filepath.Join(o.root, fmt.Sprintf("job-%d", job.ID))
}

// prepareWorkDir clears any leftover directory for the job with the same ID and creates a fresh empty one.
// Clearing first means "rerunning the same job" cannot read the previous round's stale weight file.
func (o *Optimizer) prepareWorkDir(job *store.Job) (string, error) {
	dir := o.jobDir(job)
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("clear optimizer work dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create optimizer work dir: %w", err)
	}
	return dir, nil
}

// readLimitedFile reads the contents of path, but never reads a file larger than limit bytes into memory whole.
// It first does a fast rejection by size with os.Stat (the file is read only after the adapter finished writing, so the size is stable by then),
// then uses io.LimitReader to cover the race where the file is still being written after stat, and re-checks against the bytes actually read:
// anything over the cap returns an error instead of reading the content in full.
func readLimitedFile(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("file %s is %d bytes, over the %d-byte limit", path, info.Size(), limit)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// Read one extra byte: a file exactly at the cap still passes, and any extra byte is detectable.
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file %s grew past the %d-byte limit while reading", path, limit)
	}
	return data, nil
}

// countLines counts non-empty lines, used as "the number of reviews this training actually consumed".
func countLines(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var n int64
	sc := bufio.NewScanner(f)
	// A single line is a JSON object, so the buffer cap is relaxed to avoid ErrTooLong on very long lines.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return n, nil
}
