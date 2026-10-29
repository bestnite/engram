package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/reviewday"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// TestStatsSummaryUsesTrueRetention pins down the home summary's retention convention: the same true retention rate as the stats page,
// counting only due reviews (state_before=Review), while a new card's first review and learning/relearning steps only count into reviews_total.
// A user who has only been through learning steps has retention_total 0, and the client shows "no data yet" from that.
func TestStatsSummaryUsesTrueRetention(t *testing.T) {
	type review struct{ rating, stateBefore int }
	cases := []struct {
		name        string
		reviews     []review
		wantReviews int64
		wantTotal   int64
		wantPassed  int64
		wantRate    float64
	}{
		{
			name: "learning steps are excluded from retention",
			reviews: []review{
				{3, 0}, {1, 1}, {1, 3}, // New / Learning(Again) / Relearning(Again)
				{3, 2}, {4, 2}, {1, 2}, {2, 2}, // Review: Good / Easy / Again / Hard
			},
			wantReviews: 7, wantTotal: 4, wantPassed: 3, wantRate: 0.75,
		},
		{
			name:        "learning steps only give no retention data",
			reviews:     []review{{3, 0}, {3, 1}, {4, 3}},
			wantReviews: 3, wantTotal: 0, wantPassed: 0, wantRate: 0,
		},
		{
			name:        "no reviews at all",
			wantReviews: 0, wantTotal: 0, wantPassed: 0, wantRate: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, 60, 60)
			user := seedUser(t, env.db, "retention_user", store.RoleUser)
			_, note := newDeckWithNote(t, env, user.ID)
			var card store.Card
			if err := env.db.Where("note_id = ?", note.ID).First(&card).Error; err != nil {
				t.Fatalf("load card: %v", err)
			}
			day := reviewday.Day(env.now, time.UTC, 4)
			stability := 5.0
			for i, rv := range tc.reviews {
				if err := env.db.Create(&store.Review{
					CardID: card.ID, UserID: user.ID, Rating: rv.rating, GradeSource: "self",
					ReviewedAt: env.now.Add(time.Duration(i) * time.Minute), ReviewDay: day,
					StateBefore: rv.stateBefore, Stability: &stability,
				}).Error; err != nil {
					t.Fatalf("create review: %v", err)
				}
			}

			got, err := env.api.Stats(context.Background(), user)
			if err != nil {
				t.Fatalf("Stats() error = %v", err)
			}
			if got.ReviewsTotal != tc.wantReviews || got.RetentionTotal != tc.wantTotal ||
				got.RetentionPassed != tc.wantPassed || got.Retention != tc.wantRate {
				t.Errorf("Stats() = %+v, want reviews_total %d retention %d/%d = %v",
					got, tc.wantReviews, tc.wantPassed, tc.wantTotal, tc.wantRate)
			}

			// The REST response must carry the new denominator field; the client uses it, not reviews_total, to decide "no data".
			key := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)
			status, body := doJSON(t, env.router(), http.MethodGet, "/api/v1/stats/summary", key.Plaintext, "")
			if status != http.StatusOK {
				t.Fatalf("GET /stats/summary status = %d body %s", status, body)
			}
			var raw map[string]any
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatalf("decode summary: %v", err)
			}
			if raw["retention_total"] != float64(tc.wantTotal) || raw["retention_passed"] != float64(tc.wantPassed) {
				t.Errorf("GET /stats/summary = %v, want retention_total %d retention_passed %d", raw, tc.wantTotal, tc.wantPassed)
			}
		})
	}
}

// TestStatsSummaryDueMatchesReviewQueue pins the home due count to the review part of the default review queue. The count used to be
// a hand-written query over card_states that ignored suspended_at, the card state and reviews_per_day, so the home page announced
// cards that /review then reported as "nothing due" (measured: two suspended learning cards gave due=2 against an empty queue).
// Every case also checks the count against the non-new cards DueCards actually returns.
func TestStatsSummaryDueMatchesReviewQueue(t *testing.T) {
	type seed struct {
		state     string
		dueIn     time.Duration // relative to env.now; negative means already due
		suspended bool
	}
	cases := []struct {
		name          string
		seeds         []seed
		reviewsPerDay int // 0 keeps the deck default
		wantDue       int64
	}{
		{name: "due review card counts", seeds: []seed{{"review", -time.Hour, false}}, wantDue: 1},
		{name: "due learning card counts", seeds: []seed{{"learning", -time.Minute, false}}, wantDue: 1},
		{name: "due relearning card counts", seeds: []seed{{"relearning", -time.Minute, false}}, wantDue: 1},
		{name: "review card not due yet is left out", seeds: []seed{{"review", time.Hour, false}}, wantDue: 0},
		{name: "suspended due learning card is left out", seeds: []seed{{"learning", -time.Hour, true}, {"learning", -time.Hour, true}}, wantDue: 0},
		{name: "suspended due review card is left out", seeds: []seed{{"review", -time.Hour, true}}, wantDue: 0},
		{name: "buried new card past its due_at is a new card, not a due one", seeds: []seed{{"new", -time.Hour, false}}, wantDue: 0},
		{
			name:          "reviews_per_day caps due review cards",
			seeds:         []seed{{"review", -time.Hour, false}, {"review", -2 * time.Hour, false}, {"review", -3 * time.Hour, false}},
			reviewsPerDay: 2, wantDue: 2,
		},
		{
			name:          "learning cards are not capped by reviews_per_day",
			seeds:         []seed{{"learning", -time.Minute, false}, {"learning", -2 * time.Minute, false}, {"review", -time.Hour, false}},
			reviewsPerDay: 1, wantDue: 3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, 60, 60)
			ctx := context.Background()
			user := seedUser(t, env.db, "due_user", store.RoleUser)
			deck := seedDeck(t, env.db, user.ID)
			if tc.reviewsPerDay > 0 {
				if err := env.db.Model(&store.Deck{}).Where("id = ?", deck.ID).
					Update("reviews_per_day", tc.reviewsPerDay).Error; err != nil {
					t.Fatalf("set reviews_per_day: %v", err)
				}
			}
			for i, s := range tc.seeds {
				note := seedNote(t, env, deck.ID, user.ID, fmt.Sprintf("q%d", i))
				var card store.Card
				if err := env.db.Where("note_id = ?", note.ID).First(&card).Error; err != nil {
					t.Fatalf("load card: %v", err)
				}
				due := env.now.Add(s.dueIn)
				row := store.CardState{CardID: card.ID, UserID: user.ID, State: s.state, DueAt: &due, Version: 1}
				if s.suspended {
					at := env.now.Add(-time.Minute)
					row.SuspendedAt = &at
				}
				if err := env.db.Create(&row).Error; err != nil {
					t.Fatalf("create card state: %v", err)
				}
			}

			got, err := env.api.Stats(ctx, user)
			if err != nil {
				t.Fatalf("Stats() error = %v", err)
			}
			if got.Due != tc.wantDue {
				t.Errorf("Stats().Due = %d, want %d", got.Due, tc.wantDue)
			}

			queue, err := env.api.DueCards(ctx, user, nil, nil, 500)
			if err != nil {
				t.Fatalf("DueCards() error = %v", err)
			}
			var queued int64
			for _, c := range queue {
				if c.State != schedule.StateNew.String() {
					queued++
				}
			}
			if got.Due != queued {
				t.Errorf("Stats().Due = %d, but the review queue holds %d non-new cards", got.Due, queued)
			}
		})
	}
}
