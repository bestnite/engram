//go:build unix

// Compiles only on Unix: it depends on the unix-only test scaffolding in jobs_test.go (newTestRunner and others),
// or uses `/bin/sh` directly as the fake command.

package jobs

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// optimizerBinary returns the expected absolute path of the adapter binary (tools/optimizer under the repository root).
// An absolute path is used because runProcess sets a working directory for the subprocess, and a relative executable path
// cannot be resolved in that directory (in practice fork/exec reports no such file or directory).
func optimizerBinary() string {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		root = filepath.Join("..", "..")
	}
	return filepath.Join(root, "tools", "optimizer", "target", "release", "optimizer")
}

// optimizerFixture returns the absolute path of the fixed fixture.
func optimizerFixture() string {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		root = filepath.Join("..", "..")
	}
	return filepath.Join(root, "tools", "optimizer", "testdata", "review-log.jsonl")
}

// optimizerCommand demonstrates the command injection contract of internal/jobs: M9's CommandBuilder only needs to return
// a Command like this (executable + arguments + working directory); execution, timeout and process-group killing are all jobs' job.
// The arguments write the weights to a temporary file, so they do not interleave with stderr diagnostics in the same tailBuffer.
func optimizerCommand(bin, fixture, out string) Command {
	return Command{
		Name: bin,
		Args: []string{fixture, "--out", out},
		Dir:  filepath.Dir(bin),
	}
}

// TestOptimizerAdapterDeterminism is the Go-side integration test: it invokes the
// Rust adapter through command injection, runs it twice on the fixed fixture, and asserts both weight JSON outputs are byte-identical and consist of 21 numbers.
// It skips when the binary is not built (so CI passes without a Rust toolchain).
func TestOptimizerAdapterDeterminism(t *testing.T) {
	bin := optimizerBinary()
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("optimizer binary not built at %s; run `cargo build --release` in tools/optimizer", bin)
	}
	fixture := optimizerFixture()
	if _, err := os.Stat(fixture); err != nil {
		t.Fatalf("fixture missing: %v", err)
	}

	dir := t.TempDir()
	outs := []string{filepath.Join(dir, "weights-1.json"), filepath.Join(dir, "weights-2.json")}

	for i, out := range outs {
		cmd := optimizerCommand(bin, fixture, out)
		tail, err := runProcess(context.Background(), cmd, 2*time.Minute, maxLogTailBytes)
		if err != nil {
			t.Fatalf("adapter run %d failed: %v\n%s", i+1, err, tail)
		}
		if !strings.Contains(tail, "wrote 21 weights") {
			t.Fatalf("adapter run %d did not report 21 weights; stderr/stdout tail:\n%s", i+1, tail)
		}
	}

	first, err := os.ReadFile(outs[0])
	if err != nil {
		t.Fatalf("read first output: %v", err)
	}
	second, err := os.ReadFile(outs[1])
	if err != nil {
		t.Fatalf("read second output: %v", err)
	}

	if string(first) != string(second) {
		t.Fatalf("weights differ between consecutive runs:\nfirst:  %s\nsecond: %s", first, second)
	}

	var weights []float64
	if err := json.Unmarshal(first, &weights); err != nil {
		t.Fatalf("weights are not a JSON array of numbers: %v\n%s", err, first)
	}
	if len(weights) != 21 {
		t.Fatalf("expected 21 weights, got %d: %s", len(weights), first)
	}
}

// TestOptimizerAdapterIsARealBinary only asserts the executable can be exec'd (--version succeeds),
// so that a binary that exists but is not executable gives a clear failure instead of making the previous case fail vaguely.
func TestOptimizerAdapterIsARealBinary(t *testing.T) {
	bin := optimizerBinary()
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("optimizer binary not built at %s", bin)
	}
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("--version failed: %v\n%s", err, out)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(out)), "optimizer ") {
		t.Fatalf("unexpected --version output: %q", out)
	}
}
