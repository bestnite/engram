package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// Outbox 是邮件队列与后台 worker（DESIGN.md §4.7）。
//
// 契约（M1-17）：
//   - Enqueue 只写 outbox 队列表并唤醒 worker，绝不在请求路径里同步发信。
//   - SMTP 未配置时 Enqueue 返回 ErrNotConfigured，不静默丢弃。
//   - 投递失败由 worker 带退避重试，最后一次错误与尝试次数可从管理面板读到。
//
// 并发模型：单个 worker goroutine 串行消费；没有"单并发"的语义约束——优化器的单并发
// 不适用于邮件，否则一次参数优化会把所有邮件堵在后面（DESIGN.md §4.7）。

// RetryPolicy 控制退避重试：第 n 次失败后等待 BaseDelay*2^(n-1)，上限 MaxDelay；
// 尝试次数达到 MaxAttempts 后标记为永久失败。
type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// DefaultRetryPolicy 是生产默认：最多 5 次，1s 起步，最长等 5 分钟。
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 5, BaseDelay: time.Second, MaxDelay: 5 * time.Minute}
}

// backoff 返回第 attempts 次失败后应等待的时长。
func (p RetryPolicy) backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := p.BaseDelay
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= p.MaxDelay {
			return p.MaxDelay
		}
	}
	if delay > p.MaxDelay {
		return p.MaxDelay
	}
	return delay
}

// Deps 是 Outbox 的显式依赖。
type Deps struct {
	// DB 是必填项（outbox 队列表）。
	DB *gorm.DB
	// Secrets 用于解密 SMTP 口令；为空时读不到加密口令（视为未配置认证）。
	Secrets *store.SecretCodec
	// Logger 可选；为空时用 slog.Default()。
	Logger *slog.Logger
	// Sender 是投递传输；为空时用 SMTPSender{}。测试注入替身以避免真实 SMTP。
	Sender Sender
	// LookupEnv 可注入环境变量替身；为空时用 os.LookupEnv。
	LookupEnv func(string) (string, bool)
	// Now 可注入时钟；为空时用系统 UTC 时间。
	Now func() time.Time
	// Retry 是退避策略；零值时用 DefaultRetryPolicy()。
	Retry RetryPolicy
	// PollInterval 是 worker 轮询间隔；<=0 时用 DefaultPollInterval。
	PollInterval time.Duration
	// BatchSize 是每轮最多投递的邮件数；<=0 时用 DefaultBatchSize。
	BatchSize int
}

const (
	// DefaultPollInterval 是 worker 的默认轮询间隔。
	DefaultPollInterval = 5 * time.Second
	// DefaultBatchSize 是每轮默认投递批量。
	DefaultBatchSize = 20
)

// Outbox 持有队列、配置解析器与后台 worker。
type Outbox struct {
	db       *gorm.DB
	resolver *Resolver
	sender   Sender
	logger   *slog.Logger
	now      func() time.Time
	retry    RetryPolicy
	poll     time.Duration
	batch    int

	// wake 是 Enqueue 唤醒 worker 的信号；容量 1，非阻塞发送。
	wake chan struct{}

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewOutbox 构造 Outbox；不启动 worker，需再调用 Start。
// 签名按接口契约固定为 *Outbox（不返回 error）：依赖由 cmd/engram 在装配期保证齐备，
// DB 缺失由 Enqueue/Start 在运行时显式报错，而不是让构造函数多一条调用方都要处理的分支。
func NewOutbox(deps Deps) *Outbox {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	sender := deps.Sender
	if sender == nil {
		sender = SMTPSender{}
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	retry := deps.Retry
	if retry.MaxAttempts <= 0 {
		retry = DefaultRetryPolicy()
	}
	if retry.BaseDelay <= 0 {
		retry.BaseDelay = time.Second
	}
	if retry.MaxDelay <= 0 {
		retry.MaxDelay = 5 * time.Minute
	}
	poll := deps.PollInterval
	if poll <= 0 {
		poll = DefaultPollInterval
	}
	batch := deps.BatchSize
	if batch <= 0 {
		batch = DefaultBatchSize
	}
	return &Outbox{
		db:       deps.DB,
		resolver: NewResolver(deps.DB, deps.Secrets),
		sender:   sender,
		logger:   logger,
		now:      now,
		retry:    retry,
		poll:     poll,
		batch:    batch,
		wake:     make(chan struct{}, 1),
	}
}

// Configured 报告 SMTP 是否已配置。未配置时依赖邮件的流程必须禁用并说明原因。
// 它每次现读配置（settings 表可能刚被管理员改过），因此没有 context 参数。
func (o *Outbox) Configured() bool {
	_, ok, err := o.resolver.Config(context.Background())
	if err != nil {
		o.logger.Error("mail: resolve smtp config failed", "error", err)
		return false
	}
	return ok
}

// Enqueue 把一封邮件写进 outbox 队列并唤醒 worker。
//
// 只写队列表，不在请求路径发信：投递由 worker 完成，失败不回传给调用方。
// SMTP 未配置时返回 ErrNotConfigured（不静默丢弃）。
func (o *Outbox) Enqueue(ctx context.Context, m Message) error {
	if o.db == nil {
		return errors.New("mail: outbox database is required")
	}
	if strings.TrimSpace(m.To) == "" {
		return errors.New("mail: message recipient is required")
	}
	if _, ok, err := o.resolver.Config(ctx); err != nil {
		return fmt.Errorf("mail: resolve smtp config: %w", err)
	} else if !ok {
		return ErrNotConfigured
	}
	headers := m.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	encoded, err := json.Marshal(headers)
	if err != nil {
		return fmt.Errorf("mail: encode headers: %w", err)
	}
	row := &store.OutboxMessage{
		To:          strings.TrimSpace(m.To),
		Type:        m.Type,
		Subject:     m.Subject,
		TextBody:    m.TextBody,
		HTMLBody:    m.HTMLBody,
		HeadersJSON: string(encoded),
	}
	if err := store.EnqueueOutboxMessage(ctx, o.db, row, o.now()); err != nil {
		return err
	}
	o.notify()
	return nil
}

// notify 非阻塞地唤醒 worker；已有待处理信号时直接返回。
func (o *Outbox) notify() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

// Start 启动唯一的 worker goroutine；重复调用是幂等的。ctx 取消时 worker 退出。
// 没有数据库时直接返回：没有队列表可投递，启动 worker 只会在轮询里反复报错。
func (o *Outbox) Start(ctx context.Context) {
	if o.db == nil {
		o.logger.Error("mail: outbox has no database; worker not started")
		return
	}
	o.mu.Lock()
	if o.started {
		o.mu.Unlock()
		return
	}
	o.started = true
	runCtx, cancel := context.WithCancel(ctx)
	o.cancel = cancel
	done := make(chan struct{})
	o.done = done
	o.mu.Unlock()
	go func() {
		defer close(done)
		o.loop(runCtx)
	}()
}

// Stop 优雅停止 worker：取消上下文并等待当前轮结束。可重复调用。
func (o *Outbox) Stop() {
	o.mu.Lock()
	if !o.started {
		o.mu.Unlock()
		return
	}
	o.started = false
	cancel := o.cancel
	done := o.done
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// loop 是 worker 主循环：定时轮询 + 入队唤醒。
func (o *Outbox) loop(ctx context.Context) {
	ticker := time.NewTicker(o.poll)
	defer ticker.Stop()
	// 启动即尝试一次：可能上次进程留下了到期的 pending 邮件。
	o.drain(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-o.wake:
			o.drain(ctx)
		case <-ticker.C:
			o.drain(ctx)
		}
	}
}

// drain 投递所有到期邮件。SMTP 未配置时直接返回：pending 邮件留在队列里等配置补齐，
// 而不是被误标为失败（否则一次配置缺失会把积压邮件全部烧掉）。
func (o *Outbox) drain(ctx context.Context) {
	if _, ok, err := o.resolver.Config(ctx); err != nil {
		o.logger.Error("mail: resolve smtp config failed", "error", err)
		return
	} else if !ok {
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		rows, err := store.DueOutboxMessages(ctx, o.db, o.now(), o.batch)
		if err != nil {
			o.logger.Error("mail: load due outbox messages failed", "error", err)
			return
		}
		if len(rows) == 0 {
			return
		}
		for _, row := range rows {
			if ctx.Err() != nil {
				return
			}
			o.deliver(ctx, row)
		}
		if len(rows) < o.batch {
			return
		}
	}
}

// deliver 投递一封邮件并按结果写回状态：成功 -> sent；失败 -> 退避重排或永久失败。
func (o *Outbox) deliver(ctx context.Context, row store.OutboxMessage) {
	cfg, ok, err := o.resolver.Config(ctx)
	if err != nil {
		o.logger.Error("mail: resolve smtp config failed", "outbox_id", row.ID, "error", err)
		return
	}
	if !ok {
		return
	}
	sendErr := o.sender.Send(ctx, cfg, messageFromRow(row))
	attempts := row.Attempts + 1
	if sendErr == nil {
		if err := store.MarkOutboxSent(ctx, o.db, row.ID, o.now()); err != nil {
			o.logger.Error("mail: mark outbox sent failed", "outbox_id", row.ID, "error", err)
		}
		return
	}
	o.logger.Warn("mail: delivery failed", "outbox_id", row.ID, "attempts", attempts, "error", sendErr)
	if attempts >= o.retry.MaxAttempts {
		if err := store.MarkOutboxFailed(ctx, o.db, row.ID, attempts, sendErr.Error(), o.now()); err != nil {
			o.logger.Error("mail: mark outbox failed failed", "outbox_id", row.ID, "error", err)
		}
		return
	}
	next := o.now().Add(o.retry.backoff(attempts))
	if err := store.MarkOutboxRetry(ctx, o.db, row.ID, attempts, next, sendErr.Error(), o.now()); err != nil {
		o.logger.Error("mail: reschedule outbox failed", "outbox_id", row.ID, "error", err)
	}
}

// messageFromRow 把 outbox 行还原成 Message（供投递）。
func messageFromRow(row store.OutboxMessage) Message {
	headers := map[string]string{}
	if strings.TrimSpace(row.HeadersJSON) != "" {
		_ = json.Unmarshal([]byte(row.HeadersJSON), &headers)
	}
	return Message{
		To:       row.To,
		Type:     row.Type,
		Subject:  row.Subject,
		TextBody: row.TextBody,
		HTMLBody: row.HTMLBody,
		Headers:  headers,
	}
}
