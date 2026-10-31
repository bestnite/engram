package api

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// countingLogger counts the SQL statements GORM actually executes: one per Trace call.
// It only counts and does not alter execution, used to assert "the query count is independent of queue length".
type countingLogger struct {
	mu sync.Mutex
	n  int
}

func (l *countingLogger) LogMode(gormlogger.LogLevel) gormlogger.Interface { return l }
func (l *countingLogger) Info(context.Context, string, ...interface{})     {}
func (l *countingLogger) Warn(context.Context, string, ...interface{})     {}
func (l *countingLogger) Error(context.Context, string, ...interface{})    {}
func (l *countingLogger) Trace(context.Context, time.Time, func() (string, int64), error) {
	l.mu.Lock()
	l.n++
	l.mu.Unlock()
}

func (l *countingLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

func (l *countingLogger) reset() {
	l.mu.Lock()
	l.n = 0
	l.mu.Unlock()
}

// seedReviewCard creates a note + card + one review-state card_states row (due_at already past),
// so the card is bound to enter the due queue; version is used to assert the optimistic-lock version comes back with the queue.
func seedReviewCard(t *testing.T, db *gorm.DB, deckID, userID uint64, dueAt time.Time, front string, version int) store.Card {
	t.Helper()
	now := time.Now().UTC()
	n := store.Note{
		DeckID:     deckID,
		NoteTypeID: store.Ptr(stockNoteTypeID(t, db, deckID, "basic")),
		FieldsJSON: fmt.Sprintf(`{"front":%q,"back":"a"}`, front),
		TagsJSON:   `["t1","t2"]`, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	c := store.Card{NoteID: n.ID, Template: "forward", Ordinal: 0, CreatedAt: now}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	due := dueAt.UTC()
	st := store.CardState{CardID: c.ID, UserID: userID, State: "review", DueAt: &due, Version: version}
	if err := db.Create(&st).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}
	return c
}

// seedNewCard creates a note + card without writing card_states: the card is new.
func seedNewCard(t *testing.T, db *gorm.DB, deckID uint64, front string) store.Card {
	t.Helper()
	now := time.Now().UTC()
	n := store.Note{
		DeckID:     deckID,
		NoteTypeID: store.Ptr(stockNoteTypeID(t, db, deckID, "basic")),
		FieldsJSON: fmt.Sprintf(`{"front":%q,"back":"a"}`, front),
		TagsJSON:   "[]", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	c := store.Card{NoteID: n.ID, Template: "forward", Ordinal: 0, CreatedAt: now}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	return c
}

// dueCardsStatements sets up a deck with n due review cards, calls DueCards and returns the number of statements it issued.
func dueCardsStatements(t *testing.T, n int) (int, []DueCard) {
	t.Helper()
	env := newTestEnv(t, 600, 600)
	user := seedUser(t, env.db, fmt.Sprintf("perf%d", n), store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	for i := 0; i < n; i++ {
		seedReviewCard(t, env.db, deck.ID, user.ID, env.now.Add(-time.Hour), fmt.Sprintf("q%d", i), 1)
	}
	logger := &countingLogger{}
	env.db.Logger = logger
	logger.reset()
	got, err := env.api.DueCards(context.Background(), user, []uint64{deck.ID}, nil, 500)
	if err != nil {
		t.Fatalf("DueCards() error = %v", err)
	}
	return logger.count(), got
}

// TestDueCardsQueryCountIsConstant asserts the number of statements DueCards issues during assembly is independent of queue length:
// a "2-card queue" and a "50-card queue" must issue the same number of statements (constant), not grow linearly with the card count.
func TestDueCardsQueryCountIsConstant(t *testing.T) {
	small, gotSmall := dueCardsStatements(t, 2)
	large, gotLarge := dueCardsStatements(t, 50)
	if len(gotSmall) != 2 {
		t.Fatalf("2-card queue returned %d cards, want 2", len(gotSmall))
	}
	if len(gotLarge) != 50 {
		t.Fatalf("50-card queue returned %d cards, want 50", len(gotLarge))
	}
	t.Logf("DueCards statements: 2 cards = %d, 50 cards = %d", small, large)
	if small != large {
		t.Errorf("DueCards query count depends on queue length: 2 cards = %d statements, 50 cards = %d statements; want equal",
			small, large)
	}
}

// TestQueueBuildQueryCountIsConstant records the statement count of queue building (schedule.NewQueueBuilder.Build):
// it already loads by "set / per deck", so the statement count varies with the number of decks, not with the number of cards.
func TestQueueBuildQueryCountIsConstant(t *testing.T) {
	build := func(n int) int {
		t.Helper()
		env := newTestEnv(t, 600, 600)
		user := seedUser(t, env.db, fmt.Sprintf("build%d", n), store.RoleUser)
		deck := seedDeck(t, env.db, user.ID)
		for i := 0; i < n; i++ {
			seedReviewCard(t, env.db, deck.ID, user.ID, env.now.Add(-time.Hour), fmt.Sprintf("q%d", i), 1)
		}
		sched, err := env.api.schedulerForDeck(context.Background(), user.ID, deck)
		if err != nil {
			t.Fatalf("schedulerForDeck() error = %v", err)
		}
		builder := schedule.NewQueueBuilder(env.db, store.NewDeckStore(env.db), sched)
		logger := &countingLogger{}
		env.db.Logger = logger
		logger.reset()
		items, err := builder.Build(context.Background(), user.ID, schedule.QueueOptions{
			DeckID:        deck.ID,
			Now:           env.now,
			Timezone:      "UTC",
			DayCutoffHour: store.Ptr(4),
			ReviewOrder:   schedule.OrderByDueAt,
			NewOrder:      schedule.NewOrderCreated,
		})
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		if len(items) != n {
			t.Fatalf("Build() returned %d items, want %d", len(items), n)
		}
		return logger.count()
	}
	small := build(2)
	large := build(50)
	t.Logf("QueueBuilder.Build statements: 2 cards = %d, 50 cards = %d", small, large)
	if small != large {
		t.Errorf("Build query count depends on card count: 2 cards = %d statements, 50 cards = %d statements; want equal",
			small, large)
	}
}

// TestDueCardsContractFields guards DueCards' external contract: deck external id, card order, Version, Template,
// DeckID and Kind/Fields/Tags must all be populated correctly by the batch assembly.
func TestDueCardsContractFields(t *testing.T) {
	env := newTestEnv(t, 600, 600)
	user := seedUser(t, env.db, "contract", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	first := seedReviewCard(t, env.db, deck.ID, user.ID, env.now.Add(-2*time.Hour), "q1", 7)
	second := seedReviewCard(t, env.db, deck.ID, user.ID, env.now.Add(-time.Hour), "q2", 9)

	got, err := env.api.DueCards(context.Background(), user, []uint64{deck.ID}, nil, 500)
	if err != nil {
		t.Fatalf("DueCards() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("DueCards() returned %d cards, want 2", len(got))
	}
	// Card order: due review cards in ascending due_at.
	if got[0].CardID != first.PublicID || got[1].CardID != second.PublicID {
		t.Fatalf("card order = [%s %s], want [%s %s]", got[0].CardID, got[1].CardID, first.PublicID, second.PublicID)
	}
	head := got[0]
	if head.CardID != first.PublicID {
		t.Errorf("CardID = %q, want %q", head.CardID, first.PublicID)
	}
	if head.NoteID == "" {
		t.Errorf("NoteID is empty")
	}
	if head.DeckID != deck.PublicID {
		t.Errorf("DeckID = %q, want deck public id %q", head.DeckID, deck.PublicID)
	}
	if head.State != "review" {
		t.Errorf("State = %q, want review", head.State)
	}
	if head.Version != 7 {
		t.Errorf("Version = %d, want 7", head.Version)
	}
	if head.Template != "forward" {
		t.Errorf("Template = %q, want forward", head.Template)
	}
	if head.Fields["front"] != "q1" {
		t.Errorf("Fields[front] = %v, want q1", head.Fields["front"])
	}
	if len(head.Tags) != 2 || head.Tags[0] != "t1" || head.Tags[1] != "t2" {
		t.Errorf("Tags = %v, want [t1 t2]", head.Tags)
	}
	if !head.DueAt.Equal(env.now.Add(-2 * time.Hour)) {
		t.Errorf("DueAt = %v, want %v", head.DueAt, env.now.Add(-2*time.Hour))
	}
}

// TestDueCardsLimitClamp guards the [1,500] clamping of limit: lower bound 0->1, upper bound >500->500.
// The upper bound is observed with a deck of "unlimited new cards" plus 501 new cards, stretching the queue past 500.
func TestDueCardsLimitClamp(t *testing.T) {
	env := newTestEnv(t, 600, 600)
	user := seedUser(t, env.db, "clamp", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	// The deck's daily cap is set to 0 = unlimited, so all 501 new cards can enter the queue.
	if err := store.NewDeckStore(env.db).SetCaps(context.Background(), user.ID, deck.ID, store.DeckCaps{NewPerDay: 0, ReviewsPerDay: 0}); err != nil {
		t.Fatalf("SetCaps() error = %v", err)
	}
	const seeded = 501
	for i := 0; i < seeded; i++ {
		seedNewCard(t, env.db, deck.ID, fmt.Sprintf("n%d", i))
	}

	all, err := env.api.DueCards(context.Background(), user, []uint64{deck.ID}, nil, 0)
	if err != nil {
		t.Fatalf("DueCards(limit=0) error = %v", err)
	}
	if len(all) != 1 {
		t.Errorf("DueCards(limit=0) returned %d cards, want 1 (clamped to lower bound)", len(all))
	}

	capped, err := env.api.DueCards(context.Background(), user, []uint64{deck.ID}, nil, 9999)
	if err != nil {
		t.Fatalf("DueCards(limit=9999) error = %v", err)
	}
	if len(capped) != 500 {
		t.Errorf("DueCards(limit=9999) returned %d cards, want 500 (clamped to upper bound)", len(capped))
	}
}
