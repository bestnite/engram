// Package jobs 提供单并发的后台作业框架：jobs 表持久化、子进程执行（命令可注入）、
// 超时后杀掉整个进程组、阶段级进度与日志尾巴捕获
// （DESIGN.md §3.5、§2.2 的 jobs 表；ROADMAP.md M9-1）。
//
// 设计要点与理由：
//   - 单并发：进程内只有一个 worker 消费队列；入队时若已有未完成作业，返回
//     ErrAlreadyRunning，由 REST 层映射成 409（DESIGN.md §3.5「已有任务则 409」）。
//   - 子进程执行：训练是 CPU 密集且可能崩溃/挂死，放到独立进程里既能隔离，也能超时取消；
//     优化器算法本身不是 Go 库（M9-2 的 Rust 适配器），只能走子进程。
//   - 命令可注入：Runner 通过 CommandBuilder 取得要执行的命令；生产用 M9-10 的优化器
//     适配器（internal/jobs/optimizer.go，返回直接调用 Rust 适配器的 Command），测试注入
//     `/bin/sh -c ...`，从而不依赖真实优化器。
//   - 结果可注入：Complete 可选，命令成功退出后，由 Complete 解析命令的副作用（例如适配器
//     写出的 21 维权重文件），产出 OptimizeResult 交给 FinishOptimize 落库；未配置时走
//     通用的「退出码 0 即成功」路径。
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

// 作业 kind：与 jobs.kind 列一致，取值英文且稳定（DESIGN.md §2.2）。
const (
	// KindOptimize 是参数优化作业（当前唯一类型，DESIGN.md §3.5）。
	KindOptimize = "optimize"
)

// 作业状态机：queued -> running -> succeeded | failed（DESIGN.md §2.2）。
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// 阶段名：只做阶段级进度，不编造百分比（DESIGN.md §3.5）。取值与 jobs.stage 列一致。
const (
	StageReadLogs = "read_logs"
	StageTraining = "training"
	StageWriting  = "writing"
)

const (
	// StaleJobReason 是启动时回收残留 running 作业写入的失败原因（英文，AGENTS.md §2.1）。
	// 语义：子进程随上次进程退出必然已消失，running 只是崩溃/重启遗留的假状态（M9-7）。
	StaleJobReason = "interrupted by restart"

	// NeverStartedJobReason 是启动时回收残留 queued 作业写入的失败原因（M9-8）。
	// 语义：入队只写库并把作业放进内存队列，队列随上个进程消失，所以这些行永远不会被执行；
	// 它们却被 Store.Active 当作在途作业，会让后续 Enqueue 永久返回 409。
	NeverStartedJobReason = "job never started"

	// DefaultTimeout 是子进程的默认超时；优化训练可能跑很久，但必须有上限（M9-1 验收）。
	DefaultTimeout = 30 * time.Minute
	// DefaultLogTailLines 是落库的日志尾巴行数上限。
	DefaultLogTailLines = 40
	// maxLogTailBytes 是日志尾巴的字节上限。
	//
	// 理由：日志尾巴只服务于「轮询界面看失败原因」，训练日志可能非常长；若无限量写进
	// jobs.log_tail 这个 TEXT 列，行会随作业时长膨胀，状态查询接口也要搬运大字段。
	// 只保留末尾若干字节/行，既够定位失败，又让每一行的大小有界。
	maxLogTailBytes = 16 * 1024
	// queueDepth 是内存队列容量。单并发下同时最多一个未完成作业，容量 1 足够且让入队永不阻塞。
	queueDepth = 1
)

var (
	// ErrAlreadyRunning 表示已有未完成作业，调用方应返回 409（单并发哨兵错误）。
	ErrAlreadyRunning = errors.New("a job is already running")
	// ErrKindRequired 表示入队时未给出 kind。
	ErrKindRequired = errors.New("job kind is required")
	// ErrTimedOut 表示子进程超过配置超时后被杀死。
	ErrTimedOut = errors.New("job timed out")
	// ErrNoCommand 表示没有为作业配置可执行的命令。
	ErrNoCommand = errors.New("no command configured for job")
	// ErrNotRunning 表示要取消的作业既不在运行、也不是可取消的 queued 状态（M6-6）。
	ErrNotRunning = errors.New("job is not running")
)

// CancelReason 是管理员取消作业时写入 jobs.error 的原因（英文，AGENTS.md §2.1）。
// 它与超时/崩溃原因区分开，让管理面板能明确显示「是被人取消的」。
const CancelReason = "cancelled by admin"

// Command 描述一次子进程调用。字段由调用方注入，便于测试替换成短命令/自指二进制。
type Command struct {
	Name  string    // 可执行文件路径
	Args  []string  // 参数
	Dir   string    // 工作目录，空表示继承当前进程
	Env   []string  // 环境变量，nil 表示继承当前进程
	Stdin io.Reader // 标准输入，nil 表示空
}

// Reporter 让命令构造器上报阶段级进度（写进 jobs.stage）。
type Reporter interface {
	SetStage(stage string)
}

// CommandBuilder 为一个作业构造要执行的命令；返回错误则作业直接标记为 failed。
type CommandBuilder func(ctx context.Context, job *store.Job, rep Reporter) (Command, error)

// CompleteFunc 把命令成功退出后的副作用解析成优化结果。
//
// 命令本身只负责执行（进程退出码语义），像「适配器把权重写到哪个文件」这类知识属于
// 生产装配（M9-10 的 Optimizer）；Runner 在命令成功后调用它拿到 OptimizeResult，
// 再交给 FinishOptimize 写回 job 行与 preset。返回错误则作业标记为 failed。
type CompleteFunc func(ctx context.Context, job *store.Job, cmd Command, tail string) (store.OptimizeResult, error)

// FailureFunc 在作业被标记为 failed 之后被调用（M1-24 的 D 类管理员通知挂点）。
//
// 它在状态已经落库之后运行，因此实现只能做副作用（例如把通知写进邮件 outbox），
// 不得试图改写作业状态；它的返回值被忽略，且 panic 会被 Runner 兜住——
// 通知失败绝不能让作业失败处理本身出错（DESIGN.md §4.1 附带的「发信失败不影响触发操作）
// 同样适用于作业失败通知）；它同步运行在作业 worker 的 ctx 上。可为 nil。
type FailureFunc func(ctx context.Context, job store.Job, reason string)

// Deps 是 Runner 的显式依赖。
type Deps struct {
	// DB 是必填项。
	DB *gorm.DB
	// Store 可选；为空时由 New 用 DB 构造。
	Store *Store
	// Logger 可选；为空时用 slog.Default()。
	Logger *slog.Logger
	// Timeout 是子进程超时；<=0 时用 DefaultTimeout。
	Timeout time.Duration
	// Command 为作业构造命令；为空时作业会以 ErrNoCommand 失败（生产由 Optimizer 提供）。
	Command CommandBuilder
	// Complete 可选；命令成功退出后解析其副作用并产出优化结果。为空时只按退出码判成功。
	Complete CompleteFunc
	// OnFailure 可选；作业被标记为 failed 后调用（M1-24 的 D 类管理员通知挂点）。
	// 为空时不做任何额外动作。见 FailureFunc 的契约。
	OnFailure FailureFunc
	// Now 可注入时钟；为空时用系统 UTC 时间。
	Now func() time.Time
	// LogTailLines 是日志尾巴行数上限；<=0 时用 DefaultLogTailLines。
	LogTailLines int
}

// Runner 持有单并发 worker、作业存储与子进程执行配置。
//
// 并发模型：Enqueue 在 mu 保护下先判断是否有未完成作业，再落库并入队；只有一个
// worker goroutine 从 queue 取作业串行执行。mu 保证「判断 + 落库」原子，因此两个并发
// Enqueue 只有一个能成功，另一个拿到 ErrAlreadyRunning。
type Runner struct {
	db           *gorm.DB
	store        *Store
	logger       *slog.Logger
	timeout      time.Duration
	command      CommandBuilder
	complete     CompleteFunc
	onFailure    FailureFunc
	now          func() time.Time
	logTailLines int

	mu      sync.Mutex
	started bool
	queue   chan *store.Job

	// 运行中作业的取消句柄（M6-6）。与 mu 分开，避免 Cancel 在 execute 运行期间
	// 争用 Enqueue 的锁；currentDone 在 execute 返回前关闭，Cancel 借此等待落库完成。
	runMu         sync.Mutex
	currentID     uint64
	currentCancel context.CancelFunc
	currentDone   chan struct{}
}

// New 构造 Runner；不启动 worker，需再调用 Start。
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
		now:          now,
		logTailLines: tailLines,
		queue:        make(chan *store.Job, queueDepth),
	}, nil
}

// Start 启动唯一的 worker goroutine；重复调用是幂等的。ctx 取消时 worker 退出，
// 正在执行的子进程会被杀掉（runProcess 监听 ctx.Done）。
//
// 启动前会先调用 RecoverStale 回收上次进程遗留的未完成作业（running 与 queued，M9-7 / M9-8）：
// 否则这些残留行会让 Store.Active 永远认为「有作业在跑」，后续 Enqueue 一直返回 409。恢复失败只记
// 日志，不阻塞 worker 启动；回收逻辑本身是显式入口（RecoverStale），不是隐蔽副作用。
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

// RecoverStale 是 M9-7 / M9-8 的显式入口：把重启/崩溃前遗留的未完成作业回收为 failed，
// 并保留其 log_tail 供诊断。running 作业写 StaleJobReason，queued 作业写 NeverStartedJobReason
// （原因不同，因为只有 running 的那批真的启动过）。返回被回收的行数。
// Start 会在启动 worker 前调用一次；调用方也可在需要时显式调用。
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

// List 返回一页作业（含状态/阶段/日志尾巴）与总数，供管理面板分页展示（M6-6）。
func (r *Runner) List(ctx context.Context, limit, offset int) ([]store.Job, int64, error) {
	return r.store.List(ctx, limit, offset)
}

// Cancel 取消一个作业（M6-6）：运行中的作业会被杀掉整个进程组，随后由 execute 把它
// 落库为 failed 且 error = CancelReason；仍排队的作业则直接标记为 failed（execute 会
// 跳过已处于终态的作业，所以它不会被真正执行）。
//
// 为什么要等 currentDone：验收要求「取消后不卡、新入队能成功」。单并发判定看的是
// jobs 表里的未完成行；只有等 execute 写完 failed，再次 Enqueue 才不会再撞 409。
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

	// 不在运行：可能仍在队列里排队。直接落库为 failed，worker 取到时按终态跳过。
	job, err := r.store.ByID(ctx, id)
	if err != nil {
		return err
	}
	if job.Status == StatusFailed {
		// 已终态（多半是刚被取消）：取消是幂等的，返回成功。
		return nil
	}
	if job.Status != StatusQueued && job.Status != StatusRunning {
		return ErrNotRunning
	}
	return r.store.FinishFailed(ctx, id, r.now(), CancelReason, "")
}

// SetOnFailure 设置作业失败钩子（M1-24）；须在 Start 之前调用，避免与 worker 竞态。
// 生产装配在 cmd/engram 把它接到 web 层的管理员通知上。
func (r *Runner) SetOnFailure(fn FailureFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onFailure = fn
}

// Enqueue 建一个 queued 作业并入队；已有未完成作业时返回 ErrAlreadyRunning（HTTP 409）。
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

// loop 是唯一 worker：串行消费队列。单并发由这里和 Enqueue 的 mu 共同保证。
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

// execute 执行一个作业并驱动状态机 queued -> running -> succeeded | failed。
// 任何一步失败都必须落到 failed，避免作业永远停在 running。
func (r *Runner) execute(ctx context.Context, job *store.Job) {
	// worker 不能因单个作业 panic 而整体退出；记日志并把该作业标记为 failed。
	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Error("job worker panicked", "job_id", job.ID, "panic", rec)
			r.fail(ctx, job.ID, fmt.Sprintf("job worker panicked: %v", rec), "")
		}
	}()

	// 排队期间被取消（M6-6）：Cancel 已把该行标为 failed，这里直接跳过，不再启动子进程。
	if current, err := r.store.ByID(ctx, job.ID); err == nil && current.Status == StatusFailed {
		return
	}

	// 为本次运行建立可单独取消的上下文：管理员取消只影响这一个作业，不牵连 worker 的 ctx。
	runCtx, cancelRun := context.WithCancel(ctx)
	done := make(chan struct{})
	r.registerRun(job.ID, cancelRun, done)
	defer func() {
		r.unregisterRun(job.ID)
		close(done)
	}()

	startedAt := r.now()
	if err := r.store.MarkRunning(ctx, job.ID, startedAt); err != nil {
		r.logger.Error("mark job running failed", "job_id", job.ID, "error", err)
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
		// 管理员取消与超时/正常失败要区分：前者写 CancelReason，方便面板一眼看出是谁干的。
		msg := runErr.Error()
		if !errors.Is(runErr, ErrTimedOut) && runCtx.Err() != nil && ctx.Err() == nil {
			msg = CancelReason
		}
		r.fail(ctx, job.ID, msg, tail)
		return
	}

	// 命令成功返回即进入写回阶段：配了 Complete 的作业（M9-10 的优化作业）由它解析
	// 适配器写出的权重并交给 FinishOptimize；其余作业仍按「退出码 0 即成功」处理。
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
		}
		return
	}
	if err := r.store.FinishSucceeded(ctx, job.ID, r.now(), "", tail); err != nil {
		r.logger.Error("finish job succeeded failed", "job_id", job.ID, "error", err)
	}
}

// fail 把作业标记为 failed 并记录错误与日志尾巴；失败原因写英文（AGENTS.md §2.1）。
// 状态落库成功后再调用 OnFailure 钩子（M1-24）：钩子只做副作用，且绝不改变本次结果。
func (r *Runner) fail(ctx context.Context, id uint64, msg, tail string) {
	if err := r.store.FinishFailed(ctx, id, r.now(), msg, tail); err != nil {
		r.logger.Error("finish job failed failed", "job_id", id, "error", err)
		return
	}
	r.notifyFailure(ctx, id, msg)
}

// notifyFailure 在作业失败落库后调用 OnFailure 钩子；未配置钩子时直接返回。
// 钩子 panic 会被兜住：通知失败绝不能反过来影响作业失败处理（M1-24 验收）。
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

// registerRun 记录当前运行中作业的取消句柄；Cancel 通过它找到要杀的进程组。
func (r *Runner) registerRun(id uint64, cancel context.CancelFunc, done chan struct{}) {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	r.currentID = id
	r.currentCancel = cancel
	r.currentDone = done
}

// unregisterRun 清理取消句柄；只有当前记录仍属于该作业时才清，避免误删下一作业的状态。
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

// setStage 更新阶段；失败只记日志，不因进度写失败而中断作业。
func (r *Runner) setStage(ctx context.Context, id uint64, stage string) {
	if err := r.store.SetStage(ctx, id, stage); err != nil {
		r.logger.Error("set job stage failed", "job_id", id, "stage", stage, "error", err)
	}
}

// jobReporter 把 Reporter.SetStage 转发到 Runner，供 CommandBuilder 上报阶段。
type jobReporter struct {
	ctx    context.Context
	runner *Runner
	id     uint64
}

func (j jobReporter) SetStage(stage string) { j.runner.setStage(j.ctx, j.id, stage) }

// noCommandBuilder 是未配置 CommandBuilder 时的回退：直接以 ErrNoCommand 让作业失败。
//
// 刻意不再「重新 exec 自身并带 optimize --job」：那条自指路径依赖一个已删除的 CLI 子命令，
// 会让作业在生产里以「命令未实现」收场。生产装配必须显式传入 Optimizer.CommandBuilder()，
// 与 M9-10 的真实适配器对接；这里只负责在装配缺失时大声失败。
func noCommandBuilder(context.Context, *store.Job, Reporter) (Command, error) {
	return Command{}, ErrNoCommand
}

// HTTPStatus 把 jobs 包的哨兵错误映射成 HTTP 状态码：重复入队 -> 409，其余 -> 500。
// 409 的语义只在这里定义一处，REST 层（M9-4）直接复用，避免两处漂移。
func HTTPStatus(err error) int {
	if errors.Is(err, ErrAlreadyRunning) {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// runProcess 启动子进程并等待完成。
//
// 子进程被放进独立进程组（setProcessGroup），超时或 ctx 取消时杀掉整棵树
// （killProcessGroup）：只杀直接子进程会留下它派生的进程（例如 shell 下的后台训练
// 进程），它们会继续占 CPU 并让作业「看似结束实则仍在跑」——这正是 M9-1 要求
// 「杀整个进程组」的原因。两个动作的平台实现分别在 process_unix.go、process_windows.go。
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

// tailBuffer 是一个只保留末尾 max 字节的 io.Writer；并发写（stdout/stderr）用 mu 保护。
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

// tailLines 只保留末尾 n 行；n <= 0 时原样返回。
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
