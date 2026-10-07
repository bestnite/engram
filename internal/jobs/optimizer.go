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

// maxAdapterWeightsBytes 是适配器权重文件（weights.json）的读取上限，1 MiB。
// 理由：权重是固定 21 个 float64 的 JSON 数组，正常输出只有几百字节；适配器异常产出超大文件时
// os.ReadFile 会把整个文件读进内存再解码，内存占用不受控。1 MiB 足以容纳 21 个双精度数的
// 任何合理排版（含空白），超出即判为异常输出并让作业失败，而不是先把它读全。
const maxAdapterWeightsBytes = 1 << 20

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

// Complete 解析适配器写出的权重文件，算出优化前后的拟合指标，产出交给 FinishOptimize 的结果。
//
// 拟合指标（ROADMAP.md M9-11）：用适配器训练所用的同一份复习日志，在 preset 当前权重（旧）与
// 适配器产出的新权重下各回放一次，得到 FitBefore/FitAfter，页面据此给出真实的「改善/未改善」。
// 旧权重必须在 FinishOptimize 写回之前从 preset 读出——本函数在写回前跑，正好读到旧值。
// 指标算不出来（无目标预设、日志不可读、无任何可预测 item）时不失败：权重本身仍然有效，
// 留零值表示「不可用」，页面会隐藏结论而不是污蔑成「未改善」。
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
		// 指标是页面的锦上添花，不是作业成功的条件：记录原因，留零值让页面隐藏结论。
		o.logger.Warn("compute optimizer fit metrics failed", "job_id", job.ID, "error", fitErr)
	} else {
		result.FitBefore = before
		result.FitAfter = after
	}
	return result, nil
}

// fitMetrics 在适配器训练所用的复习日志上，用 preset 旧权重与新权重各评估一次拟合。
// 日志与 preset 都在作业工作目录被删除前读取（Complete 的 defer 负责删除）。
func (o *Optimizer) fitMetrics(ctx context.Context, job *store.Job, newWeights []float64) (store.FitMetrics, store.FitMetrics, error) {
	if job.TargetID == nil || *job.TargetID == 0 {
		return store.FitMetrics{}, store.FitMetrics{}, errors.New("optimize job has no target preset; cannot load the old weights")
	}
	// 旧权重来自 preset：FinishOptimize 会在本函数之后才写回新权重，所以这里读到的是优化前的值。
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

// readOptimizerLog 读回作业目录里的 review-log.jsonl（适配器刚训练过的那一份）。
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

// readLimitedFile 读取 path 的内容，但绝不把超过 limit 字节的文件整体读进内存。
// 先用 os.Stat 按大小做快速拒绝（适配器写完文件才被读取，此刻大小已稳定），
// 再用 io.LimitReader 兜住「stat 之后文件仍被写入」的竞态，并按实际读取字节数复核：
// 任何超过上限的情况都返回错误，而不是把内容读全。
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
	// 多读 1 字节：正好等于上限的文件能通过，任何多出的字节都能被检测到。
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file %s grew past the %d-byte limit while reading", path, limit)
	}
	return data, nil
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
