package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/reviewday"
)

// This file implements the stats page's read-only aggregate queries.
//
// Why this lives in internal/store instead of a new internal/stats package: every one of these metrics is a pure aggregation over reviews /
// card_states / cards / notes / decks, and their definition is tightly bound to the schema; in store it can reuse
// the same models and visibility rules directly (soft deletes, exclusion of suspended cards), and it matches the existing AGENTS.md §2.4 convention of "a concrete Store
// type wrapping GORM access". A new package would only add a layer forwarding store models, with no gain.
//
// Every function takes now / today / the date window from the caller and never reads the wall clock, so the page and
// the hand-computed cross-check tests can reproduce the same numbers at a fixed time.

// StatsStore provides the stats page's aggregate queries; read-only, writing no table.
type StatsStore struct {
	db *gorm.DB
}

// NewStatsStore builds the stats store.
func NewStatsStore(db *gorm.DB) *StatsStore { return &StatsStore{db: db} }

// ReviewVolume is the review-volume metric: count(*) for today / the last 7 days / the last 30 days.
// Windows are inclusive ranges over review_day (the review-day string), and today is the review day the caller computed.
type ReviewVolume struct {
	Today      int64
	Last7Days  int64
	Last30Days int64
}

// ReviewVolume aggregates the review volume by review_day. today uses the review_day format (YYYY-MM-DD),
// the last 7 days = [today-6, today] and the last 30 days = [today-29, today], consistent with the other metrics' "aggregate by review_day".
func (s *StatsStore) ReviewVolume(ctx context.Context, userID uint64, today string) (ReviewVolume, error) {
	base, err := time.Parse("2006-01-02", today)
	if err != nil {
		return ReviewVolume{}, fmt.Errorf("review volume: invalid review day %q: %w", today, err)
	}
	from7 := base.AddDate(0, 0, -6).Format("2006-01-02")
	from30 := base.AddDate(0, 0, -29).Format("2006-01-02")

	var row struct {
		Today      int64 `gorm:"column:today"`
		Last7Days  int64 `gorm:"column:last7"`
		Last30Days int64 `gorm:"column:last30"`
	}
	sql := `SELECT
		COALESCE(SUM(CASE WHEN review_day = ? THEN 1 ELSE 0 END), 0) AS today,
		COALESCE(SUM(CASE WHEN review_day >= ? AND review_day <= ? THEN 1 ELSE 0 END), 0) AS last7,
		COALESCE(SUM(CASE WHEN review_day >= ? AND review_day <= ? THEN 1 ELSE 0 END), 0) AS last30
		FROM reviews WHERE user_id = ?`
	if err := s.db.WithContext(ctx).Raw(sql, today, from7, today, from30, today, userID).Scan(&row).Error; err != nil {
		return ReviewVolume{}, fmt.Errorf("review volume: %w", err)
	}
	return ReviewVolume{Today: row.Today, Last7Days: row.Last7Days, Last30Days: row.Last30Days}, nil
}

// DueForecast is the bucket counts of the due forecast: today (overdue included) / tomorrow / within 7 days / within 30 days /
// further out, over the cards that have left the new state, plus New, the user's unstudied new cards.
//
// The buckets and New are disjoint: a buried new card carries a due_at but is still new, so it is counted in New only.
// The day boundary is the review-day start from reviewday.Start: the cutoff moment of the date that local (now - day_cutoff_hour) falls in.
// The "today" bucket holds every scheduled card with due_at <= the end of today, so overdue cards are never missed.
type DueForecast struct {
	Today    int64
	Tomorrow int64
	Within7  int64
	Within30 int64
	Later    int64
	New      int64
}

// DueForecast buckets the user's scheduled cards by due_at and counts their new cards, both through the shared predicates in
// card_scope.go. With deckID 0 it covers the user's default study scope: the visible decks minus the ones the user paused
// (activeDeckIDsQuery), so it agrees with the default queue. A non-zero deckID names a deck explicitly, which counts it even
// when it is paused, as long as the user can see it.
func (s *StatsStore) DueForecast(ctx context.Context, userID, deckID uint64, now time.Time, loc *time.Location, cutoffHour int) (DueForecast, error) {
	if userID == 0 {
		return DueForecast{}, fmt.Errorf("due forecast: user id is required")
	}
	e1 := reviewday.Start(now, loc, cutoffHour).Add(24 * time.Hour) // end of today
	e2 := e1.Add(24 * time.Hour)                                    // end of tomorrow
	e7 := e1.Add(7 * 24 * time.Hour)                                // end of the 7-day window
	e30 := e1.Add(30 * 24 * time.Hour)                              // end of the 30-day window

	db := s.db.WithContext(ctx)
	scope := activeDeckIDsQuery(s.db, userID)
	if deckID != 0 {
		scope = visibleDeckIDsQuery(s.db, userID).Where("id = ?", deckID)
	}

	var row struct {
		Today    int64 `gorm:"column:b_today"`
		Tomorrow int64 `gorm:"column:b_tomorrow"`
		Within7  int64 `gorm:"column:b_within7"`
		Within30 int64 `gorm:"column:b_within30"`
		Later    int64 `gorm:"column:b_later"`
	}
	err := WhereScheduled(UserCardStates(db, userID, scope)).
		Select(`COALESCE(SUM(CASE WHEN cs.due_at <= ? THEN 1 ELSE 0 END), 0) AS b_today,
			COALESCE(SUM(CASE WHEN cs.due_at > ? AND cs.due_at <= ? THEN 1 ELSE 0 END), 0) AS b_tomorrow,
			COALESCE(SUM(CASE WHEN cs.due_at > ? AND cs.due_at <= ? THEN 1 ELSE 0 END), 0) AS b_within7,
			COALESCE(SUM(CASE WHEN cs.due_at > ? AND cs.due_at <= ? THEN 1 ELSE 0 END), 0) AS b_within30,
			COALESCE(SUM(CASE WHEN cs.due_at > ? THEN 1 ELSE 0 END), 0) AS b_later`,
			e1, e1, e2, e2, e7, e7, e30, e30).
		Scan(&row).Error
	if err != nil {
		return DueForecast{}, fmt.Errorf("due forecast: bucket: %w", err)
	}

	var fresh int64
	if err := UserNewCards(db, userID, scope).Count(&fresh).Error; err != nil {
		return DueForecast{}, fmt.Errorf("due forecast: new cards: %w", err)
	}

	return DueForecast{
		Today:    row.Today,
		Tomorrow: row.Tomorrow,
		Within7:  row.Within7,
		Within30: row.Within30,
		Later:    row.Later,
		New:      fresh,
	}, nil
}

// retentionBuckets holds the retention bucket boundaries (days): [0,1) [1,7) [7,30) [30,90) [90,180)
// [180,365) [365,+∞). The boundaries apply to the stability value.
var retentionBuckets = []struct {
	Label string
	Low   float64
	High  float64 // 0 means unbounded above
}{
	{"<1d", 0, 1},
	{"1-7d", 1, 7},
	{"7-30d", 7, 30},
	{"30-90d", 30, 90},
	{"90-180d", 90, 180},
	{"180-365d", 180, 365},
	{"365d+", 365, 0},
}

// RetentionBucket is the retention of one stability bucket; Passed is the number of ratings other than Again.
type RetentionBucket struct {
	Label  string
	Total  int64
	Passed int64
	// Rate is Passed/Total; defined as 0 when Total is 0, avoiding NaN.
	Rate float64
}

// RetentionStats is the retention result: the share of "the due review was rated something other than Again",
// bucketed by stability.
type RetentionStats struct {
	Buckets []RetentionBucket
	Total   int64
	Passed  int64
	Rate    float64
}

// retentionReviewSQL is the single predicate for the denominator of the "true retention rate" (table alias r = reviews),
// and retentionPassedSQL is the condition the numerator adds on top of the denominator.
//
// Definition: only log entries with state_before = Review(2) count -- that is a review after the card came due; a new card's first
// review and the learning/relearning steps only build memory, and mixing them in would inflate the retention rate, making it incomparable with the preset's desired retention
// target. The "remembered" test is rating != Again(1). stability IS NOT NULL excludes imported old rows
// with no stability record: bucketing needs it, and the overall value, deck rows, tag rows and the home summary exclude them too, so every denominator agrees.
// The overall page value, the stability buckets, the deck dimension, the tag dimension and the home summary all reference these two constants;
// writing a second, approximate "not Again / all reviews" definition is not allowed.
const (
	retentionReviewSQL = "r.state_before = 2 AND r.stability IS NOT NULL"
	retentionPassedSQL = "r.rating <> 1"
)

// RetentionByStability computes the retention rate of due reviews; the definition is in retentionReviewSQL.
// stability is the new stability that rating produced (reviews.stability), used for bucketing.
func (s *StatsStore) RetentionByStability(ctx context.Context, userID, deckID uint64) (RetentionStats, error) {
	if userID == 0 {
		return RetentionStats{}, fmt.Errorf("retention: user id is required")
	}
	join := `reviews AS r
		JOIN cards AS c ON c.id = r.card_id AND c.deleted_at IS NULL
		JOIN notes AS n ON n.id = c.note_id AND n.deleted_at IS NULL`
	where := "r.user_id = ? AND " + retentionReviewSQL
	args := []any{userID}
	if deckID != 0 {
		where += " AND n.deck_id = ?"
		args = append(args, deckID)
	}

	cols := make([]string, 0, len(retentionBuckets)*2)
	for i, b := range retentionBuckets {
		cond := stabilityRangeSQL(b.Low, b.High)
		cols = append(cols,
			fmt.Sprintf("COALESCE(SUM(CASE WHEN %s THEN 1 ELSE 0 END), 0) AS t%d", cond, i),
			fmt.Sprintf("COALESCE(SUM(CASE WHEN %s AND %s THEN 1 ELSE 0 END), 0) AS p%d", cond, retentionPassedSQL, i))
	}
	var raw map[string]any
	sql := fmt.Sprintf("SELECT %s FROM %s WHERE %s", strings.Join(cols, ", "), join, where)
	if err := s.db.WithContext(ctx).Raw(sql, args...).Scan(&raw).Error; err != nil {
		return RetentionStats{}, fmt.Errorf("retention: %w", err)
	}

	out := RetentionStats{Buckets: make([]RetentionBucket, 0, len(retentionBuckets))}
	for i, b := range retentionBuckets {
		total := mapInt64(raw[fmt.Sprintf("t%d", i)])
		passed := mapInt64(raw[fmt.Sprintf("p%d", i)])
		rate := 0.0
		if total > 0 {
			rate = float64(passed) / float64(total)
		}
		out.Buckets = append(out.Buckets, RetentionBucket{Label: b.Label, Total: total, Passed: passed, Rate: rate})
		out.Total += total
		out.Passed += passed
	}
	if out.Total > 0 {
		out.Rate = float64(out.Passed) / float64(out.Total)
	}
	return out, nil
}

// stabilityRangeSQL builds the range condition on stability; High 0 means unbounded above.
func stabilityRangeSQL(low, high float64) string {
	cond := fmt.Sprintf("r.stability >= %g", low)
	if high > 0 {
		cond += fmt.Sprintf(" AND r.stability < %g", high)
	}
	return cond
}

// TimeSpent is the time-invested metric: the total elapsed_ms, the average per review and the median.
type TimeSpent struct {
	TotalMS int64
	Count   int64
	// AvgMS is TotalMS/Count; 0 when Count is 0.
	AvgMS float64
	// MedianMS is the lower median (for an even sample size, the (n-1)/2-th in ascending order); 0 when Count is 0.
	MedianMS int64
}

// TimeSpent computes the total, the per-review average and the median of reviews.elapsed_ms within the review-day range [fromDay, toDay].
// The median is computed on the Go side: neither database has a portable median in SQL, and sorting for the middle value is easier to cross-check by hand.
// Entries with a NULL elapsed_ms (old data, or not timed) are not counted, and are honestly left out of Count.
func (s *StatsStore) TimeSpent(ctx context.Context, userID uint64, fromDay, toDay string) (TimeSpent, error) {
	var total struct {
		Total *int64 `gorm:"column:total"`
		N     int64  `gorm:"column:n"`
	}
	sumSQL := `SELECT SUM(elapsed_ms) AS total, COUNT(elapsed_ms) AS n FROM reviews
		WHERE user_id = ? AND elapsed_ms IS NOT NULL AND review_day >= ? AND review_day <= ?`
	if err := s.db.WithContext(ctx).Raw(sumSQL, userID, fromDay, toDay).Scan(&total).Error; err != nil {
		return TimeSpent{}, fmt.Errorf("time spent: sum: %w", err)
	}
	out := TimeSpent{Count: total.N}
	if total.Total != nil {
		out.TotalMS = *total.Total
	}
	if out.Count > 0 {
		out.AvgMS = float64(out.TotalMS) / float64(out.Count)
	}

	var values []int
	medianSQL := `SELECT elapsed_ms FROM reviews
		WHERE user_id = ? AND elapsed_ms IS NOT NULL AND review_day >= ? AND review_day <= ?
		ORDER BY elapsed_ms ASC`
	if err := s.db.WithContext(ctx).Raw(medianSQL, userID, fromDay, toDay).Scan(&values).Error; err != nil {
		return TimeSpent{}, fmt.Errorf("time spent: median: %w", err)
	}
	if n := len(values); n > 0 {
		out.MedianMS = int64(values[(n-1)/2])
	}
	return out, nil
}

// DeckStat is the stats row of one deck (the "deck dimension").
type DeckStat struct {
	DeckID uint64
	// PublicID is the deck's external id; clients only know that one, the numeric primary key never appears in a response.
	PublicID string
	Name     string
	// Review and New are what the review queue offers in this deck today (DeckQueueCounts), not a count of their own.
	Review int64
	New    int64
	// Reviews is the deck's total review count (new cards' first reviews and learning steps included).
	Reviews int64
	// RetentionTotal / Passed are the denominator and numerator of the true retention rate (definition in retentionReviewSQL),
	// and the denominator is only a subset of Reviews, so it is returned separately: with a zero denominator the UI shows "no data" rather than 0%.
	RetentionTotal int64
	Passed         int64
	// Retention is Passed/RetentionTotal; 0 when RetentionTotal is 0.
	Retention float64
	ElapsedMS int64
}

// DeckQueueCounts is what the review queue offers in one deck today: New is the new cards that can still be introduced
// within new_per_day, Review is the due learning cards plus the due review cards within reviews_per_day. Their sum is the
// number of cards available after opening the deck. schedule.QueueBuilder.DeckCounts is the only producer.
type DeckQueueCounts struct{ New, Review int }

// DeckBreakdown returns, for each visible deck that has something to study today or any review history, the queue counts
// taken from queue (keyed by deck id, as produced by schedule.QueueBuilder.DeckCounts), the review volume, the retention
// rate and the cumulative time invested.
//
// The deck scope is the decks visible to the user (the same predicate as the deck list page and the queue), so shared decks
// appear while decks unrelated to the user, others' private ones included, do not. The due numbers are not counted here:
// they come from the queue so the stats page shows the same numbers as the deck list and /review.
func (s *StatsStore) DeckBreakdown(ctx context.Context, userID uint64, queue map[uint64]DeckQueueCounts) ([]DeckStat, error) {
	if userID == 0 {
		return nil, fmt.Errorf("deck breakdown: user id is required")
	}
	deckScope := visibleDeckIDsQuery(s.db, userID)
	stats := map[uint64]*DeckStat{}
	get := func(id uint64) *DeckStat {
		if row, ok := stats[id]; ok {
			return row
		}
		row := &DeckStat{DeckID: id}
		stats[id] = row
		return row
	}

	for id, c := range queue {
		if c.New == 0 && c.Review == 0 {
			continue
		}
		row := get(id)
		row.Review = int64(c.Review)
		row.New = int64(c.New)
	}

	// Review volume, retention rate, cumulative time: aggregate reviews grouped by deck. Likewise limited to visible decks:
	// once a grant is revoked, reviews of those decks must no longer appear in the stats page's deck dimension.
	var revRows []struct {
		DeckID         uint64 `gorm:"column:deck_id"`
		Reviews        int64  `gorm:"column:reviews"`
		RetentionTotal int64  `gorm:"column:retention_total"`
		Passed         int64  `gorm:"column:passed"`
		ElapsedMS      *int64 `gorm:"column:elapsed_ms"`
	}
	revSQL := `SELECT n.deck_id AS deck_id, COUNT(r.id) AS reviews,
		COALESCE(SUM(CASE WHEN ` + retentionReviewSQL + ` THEN 1 ELSE 0 END), 0) AS retention_total,
		COALESCE(SUM(CASE WHEN ` + retentionReviewSQL + ` AND ` + retentionPassedSQL + ` THEN 1 ELSE 0 END), 0) AS passed,
		SUM(r.elapsed_ms) AS elapsed_ms
		FROM reviews AS r
		JOIN cards AS c ON c.id = r.card_id AND c.deleted_at IS NULL
		JOIN notes AS n ON n.id = c.note_id AND n.deleted_at IS NULL
		WHERE r.user_id = ? AND n.deck_id IN (?) GROUP BY n.deck_id`
	if err := s.db.WithContext(ctx).Raw(revSQL, userID, deckScope).Scan(&revRows).Error; err != nil {
		return nil, fmt.Errorf("deck breakdown: reviews: %w", err)
	}
	for _, r := range revRows {
		row := get(r.DeckID)
		row.Reviews = r.Reviews
		row.RetentionTotal = r.RetentionTotal
		row.Passed = r.Passed
		if r.ElapsedMS != nil {
			row.ElapsedMS = *r.ElapsedMS
		}
		if row.RetentionTotal > 0 {
			row.Retention = float64(row.Passed) / float64(row.RetentionTotal)
		}
	}

	if len(stats) == 0 {
		return []DeckStat{}, nil
	}
	ids := make([]uint64, 0, len(stats))
	for id := range stats {
		ids = append(ids, id)
	}
	// Only visible decks get a row, even when the queue map names another one.
	var decks []Deck
	if err := s.db.WithContext(ctx).Where("id IN ? AND id IN (?)", ids, deckScope).Find(&decks).Error; err != nil {
		return nil, fmt.Errorf("deck breakdown: deck names: %w", err)
	}
	out := make([]DeckStat, 0, len(decks))
	for _, d := range decks {
		row := stats[d.ID]
		row.Name = d.Name
		row.PublicID = d.PublicID
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeckID < out[j].DeckID })
	return out, nil
}

// TagStat is the stats row of one tag (the "tag dimension").
type TagStat struct {
	Tag string
	// Reviews is the total number of reviews with that tag within the window.
	Reviews int64
	// RetentionTotal / Passed are the denominator and numerator of the true retention rate, as in DeckStat.
	RetentionTotal int64
	Passed         int64
	// Retention is Passed/RetentionTotal; 0 when RetentionTotal is 0.
	Retention float64
}

// TagBreakdown aggregates review volume and true retention by notes.tags_json (definition in retentionReviewSQL).
//
// tags_json is note-level content metadata: reviews.user_id only guarantees that the log row belongs to the user, and does not stop a deck the user "once
// reviewed but can no longer see" from leaking tags. Without this predicate, after A revokes B's grant, B's stats page
// still lists the former deck's tags and review volume (a real defect in this repo). So it comes from the same source as DeckBreakdown and only aggregates
// the tags of "the decks currently visible to the user": their own ∪ granted through deck_grants ∪ others' public.
//
// tags_json is TEXT (the two-database compatibility convention forbids jsonb/array) and SQL cannot parse JSON portably,
// so it is decoded and aggregated on the Go side; the volume is one user's review log, which is acceptable.
func (s *StatsStore) TagBreakdown(ctx context.Context, userID uint64, fromDay, toDay string) ([]TagStat, error) {
	if userID == 0 {
		return nil, fmt.Errorf("tag breakdown: user id is required")
	}
	// The retention test is computed in SQL with the same predicate; the Go side only sums, avoiding a second definition here.
	var rows []struct {
		Counted  int    `gorm:"column:counted"`
		Passed   int    `gorm:"column:passed"`
		TagsJSON string `gorm:"column:tags_json"`
	}
	sql := `SELECT
		CASE WHEN ` + retentionReviewSQL + ` THEN 1 ELSE 0 END AS counted,
		CASE WHEN ` + retentionReviewSQL + ` AND ` + retentionPassedSQL + ` THEN 1 ELSE 0 END AS passed,
		n.tags_json AS tags_json FROM reviews AS r
		JOIN cards AS c ON c.id = r.card_id AND c.deleted_at IS NULL
		JOIN notes AS n ON n.id = c.note_id AND n.deleted_at IS NULL
		WHERE r.user_id = ? AND n.deck_id IN (?) AND r.review_day >= ? AND r.review_day <= ?`
	if err := s.db.WithContext(ctx).Raw(sql, userID, visibleDeckIDsQuery(s.db, userID), fromDay, toDay).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("tag breakdown: %w", err)
	}
	acc := map[string]*TagStat{}
	for _, row := range rows {
		var tags []string
		if err := json.Unmarshal([]byte(row.TagsJSON), &tags); err != nil {
			// Invalid JSON is not a reason for the stats to fail: skip that row rather than 500 the whole page.
			continue
		}
		for _, tag := range tags {
			if tag == "" {
				continue
			}
			t, ok := acc[tag]
			if !ok {
				t = &TagStat{Tag: tag}
				acc[tag] = t
			}
			t.Reviews++
			t.RetentionTotal += int64(row.Counted)
			t.Passed += int64(row.Passed)
		}
	}
	out := make([]TagStat, 0, len(acc))
	for _, t := range acc {
		if t.RetentionTotal > 0 {
			t.Retention = float64(t.Passed) / float64(t.RetentionTotal)
		}
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Reviews != out[j].Reviews {
			return out[i].Reviews > out[j].Reviews
		}
		return out[i].Tag < out[j].Tag
	})
	return out, nil
}

// GradeSourceStat is the count of one grading source (the "grading source distribution").
type GradeSourceStat struct {
	Source string
	Count  int64
}

// GradeSourceDistribution aggregates by grade_source, ordered by count descending (self/typed/llm).
func (s *StatsStore) GradeSourceDistribution(ctx context.Context, userID uint64) ([]GradeSourceStat, error) {
	if userID == 0 {
		return nil, fmt.Errorf("grade source distribution: user id is required")
	}
	var rows []struct {
		Source string `gorm:"column:source"`
		Count  int64  `gorm:"column:count"`
	}
	sql := `SELECT grade_source AS source, COUNT(*) AS count FROM reviews WHERE user_id = ? GROUP BY grade_source`
	if err := s.db.WithContext(ctx).Raw(sql, userID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("grade source distribution: %w", err)
	}
	out := make([]GradeSourceStat, 0, len(rows))
	for _, r := range rows {
		out = append(out, GradeSourceStat{Source: r.Source, Count: r.Count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Source < out[j].Source
	})
	return out, nil
}

// StreakStats is the streak result (the "streak").
type StreakStats struct {
	// Current is the number of consecutive review days still unbroken up to "today"; not having reviewed today yet does not break it,
	// but a whole review day skipped (no review today or yesterday) makes it 0.
	Current int
	// Longest is the longest streak of consecutive review days on record.
	Longest int
}

// Streak computes the consecutive review days. It works on review_day (the review-day string, already computed with the cutoff by the caller):
// two adjacent review days both need a review to count as consecutive. "Today" is computed from now and the cutoff on the spot, so a review at
// 03:59 still counts as the previous day -- only a whole review day skipped breaks the streak
// now/loc/cutoffHour come from the caller and the function never reads the wall clock.
func (s *StatsStore) Streak(ctx context.Context, userID uint64, now time.Time, loc *time.Location, cutoffHour int) (StreakStats, error) {
	if userID == 0 {
		return StreakStats{}, fmt.Errorf("streak: user id is required")
	}
	today := reviewday.Day(now, loc, cutoffHour)

	var days []string
	sql := `SELECT DISTINCT review_day FROM reviews WHERE user_id = ? AND review_day <= ? ORDER BY review_day ASC`
	if err := s.db.WithContext(ctx).Raw(sql, userID, today).Scan(&days).Error; err != nil {
		return StreakStats{}, fmt.Errorf("streak: %w", err)
	}
	if len(days) == 0 {
		return StreakStats{}, nil
	}

	present := make(map[string]bool, len(days))
	for _, d := range days {
		present[d] = true
	}

	// Longest run: scan the dates ascending and start a new run at every gap.
	longest, run := 0, 0
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

	// Current run: count back from the last day, but it only counts as "unbroken" when the last day is today or yesterday.
	current := 0
	last := days[len(days)-1]
	if last == today || last == reviewday.Shift(today, -1) {
		for d := last; present[d]; d = reviewday.Shift(d, -1) {
			current++
		}
	}
	return StreakStats{Current: current, Longest: longest}, nil
}

// LearningCurvePoint is one review day on the learning curve (the "learning curve").
type LearningCurvePoint struct {
	Day string
	// New is the number of cards "newly introduced" that day: first reviews with state_before = New.
	New int64
	// Review is the number of reviews that day of already seen cards (state_before != New).
	Review int64
}

// LearningCurve returns "newly introduced vs review volume" for every review day with a review record within [fromDay, toDay],
// ordered by date ascending. Newly introduced means state_before = New and everything else counts as review volume; the two are mutually exclusive and their sum is the day's total.
// fromDay/toDay are inclusive bounds in review_day format, consistent with the other metrics.
func (s *StatsStore) LearningCurve(ctx context.Context, userID uint64, fromDay, toDay string) ([]LearningCurvePoint, error) {
	if userID == 0 {
		return nil, fmt.Errorf("learning curve: user id is required")
	}
	var rows []struct {
		Day    string `gorm:"column:day"`
		New    int64  `gorm:"column:new_count"`
		Review int64  `gorm:"column:review_count"`
	}
	sql := `SELECT review_day AS day,
		COALESCE(SUM(CASE WHEN state_before = ? THEN 1 ELSE 0 END), 0) AS new_count,
		COALESCE(SUM(CASE WHEN state_before <> ? THEN 1 ELSE 0 END), 0) AS review_count
		FROM reviews WHERE user_id = ? AND review_day >= ? AND review_day <= ?
		GROUP BY review_day ORDER BY review_day ASC`
	if err := s.db.WithContext(ctx).Raw(sql, reviewStateNew, reviewStateNew, userID, fromDay, toDay).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("learning curve: %w", err)
	}
	out := make([]LearningCurvePoint, 0, len(rows))
	for _, r := range rows {
		out = append(out, LearningCurvePoint{Day: r.Day, New: r.New, Review: r.Review})
	}
	return out, nil
}

// reviewStateNew is the New value of reviews.state_before (0=New).
const reviewStateNew = 0

// The default and the cap of the learn-ahead window (minutes). The default 0 means off: learning-step intervals apply as configured,
// and cards are not pulled forward before they are due; a user who wants learn-ahead turns it on in their profile.
const (
	DefaultLearnAheadMinutes = 0
	MaxLearnAheadMinutes     = 1440
)

// ResolveLearnAhead turns the nullable learn-ahead setting into a duration: NULL uses the default, out-of-range values clamp to [0, cap].
func ResolveLearnAhead(minutes *int) time.Duration {
	m := DefaultLearnAheadMinutes
	if minutes != nil {
		m = *minutes
	}
	if m < 0 {
		m = 0
	}
	if m > MaxLearnAheadMinutes {
		m = MaxLearnAheadMinutes
	}
	return time.Duration(m) * time.Minute
}

// FormatPercent formats a 0–1 ratio as a percentage with one decimal place (without the % sign).
// The stats page and the weekly digest show the same numbers, so the format must match; hence this single implementation.
func FormatPercent(rate float64) string {
	return strconv.FormatFloat(rate*100, 'f', 1, 64)
}

// mapInt64 turns an aggregate value scanned into a GORM map into int64; the driver may hand back int64 or another numeric type.
func mapInt64(v any) int64 {
	switch n := v.(type) {
	case nil:
		return 0
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}
