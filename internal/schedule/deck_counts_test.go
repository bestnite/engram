package schedule

import (
	"context"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestDeckCountsMatchBuildPerDeck is the core acceptance test of this allowance-semantics refactor (property test):
// for every visible deck in the fixture, DeckCounts[d] must equal the number of QueueNew / QueueReview
// entries of those two kinds in Build(DeckID=d). DeckCounts and Build share the same card-fetch path (collect) and the same allowance formula
// (deckBudget), so "new X · review Y" on the list page adds up to the number of cards available after entering.
//
// The fixture covers:
//
//	(a) today's new-card allowance exhausted (that deck has 0 new, but learning cards still come out);
//	(b) a multi-deck scope sums each deck's own allowance (two 20/20 decks with different usage distributions);
//	(c) deck columns new_per_day=0 / reviews_per_day=0 (unlimited);
//	(d) negative case: another user's deck (with new cards) does not appear, whatever it is called.
//
// DeckCounts has no Now parameter and uses the clock at call time internally; the "used today" review rows in the fixture are written by the review day of now,
// which is the same day as the Now passed to Build (the two differ by milliseconds, identical except when crossing the 04:00 cutoff).
func TestDeckCountsMatchBuildPerDeck(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	day := ReviewDay(now, time.UTC, DefaultDayCutoffHour)
	future := now.Add(48 * time.Hour)
	last := now.Add(-48 * time.Hour)
	at := now.Add(-time.Hour)

	decks := store.NewDeckStore(db)
	mkDeck := func(owner uint64, name string, newPer, reviewPer int) uint64 {
		t.Helper()
		p := store.NewPreset(owner, "preset-"+name)
		p.EnableFuzz = boolPtr(false)
		if err := db.Create(&p).Error; err != nil {
			t.Fatalf("create preset: %v", err)
		}
		d := store.Deck{OwnerUserID: owner, Name: name, PresetID: p.ID, CreatedAt: now}
		if err := db.Create(&d).Error; err != nil {
			t.Fatalf("create deck %s: %v", name, err)
		}
		// Write the caps via a map update: 0 is legitimate here (unlimited), and saving the whole model would omit 0 for columns with a default.
		if err := db.Model(&store.Deck{}).Where("id = ?", d.ID).
			Updates(map[string]any{"new_per_day": newPer, "reviews_per_day": reviewPer}).Error; err != nil {
			t.Fatalf("set caps on deck %s: %v", name, err)
		}
		return d.ID
	}
	dueReview := func(deckID uint64) {
		t.Helper()
		id := seedCard(t, db, deckID, "forward", now)
		seedState(t, db, 1, id, "review", now.Add(-time.Hour), 5.0, 5.0, &last)
	}
	// introduced writes n "introduced today" new cards: a review row with state_before=0 + one state row that is not returned.
	introduced := func(deckID uint64, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			id := seedCard(t, db, deckID, "used", now)
			seedReview(t, db, 1, id, day, int(StateNew), at)
			seedState(t, db, 1, id, "review", future, 5.0, 5.0, &last)
		}
	}
	// reviewedToday writes n "reviewed today" entries (state_before<>0), used to create differing usage amounts.
	reviewedToday := func(deckID uint64, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			id := seedCard(t, db, deckID, "reviewed", now)
			seedReview(t, db, 1, id, day, int(StateReview), at)
			seedState(t, db, 1, id, "review", future, 5.0, 5.0, &last)
		}
	}
	seedNew := func(deckID uint64, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			seedCard(t, db, deckID, "new", now.Add(time.Duration(i)*time.Minute))
		}
	}

	// (a) new-card allowance 1, already used up today; plus one due learning card (not truncated by allowances).
	quota := mkDeck(1, "quota-used", 1, 200)
	introduced(quota, 1)
	seedNew(quota, 2)
	learning := seedCard(t, db, quota, "learning", now)
	seedState(t, db, 1, learning, "learning", now.Add(-time.Minute), 0, 0, nil)
	dueReview(quota)

	// (b) two 20/20 decks with different usage: limited uses 0, partial uses 5 new / 8 review.
	limited := mkDeck(1, "limited", 20, 20)
	seedNew(limited, 25)
	for i := 0; i < 25; i++ {
		dueReview(limited)
	}
	partial := mkDeck(1, "partial", 20, 20)
	introduced(partial, 5)
	reviewedToday(partial, 8)
	seedNew(partial, 25)
	for i := 0; i < 25; i++ {
		dueReview(partial)
	}

	// (c) 0 means unlimited.
	unlimited := mkDeck(1, "unlimited", 0, 0)
	seedNew(unlimited, 4)
	for i := 0; i < 4; i++ {
		dueReview(unlimited)
	}

	// (d) another user's two decks each contain new cards and neither should appear: no authorisation, no visibility.
	otherPrivate := mkDeck(2, "other-private", 20, 200)
	seedNew(otherPrivate, 5)
	otherDeck := mkDeck(2, "other-deck", 20, 200)
	seedNew(otherDeck, 2)

	sched := mustScheduler(t, testPreset(t))
	builder := NewQueueBuilder(db, decks, sched)

	visible, err := decks.VisibleIDs(ctx, 1)
	if err != nil {
		t.Fatalf("VisibleIDs() error = %v", err)
	}
	if len(visible) != 4 {
		t.Fatalf("visible decks = %v, want 4 (owned only: another user's decks are never visible)", visible)
	}
	for _, id := range visible {
		if id == otherPrivate || id == otherDeck {
			t.Errorf("visible set leaked another user's deck %d", id)
		}
	}

	counts, err := builder.DeckCounts(ctx, 1, visible, QueueOptions{})
	if err != nil {
		t.Fatalf("DeckCounts() error = %v", err)
	}
	if len(counts) != len(visible) {
		t.Errorf("DeckCounts returned %d decks, want %d", len(counts), len(visible))
	}

	// Property: the per-deck count equals Build(DeckID=d) — "new" on the list = QueueNew,
	// "review" = learning/relearning cards + due review cards, and their sum is the number of cards available after entering.
	for _, id := range visible {
		items, err := builder.Build(ctx, 1, QueueOptions{DeckID: id, Now: now, Location: time.UTC})
		if err != nil {
			t.Fatalf("Build(DeckID=%d) error = %v", id, err)
		}
		newN := countKind(items, QueueNew)
		reviewN := countKind(items, QueueReview) + countKind(items, QueueLearning)
		if got := counts[id]; got.New != newN || got.Review != reviewN {
			t.Errorf("deck %d: DeckCounts = %+v, Build(DeckID) New=%d Review(learning+due)=%d", id, got, newN, reviewN)
		}
	}

	// (a) allowance exhausted: new 0; review 2 = 1 due learning card + 1 due review card.
	if got := counts[quota]; got.New != 0 || got.Review != 2 {
		t.Errorf("quota deck counts = %+v, want New=0 Review=2 (1 learning + 1 due review)", got)
	}
	qitems, err := builder.Build(ctx, 1, QueueOptions{DeckID: quota, Now: now, Location: time.UTC})
	if err != nil {
		t.Fatalf("Build(DeckID=quota) error = %v", err)
	}
	if n := countKind(qitems, QueueLearning); n != 1 {
		t.Errorf("quota deck learning cards = %d, want 1 (learning cards are never capped)", n)
	}

	// (b) each deck's own allowance.
	if got := counts[limited]; got.New != 20 || got.Review != 20 {
		t.Errorf("limited deck counts = %+v, want New=20 Review=20", got)
	}
	if got := counts[partial]; got.New != 15 || got.Review != 12 {
		t.Errorf("partial deck counts = %+v, want New=15 Review=12 (20 minus today's usage)", got)
	}

	// (c) 0 means unlimited.
	if got := counts[unlimited]; got.New != 4 || got.Review != 4 {
		t.Errorf("unlimited deck counts = %+v, want New=4 Review=4 (0 means unlimited)", got)
	}

	// (d) whole-library mode: only visible decks are included, none of other people's decks appears.
	all, err := builder.Build(ctx, 1, QueueOptions{Now: now, Location: time.UTC, NewOrder: NewOrderCreated})
	if err != nil {
		t.Fatalf("Build(all decks) error = %v", err)
	}
	for _, it := range all {
		if it.DeckID == otherPrivate || it.DeckID == otherDeck {
			t.Errorf("all-decks queue leaked another user's deck %d", it.DeckID)
		}
	}
	allCounts, err := builder.DeckCounts(ctx, 1, nil, QueueOptions{})
	if err != nil {
		t.Fatalf("DeckCounts(nil) error = %v", err)
	}
	if len(allCounts) != len(counts) {
		t.Errorf("all-decks DeckCounts size = %d, want %d", len(allCounts), len(counts))
	}
	for id, want := range counts {
		if allCounts[id] != want {
			t.Errorf("deck %d: all-decks counts = %+v, want %+v", id, allCounts[id], want)
		}
	}
	if got, ok := allCounts[otherDeck]; ok {
		t.Errorf("all-decks counts include another user's deck %d = %+v, want absent", otherDeck, got)
	}
}

// TestDeckCountsUnlimitedIsNotCappedByBatch asserts the review count of an unlimited deck is the real number of cards:
// the single-batch fetch limit (BatchSize) only decides how many are returned at once, not how many can be studied today.
func TestDeckCountsUnlimitedIsNotCappedByBatch(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	last := now.Add(-48 * time.Hour)
	p := store.NewPreset(1, "preset")
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	d := store.Deck{OwnerUserID: 1, Name: "big", PresetID: p.ID, CreatedAt: now}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	if err := db.Model(&store.Deck{}).Where("id = ?", d.ID).
		Updates(map[string]any{"new_per_day": 0, "reviews_per_day": 0}).Error; err != nil {
		t.Fatalf("set caps: %v", err)
	}
	const due, fresh = DefaultReviewBatch + 50, 30
	for i := 0; i < due; i++ {
		id := seedCard(t, db, d.ID, "forward", now)
		seedState(t, db, 1, id, "review", now.Add(-time.Hour), 5.0, 5.0, &last)
	}
	for i := 0; i < fresh; i++ {
		seedCard(t, db, d.ID, "forward", now)
	}
	counts, err := NewQueueBuilder(db, store.NewDeckStore(db), nil).DeckCounts(ctx, 1, []uint64{d.ID}, QueueOptions{Now: now})
	if err != nil {
		t.Fatalf("DeckCounts() error = %v", err)
	}
	if got := counts[d.ID]; got.Review != due || got.New != fresh {
		t.Errorf("counts = %+v, want Review %d New %d", got, due, fresh)
	}
}

// TestDeckCountsUseTheUsersReviewDay asserts "today's usage" is counted by the review day in the user's timezone.
// 01:00 UTC is already 09:00 in UTC+8, so the user's review day is 10-07; under UTC it would still be 10-06.
// With 1 card introduced that day and a cap of 1, the new-card allowance is used up (0) under the user's semantics; using UTC semantics by mistake would show 1.
func TestDeckCountsUseTheUsersReviewDay(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 7, 1, 0, 0, 0, time.UTC)
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	p := store.NewPreset(1, "preset")
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	d := store.Deck{OwnerUserID: 1, Name: "tz", PresetID: p.ID, CreatedAt: now}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	if err := db.Model(&store.Deck{}).Where("id = ?", d.ID).
		Updates(map[string]any{"new_per_day": 1, "reviews_per_day": 0}).Error; err != nil {
		t.Fatalf("set caps: %v", err)
	}
	userDay := ReviewDay(now, shanghai, DefaultDayCutoffHour)
	if utcDay := ReviewDay(now, time.UTC, DefaultDayCutoffHour); utcDay == userDay {
		t.Fatalf("fixture error: user day %s equals UTC day", userDay)
	}
	used := seedCard(t, db, d.ID, "used", now)
	seedReview(t, db, 1, used, userDay, int(StateNew), now.Add(-time.Minute))
	future := now.Add(48 * time.Hour)
	seedState(t, db, 1, used, "review", future, 5.0, 5.0, &now)
	seedCard(t, db, d.ID, "forward", now)

	builder := NewQueueBuilder(db, store.NewDeckStore(db), nil)
	cases := []struct {
		name    string
		tz      string
		wantNew int
	}{
		{"user timezone sees today's quota used", "Asia/Shanghai", 0},
		{"UTC reading is a different day", "UTC", 1},
	}
	for _, tc := range cases {
		counts, err := builder.DeckCounts(ctx, 1, []uint64{d.ID}, QueueOptions{Now: now, Timezone: tc.tz})
		if err != nil {
			t.Fatalf("%s: DeckCounts() error = %v", tc.name, err)
		}
		if got := counts[d.ID].New; got != tc.wantNew {
			t.Errorf("%s: New = %d, want %d", tc.name, got, tc.wantNew)
		}
	}
}
