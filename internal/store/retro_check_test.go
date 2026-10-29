package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/reviewday"
)

// This file implements the "retro check": every number the stats page shows is compared one by one against
// the number recomputed directly from the raw tables (reviews / card_states / cards / notes), and any drift is
// named outright rather than argued over.
//
// Relationship to stats_test.go: those cases cross-check the aggregate functions against "one hand-written SQL", but the expected values are still hard-coded
// in the test; the retro check is a deliberate second implementation that lives only in tests -- it likewise starts from the raw tables but goes through
// none of StatsStore's aggregate functions or the shared predicates in card_scope.go, so a drift in either shows up as a named difference.
//
// Coverage: review volume, due forecast (today bucket and unstudied new cards), retention (total/passed), time invested, streak,
// learning curve (newly introduced/review totals), deck dimension (review count/total elapsed time). The per-deck due counts are
// not covered here: they come from the review queue (schedule.QueueBuilder.DeckCounts), which has its own tests.
// Not covered: the tag dimension and the grading-source distribution -- their definition depends on decoding tags_json and grouping by arbitrary source,
// which stats_test.go's hand-written SQL cases already cover, so it is not duplicated here.

// PageNumbers is the set of scalar numbers the stats page displays directly; each metric contributes one representative
// scalar, and list-shaped results (buckets, per-deck, per-tag) contribute their total here, so the check is not made fragile by row-by-row comparison.
type PageNumbers struct {
	ReviewToday int64
	Review7d    int64
	Review30d   int64

	DueToday int64
	DueNew   int64

	RetentionTotal  int64
	RetentionPassed int64

	TimeTotalMS int64
	TimeCount   int64

	StreakCurrent int
	StreakLongest int

	CurveNew    int64
	CurveReview int64

	DeckReviews   int64
	DeckElapsedMS int64
}

// PageNumbersFromStats builds the numbers along the same code path the stats page uses (StatsStore's aggregate functions),
// as the counterpart to RecomputePageNumbers' raw-table recomputation. today/fromDay/toDay are review_day-format
// inclusive bounds, and now/loc/cutoffHour determine the day boundaries for due counts and the streak.
func PageNumbersFromStats(ctx context.Context, db *gorm.DB, userID uint64, now time.Time, today, fromDay, toDay string, loc *time.Location, cutoffHour int) (PageNumbers, error) {
	s := NewStatsStore(db)
	vol, err := s.ReviewVolume(ctx, userID, today)
	if err != nil {
		return PageNumbers{}, err
	}
	due, err := s.DueForecast(ctx, userID, 0, now, loc, cutoffHour)
	if err != nil {
		return PageNumbers{}, err
	}
	ret, err := s.RetentionByStability(ctx, userID, 0)
	if err != nil {
		return PageNumbers{}, err
	}
	tm, err := s.TimeSpent(ctx, userID, fromDay, toDay)
	if err != nil {
		return PageNumbers{}, err
	}
	streak, err := s.Streak(ctx, userID, now, loc, cutoffHour)
	if err != nil {
		return PageNumbers{}, err
	}
	curve, err := s.LearningCurve(ctx, userID, fromDay, toDay)
	if err != nil {
		return PageNumbers{}, err
	}
	decks, err := s.DeckBreakdown(ctx, userID, nil)
	if err != nil {
		return PageNumbers{}, err
	}

	p := PageNumbers{
		ReviewToday:     vol.Today,
		Review7d:        vol.Last7Days,
		Review30d:       vol.Last30Days,
		DueToday:        due.Today,
		DueNew:          due.New,
		RetentionTotal:  ret.Total,
		RetentionPassed: ret.Passed,
		TimeTotalMS:     tm.TotalMS,
		TimeCount:       tm.Count,
		StreakCurrent:   streak.Current,
		StreakLongest:   streak.Longest,
	}
	for _, pt := range curve {
		p.CurveNew += pt.New
		p.CurveReview += pt.Review
	}
	for _, d := range decks {
		p.DeckReviews += d.Reviews
		p.DeckElapsedMS += d.ElapsedMS
	}
	return p, nil
}

// RecomputePageNumbers recomputes the same set of numbers using only SQL over the raw tables, deliberately not calling StatsStore:
// it is the independent second implementation, so once an aggregate function is broken (or its definition drifts) it disagrees with this recomputation.
func RecomputePageNumbers(ctx context.Context, db *gorm.DB, userID uint64, now time.Time, today, fromDay, toDay string, loc *time.Location, cutoffHour int) (PageNumbers, error) {
	var p PageNumbers
	if userID == 0 {
		return p, fmt.Errorf("retro check: user id is required")
	}
	day, err := time.Parse("2006-01-02", today)
	if err != nil {
		return p, fmt.Errorf("retro check: invalid review day %q: %w", today, err)
	}
	from7 := day.AddDate(0, 0, -6).Format("2006-01-02")
	from30 := day.AddDate(0, 0, -29).Format("2006-01-02")

	// Review volume: count rows directly by review_day.
	if err := db.WithContext(ctx).Raw(`SELECT
		COALESCE(SUM(CASE WHEN review_day = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN review_day >= ? AND review_day <= ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN review_day >= ? AND review_day <= ? THEN 1 ELSE 0 END), 0)
		FROM reviews WHERE user_id = ?`,
		today, from7, today, from30, today, userID).
		Row().Scan(&p.ReviewToday, &p.Review7d, &p.Review30d); err != nil {
		return p, fmt.Errorf("retro check: review volume: %w", err)
	}

	// Due and new: the day boundary matches the queue (the review day starts at 04:00, local).
	// Both are scoped to the default study scope, the same predicate as StatsStore.DueForecast with no deck (activeDeckIDsQuery):
	// without the visibility part, cards in someone else's private deck with no state row would be counted as new cards,
	// and without the pause part, a paused deck would still feed the forecast. A new card is never due, buried or not,
	// so the today bucket counts only rows that have left the new state and the new count takes every new card.
	e1 := reviewday.Start(now, loc, cutoffHour).Add(24 * time.Hour)
	if err := db.WithContext(ctx).Raw(`SELECT COUNT(*) FROM card_states cs
		JOIN cards c ON c.id = cs.card_id AND c.deleted_at IS NULL AND cs.suspended_at IS NULL
		JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL
		WHERE cs.user_id = ? AND n.deck_id IN (?) AND cs.state <> 'new' AND cs.due_at IS NOT NULL AND cs.due_at <= ?`,
		userID, activeDeckIDsQuery(db, userID), e1).
		Row().Scan(&p.DueToday); err != nil {
		return p, fmt.Errorf("retro check: due today: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT COUNT(c.id) FROM cards c
		JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL
		LEFT JOIN card_states cs ON cs.card_id = c.id AND cs.user_id = ?
		WHERE c.deleted_at IS NULL AND cs.suspended_at IS NULL
		  AND n.deck_id IN (?)
		  AND (cs.card_id IS NULL OR cs.state = 'new')`,
		userID, activeDeckIDsQuery(db, userID)).
		Row().Scan(&p.DueNew); err != nil {
		return p, fmt.Errorf("retro check: new cards: %w", err)
	}

	// Retention: the share of due reviews (state_before=Review) rated something other than Again, counting only rows with a non-null stability.
	if err := db.WithContext(ctx).Raw(`SELECT COUNT(r.id),
		COALESCE(SUM(CASE WHEN r.rating <> 1 THEN 1 ELSE 0 END), 0)
		FROM reviews r
		JOIN cards c ON c.id = r.card_id AND c.deleted_at IS NULL
		JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL
		WHERE r.user_id = ? AND r.state_before = 2 AND r.stability IS NOT NULL`, userID).
		Row().Scan(&p.RetentionTotal, &p.RetentionPassed); err != nil {
		return p, fmt.Errorf("retro check: retention: %w", err)
	}

	// Time invested: the sum and the sample count of elapsed_ms within the window.
	if err := db.WithContext(ctx).Raw(`SELECT COALESCE(SUM(elapsed_ms), 0), COUNT(elapsed_ms)
		FROM reviews WHERE user_id = ? AND elapsed_ms IS NOT NULL AND review_day >= ? AND review_day <= ?`,
		userID, fromDay, toDay).Row().Scan(&p.TimeTotalMS, &p.TimeCount); err != nil {
		return p, fmt.Errorf("retro check: time spent: %w", err)
	}

	// Learning curve totals: newly introduced (state_before=New) plus review volume, the two being mutually exclusive.
	if err := db.WithContext(ctx).Raw(`SELECT
		COALESCE(SUM(CASE WHEN state_before = 0 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN state_before <> 0 THEN 1 ELSE 0 END), 0)
		FROM reviews WHERE user_id = ? AND review_day >= ? AND review_day <= ?`,
		userID, fromDay, toDay).Row().Scan(&p.CurveNew, &p.CurveReview); err != nil {
		return p, fmt.Errorf("retro check: learning curve: %w", err)
	}

	// Deck-dimension totals, likewise scoped to visible decks (matching StatsStore.DeckBreakdown).
	if err := db.WithContext(ctx).Raw(`SELECT COUNT(r.id), COALESCE(SUM(r.elapsed_ms), 0)
		FROM reviews r
		JOIN cards c ON c.id = r.card_id AND c.deleted_at IS NULL
		JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL
		WHERE r.user_id = ? AND n.deck_id IN (?)`,
		userID, visibleDeckIDsQuery(db, userID)).
		Row().Scan(&p.DeckReviews, &p.DeckElapsedMS); err != nil {
		return p, fmt.Errorf("retro check: deck reviews: %w", err)
	}

	// Streak: take the distinct review days and run the same adjacency test on them.
	var days []string
	if err := db.WithContext(ctx).Raw(`SELECT DISTINCT review_day FROM reviews
		WHERE user_id = ? AND review_day <= ? ORDER BY review_day ASC`, userID, today).
		Scan(&days).Error; err != nil {
		return p, fmt.Errorf("retro check: streak days: %w", err)
	}
	p.StreakCurrent, p.StreakLongest = streakFromDays(days, today)
	return p, nil
}

// streakFromDays matches StatsStore.Streak's definition: only adjacent review days count as consecutive; the current streak holds only when
// the last day is today or yesterday. It is a free function so the recompute path does not depend on an aggregate method.
func streakFromDays(days []string, today string) (current, longest int) {
	if len(days) == 0 {
		return 0, 0
	}
	present := make(map[string]bool, len(days))
	for _, d := range days {
		present[d] = true
	}
	run := 0
	for i, d := range days {
		if i > 0 && d == reviewday.Shift(days[i-1], 1) {
			run++
		} else {
			run = 1
		}
		if run > longest {
			longest = run
		}
	}
	last := days[len(days)-1]
	if last == today || last == reviewday.Shift(today, -1) {
		for d := last; present[d]; d = reviewday.Shift(d, -1) {
			current++
		}
	}
	return current, longest
}

// DiffPageNumbers compares field by field and returns drift descriptions; an empty slice when everything matches. Each item names the number,
// so it is obvious at a glance whether an aggregate broke or a grouping definition changed.
func DiffPageNumbers(page, raw PageNumbers) []string {
	var drift []string
	add := func(name string, a, b any) {
		if fmt.Sprint(a) != fmt.Sprint(b) {
			drift = append(drift, fmt.Sprintf("%s: page=%v raw=%v", name, a, b))
		}
	}
	add("review_volume.today", page.ReviewToday, raw.ReviewToday)
	add("review_volume.last_7_days", page.Review7d, raw.Review7d)
	add("review_volume.last_30_days", page.Review30d, raw.Review30d)
	add("due_forecast.today", page.DueToday, raw.DueToday)
	add("due_forecast.new", page.DueNew, raw.DueNew)
	add("retention.total", page.RetentionTotal, raw.RetentionTotal)
	add("retention.passed", page.RetentionPassed, raw.RetentionPassed)
	add("time_spent.total_ms", page.TimeTotalMS, raw.TimeTotalMS)
	add("time_spent.count", page.TimeCount, raw.TimeCount)
	add("streak.current", page.StreakCurrent, raw.StreakCurrent)
	add("streak.longest", page.StreakLongest, raw.StreakLongest)
	add("learning_curve.new", page.CurveNew, raw.CurveNew)
	add("learning_curve.review", page.CurveReview, raw.CurveReview)
	add("deck_breakdown.reviews", page.DeckReviews, raw.DeckReviews)
	add("deck_breakdown.elapsed_ms", page.DeckElapsedMS, raw.DeckElapsedMS)
	return drift
}

// RetroCheck runs a full retro pass: page-path numbers vs the raw-table recomputation. A non-empty return means the stats page numbers drifted.
func RetroCheck(ctx context.Context, db *gorm.DB, userID uint64, now time.Time, today, fromDay, toDay string, loc *time.Location, cutoffHour int) ([]string, error) {
	page, err := PageNumbersFromStats(ctx, db, userID, now, today, fromDay, toDay, loc, cutoffHour)
	if err != nil {
		return nil, err
	}
	raw, err := RecomputePageNumbers(ctx, db, userID, now, today, fromDay, toDay, loc, cutoffHour)
	if err != nil {
		return nil, err
	}
	return DiffPageNumbers(page, raw), nil
}
