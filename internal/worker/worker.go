// Package worker 提供后台周期 worker 的共享生命周期骨架：Start/Stop、启动即跑一轮的
// tick 循环，以及把单轮错误记成英文日志的包装。
//
// 本包只收敛这一层逐字相同的骨架；业务相位（扫什么、发什么）由调用方以 Config.RunOnce
// 注入，因此本包不 import 任何业务包，也不持有 DB、邮箱等业务依赖。
//
// 抽取背景（refactor/converge-c4 的逐字对比结论）：internal/reminder 与 internal/digest
// 的 Start / Stop / loop / runAndLog 四个方法，除接收者名与 runAndLog 的日志文案外逐字相同；
// 两者的 tick 间隔（各自 DefaultInterval = 10 分钟）与错误处理（记日志后继续、单轮失败不退出）
// 也完全一致。唯一的行为差异是失败日志文案，故用 Config.FailMessage 注入，逐字保留各自原文案。
package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// DefaultInterval 是未指定轮询间隔时的默认值（10 分钟）。
//
// 两个 C 类 worker（提醒、周报）共用同一节律；默认值的选取理由见 internal/reminder 与
// internal/digest 各自的常量注释，这里集中一处，避免两边各写一份。
const DefaultInterval = 10 * time.Minute

// Config 是 Lifecycle 的构造参数。
type Config struct {
	// RunOnce 是一轮业务扫描，由调用方提供。必填；为 nil 时 New 返回错误。
	RunOnce func(ctx context.Context) error
	// Logger 记录单轮失败。必填；为 nil 时 New 返回错误。
	Logger *slog.Logger
	// FailMessage 是单轮失败时的英文日志消息，逐字注入（见包注释）。必填；为空时 New 返回错误。
	FailMessage string
	// Interval 是轮询间隔；<=0 时用 DefaultInterval。
	Interval time.Duration
}

// Lifecycle 是周期 worker 的起停与 tick 骨架。零值不可用；用 New 构造。
type Lifecycle struct {
	runOnce     func(ctx context.Context) error
	logger      *slog.Logger
	failMessage string
	interval    time.Duration

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// New 构造 Lifecycle；不启动 worker，需再调用 Start。
func New(cfg Config) (*Lifecycle, error) {
	if cfg.RunOnce == nil {
		return nil, errors.New("worker: Config.RunOnce is required")
	}
	if cfg.Logger == nil {
		return nil, errors.New("worker: Config.Logger is required")
	}
	if cfg.FailMessage == "" {
		return nil, errors.New("worker: Config.FailMessage is required")
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Lifecycle{
		runOnce:     cfg.RunOnce,
		logger:      cfg.Logger,
		failMessage: cfg.FailMessage,
		interval:    interval,
	}, nil
}

// Start 启动唯一的 worker goroutine；重复调用幂等。ctx 取消时 worker 退出。
func (l *Lifecycle) Start(ctx context.Context) {
	l.mu.Lock()
	if l.started {
		l.mu.Unlock()
		return
	}
	l.started = true
	runCtx, cancel := context.WithCancel(ctx)
	l.cancel = cancel
	done := make(chan struct{})
	l.done = done
	l.mu.Unlock()
	go func() {
		defer close(done)
		l.loop(runCtx)
	}()
}

// Stop 优雅停止 worker：取消上下文并等待当前轮结束。可重复调用。
func (l *Lifecycle) Stop() {
	l.mu.Lock()
	if !l.started {
		l.mu.Unlock()
		return
	}
	l.started = false
	cancel := l.cancel
	done := l.done
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// loop 是 worker 主循环：启动即跑一轮（补上进程停机期间错过的窗口），随后定时轮询。
func (l *Lifecycle) loop(ctx context.Context) {
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()
	l.runAndLog(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.runAndLog(ctx)
		}
	}
}

// runAndLog 跑一轮并把查询级错误记成英文日志（worker 不能因单轮失败而退出）。
func (l *Lifecycle) runAndLog(ctx context.Context) {
	if err := l.runOnce(ctx); err != nil {
		l.logger.Error(l.failMessage, "error", err)
	}
}
