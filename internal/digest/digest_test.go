package digest

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/pgtest"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// fakeEnqueuer 记录入队的邮件，避免依赖真实 SMTP 或 outbox worker；
// Configured 可切换，用来覆盖「SMTP 未配置」路径。
type fakeEnqueuer struct {
	configured bool

	mu   sync.Mutex
	msgs []mail.Message
}

func (f *fakeEnqueuer) Configured() bool { return f.configured }

func (f *fakeEnqueuer) Enqueue(_ context.Context, m mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, m)
	return nil
}

func (f *fakeEnqueuer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.msgs)
}

func (f *fakeEnqueuer) last() mail.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.msgs[len(f.msgs)-1]
}

// newTestDB 优先用 TEST_PG_DSN 指向的真实 PostgreSQL（每次一个独立 schema），
// 未设置时退回临时 SQLite 库。两条路径都能复现「每周一封」的唯一约束行为。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	var db *gorm.DB
	if pg, ok := pgtest.Open(t); ok {
		db = pg
	} else {
		opened, err := store.Open("sqlite", filepath.Join(t.TempDir(), "digest.db"))
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		db = opened
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	return db
}

// seedUser 建一个可收信的活跃用户。
func seedUser(t *testing.T, db *gorm.DB, name, email, locale, tz string, cutoff int) uint64 {
	t.Helper()
	now := time.Now().UTC()
	user := store.User{
		Username: name, Email: email, DisplayName: name, Role: store.RoleUser,
		Status: store.StatusActive, Locale: locale, Timezone: tz,
		DayCutoffHour: cutoff, CreatedAt: now,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user.ID
}

// seedCard 建一张属于该用户的卡（deck/note/card），返回 cardID。
// 统计里的留存率/学习曲线都要 JOIN 到未删除的 card+note，所以复习日志必须挂到真实卡上。
func seedCard(t *testing.T, db *gorm.DB, userID uint64) uint64 {
	t.Helper()
	now := time.Now().UTC()
	fuzz := true
	preset := store.Preset{
		OwnerUserID: userID, Name: "default", DesiredRetention: 0.9,
		LearningSteps: "1m,10m", RelearningSteps: "10m", MaximumIntervalDays: 36500,
		EnableFuzz: &fuzz, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&preset).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := store.Deck{
		OwnerUserID: userID, Name: "Deck", Description: "", Visibility: "private",
		NewPerDay: 20, ReviewsPerDay: 200, PresetID: preset.ID, CreatedAt: now,
	}
	if err := db.Create(&deck).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	note := store.Note{
		DeckID: deck.ID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`,
		TagsJSON: "[]", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&note).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	card := store.Card{NoteID: note.ID, Template: "forward", CreatedAt: now}
	if err := db.Create(&card).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	return card.ID
}

// seedReview 写一条复习日志。reviewDay 直接给定，避免测试依赖挂钟。
func seedReview(t *testing.T, db *gorm.DB, userID, cardID uint64, reviewDay string, rating, stateBefore int, stability float64) {
	t.Helper()
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	row := store.Review{
		CardID: cardID, UserID: userID, Rating: rating, GradeSource: "self",
		ReviewedAt: at, ReviewDay: reviewDay, StateBefore: stateBefore,
		Stability: &stability,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create review: %v", err)
	}
}

// seedDueCard 给一张卡挂上「已到期」的状态行，让 DueForecast.Today 非零。
func seedDueCard(t *testing.T, db *gorm.DB, userID, cardID uint64, dueAt time.Time) {
	t.Helper()
	due := dueAt.UTC()
	state := store.CardState{CardID: cardID, UserID: userID, State: "review", DueAt: &due}
	if err := db.Create(&state).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}
}

// setDigestPref 显式开关该用户的每周摘要偏好（C 类默认关）。
func setDigestPref(t *testing.T, db *gorm.DB, userID uint64, enabled bool) {
	t.Helper()
	prefs := store.NewEmailPrefStore(db)
	if err := prefs.SetChoices(context.Background(), userID, map[string]bool{
		string(mail.TypeStudyDigest): enabled,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("set digest pref: %v", err)
	}
}

// newWorker 构造一个用可控时钟与假 enqueuer 的 worker。
func newWorker(t *testing.T, db *gorm.DB, enq Enqueuer, now func() time.Time) *Worker {
	t.Helper()
	translator, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	tokens, err := auth.NewActionTokenService(store.NewActionTokenStore(db))
	if err != nil {
		t.Fatalf("NewActionTokenService: %v", err)
	}
	w, err := New(Deps{
		DB: db, Outbox: enq, Translator: translator, Now: now,
		BaseURL: "http://localhost:3012", Tokens: tokens,
	})
	if err != nil {
		t.Fatalf("digest.New: %v", err)
	}
	return w
}

// shanghaiAt 返回「上海本地时间 hh:mm」对应的 UTC 时刻（日期固定 2026-06-01，星期一）。
func shanghaiAt(t *testing.T, hour, min int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	return time.Date(2026, 6, 1, hour, min, 0, 0, loc).UTC()
}

// TestDigestSendsExactlyOncePerWeek 是验收①：每人每周恰好一封。
// 同一周的周一到周日反复轮询都只发一封；跨到下一周（下周一）才发第二封。
func TestDigestSendsExactlyOncePerWeek(t *testing.T) {
	db := newTestDB(t)
	userID := seedUser(t, db, "alice", "alice@example.com", "zh-CN", "Asia/Shanghai", 4)
	seedCard(t, db, userID)
	setDigestPref(t, db, userID, true)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 10, 0) // 2026-06-01 星期一
	w := newWorker(t, db, enq, func() time.Time { return clock })
	ctx := context.Background()

	// 本周第一次：发。
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("first run enqueued %d, want 1", got)
	}

	// 同一天稍晚 + 本周其余每一天反复跑：都不再发。
	for _, offset := range []time.Duration{8 * time.Hour, 24 * time.Hour, 3 * 24 * time.Hour, 6 * 24 * time.Hour} {
		clock = shanghaiAt(t, 10, 0).Add(offset)
		if err := w.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce at +%v: %v", offset, err)
		}
		if got := enq.count(); got != 1 {
			t.Fatalf("same week (+%v) enqueued %d, want 1 (one per week)", offset, got)
		}
	}

	// 下周一：新的一周，发第二封。
	clock = shanghaiAt(t, 10, 0).Add(7 * 24 * time.Hour)
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce next week: %v", err)
	}
	if got := enq.count(); got != 2 {
		t.Fatalf("next week enqueued %d, want 2 (one per week)", got)
	}

	msg := enq.last()
	if msg.Type != string(mail.TypeStudyDigest) {
		t.Errorf("message type = %q, want %q", msg.Type, mail.TypeStudyDigest)
	}
	if msg.To != "alice@example.com" {
		t.Errorf("message to = %q, want alice@example.com", msg.To)
	}
	if msg.Subject == "" || msg.TextBody == "" {
		t.Errorf("message subject/body must be localized, got subject=%q body=%q", msg.Subject, msg.TextBody)
	}
}

// TestDigestOptOutStopsImmediately 是验收②：关掉后立即停止。
func TestDigestOptOutStopsImmediately(t *testing.T) {
	db := newTestDB(t)
	userID := seedUser(t, db, "bob", "bob@example.com", "en", "Asia/Shanghai", 4)
	seedCard(t, db, userID)
	setDigestPref(t, db, userID, true)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 10, 0)
	w := newWorker(t, db, enq, func() time.Time { return clock })
	ctx := context.Background()

	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("enabled RunOnce: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("enabled run enqueued %d, want 1", got)
	}

	// 关掉偏好，并在**下一周**再跑：新的一周不受本周台账约束，
	// 不发只可能是偏好门禁生效，而不是去重台账挡住的。
	setDigestPref(t, db, userID, false)
	clock = shanghaiAt(t, 10, 0).Add(7 * 24 * time.Hour)
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("disabled RunOnce: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("after opt-out enqueued %d, want 1 (no new send)", got)
	}

	// 重新打开后，同一周内也不补发（她已在本周关过一次；开启发生在指针移动之前需再等下一周）。
	// 这里只断言：重新打开 + 再下一周会恢复发送，证明「关掉」不是不可逆的。
	setDigestPref(t, db, userID, true)
	clock = shanghaiAt(t, 10, 0).Add(14 * 24 * time.Hour)
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("re-enabled RunOnce: %v", err)
	}
	if got := enq.count(); got != 2 {
		t.Fatalf("after re-enable enqueued %d, want 2 (resumes)", got)
	}
}

// TestDigestNumbersMatchStatsPage 是验收③：摘要数字与统计页同一区间一致。
//
// 摘要的每个数字都直接来自 StatsStore，测试用**同一次调用参数**再算一遍，
// 两边逐字段相等即证明摘要没有另写一套 SQL、也不会与统计页漂移。
func TestDigestNumbersMatchStatsPage(t *testing.T) {
	db := newTestDB(t)
	userID := seedUser(t, db, "carol", "carol@example.com", "en", "Asia/Shanghai", 4)
	cardID := seedCard(t, db, userID)
	setDigestPref(t, db, userID, true)

	// 复习历史：窗口 [2026-05-26, 2026-06-01] 内 4 条，窗口外 1 条。
	seedReview(t, db, userID, cardID, "2026-06-01", 3, 0, 5.0)  // 新引入
	seedReview(t, db, userID, cardID, "2026-06-01", 1, 2, 0.5)  // 到期复习，失败
	seedReview(t, db, userID, cardID, "2026-05-31", 3, 2, 10.0) // 到期复习，通过
	seedReview(t, db, userID, cardID, "2026-05-30", 4, 0, 2.0)  // 新引入
	seedReview(t, db, userID, cardID, "2026-05-01", 3, 2, 20.0) // 窗口外（影响留存率与连续打卡）
	seedDueCard(t, db, userID, cardID, time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC))

	clock := shanghaiAt(t, 10, 0)
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	now := clock
	cutoff := 4

	got, err := ComputeStats(context.Background(), db, userID, now, loc, cutoff)
	if err != nil {
		t.Fatalf("ComputeStats: %v", err)
	}

	// —— 统计页口径：同一批 StatsStore 调用、同一参数 ——
	stats := store.NewStatsStore(db)
	today := schedule.ReviewDay(now, loc, cutoff)
	volume, err := stats.ReviewVolume(context.Background(), userID, today)
	if err != nil {
		t.Fatalf("ReviewVolume: %v", err)
	}
	retention, err := stats.RetentionByStability(context.Background(), userID, 0)
	if err != nil {
		t.Fatalf("RetentionByStability: %v", err)
	}
	streak, err := stats.Streak(context.Background(), userID, now, loc, cutoff)
	if err != nil {
		t.Fatalf("Streak: %v", err)
	}
	curve, err := stats.LearningCurve(context.Background(), userID, "2026-05-26", today)
	if err != nil {
		t.Fatalf("LearningCurve: %v", err)
	}
	due, err := stats.DueForecast(context.Background(), userID, 0, now, loc, cutoff)
	if err != nil {
		t.Fatalf("DueForecast: %v", err)
	}
	var wantNew int64
	for _, p := range curve {
		wantNew += p.New
	}

	if got.WindowFrom != "2026-05-26" || got.WindowTo != "2026-06-01" {
		t.Errorf("window = [%s, %s], want [2026-05-26, 2026-06-01]", got.WindowFrom, got.WindowTo)
	}
	if got.Reviewed != volume.Last7Days {
		t.Errorf("Reviewed = %d, stats page Last7Days = %d", got.Reviewed, volume.Last7Days)
	}
	if got.PassRate != retention.Rate {
		t.Errorf("PassRate = %v, stats page retention rate = %v", got.PassRate, retention.Rate)
	}
	if got.StreakCurrent != streak.Current {
		t.Errorf("StreakCurrent = %d, stats page streak = %d", got.StreakCurrent, streak.Current)
	}
	if got.NewCards != wantNew {
		t.Errorf("NewCards = %d, stats page learning-curve new sum = %d", got.NewCards, wantNew)
	}
	if got.DueNow != due.Today {
		t.Errorf("DueNow = %d, stats page due-today = %d", got.DueNow, due.Today)
	}
	// 顺手钉住期望值，防止「两边同时算错」也能通过。
	if got.Reviewed != 4 || got.NewCards != 2 || got.StreakCurrent != 3 || got.DueNow != 1 {
		t.Fatalf("unexpected digest numbers: %+v", got)
	}

	// 邮件正文必须真的用上了这些数字。
	enq := &fakeEnqueuer{configured: true}
	w := newWorker(t, db, enq, func() time.Time { return clock })
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if enq.count() != 1 {
		t.Fatalf("enqueued %d, want 1", enq.count())
	}
	body := enq.last().TextBody
	if !strings.Contains(body, "Reviews in the last 7 days: 4") {
		t.Errorf("body missing reviewed count, got:\n%s", body)
	}
	if !strings.Contains(body, "Current streak: 3 day(s)") {
		t.Errorf("body missing streak, got:\n%s", body)
	}
	if !strings.Contains(body, "Cards due now: 1") {
		t.Errorf("body missing due count, got:\n%s", body)
	}
}

// TestDigestDefaultOffWhenNoPreference 验证 C 类默认关：没有偏好行时不发。
func TestDigestDefaultOffWhenNoPreference(t *testing.T) {
	db := newTestDB(t)
	seedUser(t, db, "dave", "dave@example.com", "en", "Asia/Shanghai", 4)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 10, 0)
	w := newWorker(t, db, enq, func() time.Time { return clock })

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := enq.count(); got != 0 {
		t.Fatalf("class C default off: enqueued %d, want 0", got)
	}
}

// TestDigestNotConfiguredDoesNotSendOrError 验证 SMTP 未配置时不发也不报错。
func TestDigestNotConfiguredDoesNotSendOrError(t *testing.T) {
	db := newTestDB(t)
	userID := seedUser(t, db, "erin", "erin@example.com", "en", "Asia/Shanghai", 4)
	setDigestPref(t, db, userID, true)

	enq := &fakeEnqueuer{configured: false}
	clock := shanghaiAt(t, 10, 0)
	w := newWorker(t, db, enq, func() time.Time { return clock })

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce with smtp unconfigured returned error: %v", err)
	}
	if got := enq.count(); got != 0 {
		t.Fatalf("smtp unconfigured: enqueued %d, want 0", got)
	}
}

// TestDigestUnsubscribeHeaders 验证 C 类摘要带 M1-22 的一键退订头，且令牌指名 study_digest。
func TestDigestUnsubscribeHeaders(t *testing.T) {
	db := newTestDB(t)
	userID := seedUser(t, db, "frank", "frank@example.com", "en", "Asia/Shanghai", 4)
	seedCard(t, db, userID)
	setDigestPref(t, db, userID, true)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 10, 0)
	w := newWorker(t, db, enq, func() time.Time { return clock })

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if enq.count() != 1 {
		t.Fatalf("enqueued %d, want 1", enq.count())
	}
	headers := enq.last().Headers
	if headers[mail.HeaderListUnsubscribe] == "" {
		t.Errorf("missing %s header: %v", mail.HeaderListUnsubscribe, headers)
	}
	if got := headers[mail.HeaderListUnsubscribePost]; got != mail.ListUnsubscribePostValue {
		t.Errorf("%s = %q, want %q", mail.HeaderListUnsubscribePost, got, mail.ListUnsubscribePostValue)
	}
	if link := headers[mail.HeaderListUnsubscribe]; !strings.Contains(link, "/unsubscribe?token=") {
		t.Errorf("%s = %q, want an unsubscribe URL", mail.HeaderListUnsubscribe, link)
	}
}

// TestDigestQuietWindowHoldsUntilItOpens 验证落在本地静默窗口（23:00–07:00）时不发，
// 窗口开启后的下一轮补发且只发一次。
func TestDigestQuietWindowHoldsUntilItOpens(t *testing.T) {
	db := newTestDB(t)
	userID := seedUser(t, db, "grace", "grace@example.com", "en", "Asia/Shanghai", 4)
	seedCard(t, db, userID)
	setDigestPref(t, db, userID, true)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 0, 30) // 周一 00:30，静默窗口内
	w := newWorker(t, db, enq, func() time.Time { return clock })
	ctx := context.Background()

	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce at 00:30: %v", err)
	}
	if got := enq.count(); got != 0 {
		t.Fatalf("00:30 in quiet window: enqueued %d, want 0", got)
	}

	clock = shanghaiAt(t, 7, 0) // 窗口开启
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce at 07:00: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("07:00 window open: enqueued %d, want 1", got)
	}
}
