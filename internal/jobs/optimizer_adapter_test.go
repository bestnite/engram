//go:build unix

// 只在 Unix 上编译：依赖 jobs_test.go 里 unix-only 的测试脚手架（newTestRunner 等），
// 或直接以 `/bin/sh` 作为假命令。

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

// optimizerBinary 返回适配器二进制的预期绝对路径（仓库根下的 tools/optimizer）。
// 用绝对路径是因为 runProcess 会给子进程设置工作目录，相对的可执行文件路径在那个目录下
// 解析不到（实测 fork/exec 会报 no such file or directory）。
func optimizerBinary() string {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		root = filepath.Join("..", "..")
	}
	return filepath.Join(root, "tools", "optimizer", "target", "release", "optimizer")
}

// optimizerFixture 返回固定 fixture 的绝对路径。
func optimizerFixture() string {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		root = filepath.Join("..", "..")
	}
	return filepath.Join(root, "tools", "optimizer", "testdata", "review-log.jsonl")
}

// optimizerCommand 演示 internal/jobs 的命令注入契约：M9 的 CommandBuilder 只需返回
// 这样一条 Command（可执行文件 + 参数 + 工作目录），执行、超时与杀进程组都由 jobs 负责。
// 参数里把权重写到临时文件，避免与 stderr 诊断在同一 tailBuffer 里交错。
func optimizerCommand(bin, fixture, out string) Command {
	return Command{
		Name: bin,
		Args: []string{fixture, "--out", out},
		Dir:  filepath.Dir(bin),
	}
}

// TestOptimizerAdapterDeterminism 是针对 Go 侧集成测试：以命令注入方式调用
// Rust 适配器，对固定 fixture 连跑两次，断言两次权重 JSON 逐字节相同且为 21 个数字。
// 二进制未构建时跳过（CI 不装 Rust 工具链也能通过）。
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

// TestOptimizerAdapterIsARealBinary 只断言可执行文件可被 exec 调用（--version 成功），
// 用于在二进制存在但不可执行时给出清晰失败，而不是让上一条用例含糊报错。
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
