package schedule

import (
	"context"
	"testing"
	"time"

	"github.com/open-spaced-repetition/go-fsrs/v4"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/pgtest"
	"git.nite07.com/nite/engram/internal/store"
)

// stepRangeDatabases returns the SQLite database and the PostgreSQL one when TEST_PG_DSN is set.
func stepRangeDatabases(t *testing.T) map[string]*gorm.DB {
	t.Helper()
	out := map[string]*gorm.DB{"sqlite": newTestDB(t)}
	if pg, ok := pgtest.Open(t); ok {
		if err := pg.AutoMigrate(store.AllModels()...); err != nil {
			t.Fatalf("AutoMigrate postgres: %v", err)
		}
		out["postgres"] = pg
	}
	return out
}

// TestStepsOutsideSchedulerRangeAreRefused covers both validators that read a step list: the
// scheduler's parser refuses a value it cannot turn into a due date, and a legal value one day
// under the cap still schedules normally.
func TestStepsOutsideSchedulerRangeAreRefused(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		steps   string
		wantErr bool
	}{
		{"just under the cap", "1m,36499d", false},
		{"at the cap", "1m,36500d", false},
		{"one day over the cap", "36501d", true},
		{"the overflow from the report", "200000d", true},
		{"non-finite after scaling", "1e308d", true},
		{"not a number", "NaNd", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			preset := testPreset(t)
			preset.LearningSteps = tc.steps
			scheduler, err := NewScheduler(preset)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NewScheduler(%q) error = nil, want the step refused", tc.steps)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewScheduler(%q) error = %v", tc.steps, err)
			}
			out, err := scheduler.Next(newTestCardState(), now, Good)
			if err != nil {
				t.Fatalf("Next() error = %v", err)
			}
			if !out.Due.After(now) {
				t.Errorf("due = %v, want after %v", out.Due, now)
			}
			// Good advances to the second step; the test proves the step really reached the due date.
			if out.State != StateReview || out.ScheduledDays < 36499 {
				t.Errorf("Good on %q: state=%v scheduled_days=%d, want review with the large step", tc.steps, out.State, out.ScheduledDays)
			}
		})
	}
}

// TestSubmitRefusesDueDateBeforeNow proves the review path writes nothing when the scheduler's
// computed due date is in the past. The scheduler is built past NewScheduler's validation on
// purpose: it recreates the overflow this check exists to catch.
func TestSubmitRefusesDueDateBeforeNow(t *testing.T) {
	for driver, db := range stepRangeDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			deckID := seedDeck(t, db, now)
			cardID := seedCard(t, db, deckID, "forward", now)

			p := fsrs.DefaultParam()
			p.EnableFuzz = false
			p.LearningSteps = []float64{1, 200000 * 24 * 60}
			overflowing := &Scheduler{fsrs: fsrs.NewFSRS(p), preview: fsrs.NewFSRS(p)}

			tx := db.Begin()
			_, err := Submit(ctx, tx, SubmitInput{
				CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: 0,
				Scheduler: overflowing, Now: now, Location: time.UTC,
			})
			tx.Rollback()
			if err == nil {
				t.Fatal("Submit() error = nil, want the out-of-range due date refused")
			}

			var reviews int64
			if err := db.Model(&store.Review{}).Count(&reviews).Error; err != nil {
				t.Fatalf("count reviews: %v", err)
			}
			var states int64
			if err := db.Model(&store.CardState{}).Count(&states).Error; err != nil {
				t.Fatalf("count card states: %v", err)
			}
			if reviews != 0 || states != 0 {
				t.Errorf("after the refused grade: reviews=%d states=%d, want 0/0", reviews, states)
			}
		})
	}
}

// TestLegitimateLongStepStillWritesReview checks the other side of the boundary: a step at the cap
// is accepted and its grade is recorded normally.
func TestLegitimateLongStepStillWritesReview(t *testing.T) {
	for driver, db := range stepRangeDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			deckID := seedDeck(t, db, now)
			cardID := seedCard(t, db, deckID, "forward", now)

			preset := testPreset(t)
			preset.LearningSteps = "1m,36500d"
			scheduler := mustScheduler(t, preset)

			tx := db.Begin()
			res, err := Submit(ctx, tx, SubmitInput{
				CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: 0,
				Scheduler: scheduler, Now: now, Location: time.UTC,
			})
			if err != nil {
				tx.Rollback()
				t.Fatalf("Submit() error = %v", err)
			}
			if err := tx.Commit().Error; err != nil {
				t.Fatalf("commit: %v", err)
			}
			if res.ReviewID == 0 {
				t.Fatal("Submit() wrote no review")
			}
			var rv store.Review
			if err := db.First(&rv, res.ReviewID).Error; err != nil {
				t.Fatalf("load review %d: %v", res.ReviewID, err)
			}
			if rv.IntervalDays == nil || *rv.IntervalDays <= 0 {
				t.Errorf("review interval_days = %v, want a positive interval", rv.IntervalDays)
			}
		})
	}
}
