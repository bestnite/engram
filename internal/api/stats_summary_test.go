package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

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
			day := schedule.ReviewDay(env.now, time.UTC, 4)
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
