// Package digest 实现 C 类「每周学习摘要」的周期后台 worker（ROADMAP.md M1-23；DESIGN.md §4.7）。
//
// 行为约束（DESIGN.md §4.7）：
//   - 摘要默认**每周**一次（不是每日），按用户本地时区成周；可关闭（C 类默认关）。
//   - 每人每周**恰好一封**：台账见 store.DigestLog，主键 (user_id, week_start) 是跨进程重启
//     仍成立最后防线——进程内计数器一重启就归零，重启后当周会再发一封。
//   - 绝不在请求路径里同步发信：一律 Enqueue，投递由 outbox worker 负责。
//   - 落在静默窗口（本地 23:00–07:00）时整轮跳过、不记台账，窗口开启后的下一轮再发。
//   - SMTP 未配置时不发也不报错，只记一条英文日志。
//   - 摘要是可选类型，带 RFC 8058 一键退订头（M1-22）。
//
// 数字口径：摘要展示的每个数字都直接来自 internal/store 的 StatsStore 聚合方法（与统计页
// M7-3 是同一批调用），handler 与 worker 都不写统计 SQL。窗口与统计页的「近 7 日」一致：
// [today-6, today]（review_day 闭区间）。
//
// 为什么是独立 worker 而不是复用提醒 worker：两者节律不同（日 vs 周）、候选集不同
// （有到期卡的人 vs 所有可收信的人）、文案与去重台账都不同；强塞进一个 worker 会让
// 「提醒」与「摘要」的判定互相纠缠。两者都只是周期性小扫描，不占优化器的单并发 jobs。
package digest

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/reminder"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

const (
	// DefaultInterval 是默认轮询间隔。摘要是周粒度，10 分钟一轮只是为了让「新的一周开始」
	// 与「静默窗口结束」能在分钟级内被补上，DB 开销退化成一条候选查询。
	DefaultInterval = 10 * time.Minute
	// DefaultStatsPath 是统计页路径（摘要正文里的回顾链接）。
	DefaultStatsPath = "/stats"
)

// Enqueuer 是摘要 worker 依赖的发信入口；生产由 *mail.Outbox 满足，测试注入替身。
type Enqueuer interface {
	// Enqueue 只入队，不同步发信。
	Enqueue(ctx context.Context, m mail.Message) error
	// Configured 报告 SMTP 是否已配置。
	Configured() bool
}

// Deps 是 Worker 的显式依赖。
type Deps struct {
	// DB 是必填项（候选查询与发送台账）。
	DB *gorm.DB
	// Outbox 是必填项的发信入口。
	Outbox Enqueuer
	// Translator 是必填项：邮件文案是用户可见文案，一律走语言包。
	Translator *i18n.Translator
	// Logger 可选；为空时用 slog.Default()。
	Logger *slog.Logger
	// Now 可注入时钟；为空时用系统 UTC 时间。
	Now func() time.Time
	// Interval 是轮询间隔；<=0 时用 DefaultInterval。
	Interval time.Duration
	// BaseURL 是站点根地址，用于拼统计页与退订链接；为空时退化成相对路径。
	BaseURL string
	// StatsPath 覆盖统计页路径；为空时用 DefaultStatsPath。
	StatsPath string
	// Tokens 签发一键退订令牌（M1-22）；为空时邮件不带退订头。
	Tokens *auth.ActionTokenService
}

// Worker 持有后台 worker 与配置。
type Worker struct {
	db         *gorm.DB
	outbox     Enqueuer
	translator *i18n.Translator
	logger     *slog.Logger
	now        func() time.Time
	interval   time.Duration
	baseURL    string
	statsPath  string
	tokens     *auth.ActionTokenService

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// New 构造 Worker；不启动 worker，需再调用 Start。
func New(deps Deps) (*Worker, error) {
	if deps.DB == nil {
		return nil, errors.New("digest: Deps.DB is required")
	}
	if deps.Outbox == nil {
		return nil, errors.New("digest: Deps.Outbox is required")
	}
	if deps.Translator == nil {
		return nil, errors.New("digest: Deps.Translator is required")
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
	path := strings.TrimSpace(deps.StatsPath)
	if path == "" {
		path = DefaultStatsPath
	}
	return &Worker{
		db:         deps.DB,
		outbox:     deps.Outbox,
		translator: deps.Translator,
		logger:     logger,
		now:        now,
		interval:   interval,
		baseURL:    strings.TrimRight(strings.TrimSpace(deps.BaseURL), "/"),
		statsPath:  path,
		tokens:     deps.Tokens,
	}, nil
}

// Start 启动唯一的 worker goroutine；重复调用幂等。ctx 取消时 worker 退出。
func (w *Worker) Start(ctx context.Context) {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return
	}
	w.started = true
	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	done := make(chan struct{})
	w.done = done
	w.mu.Unlock()
	go func() {
		defer close(done)
		w.loop(runCtx)
	}()
}

// Stop 优雅停止 worker：取消上下文并等待当前轮结束。可重复调用。
func (w *Worker) Stop() {
	w.mu.Lock()
	if !w.started {
		w.mu.Unlock()
		return
	}
	w.started = false
	cancel := w.cancel
	done := w.done
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// loop 是 worker 主循环：启动即跑一轮，随后定时轮询。
func (w *Worker) loop(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	w.runAndLog(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runAndLog(ctx)
		}
	}
}

// runAndLog 跑一轮并把查询级错误记成英文日志（worker 不能因单轮失败而退出）。
func (w *Worker) runAndLog(ctx context.Context) {
	if err := w.RunOnce(ctx); err != nil {
		w.logger.Error("weekly digest run failed", "error", err)
	}
}

// RunOnce 执行一轮扫描：对每个可收信的活跃用户判定是否应发本周摘要。
// 导出供测试与运维手动触发；单个用户的失败记日志后继续处理其余用户。
func (w *Worker) RunOnce(ctx context.Context) error {
	if !w.outbox.Configured() {
		w.logger.Info("weekly digest skipped: smtp not configured")
		return nil
	}
	now := w.now().UTC()
	candidates, err := store.DigestCandidates(ctx, w.db)
	if err != nil {
		return err
	}
	for _, c := range candidates {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := w.maybeSend(ctx, c, now); err != nil {
			w.logger.Error("weekly digest failed", "user_id", c.ID, "error", err)
		}
	}
	return nil
}

// maybeSend 判定并（在满足条件时）入队一封摘要。
//
// 判定顺序（每一道都必须在入队前通过）：
//  1. 静默窗口：落在用户本地 23:00–07:00 就整轮跳过，**不记录台账**——窗口开启后的下一轮会重新评估并补发。
//  2. 每周上限：用户本地周起始日（周一）已发过就不再发。
//  3. 偏好门禁：C 类默认关，用户关掉后立即生效（每轮现读偏好，不缓存）。
//
// 台账只在 Enqueue 成功后才写，避免入队失败却把本周额度用掉。
func (w *Worker) maybeSend(ctx context.Context, c store.DigestCandidate, now time.Time) error {
	loc := loadLocation(c.Timezone)
	if reminder.InQuietHours(now.In(loc)) {
		return nil
	}
	cutoff := normalizedCutoff(c.DayCutoffHour)
	day := schedule.ReviewDay(now, loc, cutoff)
	weekStart := weekStartOf(day)

	sent, err := store.HasDigestBeenSent(ctx, w.db, c.ID, weekStart)
	if err != nil {
		return err
	}
	if sent {
		return nil
	}

	choices, err := store.NewEmailPrefStore(w.db).Choices(ctx, c.ID)
	if err != nil {
		return err
	}
	if !mail.ResolveEnabled(choices, mail.TypeStudyDigest) {
		return nil
	}

	stats, err := ComputeStats(ctx, w.db, c.ID, now, loc, cutoff)
	if err != nil {
		return err
	}
	if err := w.outbox.Enqueue(ctx, w.message(ctx, c, stats)); err != nil {
		return err
	}
	if err := store.RecordDigestSent(ctx, w.db, c.ID, weekStart, now); err != nil {
		return err
	}
	w.logger.Info("weekly digest enqueued", "user_id", c.ID, "week_start", weekStart, "reviews", stats.Reviewed)
	return nil
}

// DigestStats 是一封周报展示的数字。每个字段都直接来自 StatsStore 的聚合方法。
type DigestStats struct {
	// WindowFrom / WindowTo 是复习数窗口（review_day 闭区间），与统计页「近 7 日」一致。
	WindowFrom string
	WindowTo   string
	// Reviewed 是窗口内的复习次数（= ReviewVolume.Last7Days）。
	Reviewed int64
	// PassRate 是统计页同口径的通过率/留存率（= RetentionByStability.Rate，0–1）。
	PassRate float64
	// StreakCurrent 是到「今天」为止未中断的连续复习天数（= Streak.Current）。
	StreakCurrent int
	// NewCards 是窗口内新引入的卡数（LearningCurve 的 New 之和）。
	NewCards int64
	// DueNow 是当前到期（含逾期）的卡数（= DueForecast.Today）。
	DueNow int64
}

// ComputeStats 用统计服务算出摘要数字。这是摘要数字的唯一来源：它与统计页 statsData 调用
// 同一批 StatsStore 方法、同一复习日窗口，因此两边不会分歧（验收③）。
func ComputeStats(ctx context.Context, db *gorm.DB, userID uint64, now time.Time, loc *time.Location, cutoffHour int) (DigestStats, error) {
	today := schedule.ReviewDay(now, loc, cutoffHour)
	from := shiftDay(today, -6)

	stats := store.NewStatsStore(db)
	volume, err := stats.ReviewVolume(ctx, userID, today)
	if err != nil {
		return DigestStats{}, err
	}
	retention, err := stats.RetentionByStability(ctx, userID, 0)
	if err != nil {
		return DigestStats{}, err
	}
	streak, err := stats.Streak(ctx, userID, now, loc, cutoffHour)
	if err != nil {
		return DigestStats{}, err
	}
	curve, err := stats.LearningCurve(ctx, userID, from, today)
	if err != nil {
		return DigestStats{}, err
	}
	due, err := stats.DueForecast(ctx, userID, 0, now, loc, cutoffHour)
	if err != nil {
		return DigestStats{}, err
	}

	var newCards int64
	for _, p := range curve {
		newCards += p.New
	}
	return DigestStats{
		WindowFrom:    from,
		WindowTo:      today,
		Reviewed:      volume.Last7Days,
		PassRate:      retention.Rate,
		StreakCurrent: streak.Current,
		NewCards:      newCards,
		DueNow:        due.Today,
	}, nil
}

// message 按用户语言组装摘要邮件。摘要是 C 类可选邮件，故带 RFC 8058 一键退订头（M1-22）：
// 令牌指名 study_digest 这一个类型，收件人不登录即可关掉它。令牌签发失败只记日志并照常发信。
func (w *Worker) message(ctx context.Context, c store.DigestCandidate, s DigestStats) mail.Message {
	lc := w.translator.Localizer(w.translator.Pick("", c.Locale, ""))
	lines := []string{
		lc.Tf("mail.digest.reviewed", map[string]any{"count": s.Reviewed}),
		lc.Tf("mail.digest.pass_rate", map[string]any{"rate": formatPercent(s.PassRate)}),
		lc.Tf("mail.digest.streak", map[string]any{"days": s.StreakCurrent}),
		lc.Tf("mail.digest.new_cards", map[string]any{"count": s.NewCards}),
		lc.Tf("mail.digest.due", map[string]any{"count": s.DueNow}),
		lc.Tf("mail.digest.link", map[string]any{"url": w.statsURL()}),
	}
	msg := mail.Message{
		To:       c.Email,
		Type:     string(mail.TypeStudyDigest),
		Subject:  lc.T("mail.digest.subject"),
		TextBody: strings.Join(lines, "\n"),
	}
	msg.Headers = w.unsubscribeHeaders(ctx, c.ID)
	return msg
}

// unsubscribeHeaders 为 study_digest 签发退订令牌并返回 RFC 8058 头；不可用时返回 nil。
func (w *Worker) unsubscribeHeaders(ctx context.Context, userID uint64) map[string]string {
	if w.tokens == nil {
		return nil
	}
	token, err := w.tokens.Issue(ctx, userID, store.ActionTokenUnsubscribe, string(mail.TypeStudyDigest), auth.UnsubscribeTTL)
	if err != nil {
		w.logger.Error("weekly digest: issue unsubscribe token failed", "user_id", userID, "error", err)
		return nil
	}
	link := w.baseURL + "/unsubscribe?token=" + url.QueryEscape(token)
	return mail.UnsubscribeHeaders(mail.TypeStudyDigest, link)
}

// statsURL 拼统计页链接；未配置 BaseURL 时退化成相对路径。
func (w *Worker) statsURL() string {
	return w.baseURL + w.statsPath
}

// weekStartOf 返回 day 所在周的周一（YYYY-MM-DD）。Go 的 Weekday 以周日为 0，这里换算出
// 「距本周一的天数」再回退，保证本地周一凌晨与同周周日落在同一个 week_start。
func weekStartOf(day string) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	offset := (int(t.Weekday()) + 6) % 7
	return t.AddDate(0, 0, -offset).Format("2006-01-02")
}

// shiftDay 把 review_day 字符串（YYYY-MM-DD）平移 delta 天；解析失败时原样返回。
func shiftDay(day string, delta int) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	return t.AddDate(0, 0, delta).Format("2006-01-02")
}

// formatPercent 把 0–1 的比例格式化成一位小数的百分数（不含 % 号），与统计页同格式。
func formatPercent(rate float64) string {
	return strconv.FormatFloat(rate*100, 'f', 1, 64)
}

// loadLocation 解析 IANA 时区；为空或解析失败时退回 UTC（与 schedule 的口径一致）。
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
