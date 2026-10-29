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
	"git.nite07.com/nite/engram/internal/reminder"
	"git.nite07.com/nite/engram/internal/reviewday"
	"git.nite07.com/nite/engram/internal/store"
)

// fakeEnqueuer records enqueued mail, avoiding dependence on real SMTP or the outbox worker;
// Configured is switchable, used to cover the "SMTP is not configured" path.
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

// newTestDB prefers the real PostgreSQL pointed to by TEST_PG_DSN (a separate schema each time),
// falling back to a temporary SQLite database when unset. Both paths can reproduce the "one per week" unique-constraint behavior.
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

// seedUser creates an active user who can receive mail.
func seedUser(t *testing.T, db *gorm.DB, name, email, locale, tz string, cutoff int) uint64 {
	t.Helper()
	now := time.Now().UTC()
	user := store.User{
		Username: name, Email: email, DisplayName: name, Role: store.RoleUser,
		Status: store.StatusActive, Locale: locale, Timezone: tz,
		DayCutoffHour: store.Ptr(cutoff), CreatedAt: now,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user.ID
}

// seedCard creates a card belonging to that user (deck/note/card) and returns cardID.
// The retention rate and learning curve in stats JOIN against undeleted card+note, so review logs must hang off a real card.
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
		OwnerUserID: userID, Name: "Deck", Description: "",
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

// seedReview writes one review log. reviewDay is given directly so the test does not depend on the wall clock.
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

// seedDueCard attaches an "already due" state row to a card so DueForecast.Today is non-zero.
func seedDueCard(t *testing.T, db *gorm.DB, userID, cardID uint64, dueAt time.Time) {
	t.Helper()
	due := dueAt.UTC()
	state := store.CardState{CardID: cardID, UserID: userID, State: "review", DueAt: &due}
	if err := db.Create(&state).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}
}

// setDigestPref explicitly toggles that user's weekly digest preference (C-class defaults to off).
func setDigestPref(t *testing.T, db *gorm.DB, userID uint64, enabled bool) {
	t.Helper()
	prefs := store.NewEmailPrefStore(db)
	if err := prefs.SetChoices(context.Background(), userID, map[string]bool{
		string(mail.TypeStudyDigest): enabled,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("set digest pref: %v", err)
	}
}

// newWorker builds a worker with a controllable clock and a fake enqueuer.
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

// shanghaiAt returns the UTC instant for "Shanghai local time hh:mm" (date fixed at 2026-06-01, a Monday).
func shanghaiAt(t *testing.T, hour, min int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	return time.Date(2026, 6, 1, hour, min, 0, 0, loc).UTC()
}

// TestDigestSendsExactlyOncePerWeek is acceptance 1: exactly one per user per week.
// Repeated polling from Monday to Sunday of the same week sends only one; the second mail goes out only in the next week (next Monday).
func TestDigestSendsExactlyOncePerWeek(t *testing.T) {
	db := newTestDB(t)
	userID := seedUser(t, db, "alice", "alice@example.com", "zh-CN", "Asia/Shanghai", 4)
	seedCard(t, db, userID)
	setDigestPref(t, db, userID, true)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 20, 0) // 2026-06-01, Monday
	w := newWorker(t, db, enq, func() time.Time { return clock })
	ctx := context.Background()

	// First time this week: send.
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("first run enqueued %d, want 1", got)
	}

	// Later the same day, plus every other day of the week run repeatedly: no further sends.
	for _, offset := range []time.Duration{8 * time.Hour, 24 * time.Hour, 3 * 24 * time.Hour, 6 * 24 * time.Hour} {
		clock = shanghaiAt(t, 20, 0).Add(offset)
		if err := w.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce at +%v: %v", offset, err)
		}
		if got := enq.count(); got != 1 {
			t.Fatalf("same week (+%v) enqueued %d, want 1 (one per week)", offset, got)
		}
	}

	// Next Monday: a new week, send the second mail.
	clock = shanghaiAt(t, 20, 0).Add(7 * 24 * time.Hour)
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

// TestDigestOptOutStopsImmediately is acceptance 2: sending stops immediately after opting out.
func TestDigestOptOutStopsImmediately(t *testing.T) {
	db := newTestDB(t)
	userID := seedUser(t, db, "bob", "bob@example.com", "en", "Asia/Shanghai", 4)
	seedCard(t, db, userID)
	setDigestPref(t, db, userID, true)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 20, 0)
	w := newWorker(t, db, enq, func() time.Time { return clock })
	ctx := context.Background()

	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("enabled RunOnce: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("enabled run enqueued %d, want 1", got)
	}

	// Turn the preference off and run again in the **next week**: a new week is not bound by this week's ledger,
	// so not sending can only mean the preference gate took effect, not that the dedup ledger blocked it.
	setDigestPref(t, db, userID, false)
	clock = shanghaiAt(t, 20, 0).Add(7 * 24 * time.Hour)
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("disabled RunOnce: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("after opt-out enqueued %d, want 1 (no new send)", got)
	}

	// After turning it back on, there is no backfill within the same week either (she already turned it off once this week;
	// turning it on before the pointer moves means waiting for the next week). The assertion here is only that turning it
	// back on + the week after next resumes sending, proving that "off" is not irreversible.
	setDigestPref(t, db, userID, true)
	clock = shanghaiAt(t, 20, 0).Add(14 * 24 * time.Hour)
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("re-enabled RunOnce: %v", err)
	}
	if got := enq.count(); got != 2 {
		t.Fatalf("after re-enable enqueued %d, want 2 (resumes)", got)
	}
}

// TestDigestNumbersMatchStatsPage is acceptance 3: digest numbers match the stats page over the same interval.
//
// Every digest number comes directly from StatsStore, and the test recomputes it with the **same call arguments**;
// field-by-field equality proves the digest wrote no SQL of its own and will not drift from the stats page.
func TestDigestNumbersMatchStatsPage(t *testing.T) {
	db := newTestDB(t)
	userID := seedUser(t, db, "carol", "carol@example.com", "en", "Asia/Shanghai", 4)
	cardID := seedCard(t, db, userID)
	setDigestPref(t, db, userID, true)

	// Review history: 4 rows inside the window [2026-05-26, 2026-06-01], 1 row outside it.
	seedReview(t, db, userID, cardID, "2026-06-01", 3, 0, 5.0)  // newly introduced
	seedReview(t, db, userID, cardID, "2026-06-01", 1, 2, 0.5)  // due review, failed
	seedReview(t, db, userID, cardID, "2026-05-31", 3, 2, 10.0) // due review, passed
	seedReview(t, db, userID, cardID, "2026-05-30", 4, 0, 2.0)  // newly introduced
	seedReview(t, db, userID, cardID, "2026-05-01", 3, 2, 20.0) // outside the window (affects retention rate and streak)
	seedDueCard(t, db, userID, cardID, time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC))

	clock := shanghaiAt(t, 20, 0)
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

	// --- stats page basis: same batch of StatsStore calls, same arguments ---
	stats := store.NewStatsStore(db)
	today := reviewday.Day(now, loc, cutoff)
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
	// Also pin the expected values, so "both sides got it wrong the same way" cannot pass either.
	if got.Reviewed != 4 || got.NewCards != 2 || got.StreakCurrent != 3 || got.DueNow != 1 {
		t.Fatalf("unexpected digest numbers: %+v", got)
	}

	// The mail body must actually use these numbers.
	enq := &fakeEnqueuer{configured: true}
	w := newWorker(t, db, enq, func() time.Time { return clock })
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if enq.count() != 1 {
		t.Fatalf("enqueued %d, want 1", enq.count())
	}
	body := enq.last().TextBody
	if !strings.Contains(body, "Reviews: 4") {
		t.Errorf("body missing reviewed count, got:\n%s", body)
	}
	if !strings.Contains(body, "Current streak: 3 days") {
		t.Errorf("body missing streak, got:\n%s", body)
	}
	if !strings.Contains(body, "Due now: 1") {
		t.Errorf("body missing due count, got:\n%s", body)
	}
}

// TestDigestDefaultOffWhenNoPreference verifies C-class defaults to off: no preference row means no send.
func TestDigestDefaultOffWhenNoPreference(t *testing.T) {
	db := newTestDB(t)
	seedUser(t, db, "dave", "dave@example.com", "en", "Asia/Shanghai", 4)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 20, 0)
	w := newWorker(t, db, enq, func() time.Time { return clock })

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := enq.count(); got != 0 {
		t.Fatalf("class C default off: enqueued %d, want 0", got)
	}
}

// TestDigestNotConfiguredDoesNotSendOrError verifies that with SMTP unconfigured nothing is sent and no error is raised.
func TestDigestNotConfiguredDoesNotSendOrError(t *testing.T) {
	db := newTestDB(t)
	userID := seedUser(t, db, "erin", "erin@example.com", "en", "Asia/Shanghai", 4)
	setDigestPref(t, db, userID, true)

	enq := &fakeEnqueuer{configured: false}
	clock := shanghaiAt(t, 20, 0)
	w := newWorker(t, db, enq, func() time.Time { return clock })

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce with smtp unconfigured returned error: %v", err)
	}
	if got := enq.count(); got != 0 {
		t.Fatalf("smtp unconfigured: enqueued %d, want 0", got)
	}
}

// TestDigestUnsubscribeHeaders verifies the C-class digest carries the one-click unsubscribe header and the token names study_digest.
func TestDigestUnsubscribeHeaders(t *testing.T) {
	db := newTestDB(t)
	userID := seedUser(t, db, "frank", "frank@example.com", "en", "Asia/Shanghai", 4)
	seedCard(t, db, userID)
	setDigestPref(t, db, userID, true)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 20, 0)
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

// setUserSendHour changes users.reminder_hour directly in the database; nil means unset (falls back to the global default).
func setUserSendHour(t *testing.T, db *gorm.DB, userID uint64, hour *int) {
	t.Helper()
	users := store.NewUserStore(db)
	u, err := users.ByID(context.Background(), userID)
	if err != nil {
		t.Fatalf("load user %d: %v", userID, err)
	}
	u.ReminderHour = hour
	if err := users.Update(context.Background(), u); err != nil {
		t.Fatalf("update user %d: %v", userID, err)
	}
}

// TestDigestUsesUserSendHour acceptance: the weekly digest reuses the review reminder's send hour (it is not configured separately) —
// nothing is sent before the hour, and it is only sent on the first round after the user's chosen hour.
func TestDigestUsesUserSendHour(t *testing.T) {
	if reminder.DefaultSendHour != 19 {
		t.Fatalf("reminder.DefaultSendHour = %d, want 19", reminder.DefaultSendHour)
	}
	db := newTestDB(t)
	userID := seedUser(t, db, "grace", "grace@example.com", "en", "Asia/Shanghai", 4)
	seedCard(t, db, userID)
	setDigestPref(t, db, userID, true)
	// Explicitly set to 21:00: the default 19:00 round must not send; only the 21:00 round does.
	hour := 21
	setUserSendHour(t, db, userID, &hour)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 19, 0)
	w := newWorker(t, db, enq, func() time.Time { return clock })
	ctx := context.Background()

	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce at 19:00: %v", err)
	}
	if got := enq.count(); got != 0 {
		t.Fatalf("digest at local 19:00 before the chosen hour 21: enqueued %d, want 0", got)
	}

	clock = shanghaiAt(t, 21, 0)
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce at 21:00: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("digest at local 21:00 at the chosen hour: enqueued %d, want 1", got)
	}
}
