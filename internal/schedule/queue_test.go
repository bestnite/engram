package schedule

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// newTestDB creates a temporary SQLite database and runs all model migrations (AGENTS.md §2.5: do not mock the database).
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "schedule.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(store.AllModels()...); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return db
}

// seedDeck creates a preset + private deck and returns the deck id.
func seedDeck(t *testing.T, db *gorm.DB, now time.Time) uint64 {
	t.Helper()
	p := store.NewPreset(1, "preset")
	p.EnableFuzz = boolPtr(false)
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	d := store.Deck{OwnerUserID: 1, Name: "Deck", Description: "", PresetID: p.ID, CreatedAt: now}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	return d.ID
}

// seedCard creates a basic note and one of its cards, returning the card id.
func seedCard(t *testing.T, db *gorm.DB, deckID uint64, template string, createdAt time.Time) uint64 {
	t.Helper()
	n := store.Note{DeckID: deckID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`, TagsJSON: "[]", CreatedAt: createdAt, UpdatedAt: createdAt}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	c := store.Card{NoteID: n.ID, Template: template, Ordinal: 0, CreatedAt: createdAt}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	return c.ID
}

// seedState writes one card_states row.
func seedState(t *testing.T, db *gorm.DB, userID, cardID uint64, state string, due time.Time, stability, difficulty float64, last *time.Time) {
	t.Helper()
	row := store.CardState{UserID: userID, CardID: cardID, State: state, DueAt: &due, StepIndex: 1, ScheduledDays: 10}
	if stability > 0 {
		row.Stability = &stability
	}
	if difficulty > 0 {
		row.Difficulty = &difficulty
	}
	row.LastReviewAt = last
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}
}

// seedReview writes one append-only review log row.
func seedReview(t *testing.T, db *gorm.DB, userID, cardID uint64, day string, stateBefore int, at time.Time) {
	t.Helper()
	r := store.Review{CardID: cardID, UserID: userID, Rating: int(Good), GradeSource: "self", ReviewedAt: at, ReviewDay: day, StateBefore: stateBefore}
	if err := db.Create(&r).Error; err != nil {
		t.Fatalf("create review: %v", err)
	}
}

// kindsOf extracts the priority sequence of queue items, making order assertions easy.
func kindsOf(items []QueueItem) []QueueKind {
	out := make([]QueueKind, len(items))
	for i, it := range items {
		out[i] = it.Kind
	}
	return out
}

// TestBuildOrdering asserts the queue order: learning cards → due review cards → new cards, and that review cards can be sorted two ways.
func TestBuildOrdering(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)

	learningCard := seedCard(t, db, deckID, "forward", now)
	seedState(t, db, 1, learningCard, "learning", now.Add(-time.Minute), 0, 0, nil)

	// Two review cards: R1 has low stability (more likely to be forgotten), R2 is due earlier but has high stability.
	lowStab := seedCard(t, db, deckID, "forward", now)
	highStab := seedCard(t, db, deckID, "forward", now)
	last := now.Add(-48 * time.Hour)
	seedState(t, db, 1, lowStab, "review", now.Add(-time.Hour), 1.0, 5.0, &last)
	seedState(t, db, 1, highStab, "review", now.Add(-2*time.Hour), 50.0, 5.0, &last)

	newIDs := []uint64{
		seedCard(t, db, deckID, "forward", now.Add(time.Minute)),
		seedCard(t, db, deckID, "forward", now.Add(2*time.Minute)),
		seedCard(t, db, deckID, "forward", now.Add(3*time.Minute)),
	}

	s := mustScheduler(t, testPreset(t))
	builder := NewQueueBuilder(db, store.NewDeckStore(db), s)
	base := QueueOptions{
		DeckID: deckID, Now: now, Location: time.UTC,
		NewPerDay: 10, ReviewsPerDay: 200, NewOrder: NewOrderCreated,
	}

	ctx := context.Background()
	items, err := builder.Build(ctx, 1, withOrder(base, OrderByRetrievability))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	wantKinds := []QueueKind{QueueLearning, QueueReview, QueueReview, QueueNew, QueueNew, QueueNew}
	if got := kindsOf(items); len(got) != len(wantKinds) {
		t.Fatalf("Build() kinds = %v, want %v", got, wantKinds)
	} else {
		for i := range wantKinds {
			if got[i] != wantKinds[i] {
				t.Fatalf("Build() kinds = %v, want %v", got, wantKinds)
			}
		}
	}
	if items[0].CardID != learningCard {
		t.Errorf("first item card = %d, want learning card %d", items[0].CardID, learningCard)
	}
	// Ascending retrievability: low-stability R1 comes before high-stability R2.
	if items[1].CardID != lowStab || items[2].CardID != highStab {
		t.Errorf("review order = [%d %d], want [%d %d] by retrievability", items[1].CardID, items[2].CardID, lowStab, highStab)
	}
	if !(items[1].Retrievability < items[2].Retrievability) {
		t.Errorf("retrievabilities = %v, %v; want ascending", items[1].Retrievability, items[2].Retrievability)
	}
	for i, id := range newIDs {
		if items[3+i].CardID != id {
			t.Errorf("new item %d = %d, want %d (creation order)", i, items[3+i].CardID, id)
		}
	}

	// Ascending due_at: R2 is due earlier, so it comes before R1.
	byDue, err := builder.Build(ctx, 1, withOrder(base, OrderByDueAt))
	if err != nil {
		t.Fatalf("Build(due) error = %v", err)
	}
	if byDue[1].CardID != highStab || byDue[2].CardID != lowStab {
		t.Errorf("due order = [%d %d], want [%d %d]", byDue[1].CardID, byDue[2].CardID, highStab, lowStab)
	}
}

func withOrder(o QueueOptions, order ReviewOrder) QueueOptions {
	o.ReviewOrder = order
	return o
}

// TestDailyNewCapAcrossDayBoundary asserts that 03:59 / 04:00 belong to different review days,
// so the daily new-card cap is correctly reduced by each side's introduced count (the cutoff rule).
func TestDailyNewCapAcrossDayBoundary(t *testing.T) {
	db := newTestDB(t)
	loc := time.FixedZone("test", 8*3600)
	deckID := seedDeck(t, db, time.Now().UTC())

	// 5 new cards.
	for i := 0; i < 5; i++ {
		seedCard(t, db, deckID, string(rune('A'+i)), time.Now().UTC().Add(time.Duration(i)*time.Minute))
	}
	// Yesterday (review day 2026-10-01) already introduced 2 new cards.
	seedReview(t, db, 1, 1, "2026-10-01", int(StateNew), time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC))
	seedReview(t, db, 1, 2, "2026-10-01", int(StateNew), time.Date(2026, 10, 1, 20, 5, 0, 0, time.UTC))

	if got := ReviewDay(time.Date(2026, 10, 2, 3, 59, 0, 0, loc), loc, 4); got != "2026-10-01" {
		t.Fatalf("ReviewDay(03:59) = %q, want 2026-10-01", got)
	}
	if got := ReviewDay(time.Date(2026, 10, 2, 4, 0, 0, 0, loc), loc, 4); got != "2026-10-02" {
		t.Fatalf("ReviewDay(04:00) = %q, want 2026-10-02", got)
	}

	s := mustScheduler(t, testPreset(t))
	builder := NewQueueBuilder(db, store.NewDeckStore(db), s)
	newCount := func(now time.Time) int {
		t.Helper()
		items, err := builder.Build(context.Background(), 1, QueueOptions{
			DeckID: deckID, Now: now, Location: loc, NewPerDay: 3, ReviewsPerDay: 200, NewOrder: NewOrderCreated,
		})
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		n := 0
		for _, it := range items {
			if it.Kind == QueueNew {
				n++
			}
		}
		return n
	}

	if got := newCount(time.Date(2026, 10, 2, 3, 59, 0, 0, loc)); got != 1 {
		t.Errorf("new cards at 03:59 = %d, want 1 (3 cap - 2 already introduced on 2026-10-01)", got)
	}
	if got := newCount(time.Date(2026, 10, 2, 4, 0, 0, 0, loc)); got != 3 {
		t.Errorf("new cards at 04:00 = %d, want 3 (fresh review day)", got)
	}
}

// TestReviewsPerDayCap asserts the daily review cap comes from the reviews aggregate: 0 means unlimited.
func TestReviewsPerDayCap(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	for i := 0; i < 3; i++ {
		id := seedCard(t, db, deckID, string(rune('A'+i)), now)
		last := now.Add(-24 * time.Hour)
		seedState(t, db, 1, id, "review", now.Add(-time.Hour), 5.0, 5.0, &last)
	}

	s := mustScheduler(t, testPreset(t))
	builder := NewQueueBuilder(db, store.NewDeckStore(db), s)
	ctx := context.Background()

	capped, err := builder.Build(ctx, 1, QueueOptions{DeckID: deckID, Now: now, Location: time.UTC, ReviewsPerDay: 1})
	if err != nil {
		t.Fatalf("Build(cap=1) error = %v", err)
	}
	if len(capped) != 1 || capped[0].Kind != QueueReview {
		t.Fatalf("Build(cap=1) = %+v, want exactly 1 review card", capped)
	}

	unlimited, err := builder.Build(ctx, 1, QueueOptions{DeckID: deckID, Now: now, Location: time.UTC, ReviewsPerDay: 0})
	if err != nil {
		t.Fatalf("Build(cap=0) error = %v", err)
	}
	if len(unlimited) != 3 {
		t.Errorf("Build(cap=0) returned %d cards, want 3 (unlimited)", len(unlimited))
	}
}

// TestQueueExcludesSuspendedAndDeleted asserts suspended cards and cards of soft-deleted notes do not appear in the queue.
func TestQueueExcludesSuspendedAndDeleted(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)

	visible := seedCard(t, db, deckID, "forward", now)
	last := now.Add(-24 * time.Hour)
	seedState(t, db, 1, visible, "review", now.Add(-time.Hour), 5.0, 5.0, &last)

	suspended := seedCard(t, db, deckID, "forward", now)
	seedState(t, db, 1, suspended, "review", now.Add(-time.Hour), 5.0, 5.0, &last)
	if err := db.Model(&store.CardState{}).Where("card_id = ? AND user_id = ?", suspended, 1).Update("suspended_at", now).Error; err != nil {
		t.Fatalf("suspend card: %v", err)
	}

	deleted := seedCard(t, db, deckID, "forward", now)
	seedState(t, db, 1, deleted, "review", now.Add(-time.Hour), 5.0, 5.0, &last)
	if err := db.Where("id = ?", deleted).Delete(&store.Card{}).Error; err != nil {
		t.Fatalf("delete card: %v", err)
	}

	s := mustScheduler(t, testPreset(t))
	items, err := NewQueueBuilder(db, store.NewDeckStore(db), s).Build(context.Background(), 1, QueueOptions{DeckID: deckID, Now: now, Location: time.UTC})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(items) != 1 || items[0].CardID != visible {
		t.Errorf("Build() = %+v, want only the visible card %d", items, visible)
	}
}

// ---- : multi-deck review scope ----

// seedDeckCaps creates a deck and writes the given daily caps into it (deck-level configuration).
func seedDeckCaps(t *testing.T, db *gorm.DB, now time.Time, newPerDay, reviewsPerDay int) uint64 {
	t.Helper()
	deckID := seedDeck(t, db, now)
	if err := db.Model(&store.Deck{}).Where("id = ?", deckID).
		Updates(map[string]any{"new_per_day": newPerDay, "reviews_per_day": reviewsPerDay}).Error; err != nil {
		t.Fatalf("set deck caps: %v", err)
	}
	return deckID
}

// countKind counts the number of queue entries of a given priority.
func countKind(items []QueueItem, kind QueueKind) int {
	n := 0
	for _, it := range items {
		if it.Kind == kind {
			n++
		}
	}
	return n
}

// deckIDsOf extracts the deduplicated deck set of the queue items.
func deckIDsOf(items []QueueItem) map[uint64]bool {
	out := map[uint64]bool{}
	for _, it := range items {
		out[it.DeckID] = true
	}
	return out
}

// TestDeckIDsScopeSelectsExactSet asserts the set mode takes only the selected decks and takes precedence over DeckID;
// an empty set is equivalent to DeckID=0 (whole library).
func TestDeckIDsScopeSelectsExactSet(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckA := seedDeck(t, db, now)
	deckB := seedDeck(t, db, now)
	deckC := seedDeck(t, db, now)
	seedCard(t, db, deckA, "forward", now)
	seedCard(t, db, deckB, "forward", now)
	seedCard(t, db, deckC, "forward", now)

	s := mustScheduler(t, testPreset(t))
	builder := NewQueueBuilder(db, store.NewDeckStore(db), s)
	ctx := context.Background()
	// Explicit cap overrides, so the deck columns cannot interfere with this case.
	base := QueueOptions{Now: now, Location: time.UTC, NewPerDay: 10, ReviewsPerDay: 200, NewOrder: NewOrderCreated}

	set := base
	set.DeckIDs = []uint64{deckA, deckB}
	got, err := builder.Build(ctx, 1, set)
	if err != nil {
		t.Fatalf("Build(DeckIDs=[A,B]) error = %v", err)
	}
	if ids := deckIDsOf(got); len(got) != 2 || !ids[deckA] || !ids[deckB] || ids[deckC] {
		t.Errorf("Build(DeckIDs=[A,B]) = %v, want exactly decks A and B", ids)
	}

	// The set wins over a single deck: DeckID=B but DeckIDs=[A] → only A is taken.
	both := base
	both.DeckID = deckB
	both.DeckIDs = []uint64{deckA}
	got, err = builder.Build(ctx, 1, both)
	if err != nil {
		t.Fatalf("Build(DeckID=B,DeckIDs=[A]) error = %v", err)
	}
	if ids := deckIDsOf(got); len(got) != 1 || !ids[deckA] {
		t.Errorf("Build(DeckID=B,DeckIDs=[A]) = %v, want only deck A (DeckIDs wins)", ids)
	}

	// Empty set = whole library, the same as DeckID=0.
	empty := base
	empty.DeckIDs = []uint64{}
	got, err = builder.Build(ctx, 1, empty)
	if err != nil {
		t.Fatalf("Build(DeckIDs=[]) error = %v", err)
	}
	if len(got) != 3 {
		t.Errorf("Build(DeckIDs=[]) returned %d cards, want 3 (whole collection)", len(got))
	}
	whole, err := builder.Build(ctx, 1, base)
	if err != nil {
		t.Fatalf("Build(DeckID=0) error = %v", err)
	}
	if len(whole) != len(got) {
		t.Errorf("empty DeckIDs (%d cards) differs from DeckID=0 (%d cards)", len(got), len(whole))
	}

	// Set deduplication: writing the same deck twice does not change the result.
	dup := base
	dup.DeckIDs = []uint64{deckA, deckA}
	got, err = builder.Build(ctx, 1, dup)
	if err != nil {
		t.Fatalf("Build(DeckIDs=[A,A]) error = %v", err)
	}
	if ids := deckIDsOf(got); len(got) != 1 || !ids[deckA] {
		t.Errorf("Build(DeckIDs=[A,A]) = %v, want exactly one card from deck A", ids)
	}
}

// TestScopeHonoursPerDeckCaps asserts the set mode (with multiple decks) also computes by **each deck's own** allowance:
// deck A 1/1, deck B 3/3, each with 3 new cards + 3 due review cards → total new 1+3, review 1+3.
// This is the direct acceptance test for "multiple decks = the sum of the per-deck allowances".
func TestScopeHonoursPerDeckCaps(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckA := seedDeckCaps(t, db, now, 1, 1)
	deckB := seedDeckCaps(t, db, now, 3, 3)

	for _, deckID := range []uint64{deckA, deckB} {
		for i := 0; i < 3; i++ {
			seedCard(t, db, deckID, string(rune('A'+i)), now.Add(time.Duration(i)*time.Minute))
		}
		last := now.Add(-24 * time.Hour)
		for i := 0; i < 3; i++ {
			id := seedCard(t, db, deckID, string(rune('R'+i)), now)
			seedState(t, db, 1, id, "review", now.Add(-time.Hour), 5.0, 5.0, &last)
		}
	}

	s := mustScheduler(t, testPreset(t))
	builder := NewQueueBuilder(db, store.NewDeckStore(db), s)
	ctx := context.Background()

	single, err := builder.Build(ctx, 1, QueueOptions{DeckID: deckA, Now: now, Location: time.UTC})
	if err != nil {
		t.Fatalf("Build(DeckID) error = %v", err)
	}
	if n := countKind(single, QueueNew); n != 1 {
		t.Errorf("single-deck new cards = %d, want 1 (deck A new_per_day)", n)
	}
	if n := countKind(single, QueueReview); n != 1 {
		t.Errorf("single-deck review cards = %d, want 1 (deck A reviews_per_day)", n)
	}

	multi, err := builder.Build(ctx, 1, QueueOptions{DeckIDs: []uint64{deckA, deckB}, Now: now, Location: time.UTC})
	if err != nil {
		t.Fatalf("Build(DeckIDs) error = %v", err)
	}
	if n := countKind(multi, QueueNew); n != 4 {
		t.Errorf("multi-deck new cards = %d, want 4 (A 1 + B 3)", n)
	}
	if n := countKind(multi, QueueReview); n != 4 {
		t.Errorf("multi-deck review cards = %d, want 4 (A 1 + B 3)", n)
	}
}

// TestLearnAheadAppendsSoonDueLearningCards covers learn-ahead: learning cards falling due within the window go to the end of the queue
// (after the other cards), those outside the window do not appear, and with a window of 0 nothing is pulled ahead (negative case).
func TestLearnAheadAppendsSoonDueLearningCards(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	last := now.Add(-time.Minute)
	soon := seedCard(t, db, deckID, "forward", now)
	seedState(t, db, 1, soon, "learning", now.Add(5*time.Minute), 1, 5, &last)
	later := seedCard(t, db, deckID, "forward", now)
	seedState(t, db, 1, later, "relearning", now.Add(time.Hour), 1, 5, &last)
	fresh := seedCard(t, db, deckID, "forward", now)

	s := mustScheduler(t, testPreset(t))
	build := func(ahead time.Duration) []uint64 {
		t.Helper()
		items, err := NewQueueBuilder(db, store.NewDeckStore(db), s).Build(context.Background(), 1,
			QueueOptions{DeckID: deckID, Now: now, Location: time.UTC, LearnAhead: ahead, NewOrder: NewOrderCreated})
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		ids := make([]uint64, 0, len(items))
		for _, it := range items {
			ids = append(ids, it.CardID)
		}
		return ids
	}
	cases := []struct {
		name  string
		ahead time.Duration
		want  []uint64
	}{
		{"no learn-ahead", 0, []uint64{fresh}},
		{"20 minutes pulls the 5-minute card in after the new card", 20 * time.Minute, []uint64{fresh, soon}},
		{"two hours also pulls the 1-hour card, in due order", 2 * time.Hour, []uint64{fresh, soon, later}},
	}
	for _, tc := range cases {
		got := build(tc.ahead)
		if len(got) != len(tc.want) {
			t.Errorf("%s: queue = %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: queue = %v, want %v", tc.name, got, tc.want)
				break
			}
		}
	}
}
