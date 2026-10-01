// Package jobs 提供单并发的后台作业框架：jobs 表持久化、子进程执行（命令可注入）、
// 超时后杀掉整个进程组、阶段级进度与日志尾巴捕获
// （DESIGN.md §3.5、§2.2 的 jobs 表；AGENTS.md §5 M9-1）。
//
// 设计要点与理由：
//   - 单并发：进程内只有一个 worker 消费队列；入队时若已有未完成作业，返回
//     ErrAlreadyRunning，由 REST 层映射成 409（DESIGN.md §3.5「已有任务则 409」）。
//   - 子进程执行：训练是 CPU 密集且可能崩溃/挂死，放到独立进程里既能隔离，也能超时取消；
//     优化器算法本身不是 Go 库（M9-2 的 Rust 适配器），只能走子进程。
//   - 命令可注入：Runner 通过 CommandBuilder 取得要执行的命令；生产用自指二进制
//     （`optimize --job <id>`），测试注入 `/bin/sh -c ...`，从而不依赖真实优化器。
package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gorm.io/gorm"

	"example.com/flashcard/internal/store"
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
)

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
	// Command 为作业构造命令；为空时用 DefaultCommandBuilder（自指二进制）。
	Command CommandBuilder
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
	now          func() time.Time
	logTailLines int

	mu      sync.Mutex
	started bool
	queue   chan *store.Job
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
		command = DefaultCommandBuilder()
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
		now:          now,
		logTailLines: tailLines,
		queue:        make(chan *store.Job, queueDepth),
	}, nil
}

// Start 启动唯一的 worker goroutine；重复调用是幂等的。ctx 取消时 worker 退出，
// 正在执行的子进程会被杀掉（runProcess 监听 ctx.Done）。
func (r *Runner) Start(ctx context.Context) {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	r.mu.Unlock()
	go r.loop(ctx)
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
	out, runErr := runProcess(ctx, cmd, r.timeout, maxLogTailBytes)
	tail := tailLines(out, r.logTailLines)
	if runErr != nil {
		r.fail(ctx, job.ID, runErr.Error(), tail)
		return
	}

	// 命令成功返回即进入写回阶段（真正写回由 M9-2/M9-5 在适配器与阈值逻辑里完成）。
	r.setStage(ctx, job.ID, StageWriting)
	if err := r.store.FinishSucceeded(ctx, job.ID, r.now(), "", tail); err != nil {
		r.logger.Error("finish job succeeded failed", "job_id", job.ID, "error", err)
	}
}

// fail 把作业标记为 failed 并记录错误与日志尾巴；失败原因写英文（AGENTS.md §2.1）。
func (r *Runner) fail(ctx context.Context, id uint64, msg, tail string) {
	if err := r.store.FinishFailed(ctx, id, r.now(), msg, tail); err != nil {
		r.logger.Error("finish job failed failed", "job_id", id, "error", err)
	}
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

// DefaultCommandBuilder 返回优化作业的默认命令：重新 exec 自身二进制并带
// `optimize --job <id>`（DESIGN.md §3.5：由 web 侧建 job 后 fork 出来执行）。
// M9-2 提供 Rust 适配器后，把它换成一个直接调用适配器的 CommandBuilder 即可。
func DefaultCommandBuilder() CommandBuilder {
	return func(_ context.Context, job *store.Job, _ Reporter) (Command, error) {
		self, err := os.Executable()
		if err != nil {
			return Command{}, fmt.Errorf("locate own executable: %w", err)
		}
		return Command{
			Name: self,
			Args: []string{"optimize", "--job", strconv.FormatUint(job.ID, 10)},
			Dir:  filepath.Dir(self),
		}, nil
	}
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
// 子进程用 Setpgid 单独成组，超时或 ctx 取消时向整个进程组发 SIGKILL：只杀直接子进程
// 会留下它派生的进程（例如 shell 下的后台训练进程），它们会继续占 CPU 并让作业「看似
// 结束实则仍在跑」——这正是 M9-1 要求「杀整个进程组」的原因。
func runProcess(ctx context.Context, c Command, timeout time.Duration, maxBytes int) (string, error) {
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	cmd.Stdin = c.Stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

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
		killGroup(cmd.Process.Pid)
		<-done
		return tail.String(), fmt.Errorf("%w after %s", ErrTimedOut, timeout)
	case <-ctx.Done():
		killGroup(cmd.Process.Pid)
		<-done
		return tail.String(), fmt.Errorf("job canceled: %w", ctx.Err())
	}
}

// killGroup 向进程组发送 SIGKILL；Setpgid 让子进程 PID 成为组长，故负 PID 即整组。
func killGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
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
