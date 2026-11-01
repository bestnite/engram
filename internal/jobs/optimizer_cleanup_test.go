//go:build unix

package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

type cleanupTestReporter struct{}

func (cleanupTestReporter) SetStage(string) {}

// TestFailedJobRemovesTheExportedReviewLog asserts a run that ends failed leaves no work directory
// behind: the exported review log is that user's history and must not outlive the job.
func TestFailedJobRemovesTheExportedReviewLog(t *testing.T) {
	root := t.TempDir()
	var workDir string
	runner, st, _ := newTestRunner(t, 5*time.Second, func(_ context.Context, job *store.Job, _ Reporter) (Command, error) {
		workDir = filepath.Join(root, fmt.Sprintf("job-%d", job.ID))
		if err := os.MkdirAll(workDir, 0o700); err != nil {
			return Command{}, err
		}
		if err := os.WriteFile(filepath.Join(workDir, "review-log.jsonl"), []byte("{}\n"), 0o600); err != nil {
			return Command{}, err
		}
		return Command{
			Name:    "/bin/sh",
			Args:    []string{"-c", "exit 3"},
			Cleanup: func() { _ = os.RemoveAll(workDir) },
		}, nil
	})

	job, err := runner.Enqueue(context.Background(), KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	failed := waitForStatus(t, st, job.ID, StatusFailed, 5*time.Second)
	if failed.Error == nil {
		t.Fatalf("job %d failed without an error message", failed.ID)
	}
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Fatalf("work dir %s still exists after the failed job, want it removed", workDir)
	}
	if _, err := os.Stat(filepath.Join(workDir, "review-log.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("review-log.jsonl still exists after the failed job, want it removed")
	}
}

// TestOptimizerBuilderRemovesTheWorkDirWhenTheExportFails asserts the builder itself cleans up when the
// review-log export fails after the work directory was created, so that path cannot leak the file either.
func TestOptimizerBuilderRemovesTheWorkDirWhenTheExportFails(t *testing.T) {
	ctx := context.Background()
	db, _ := newWiringDB(t)
	_, preset := seedOptimizerUserAndPreset(t, db)
	// Dropping the review table makes the export fail after the owner and the work directory were resolved.
	if err := db.Migrator().DropTable(&store.Review{}); err != nil {
		t.Fatalf("drop reviews: %v", err)
	}
	opt, err := NewOptimizer(OptimizerDeps{DB: db, Binary: "/bin/sh"})
	if err != nil {
		t.Fatalf("NewOptimizer: %v", err)
	}
	opt.root = t.TempDir()
	job := &store.Job{ID: 7, Kind: KindOptimize, TargetID: &preset.ID}

	if _, err := opt.CommandBuilder()(ctx, job, cleanupTestReporter{}); err == nil {
		t.Fatal("CommandBuilder error = nil, want the export failure")
	}
	if _, err := os.Stat(opt.jobDir(job)); !os.IsNotExist(err) {
		t.Fatalf("work dir %s still exists after the export failed, want it removed", opt.jobDir(job))
	}
}

// TestNewOptimizerClearsAStaleRoot asserts the per-pid work root from an earlier process is removed at
// startup, so a crash cannot leave an exported review log on disk indefinitely.
func TestNewOptimizerClearsAStaleRoot(t *testing.T) {
	root := filepath.Join(os.TempDir(), fmt.Sprintf("engram-optimize-%d", os.Getpid()))
	stale := filepath.Join(root, "job-1")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatalf("create stale work dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stale, "review-log.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write stale review log: %v", err)
	}

	db, _ := newWiringDB(t)
	if _, err := NewOptimizer(OptimizerDeps{DB: db}); err != nil {
		t.Fatalf("NewOptimizer: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("stale root %s still exists after NewOptimizer, want it removed", root)
	}
}
