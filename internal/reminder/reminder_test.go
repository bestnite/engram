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

// fakeEnqueuer records enqueued mail, avoiding a dependency on real SMTP or the outbox worker.
// It also implements Enqueuer's Configured, so the "SMTP not configured" path is easy to test.
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

// newTestDB opens a temporary SQLite database and migrates all models (including reminder_log).
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

// seedUserWithDueCard creates an active user and attaches one due review card.
// dueAt is a past timestamp, guaranteeing the candidate query always matches.
func seedUserWithDueCard(t *testing.T, db *gorm.DB, name, email, locale, tz string, cutoff int, dueAt time.Time) uint64 {
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
		OwnerUserID: user.ID, Name: "Deck", Description: "",
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

// enableReminder explicitly turns on that user's review reminder preference (class C defaults off).
func enableReminder(t *testing.T, db *gorm.DB, userID uint64) {
	t.Helper()
	store := store.NewEmailPrefStore(db)
	if err := store.SetChoices(context.Background(), userID, map[string]bool{
		string(mail.TypeReviewReminder): true,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("enable reminder: %v", err)
	}
}

// disableReminder explicitly turns off that user's review reminder preference.
func disableReminder(t *testing.T, db *gorm.DB, userID uint64) {
	t.Helper()
	prefs := store.NewEmailPrefStore(db)
	if err := prefs.SetChoices(context.Background(), userID, map[string]bool{
		string(mail.TypeReviewReminder): false,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("disable reminder: %v", err)
	}
}

// newReminder builds a Reminder with a controllable clock and a fake enqueuer.
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

// shanghaiAt returns the UTC instant for "Shanghai local hh:mm" (same day, date fixed at 2026-06-01).
func shanghaiAt(t *testing.T, hour, min int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	return time.Date(2026, 6, 1, hour, min, 0, 0, loc).UTC()
}

// TestSecondReminderSameDayNotSent verifies the second mail on the same day is not sent (a pure daily cap, independent of send time).
func TestSecondReminderSameDayNotSent(t *testing.T) {
	db := newTestDB(t)
	userID := seedUserWithDueCard(t, db, "bob", "bob@example.com", "en", "Asia/Shanghai", 4,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	enableReminder(t, db, userID)

	enq := &fakeEnqueuer{configured: true}
	// Default send hour is 19:00: the first poll uses 19:05, already past the hour.
	clock := shanghaiAt(t, 19, 5)
	r := newReminder(t, db, enq, func() time.Time { return clock })
	ctx := context.Background()

	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("first run enqueued %d, want 1", got)
	}
	// Run another round later the same day: the ledger already has an entry, so no second mail.
	clock = shanghaiAt(t, 23, 30)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("second run same day enqueued %d, want 1", got)
	}
}

// TestOptOutStopsImmediately verifies sending stops immediately once the user turns it off: a new day could send, but the preference is off, so nothing is sent.
func TestOptOutStopsImmediately(t *testing.T) {
	db := newTestDB(t)
	userID := seedUserWithDueCard(t, db, "carol", "carol@example.com", "en", "Asia/Shanghai", 4,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	enableReminder(t, db, userID)

	enq := &fakeEnqueuer{configured: true}
	// Default send hour is 19:00: use 19:05, already past the hour.
	clock := shanghaiAt(t, 19, 5)
	r := newReminder(t, db, enq, func() time.Time { return clock })
	ctx := context.Background()

	// While on, one mail goes out normally.
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("enabled RunOnce: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("enabled run enqueued %d, want 1", got)
	}

	// Turn the preference off and run again on a **new day**: the new day is not capped by the daily limit, so only the preference being off can stop it.
	disableReminder(t, db, userID)
	clock = shanghaiAt(t, 19, 5).Add(24 * time.Hour)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("disabled RunOnce: %v", err)
	}
	if got := enq.count(); got != 1 {
		t.Fatalf("after opt-out enqueued %d, want 1 (no new send)", got)
	}
}

// TestDefaultOffWhenNoPreference verifies class C defaults to off: with no preference row, no send.
func TestDefaultOffWhenNoPreference(t *testing.T) {
	db := newTestDB(t)
	seedUserWithDueCard(t, db, "dave", "dave@example.com", "en", "Asia/Shanghai", 4,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 20, 0)
	r := newReminder(t, db, enq, func() time.Time { return clock })

	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := enq.count(); got != 0 {
		t.Fatalf("class C default off: enqueued %d, want 0", got)
	}
}

// TestNotConfiguredDoesNotSendOrError verifies that nothing is sent and no error is reported when SMTP is unconfigured.
func TestNotConfiguredDoesNotSendOrError(t *testing.T) {
	db := newTestDB(t)
	userID := seedUserWithDueCard(t, db, "erin", "erin@example.com", "en", "Asia/Shanghai", 4,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	enableReminder(t, db, userID)

	enq := &fakeEnqueuer{configured: false}
	clock := shanghaiAt(t, 20, 0)
	r := newReminder(t, db, enq, func() time.Time { return clock })

	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce with smtp unconfigured returned error: %v", err)
	}
	if got := enq.count(); got != 0 {
		t.Fatalf("smtp unconfigured: enqueued %d, want 0", got)
	}
}

// TestNoDueCardsNoReminder verifies a user with no due cards is not nagged.
func TestNoDueCardsNoReminder(t *testing.T) {
	db := newTestDB(t)
	userID := seedUserWithDueCard(t, db, "frank", "frank@example.com", "en", "Asia/Shanghai", 4,
		// Due time is in the future → no due cards right now.
		time.Now().UTC().Add(72*time.Hour))
	enableReminder(t, db, userID)

	enq := &fakeEnqueuer{configured: true}
	clock := shanghaiAt(t, 20, 0)
	r := newReminder(t, db, enq, func() time.Time { return clock })

	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := enq.count(); got != 0 {
		t.Fatalf("no due cards: enqueued %d, want 0", got)
	}
}
