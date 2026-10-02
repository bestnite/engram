// Package reminder 实现 C 类「复习到期提醒」的周期后台 worker（DESIGN.md §4.7；AGENTS.md M1-21）。
//
// 行为约束（DESIGN.md §4.7）：
//   - 绝不在请求路径里同步发信：一律 Enqueue，投递由 outbox worker 负责。
//   - 静默窗口按**用户本地时间** 23:00–07:00：落在窗口内的提醒**推迟**到窗口开启后的
//     下一次轮询再发（不是丢弃）；推迟后仍受「每天最多一封」约束。
//   - 每天最多一封：按用户本地复习日（与 reviews.review_day 同一时区/切点口径）判定，
//     台账见 store.ReminderLog。
//   - SMTP 未配置时不发也不报错，只记一条英文日志（C 类是可选运营邮件，没有页面要禁用）。
//
// 为什么是周期 worker 而不是 job 表：优化器的单并发约束不适用于邮件（DESIGN.md §4.7），
// 把提醒塞进 jobs 会与参数优化互相阻塞。提醒本身不是长任务，只是一个周期性的「到期扫描」，
// 因此独立起一个轻量 worker，随 serve 启动、随信号优雅停止。
//
// 「事件驱动」在这里落地为：每轮扫描「是否有到期卡」这一事件，有才发；没有到期卡不发。
package reminder

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"example.com/engram/internal/i18n"
	"example.com/engram/internal/mail"
	"example.com/engram/internal/schedule"
	"example.com/engram/internal/store"
)

const (
	// DefaultInterval 是默认轮询间隔。
	//
	// 选 10 分钟的理由：提醒是「催一下」而不是准点事件，用户感知不到分钟级差异；
	// 静默窗口以小时为粒度、每日上限以复习日为粒度，10 分钟足够在窗口开启后很快补发，
	// 又把每轮的 DB 开销压到「一条候选查询 + 每用户一两次小查询」的量级。
	DefaultInterval = 10 * time.Minute
	// QuietStartHour 是静默窗口起点（本地小时，含）。23:00 起算。
	QuietStartHour = 23
	// QuietEndHour 是静默窗口终点（本地小时，不含）。07:00 结束，07:00 整可发。
	QuietEndHour = 7
	// DefaultReviewPath 是复习页路径（提醒正文里的链接）。
	DefaultReviewPath = "/review"
)

// Enqueuer 是提醒 worker 依赖的发信入口；生产由 *mail.Outbox 满足，测试注入替身。
type Enqueuer interface {
	// Enqueue 只入队，不同步发信。
	Enqueue(ctx context.Context, m mail.Message) error
	// Configured 报告 SMTP 是否已配置。
	Configured() bool
}

// Deps 是 Reminder 的显式依赖。
type Deps struct {
	// DB 是必填项（候选查询与发送台账）。
	DB *gorm.DB
	// Outbox 是必填项的发信入口。
	Outbox Enqueuer
	// Translator 是必填项：邮件文案是用户可见文案，一律走语言包。
	Translator *i18n.Translator
	// Logger 可选；为空时用 slog.Default()。
	Logger *slog.Logger
	// Now 可注入时钟；为空时用系统 UTC 时间。静默窗口与每日上限的测试靠它控制。
	Now func() time.Time
	// Interval 是轮询间隔；<=0 时用 DefaultInterval。
	Interval time.Duration
	// BaseURL 是站点根地址，用于拼复习页链接；为空时链接为相对路径。
	BaseURL string
	// ReviewPath 覆盖复习页路径；为空时用 DefaultReviewPath。
	ReviewPath string
}

// Reminder 持有后台 worker 与配置。
type Reminder struct {
	db         *gorm.DB
	outbox     Enqueuer
	translator *i18n.Translator
	logger     *slog.Logger
	now        func() time.Time
	interval   time.Duration
	baseURL    string
	reviewPath string

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// New 构造 Reminder；不启动 worker，需再调用 Start。
func New(deps Deps) (*Reminder, error) {
	if deps.DB == nil {
		return nil, errors.New("reminder: Deps.DB is required")
	}
	if deps.Outbox == nil {
		return nil, errors.New("reminder: Deps.Outbox is required")
	}
	if deps.Translator == nil {
		return nil, errors.New("reminder: Deps.Translator is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	interval := deps.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	path := strings.TrimSpace(deps.ReviewPath)
	if path == "" {
		path = DefaultReviewPath
	}
	return &Reminder{
		db:         deps.DB,
		outbox:     deps.Outbox,
		translator: deps.Translator,
		logger:     logger,
		now:        now,
		interval:   interval,
		baseURL:    strings.TrimRight(strings.TrimSpace(deps.BaseURL), "/"),
		reviewPath: path,
	}, nil
}

// Start 启动唯一的 worker goroutine；重复调用幂等。ctx 取消时 worker 退出。
func (r *Reminder) Start(ctx context.Context) {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	done := make(chan struct{})
	r.done = done
	r.mu.Unlock()
	go func() {
		defer close(done)
		r.loop(runCtx)
	}()
}

// Stop 优雅停止 worker：取消上下文并等待当前轮结束。可重复调用。
func (r *Reminder) Stop() {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return
	}
	r.started = false
	cancel := r.cancel
	done := r.done
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// loop 是 worker 主循环：启动即跑一轮（补上进程停机期间错过的窗口），随后定时轮询。
func (r *Reminder) loop(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	r.runAndLog(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runAndLog(ctx)
		}
	}
}

// runAndLog 跑一轮并把查询级错误记成英文日志（worker 不能因单轮失败而退出）。
func (r *Reminder) runAndLog(ctx context.Context) {
	if err := r.RunOnce(ctx); err != nil {
		r.logger.Error("review reminder run failed", "error", err)
	}
}

// RunOnce 执行一轮到期扫描：对每个有到期卡的活跃用户判定是否应发提醒。
// 导出供测试与运维手动触发；返回的 error 只表示查询/入队层面的失败，单个用户的失败
// 记日志后继续处理其余用户。
func (r *Reminder) RunOnce(ctx context.Context) error {
	if !r.outbox.Configured() {
		// C 类是可选运营邮件：SMTP 未配置时静默跳过，不报错，但要留下英文日志。
		r.logger.Info("review reminder skipped: smtp not configured")
		return nil
	}
	now := r.now().UTC()
	candidates, err := store.ReminderCandidates(ctx, r.db, now)
	if err != nil {
		return err
	}
	for _, c := range candidates {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := r.maybeSend(ctx, c, now); err != nil {
			r.logger.Error("review reminder failed", "user_id", c.ID, "error", err)
		}
	}
	return nil
}

// maybeSend 判定并（在满足条件时）入队一封提醒。
//
// 判定顺序（每一道都必须在入队前通过）：
//  1. 静默窗口：落在用户本地 23:00–07:00 就整轮跳过，**不记录台账**——这样窗口开启后的
//     下一次轮询会重新评估并补发，即「推迟到窗口开启再发」。
//  2. 每日上限：用户本地复习日已发过就不再发。
//  3. 偏好门禁：C 类默认关，用户关掉后立即生效（每轮现读偏好，不缓存）。
//
// 台账只在 Enqueue 成功后才写，避免入队失败却把当天额度用掉。
func (r *Reminder) maybeSend(ctx context.Context, c store.ReminderCandidate, now time.Time) error {
	loc := loadLocation(c.Timezone)
	if InQuietHours(now.In(loc)) {
		return nil
	}
	cutoff := normalizedCutoff(c.DayCutoffHour)
	day := schedule.ReviewDay(now, loc, cutoff)

	sent, err := store.HasReminderBeenSent(ctx, r.db, c.ID, day)
	if err != nil {
		return err
	}
	if sent {
		return nil
	}

	choices, err := store.NewEmailPrefStore(r.db).Choices(ctx, c.ID)
	if err != nil {
		return err
	}
	if !mail.ResolveEnabled(choices, mail.TypeReviewReminder) {
		return nil
	}

	if err := r.outbox.Enqueue(ctx, r.message(c)); err != nil {
		return err
	}
	if err := store.RecordReminderSent(ctx, r.db, c.ID, day, now); err != nil {
		return err
	}
	r.logger.Info("review reminder enqueued", "user_id", c.ID, "review_day", day, "due_count", c.DueCount)
	return nil
}

// message 按用户语言组装提醒邮件。
func (r *Reminder) message(c store.ReminderCandidate) mail.Message {
	lc := r.translator.Localizer(r.translator.Pick("", c.Locale, ""))
	subject := lc.T("mail.reminder.subject")
	body := lc.Tf("mail.reminder.body", map[string]any{
		"count": c.DueCount,
		"url":   r.reviewURL(),
	})
	return mail.Message{
		To:       c.Email,
		Type:     string(mail.TypeReviewReminder),
		Subject:  subject,
		TextBody: body,
	}
}

// reviewURL 拼复习页链接；未配置 BaseURL 时退化成相对路径。
func (r *Reminder) reviewURL() string {
	return r.baseURL + r.reviewPath
}

// InQuietHours 报告本地时间是否落在静默窗口 [23:00, 07:00) 内。
// 边界：23:00 与 06:59 属于窗口；07:00 整已可发。
func InQuietHours(local time.Time) bool {
	h := local.Hour()
	return h >= QuietStartHour || h < QuietEndHour
}

// loadLocation 解析 IANA 时区；为空或解析失败时退回 UTC（与 schedule 的口径一致，
// 不因一个坏时区名阻塞提醒）。复习日的计算仍复用 schedule.ReviewDay。
func loadLocation(tz string) *time.Location {
	if strings.TrimSpace(tz) != "" {
		if loaded, err := time.LoadLocation(tz); err == nil {
			return loaded
		}
	}
	return time.UTC
}

// normalizedCutoff 与 schedule 的默认规则一致：0 或越界视为未设置，用默认 4。
func normalizedCutoff(hour int) int {
	if hour < 0 || hour > 23 || hour == 0 {
		return schedule.DefaultDayCutoffHour
	}
	return hour
}

// 让编译期确认 *mail.Outbox 满足 Enqueuer（契约漂移时在这里报错，而不是在装配处）。
var _ Enqueuer = (*mail.Outbox)(nil)
