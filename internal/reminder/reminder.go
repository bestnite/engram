// Package reminder 实现 C 类「复习到期提醒」的周期后台 worker（DESIGN.md §4.7；AGENTS.md M1-21）。
//
// 行为约束（DESIGN.md §4.7）：
//   - 绝不在请求路径里同步发信：一律 Enqueue，投递由 outbox worker 负责。
//   - 发送时间按**用户本地时间**：每个用户可选自己的整点小时（users.reminder_hour，0–23），
//     未设置时用全局默认 DefaultSendHour（19:00）。到达该小时后的首轮轮询即发；小时按用户
//     时区解释（见 SendHour / reachedSendHour）。
//   - 每天最多一封：按用户本地复习日（与 reviews.review_day 同一时区/切点口径）判定，
//     台账见 store.ReminderLog。
//   - SMTP 未配置时不发也不报错，只记一条英文日志（C 类是可选运营邮件，没有页面要禁用）。
//
// 为什么删掉旧的静默窗口（本地 23:00–07:00）：那条启发式的唯一目的是把「过了日切点就发」
// 的提醒挪开深夜，而它正是「大家一律在早上 7 点被催」的根因。现在发送时刻由用户显式选择
// （默认 19:00，本就在窗外），静默窗既无用途，又会反过来压制用户明确选的深夜时段——用户
// 显式选择优先，因此整条启发式连同 InQuietHours 一并删除，其用例也随之移除。
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
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/worker"
	"strconv"
)

const (
	// DefaultInterval 是默认轮询间隔。
	//
	// 选 10 分钟的理由：提醒是「催一下」而不是准点事件，用户感知不到分钟级差异；
	// 发送时刻以小时为粒度、每日上限以复习日为粒度，10 分钟足够在用户选定的小时到达后很快
	// 补发，又把每轮的 DB 开销压到「一条候选查询 + 每用户一两次小查询」的量级。
	DefaultInterval = worker.DefaultInterval
	// DefaultSendHour 是未设置每用户发送小时时的全局默认（本地 19:00）。
	//
	// 选 19:00：提醒是「今天还有卡没复习」的催促，傍晚是大多数人一天里能坐下来复习的时段，
	// 又在旧的静默窗（23:00–07:00）之外。周报复用同一小时，两个 C 类邮件的到达时刻因此一致。
	DefaultSendHour = 19
	// MinSendInterval 是同一用户两封复习提醒之间的最小间隔（跨复习日生效）。
	//
	// 为什么需要它：发送时刻由用户选的整点决定，账本却按复习日计。若用户选的小时早于其日切点
	// （例如选 0 点、切点 4 点），前一个复习日的尾（00:00）与新复习日的头（04:00）落在同一自然
	// 日、只隔几小时——「每个复习日至多一封」按定义拦不住这种贴身双发。
	//
	// 为什么取 20 小时：相邻两个复习日的正常节律是 24 小时（夏令时下 23 小时），20 小时在它之下
	// 留出至少 3 小时余量，因此不会压掉真正的隔日提醒；而贴身双发的间隔等于「日切点 − 所选
	// 小时」，量级是几小时（0 点/4 点只隔 4 小时），远在 20 小时之下，必被拦住。它又远小于一周，
	// 不与周报的周节律纠缠。取整到「小时级」与发送时刻的粒度一致，便于读注释时心算。
	//
	// 边界：「日切点 − 所选小时」越接近一天就越不像贴身双发（极端如选 0 点、切点 23，两封已相隔
	// 约一天，与正常隔日节律无从区分），本门不拦——拦它得把阈值抬到 24 小时之上，反而会压掉正常
	// 的每日提醒。20 小时因此是「拦住贴身双发」与「保住每日节律」之间的取舍点。
	MinSendInterval = 20 * time.Hour
	// DefaultReviewPath 是复习页路径（提醒正文里的链接）。
	DefaultReviewPath = "/review"
	// runFailedLog 是单轮失败时的英文日志消息，注入共享骨架 worker.Config.FailMessage；
	// 文案与重构前逐字一致。
	runFailedLog = "review reminder run failed"
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
	// Tokens 签发一键退订令牌（M1-22）；为空时提醒邮件不带退订头（可选类型才需要）。
	Tokens *auth.ActionTokenService
	// Templates 取管理员自定义的邮件模板（DESIGN.md §4.7）；为空表示只用内置正文。
	Templates mail.LookupFunc
	// SiteDefaultLocale 是站点默认语言，模板回退链的第二级；为空时跳过该级。
	SiteDefaultLocale func() string
}

// Reminder 持有后台 worker 与配置。
//
// 起停与 tick 循环来自内嵌的共享骨架 *worker.Lifecycle（internal/worker）：Start/Stop
// 由它提升，签名与重构前一致，cmd/engram 的调用不变。
type Reminder struct {
	*worker.Lifecycle

	db         *gorm.DB
	outbox     Enqueuer
	translator *i18n.Translator
	logger     *slog.Logger
	now        func() time.Time
	baseURL    string
	reviewPath string
	// tokens 签发退订令牌（M1-22）；为空时不加退订头。
	tokens *auth.ActionTokenService
	// templates 取自定义邮件模板；缺失即用内置正文（DESIGN.md §4.7）。
	templates mail.LookupFunc
	// siteLocale 提供站点默认语言（回退链第二级）；可为 nil。
	siteLocale func() string
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
	path := strings.TrimSpace(deps.ReviewPath)
	if path == "" {
		path = DefaultReviewPath
	}
	r := &Reminder{
		db:         deps.DB,
		outbox:     deps.Outbox,
		translator: deps.Translator,
		logger:     logger,
		now:        now,
		baseURL:    strings.TrimRight(strings.TrimSpace(deps.BaseURL), "/"),
		reviewPath: path,
		tokens:     deps.Tokens,
		templates:  deps.Templates,
		siteLocale: deps.SiteDefaultLocale,
	}
	// 起停与 tick 循环交给共享骨架；RunOnce 作为业务相位注入，失败日志文案逐字保留。
	lifecycle, err := worker.New(worker.Config{
		RunOnce:     r.RunOnce,
		Logger:      logger,
		FailMessage: runFailedLog,
		Interval:    deps.Interval,
	})
	if err != nil {
		return nil, err
	}
	r.Lifecycle = lifecycle
	return r, nil
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
//  1. 发送时刻：用户本地时间还未到其选定的小时（默认 19:00）就整轮跳过，**不记录台账**——
//     到达该小时后的下一轮会重新评估并发出，即「到点再发」。
//  2. 最小间隔：距上次成功发送不足 MinSendInterval（跨复习日取最大 SentAt）就整轮跳过，
//     **不记录台账**——间隔过后仍可补发。这一道专治「所选小时早于日切点」时同一自然日
//     里的贴身双发（日账本按复习日计，拦不住它）。
//  3. 每日上限：用户本地复习日已发过就不再发。
//  4. 偏好门禁：C 类默认关，用户关掉后立即生效（每轮现读偏好，不缓存）。
//
// 台账只在 Enqueue 成功后才写，避免入队失败却把当天额度用掉。
//
// 小时与复习日的边界：发送时刻按用户本地整点比较（local.Hour() >= 选定小时），复习日仍按
// day_cutoff_hour 切分。若用户选的整点早于其日切点（例如选 0 点、切点 4），该时刻仍属于
// 前一个复习日，于是同一自然日里 0 点与 4 点会各命中一个复习日；账本对此无能为力，故由上面
// 的第 2 道最小间隔门（MinSendInterval）拦住，见该常量的注释。
func (r *Reminder) maybeSend(ctx context.Context, c store.ReminderCandidate, now time.Time) error {
	loc := store.LoadLocation(c.Timezone)
	if !ReachedSendHour(now.In(loc), SendHour(c.ReminderHour)) {
		return nil
	}
	if last, ok, err := store.LastReminderSentAt(ctx, r.db, c.ID); err != nil {
		return err
	} else if ok && now.Sub(last) < MinSendInterval {
		return nil
	}
	cutoff := store.ResolveCutoff(c.DayCutoffHour)
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

	if err := r.outbox.Enqueue(ctx, r.message(ctx, c)); err != nil {
		return err
	}
	if err := store.RecordReminderSent(ctx, r.db, c.ID, day, now); err != nil {
		return err
	}
	r.logger.Info("review reminder enqueued", "user_id", c.ID, "review_day", day, "due_count", c.DueCount)
	return nil
}

// message 按用户语言组装提醒邮件。提醒是 C 类可选邮件，故带 RFC 8058 一键退订头（M1-22）：
// 令牌指名 review_reminder 这一个类型，收件人（必为本站用户）不登录即可关掉它。
// 令牌签发失败只记日志并照常发信：退订入口缺失不该让提醒发不出去。
func (r *Reminder) message(ctx context.Context, c store.ReminderCandidate) mail.Message {
	lc := r.translator.Localizer(r.translator.Pick("", c.Locale, ""))
	site := lc.T("app.name")
	reviewURL := r.reviewURL()
	unsubURL := r.unsubscribeLink(ctx, c.ID)

	vars := mail.Vars{"site": site, "count": strconv.FormatInt(c.DueCount, 10), "url": reviewURL}
	if unsubURL != "" {
		vars["unsubscribe_url"] = unsubURL
	}
	footerNote, unsubLabel := "", ""
	if unsubURL != "" {
		footerNote = lc.T("mail.footer.note")
		unsubLabel = lc.T("mail.footer.unsubscribe")
	}
	tpl := mail.Resolve(r.templates, mail.TypeReviewReminder, lc.Locale(), r.siteDefaultLocale())
	out := mail.RenderOrFallback(mail.RenderInput{
		Type:             mail.TypeReviewReminder,
		Site:             site,
		Vars:             vars,
		Subject:          tpl.Subject,
		BodyMD:           tpl.Body,
		FallbackSubject:  lc.T("mail.reminder.subject"),
		FallbackText:     lc.Tf("mail.reminder.body", map[string]any{"count": c.DueCount, "url": reviewURL}),
		UnsubscribeURL:   unsubURL,
		FooterNote:       footerNote,
		UnsubscribeLabel: unsubLabel,
	})
	msg := mail.Message{
		To:       c.Email,
		Type:     string(mail.TypeReviewReminder),
		Subject:  out.Subject,
		TextBody: out.Text,
		HTMLBody: out.HTML,
	}
	msg.Headers = mail.UnsubscribeHeaders(mail.TypeReviewReminder, unsubURL)
	return msg
}

// siteDefaultLocale 返回站点默认语言（模板回退链第二级）；未注入时返回空串。
func (r *Reminder) siteDefaultLocale() string {
	if r.siteLocale == nil {
		return ""
	}
	return strings.TrimSpace(r.siteLocale())
}

// unsubscribeHeaders 为 review_reminder 签发退订令牌并返回 RFC 8058 头；不可用时返回 nil。
// unsubscribeLink 为 review_reminder 签发退订令牌并返回退订链接；不可用时返回空串。
//
// 返回链接而不是头：正文页脚与纯文本段也要有它（头只有邮件客户端看得到）。
func (r *Reminder) unsubscribeLink(ctx context.Context, userID uint64) string {
	if r.tokens == nil {
		return ""
	}
	token, err := r.tokens.Issue(ctx, userID, store.ActionTokenUnsubscribe, string(mail.TypeReviewReminder), auth.UnsubscribeTTL)
	if err != nil {
		r.logger.Error("review reminder: issue unsubscribe token failed", "user_id", userID, "error", err)
		return ""
	}
	return r.baseURL + "/unsubscribe?token=" + url.QueryEscape(token)
}

// reviewURL 拼复习页链接；未配置 BaseURL 时退化成相对路径。
func (r *Reminder) reviewURL() string {
	return r.baseURL + r.reviewPath
}

// SendHour 把用户选择的发送小时解析成实际使用的整点：nil（未设置）或越界值回落到
// DefaultSendHour。返回 int 而不是指针，调用方无需再判空。
//
// 0 是合法值（午夜）：绝不能把 0 和「未设置」混为一谈——这正是列取可空指针、而不是
// 「普通 int + 默认值」的原因（见 store.User.ReminderHour 的注释）。
func SendHour(userHour *int) int {
	if userHour == nil || *userHour < 0 || *userHour > 23 {
		return DefaultSendHour
	}
	return *userHour
}

// ReachedSendHour 报告本地时间是否已到（或已过）选定的整点小时。
//
// 用「大于等于」而不是「等于」，是为了在 worker 停机、轮询错过整点后仍能补发：
// 到达选定小时后的首轮轮询即满足条件，之后的轮询由「每个复习日一封」的台账兜住，不会重复。
//
// 导出供周报 worker 复用：两个 C 类邮件共用同一小时，判定必须是同一份实现。
func ReachedSendHour(local time.Time, hour int) bool {
	return local.Hour() >= hour
}

// 让编译期确认 *mail.Outbox 满足 Enqueuer（契约漂移时在这里报错，而不是在装配处）。
var _ Enqueuer = (*mail.Outbox)(nil)
