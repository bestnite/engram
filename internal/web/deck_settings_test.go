package web

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/reviewday"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// seedTodayUsage writes today's usage records: newUsed occurrences of "new card introduced" (state_before=0) and
// reviewUsed occurrences of "review" (state_before<>0). Each card also gets a future card_states row,
// so that they do not re-enter the queue as "new cards" and skew the counts.
func seedTodayUsage(t *testing.T, db *gorm.DB, ownerID, deckID uint64, newUsed, reviewUsed int) {
	t.Helper()
	now := time.Now().UTC()
	// The server counts "today's usage" on the owner's own review day (timezone + cutoff), so the seeded
	// rows must use the same day: hard-coding UTC here made the test fail whenever the user's local day
	// differed from the UTC day.
	owner, err := store.NewUserStore(db).ByID(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("load owner %d: %v", ownerID, err)
	}
	day := reviewday.Day(now, reviewday.Location(owner.Timezone), reviewday.ResolveCutoff(owner.DayCutoffHour))
	stability, difficulty := 5.0, 5.0
	future := now.Add(48 * time.Hour)
	last := now.Add(-48 * time.Hour)
	seed := func(n int, stateBefore int) {
		for i := 0; i < n; i++ {
			cardID := seedCardRow(t, db, deckID, "used", now)
			if err := db.Create(&store.Review{CardID: cardID, UserID: ownerID, Rating: 3, GradeSource: "self",
				ReviewedAt: now.Add(-time.Hour), ReviewDay: day, StateBefore: stateBefore}).Error; err != nil {
				t.Fatalf("create review: %v", err)
			}
			if err := db.Create(&store.CardState{CardID: cardID, UserID: ownerID, State: "review",
				DueAt: &future, Stability: &stability, Difficulty: &difficulty, LastReviewAt: &last}).Error; err != nil {
				t.Fatalf("create card state: %v", err)
			}
		}
	}
	seed(newUsed, int(schedule.StateNew))
	seed(reviewUsed, int(schedule.StateReview))
}

// deckQueueCountsNow uses the real queue (Build(DeckID=d)) to count the new and review cards a deck can study today
// (the review count includes due learning/relearning cards), from the same source as the two numbers on the list page.
func deckQueueCountsNow(t *testing.T, srv *Server, db *gorm.DB, userID, deckID uint64) (newN, reviewN int) {
	t.Helper()
	preset := store.NewPreset(userID, "counts preset")
	sched, err := schedule.NewScheduler(&preset)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	builder := schedule.NewQueueBuilder(db, srv.decks, sched)
	items, err := builder.Build(context.Background(), userID,
		schedule.QueueOptions{DeckID: deckID, Now: time.Now().UTC(), Location: time.UTC, NewOrder: schedule.NewOrderCreated})
	if err != nil {
		t.Fatalf("Build(DeckID=%d): %v", deckID, err)
	}
	for _, it := range items {
		switch it.Kind {
		case schedule.QueueNew:
			newN++
		case schedule.QueueReview, schedule.QueueLearning:
			reviewN++
		}
	}
	return newN, reviewN
}

// deckCapsFromDB reads back the deck's two cap columns, to assert "nothing was written" or the written values.
func deckCapsFromDB(t *testing.T, db *gorm.DB, deckID uint64) store.DeckCaps {
	t.Helper()
	caps, err := store.NewDeckStore(db).Caps(context.Background(), deckID)
	if err != nil {
		t.Fatalf("read deck caps: %v", err)
	}
	return caps
}
