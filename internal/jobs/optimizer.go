package jobs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"example.com/engram/internal/store"
)

// 本文件是 M9-10 的生产接线：把 M9-2 的 Rust 适配器（tools/optimizer）接进作业链路。
//
// 数据流：web 用 EnqueueOptimize 建 job -> Runner 执行 Optimizer.CommandBuilder 造出的命令
// （先由 Go 侧 ExportOptimizerLog 导出该用户复习日志，再 exec 适配器读日志、写权重文件）
// -> Runner 调 Optimizer.Complete 解析权重 -> FinishOptimize 写回 job 并写回 preset。
//
// 适配器契约（tools/optimizer/src/main.rs，稳定）：`optimizer <review-log.jsonl> [--out PATH]`，
// 成功时把 21 元权重 JSON 写进 --out，诊断走 stderr，退出码 0/2/3/4。

// optimizerBinaryName 是适配器二进制的文件名（与 Cargo 的 bin 名一致）。
const optimizerBinaryName = "optimizer"

// optimizerWeightCount 是 FSRS 权重维度，用来挡住上游 API 变化造成的不完整结果。
const optimizerWeightCount = 21

// ErrOptimizerNotFound 表示适配器二进制不存在；错误消息里点名期望路径，便于运维定位。
var ErrOptimizerNotFound = errors.New("optimizer adapter not found")

// OptimizerDeps 是 Optimizer 的显式依赖。
type OptimizerDeps struct {
	// DB 必填：用于导出复习日志与解析作业目标预设的归属者。
	DB *gorm.DB
	// Logger 可选；为空时用 slog.Default()。
	Logger *slog.Logger
	// Binary 是配置的适配器路径（config.KeyOptimizerPath）；为空时按 ResolveOptimizerBinary 解析。
	Binary string
	// Executable 仅测试注入：解析「服务二进制旁」时的替身；为空时用 os.Executable()。
	Executable string
	// Now 可注入时钟；为空时用系统 UTC 时间。
	Now func() time.Time
}

// Optimizer 把一个优化作业变成「导出日志 -> 跑适配器 -> 解析权重」的命令构造器与结果解析器。
// 它无状态（除进程内工作目录），生产在 main.go 装配时构造一次，供单并发 worker 使用。
type Optimizer struct {
	db     *gorm.DB
	logger *slog.Logger
	binary string
	now    func() time.Time
	// root 是本进程的工作根目录：每个作业在自己的子目录里放 review-log.jsonl 与 weights.json。
	// 进程号参与命名，避免同机多实例（或并发测试）互相踩踏；作业结束后目录被删除。
	root string
}

// NewOptimizer 构造生产优化器接线；不启动任何 goroutine。
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

// Binary 返回实际会执行的适配器路径，供启动日志展示「生效值」。
func (o *Optimizer) Binary() string { return o.binary }

// ResolveOptimizerBinary 解析适配器路径。顺序：
//  1. configured 非空：原样使用（管理员显式指定就尊重它，缺失时由作业失败并点名该路径）；
//  2. 服务二进制旁的 optimizer（发布形态：适配器与主程序同目录分发）；executable 为空时用 os.Executable()；
//  3. 仓库构建产物 tools/optimizer/target/release/optimizer（开发/测试形态）。
//
// 都没有时返回第 2 步的路径，让失败消息指向「本该在的位置」。
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

// repoOptimizerCandidates 是开发/测试形态下的候选路径（相对当前工作目录）。
// `go test` 的工作目录是包目录（internal/jobs），因此 `../../` 指向仓库根；
// 从仓库根启动服务时第一条即可命中。
func repoOptimizerCandidates() []string {
	rel := filepath.Join("tools", optimizerBinaryName, "target", "release", optimizerBinaryName)
	return []string{rel, filepath.Join("..", "..", rel)}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// CommandBuilder 返回优化作业的 CommandBuilder：导出复习日志 -> exec 适配器。
// 适配器缺失时返回 ErrOptimizerNotFound 且消息含路径，作业随之 failed。
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
		exportErr := store.NewReviewStore(o.db).ExportOptimizerLog(ctx, ownerID, f)
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

// Complete 解析适配器写出的权重文件，产出交给 FinishOptimize 的结果。
//
// 说明：M9-5 要求的 FitBefore/FitAfter 拟合指标目前在代码里没有任何计算实现，这里
// 刻意留零值（零值即「未提供」），不臆造公式；补齐需要 DESIGN.md 决策。
func (o *Optimizer) Complete(_ context.Context, job *store.Job, _ Command, _ string) (store.OptimizeResult, error) {
	dir := o.jobDir(job)
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			o.logger.Warn("remove optimizer work dir failed", "job_id", job.ID, "dir", dir, "error", err)
		}
	}()

	raw, err := os.ReadFile(filepath.Join(dir, "weights.json"))
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
	return store.OptimizeResult{
		ReviewsUsed: used,
		Weights:     weights,
		OptimizedAt: o.now(),
	}, nil
}

// ownerUserID 从作业目标预设反查复习日志的归属者（作业只带 preset_id，日志按用户统计）。
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

// jobDir 返回某作业的工作目录（不创建）。
func (o *Optimizer) jobDir(job *store.Job) string {
	return filepath.Join(o.root, fmt.Sprintf("job-%d", job.ID))
}

// prepareWorkDir 清掉同 ID 作业的残留目录并新建空目录。
// 先清后建让「同一作业重跑」不会读到上一轮的旧权重文件。
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

// countLines 数非空行数，用作「本次训练实际使用的复习条数」。
func countLines(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var n int64
	sc := bufio.NewScanner(f)
	// 单行是 JSON 对象，放宽缓冲上限，避免超长行触发 ErrTooLong。
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
