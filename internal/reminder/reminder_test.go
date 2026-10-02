package reminder

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// fakeEnqueuer 记录入队的邮件，避免依赖真实 SMTP 或 outbox worker。
// 它同时实现 Enqueuer 的 Configured，便于测试「SMTP 未配置」路径。
type fakeEnqueuer struct {
	configured bool
	failNext   error

	mu   sync.Mutex
	msgs []mail.Message
}

func (f *fakeEnqueuer) Configured() bool { return f.configured }

func (f *fakeEnqueuer) Enqueue(_ context.Context, m mail.Message) error {
	if f.failNext != nil {
		return f.failNext
	}
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

// newTestDB 开一个临时 SQLite 库并迁移全部模型（含 reminder_log）。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "reminder.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	return db
}

// seedUserWithDueCard 建一个活跃用户，并挂上一张到期的复习卡。
// dueAt 传过去的时间，保证候选查询一定命中。
func seedUserWithDueCard(t *testing.T, db *gorm.DB, name, email, locale, tz string, cutoff int, dueAt time.Time) uint64 {
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
	fuzz := true
	preset := store.Preset{
		OwnerUserID: user.ID, Name: "default", DesiredRetention: 0.9,
		LearningSteps: "1m,10m", RelearningSteps: "10m", MaximumIntervalDays: 36500,
		EnableFuzz: &fuzz, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&preset).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := store.Deck{
		OwnerUserID: user.ID, Name: "Deck", Description: "", Visibility: "private",
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
	due := dueAt.UTC()
	state := store.CardState{CardID: card.ID, UserID: user.ID, State: "review", DueAt: &due}
	if err := db.Create(&state).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}
	return user.ID
}

// enableReminder 显式打开该用户的复习提醒偏好（C 类默认关）。
func enableReminder(t *testing.T, db *gorm.DB, userID uint64) {
	t.Helper()
	store := store.NewEmailPrefStore(db)
	if err := store.SetChoices(context.Background(), userID, map[string]bool{
		string(mail.TypeReviewReminder): true,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("enable reminder: %v", err)
	}
}

// disableReminder 显式关闭该用户的复习提醒偏好。
func disableReminder(t *testing.T, db *gorm.DB, userID uint64) {
	t.Helper()
	prefs := store.NewEmailPrefStore(db)
	if err := prefs.SetChoices(context.Background(), userID, map[string]bool{
		string(mail.TypeReviewReminder): false,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("disable reminder: %v", err)
	}
}

// newReminder 构造一个用可控时钟与假 enqueuer 的 Reminder。
func newReminder(t *testing.T, db *gorm.DB, enq Enqueuer, now func() time.Time) *Reminder {
	t.Helper()
	translator, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	r, err := New(Deps{
		DB: db, Outbox: enq, Translator: translator, Now: now,
		BaseURL: "http://localhost:3012",
	})
	if err != nil {
		t.Fatalf("reminder.New: %v", err)
	}
	return r
}

// shanghaiAt 返回「上海本地时间 hh:mm」对应的 UTC 时刻（同一天，日期取 2026-06-01）。
func shanghaiAt(t *testing.T, hour, min int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	return time.Date(2026, 6, 1, hour, min, 0, 0, loc).UTC()
}

// TestInQuietHoursBoundaries 固定静默窗口的边界：23:00 与 06:59 属于窗口，07:00 整不属于。
func TestInQuietHoursBoundaries(t *testing.T) {
	cases := []struct {
		hour, min int
		want      bool
	}{
		{22, 59, false},
		{23, 0, true},
		{23, 30, true},
		{0, 0, true},
		{6, 59, true},
		{7, 0, false},
		{12, 0, false},
	}
	for _, tc := range cases {
		local := time.Date(2026, 6, 1, tc.hour, tc.min, 0, 0, time.UTC)
		if got := InQuietHours(local); got != tc.want {
			t.Errorf("InQuietHours(%02d:%02d) = %v, want %v", tc.hour, tc.min, got, tc.want)
		}
	}
}

// TestQuietWindowHoldsUntilItOpensThenSendsOnce 是 M1-21 的核心验收：
// 落在 23:00–07:00 的提醒被推迟到窗口开启后，并且只发一次。
func TestQuietWindowHoldsUntilItOpensThenSendsOnce(t *testing.T) {
	db := newTestDB(t)
	// 上海时区、默认切点 4；到期的卡早已到期。
	userID := seedUserWithDueCard(t, db, "alice", "alice@example.com", "zh-CN", "Asia/Shanghai", 4,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	enableReminder(t, db, userID)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 23, 30)
	r := newReminder(t, db, enq, func() time.Time { return clock })
	ctx := context.Background()

	// 23:30：静默窗口内，绝不发送。
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce at 23:30: %v", err)
	}
	if got := enq.count(); got != 0 {
		t.Fatalf("23:30 in quiet window: enqueued %d, want 0", got)
	}

	// 次日 06:00：仍在窗口内，仍然不发（推迟而非丢弃）。
	clock = shanghaiAt(t, 6, 0).Add(24 * time.Hour)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce at 06:00: %v", err)
	}
	if got := enq.count(); got != 0 {
		t.Fatalf("06:00 in quiet window: enqueued %d, want 0", got)
	}

	// 07:00：窗口开启，补发一次。
	clock = shanghaiAt(t, 7, 0).Add(24 * time.Hour)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce at 07:00: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("07:00 window open: enqueued %d, want 1", got)
	}

	// 07:30 同一天：每日上限生效，第二封不发。
	clock = shanghaiAt(t, 7, 30).Add(24 * time.Hour)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce at 07:30: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("07:30 same review day: enqueued %d, want 1 (daily cap)", got)
	}

	// 发出的邮件类型与收件人都正确。
	msg := enq.last()
	if msg.Type != string(mail.TypeReviewReminder) {
		t.Errorf("message type = %q, want %q", msg.Type, mail.TypeReviewReminder)
	}
	if msg.To != "alice@example.com" {
		t.Errorf("message to = %q, want alice@example.com", msg.To)
	}
	if msg.Subject == "" || msg.TextBody == "" {
		t.Errorf("message subject/body must be localized, got subject=%q body=%q", msg.Subject, msg.TextBody)
	}
}

// TestSecondReminderSameDayNotSent 验证同一天的第二封不发（与静默窗口无关的纯每日上限）。
func TestSecondReminderSameDayNotSent(t *testing.T) {
	db := newTestDB(t)
	userID := seedUserWithDueCard(t, db, "bob", "bob@example.com", "en", "Asia/Shanghai", 4,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	enableReminder(t, db, userID)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 10, 0)
	r := newReminder(t, db, enq, func() time.Time { return clock })
	ctx := context.Background()

	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("first run enqueued %d, want 1", got)
	}
	// 同一天稍晚再跑一轮：台账已存在，不发第二封。
	clock = shanghaiAt(t, 15, 0)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("second run same day enqueued %d, want 1", got)
	}
}

// TestOptOutStopsImmediately 验证用户关掉后立即停发：新的一天本可以发，但偏好已关，所以不发。
func TestOptOutStopsImmediately(t *testing.T) {
	db := newTestDB(t)
	userID := seedUserWithDueCard(t, db, "carol", "carol@example.com", "en", "Asia/Shanghai", 4,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	enableReminder(t, db, userID)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 10, 0)
	r := newReminder(t, db, enq, func() time.Time { return clock })
	ctx := context.Background()

	// 打开时正常发一封。
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("enabled RunOnce: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("enabled run enqueued %d, want 1", got)
	}

	// 关掉偏好，并在**新的一天**再跑：新一天不受每日上限约束，能不发只因偏好关。
	disableReminder(t, db, userID)
	clock = shanghaiAt(t, 10, 0).Add(24 * time.Hour)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("disabled RunOnce: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("after opt-out enqueued %d, want 1 (no new send)", got)
	}
}

// TestDefaultOffWhenNoPreference 验证 C 类默认关：没有偏好行时不发。
func TestDefaultOffWhenNoPreference(t *testing.T) {
	db := newTestDB(t)
	seedUserWithDueCard(t, db, "dave", "dave@example.com", "en", "Asia/Shanghai", 4,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 10, 0)
	r := newReminder(t, db, enq, func() time.Time { return clock })

	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := enq.count(); got != 0 {
		t.Fatalf("class C default off: enqueued %d, want 0", got)
	}
}

// TestNotConfiguredDoesNotSendOrError 验证 SMTP 未配置时不发也不报错。
func TestNotConfiguredDoesNotSendOrError(t *testing.T) {
	db := newTestDB(t)
	userID := seedUserWithDueCard(t, db, "erin", "erin@example.com", "en", "Asia/Shanghai", 4,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	enableReminder(t, db, userID)

	enq := &fakeEnqueuer{configured: false}
	clock := shanghaiAt(t, 10, 0)
	r := newReminder(t, db, enq, func() time.Time { return clock })

	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce with smtp unconfigured returned error: %v", err)
	}
	if got := enq.count(); got != 0 {
		t.Fatalf("smtp unconfigured: enqueued %d, want 0", got)
	}
}

// TestNoDueCardsNoReminder 验证没有到期卡的用户不会被催促。
func TestNoDueCardsNoReminder(t *testing.T) {
	db := newTestDB(t)
	userID := seedUserWithDueCard(t, db, "frank", "frank@example.com", "en", "Asia/Shanghai", 4,
		// 到期时间在未来 → 当前没有到期卡。
		time.Now().UTC().Add(72*time.Hour))
	enableReminder(t, db, userID)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 10, 0)
	r := newReminder(t, db, enq, func() time.Time { return clock })

	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := enq.count(); got != 0 {
		t.Fatalf("no due cards: enqueued %d, want 0", got)
	}
}
