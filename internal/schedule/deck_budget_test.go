package schedule

import (
	"context"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/reviewday"
	"git.nite07.com/nite/engram/internal/store"
)

// TestDeckBudgetsReportsUsedAndLeft is the acceptance test for the settings page's "used today / left" data source:
// DeckBudgets must share the queue's source (reusing deckBudget's formula), and express "unlimited" with a boolean rather than
// -1/0.
//
// The fixture covers three boundaries:
//
//	(a) deck column written as 0 (unlimited): PerDay is 0, Unlimited is true, and nothing is capped even with usage today;
//	(b) used equals the cap → Left 0; used above the cap → still 0, never negative;
//	(c) a deck with no card_states / reviews at all: today's usage is 0, Left equals PerDay.
func TestDeckBudgetsReportsUsedAndLeft(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	day := reviewday.Day(now, time.UTC, reviewday.DefaultCutoffHour)
	at := now.Add(-time.Hour)
	future := now.Add(48 * time.Hour)
	last := now.Add(-48 * time.Hour)

	decks := store.NewDeckStore(db)
	mkDeck := func(name string, newPer, reviewPer int) uint64 {
		t.Helper()
		p := store.NewPreset(1, "preset-"+name)
		p.EnableFuzz = boolPtr(false)
		if err := db.Create(&p).Error; err != nil {
			t.Fatalf("create preset: %v", err)
		}
		d := store.Deck{OwnerUserID: 1, Name: name, PresetID: p.ID, CreatedAt: now}
		if err := db.Create(&d).Error; err != nil {
			t.Fatalf("create deck %s: %v", name, err)
		}
		// Write the caps via a map: 0 is legitimate here (unlimited), and saving the whole model would omit 0 for columns with a default.
		if err := db.Model(&store.Deck{}).Where("id = ?", d.ID).
			Updates(map[string]any{"new_per_day": newPer, "reviews_per_day": reviewPer}).Error; err != nil {
			t.Fatalf("set caps on deck %s: %v", name, err)
		}
		return d.ID
	}
	// usedNew records n "new card introduced today" entries (state_before = 0).
	usedNew := func(deckID uint64, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			id := seedCard(t, db, deckID, "new-used", now)
			seedReview(t, db, 1, id, day, int(StateNew), at)
			seedState(t, db, 1, id, "review", future, 5.0, 5.0, &last)
		}
	}
	// usedReview records n "reviewed today" entries (state_before <> 0).
	usedReview := func(deckID uint64, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			id := seedCard(t, db, deckID, "review-used", now)
			seedReview(t, db, 1, id, day, int(StateReview), at)
			seedState(t, db, 1, id, "review", future, 5.0, 5.0, &last)
		}
	}

	// (a) 0/0 means unlimited: no cap, both Unlimited flags are true.
	unlimited := mkDeck("unlimited", 0, 0)
	usedNew(unlimited, 7)
	usedReview(unlimited, 9)

	// (b1) used equals the cap; b2 used exceeds the cap (5 reviews used, cap 3).
	atCap := mkDeck("at-cap", 2, 3)
	usedNew(atCap, 2)
	usedReview(atCap, 3)
	overCap := mkDeck("over-cap", 5, 3)
	usedNew(overCap, 1)
	usedReview(overCap, 5)

	// (c) a deck never used at all: usage is 0, remaining equals the cap.
	untouched := mkDeck("untouched", 5, 10)

	sched := mustScheduler(t, testPreset(t))
	builder := NewQueueBuilder(db, decks, sched)

	budgets, err := builder.DeckBudgets(ctx, 1, []uint64{unlimited, atCap, overCap, untouched}, QueueOptions{})
	if err != nil {
		t.Fatalf("DeckBudgets() error = %v", err)
	}
	if len(budgets) != 4 {
		t.Fatalf("DeckBudgets returned %d decks, want 4", len(budgets))
	}

	// (a) unlimited.
	u := budgets[unlimited]
	if !u.NewUnlimited || !u.ReviewUnlimited {
		t.Errorf("unlimited deck: NewUnlimited=%v ReviewUnlimited=%v, want both true for caps 0/0", u.NewUnlimited, u.ReviewUnlimited)
	}
	if u.NewPerDay != 0 || u.ReviewsPerDay != 0 {
		t.Errorf("unlimited deck: PerDay = %d/%d, want 0/0", u.NewPerDay, u.ReviewsPerDay)
	}
	if u.NewUsed != 7 || u.ReviewUsed != 9 {
		t.Errorf("unlimited deck: Used = %d/%d, want 7/9 (usage is still reported when unlimited)", u.NewUsed, u.ReviewUsed)
	}

	// (b1) used equals the cap.
	eq := budgets[atCap]
	if eq.NewUnlimited || eq.ReviewUnlimited {
		t.Errorf("at-cap deck: Unlimited flags = %v/%v, want false", eq.NewUnlimited, eq.ReviewUnlimited)
	}
	if eq.NewUsed != 2 || eq.ReviewUsed != 3 {
		t.Errorf("at-cap deck: Used = %d/%d, want 2/3", eq.NewUsed, eq.ReviewUsed)
	}
	if eq.NewLeft != 0 || eq.ReviewLeft != 0 {
		t.Errorf("at-cap deck: Left = %d/%d, want 0/0 when usage equals the cap", eq.NewLeft, eq.ReviewLeft)
	}

	// (b2) used exceeds the cap: remaining is still 0, never negative.
	over := budgets[overCap]
	if over.ReviewUsed != 5 || over.ReviewLeft != 0 {
		t.Errorf("over-cap deck: ReviewUsed=%d ReviewLeft=%d, want 5/0 (never negative)", over.ReviewUsed, over.ReviewLeft)
	}
	if over.NewUsed != 1 || over.NewLeft != 4 {
		t.Errorf("over-cap deck: NewUsed=%d NewLeft=%d, want 1/4", over.NewUsed, over.NewLeft)
	}

	// (c) an untouched deck: usage 0, remaining equals the cap.
	ut := budgets[untouched]
	if ut.NewUsed != 0 || ut.ReviewUsed != 0 {
		t.Errorf("untouched deck: Used = %d/%d, want 0/0", ut.NewUsed, ut.ReviewUsed)
	}
	if ut.NewLeft != 5 || ut.ReviewLeft != 10 {
		t.Errorf("untouched deck: Left = %d/%d, want 5/10", ut.NewLeft, ut.ReviewLeft)
	}
	if ut.NewUnlimited || ut.ReviewUnlimited {
		t.Errorf("untouched deck: Unlimited flags = %v/%v, want false", ut.NewUnlimited, ut.ReviewUnlimited)
	}

	// A non-existent deck id uses the resolveScope fallback: the new-card cap takes the default, reviews are unlimited.
	missing, err := builder.DeckBudgets(ctx, 1, []uint64{99999}, QueueOptions{})
	if err != nil {
		t.Fatalf("DeckBudgets(missing) error = %v", err)
	}
	if got := missing[99999]; got.NewPerDay != DefaultNewPerDay || !got.ReviewUnlimited {
		t.Errorf("missing deck: %+v, want NewPerDay=%d and ReviewUnlimited=true (documented fallback)", got, DefaultNewPerDay)
	}

	// Empty-set and non-zero userID validation.
	empty, err := builder.DeckBudgets(ctx, 1, nil, QueueOptions{})
	if err != nil {
		t.Fatalf("DeckBudgets(nil) error = %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("DeckBudgets(nil) returned %d entries, want 0", len(empty))
	}
	if _, err := builder.DeckBudgets(ctx, 0, []uint64{unlimited}, QueueOptions{}); err == nil {
		t.Error("DeckBudgets(userID=0) error = nil, want an error")
	}
}

// TestDeckBudgetsUseTheUsersReviewDay asserts the settings page's "used today" is counted by the review day in the user's timezone (the negative case: UTC semantics
// would count a UTC+8 morning as the previous day, showing an already-used allowance as unused).
func TestDeckBudgetsUseTheUsersReviewDay(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 7, 1, 0, 0, 0, time.UTC)
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	deckID := seedDeck(t, db, now)
	userDay := reviewday.Day(now, shanghai, reviewday.DefaultCutoffHour)
	used := seedCard(t, db, deckID, "used", now)
	seedReview(t, db, 1, used, userDay, int(StateNew), now.Add(-time.Minute))
	builder := NewQueueBuilder(db, store.NewDeckStore(db), nil)
	for _, tc := range []struct {
		tz       string
		wantUsed int
	}{{"Asia/Shanghai", 1}, {"UTC", 0}} {
		budgets, err := builder.DeckBudgets(ctx, 1, []uint64{deckID}, QueueOptions{Now: now, Timezone: tc.tz})
		if err != nil {
			t.Fatalf("DeckBudgets(%s) error = %v", tc.tz, err)
		}
		if got := budgets[deckID].NewUsed; got != tc.wantUsed {
			t.Errorf("%s: NewUsed = %d, want %d", tc.tz, got, tc.wantUsed)
		}
	}
}
