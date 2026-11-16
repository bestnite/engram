package api

import (
	"context"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/reviewday"
	"git.nite07.com/nite/engram/internal/store"
)

// First ratings and subsequent ratings must remain disjoint across review days and users.
func TestStatsSeparatesStudyCategories(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "study_counts", store.RoleUser)
	other := seedUser(t, env.db, "other_counts", store.RoleUser)
	deck, note := newDeckWithNote(t, env, user.ID)
	var card store.Card
	if err := env.db.Where("note_id = ?", note.ID).First(&card).Error; err != nil {
		t.Fatal(err)
	}
	today := reviewday.Day(env.now, time.UTC, 4)
	for _, row := range []struct {
		userID uint64
		day    string
		state  int
	}{
		{user.ID, today, 0}, {user.ID, today, 1}, {user.ID, today, 2}, {user.ID, today, 3},
		{user.ID, reviewday.Shift(today, -1), 0}, {user.ID, reviewday.Shift(today, -1), 2},
		{other.ID, today, 0}, {other.ID, today, 2},
	} {
		if err := env.db.Create(&store.Review{CardID: card.ID, UserID: row.userID, ReviewDay: row.day, StateBefore: row.state, Rating: 3, GradeSource: "self", ReviewedAt: env.now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	got, err := env.api.Stats(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if got.NewToday != 1 || got.NewTotal != 2 || got.ReviewsToday != 3 || got.ReviewsTotal != 4 {
		t.Fatalf("unexpected study categories: %+v", got)
	}
	// A card without a scheduling row is still a new card, never a due review.
	if got.NewAvailable != 1 || got.Due != 0 {
		t.Fatalf("unexpected available categories: %+v", got)
	}
	if _, err := env.api.decks.SetPaused(context.Background(), user.ID, deck.ID, true, env.now); err != nil {
		t.Fatal(err)
	}
	paused, err := env.api.Stats(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if paused.NewAvailable != 0 || paused.NewTotal != 2 || paused.ReviewsTotal != 4 {
		t.Fatalf("pause must hide availability but preserve history: %+v", paused)
	}
	if _, err := env.api.decks.SetPaused(context.Background(), user.ID, deck.ID, false, env.now); err != nil {
		t.Fatal(err)
	}
	if err := env.db.Model(&store.Deck{}).Where("id = ?", deck.ID).Update("new_per_day", 1).Error; err != nil {
		t.Fatal(err)
	}
	capped, err := env.api.Stats(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if capped.NewAvailable != 0 {
		t.Fatalf("daily new allowance ignored: %+v", capped)
	}
}
