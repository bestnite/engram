package store

import (
	"context"
	"strconv"
	"testing"
	"time"

	"gorm.io/gorm"
)

// statsNow is the fixed observation moment of all stats cases; fixing it is what lets the bucket boundaries stay constants.
var statsNow = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

// statsFixture holds the row ids seedStatsFixture created, for the assertions to reference.
type statsFixture struct {
	deckID uint64
	card1  uint64 // note1 ["go","fsrs"], the one mostly reviewed
	card2  uint64 // note2 ["go"]
}

// seedStatsFixture builds the complete dataset for the stats cases. The numbers are hand-computed on purpose:
//
//	reviews: r1(card1,10-02,Good,Review,stab5,1000ms,self) r2(card1,10-02,Again,Review,stab0.5,2000ms,self)
//	         r3(card2,10-01,Easy,Review,stab40,3000ms,typed) r4(card1,09-20,Hard,Learning,stab2,nil,self)
//	         r5(card2,08-01,Good,Review,stab100,500ms,llm)
func seedStatsFixture(t *testing.T, db *gorm.DB) statsFixture {
	t.Helper()
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	p := NewPreset(1, "stats")
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	d := Deck{OwnerUserID: 1, Name: "Deck A", PresetID: p.ID, CreatedAt: statsNow}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	n1 := Note{DeckID: d.ID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`, TagsJSON: `["go","fsrs"]`, CreatedAt: statsNow, UpdatedAt: statsNow}
	n2 := Note{DeckID: d.ID, Kind: "basic", FieldsJSON: `{"front":"q2","back":"a2"}`, TagsJSON: `["go"]`, CreatedAt: statsNow, UpdatedAt: statsNow}
	if err := db.Create(&n1).Error; err != nil {
		t.Fatalf("create note1: %v", err)
	}
	if err := db.Create(&n2).Error; err != nil {
		t.Fatalf("create note2: %v", err)
	}
	card1 := createStatsCard(t, db, n1.ID, "forward")
	card2 := createStatsCard(t, db, n2.ID, "back")

	// Cards for the due-forecast buckets: today / tomorrow / within 7 days / within 30 days / further out / a new card with no state row.
	setState(t, db, card1, "review", statsNow.Add(-2*time.Hour), 5)
	setState(t, db, card2, "review", time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), 40)
	b5 := createStatsCard(t, db, n1.ID, "b5")
	setState(t, db, b5, "review", time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC), 30)
	b6 := createStatsCard(t, db, n1.ID, "b6")
	setState(t, db, b6, "review", time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC), 20)
	b7 := createStatsCard(t, db, n1.ID, "b7")
	setState(t, db, b7, "review", time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC), 15)
	_ = createStatsCard(t, db, n1.ID, "b8") // new card: no card_states row

	seedReview(t, db, card1, "2026-10-02", 3, 2, 5, 1000, "self", statsNow)
	seedReview(t, db, card1, "2026-10-02", 1, 2, 0.5, 2000, "self", statsNow.Add(time.Minute))
	seedReview(t, db, card2, "2026-10-01", 4, 2, 40, 3000, "typed", statsNow.Add(-24*time.Hour))
	seedReview(t, db, card1, "2026-09-20", 2, 1, 2, -1, "self", statsNow.Add(-12*24*time.Hour)) // -1 = elapsed_ms NULL
	seedReview(t, db, card2, "2026-08-01", 3, 2, 100, 500, "llm", statsNow.Add(-62*24*time.Hour))

	return statsFixture{deckID: d.ID, card1: card1, card2: card2}
}

func createStatsCard(t *testing.T, db *gorm.DB, noteID uint64, template string) uint64 {
	t.Helper()
	c := Card{NoteID: noteID, Template: template, Ordinal: 0, CreatedAt: statsNow}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("create card %s: %v", template, err)
	}
	return c.ID
}

func setState(t *testing.T, db *gorm.DB, cardID uint64, state string, due time.Time, stability float64) {
	t.Helper()
	st := CardState{CardID: cardID, UserID: 1, State: state, DueAt: &due, Stability: &stability, ScheduledDays: 10}
	if err := db.Create(&st).Error; err != nil {
		t.Fatalf("create card_state for card %d: %v", cardID, err)
	}
}

// seedReview writes one review log entry; elapsedMS -1 means NULL.
func seedReview(t *testing.T, db *gorm.DB, cardID uint64, day string, rating, stateBefore int, stability float64, elapsedMS int, source string, at time.Time) {
	t.Helper()
	rv := Review{
		CardID: cardID, UserID: 1, Rating: rating, GradeSource: source,
		ReviewedAt: at, ReviewDay: day, StateBefore: stateBefore, Stability: &stability,
	}
	if elapsedMS >= 0 {
		rv.ElapsedMS = &elapsedMS
	}
	if err := db.Create(&rv).Error; err != nil {
		t.Fatalf("create review: %v", err)
	}
}

// TestStatsReviewVolumeMatchesHandSQL cross-checks the review volume against independent SQL (the first metric).
// The expected values also match the fixed dataset: today 2, last 7 days 3, last 30 days 4.
func TestStatsReviewVolumeMatchesHandSQL(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			seedStatsFixture(t, db)
			ctx := context.Background()
			today := "2026-10-02"
			from7, from30 := "2026-09-26", "2026-09-03"

			got, err := NewStatsStore(db).ReviewVolume(ctx, 1, today)
			if err != nil {
				t.Fatalf("ReviewVolume() error = %v", err)
			}
			if got.Today != 2 || got.Last7Days != 3 || got.Last30Days != 4 {
				t.Fatalf("ReviewVolume() = %+v, want {Today:2 Last7Days:3 Last30Days:4}", got)
			}

			var handToday, hand7, hand30 int64
			db.Raw("SELECT COUNT(*) FROM reviews WHERE user_id = ? AND review_day = ?", 1, today).Scan(&handToday)
			db.Raw("SELECT COUNT(*) FROM reviews WHERE user_id = ? AND review_day >= ? AND review_day <= ?", 1, from7, today).Scan(&hand7)
			db.Raw("SELECT COUNT(*) FROM reviews WHERE user_id = ? AND review_day >= ? AND review_day <= ?", 1, from30, today).Scan(&hand30)
			if got.Today != handToday || got.Last7Days != hand7 || got.Last30Days != hand30 {
				t.Errorf("ReviewVolume() = %+v, hand SQL = today %d / 7d %d / 30d %d", got, handToday, hand7, hand30)
			}
		})
	}
}

// TestStatsDueForecastMatchesHandSQL cross-checks the due-forecast buckets against independent SQL.
// The day boundary is the review-day start at 04:00 UTC: today ends 10-03 04:00, tomorrow ends 10-04 04:00,
// the 7-day window ends 10-09 04:00, the 30-day window ends 11-01 04:00.
func TestStatsDueForecastMatchesHandSQL(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			seedStatsFixture(t, db)
			ctx := context.Background()
			got, err := NewStatsStore(db).DueForecast(ctx, 1, 0, statsNow, time.UTC, 4)
			if err != nil {
				t.Fatalf("DueForecast() error = %v", err)
			}

			edges := []time.Time{
				time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC),
				time.Date(2026, 10, 4, 4, 0, 0, 0, time.UTC),
				time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC),
				time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC),
			}
			hand := func(sql string, args ...any) int64 {
				var n int64
				db.Raw(sql, args...).Scan(&n)
				return n
			}
			join := "FROM card_states cs JOIN cards c ON c.id = cs.card_id AND c.deleted_at IS NULL AND cs.suspended_at IS NULL JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL WHERE cs.user_id = ?"
			wantToday := hand("SELECT COUNT(*) "+join+" AND cs.due_at <= ?", 1, edges[0])
			wantTomorrow := hand("SELECT COUNT(*) "+join+" AND cs.due_at > ? AND cs.due_at <= ?", 1, edges[0], edges[1])
			want7 := hand("SELECT COUNT(*) "+join+" AND cs.due_at > ? AND cs.due_at <= ?", 1, edges[1], edges[2])
			want30 := hand("SELECT COUNT(*) "+join+" AND cs.due_at > ? AND cs.due_at <= ?", 1, edges[2], edges[3])
			wantLater := hand("SELECT COUNT(*) "+join+" AND cs.due_at > ?", 1, edges[3])
			wantNew := hand(`SELECT COUNT(*) FROM cards c JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL LEFT JOIN card_states cs ON cs.card_id = c.id AND cs.user_id = ? WHERE c.deleted_at IS NULL AND cs.suspended_at IS NULL AND (cs.card_id IS NULL OR (cs.state = 'new' AND (cs.due_at IS NULL OR cs.due_at > ?)))`, 1, statsNow)

			if got.Today != wantToday || got.Tomorrow != wantTomorrow || got.Within7 != want7 ||
				got.Within30 != want30 || got.Later != wantLater || got.NewNotDue != wantNew {
				t.Errorf("DueForecast() = %+v, hand SQL = today %d tomorrow %d 7d %d 30d %d later %d new %d",
					got, wantToday, wantTomorrow, want7, want30, wantLater, wantNew)
			}
			if got.Today != 1 || got.Tomorrow != 1 || got.Within7 != 1 || got.Within30 != 1 || got.Later != 1 || got.NewNotDue != 1 {
				t.Errorf("DueForecast() = %+v, want one card per bucket", got)
			}
		})
	}
}

// TestStatsRetentionByStabilityMatchesHandSQL cross-checks the retention rate against independent SQL:
// the share of "rated something other than Again" among due reviews (state_before=Review), bucketed by stability.
func TestStatsRetentionByStabilityMatchesHandSQL(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			seedStatsFixture(t, db)
			got, err := NewStatsStore(db).RetentionByStability(context.Background(), 1, 0)
			if err != nil {
				t.Fatalf("RetentionByStability() error = %v", err)
			}
			if got.Total != 4 || got.Passed != 3 || got.Rate != 0.75 {
				t.Fatalf("RetentionByStability() total/passed/rate = %d/%d/%v, want 4/3/0.75", got.Total, got.Passed, got.Rate)
			}

			join := "FROM reviews r JOIN cards c ON c.id = r.card_id AND c.deleted_at IS NULL JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL WHERE r.user_id = ? AND r.state_before = 2 AND r.stability IS NOT NULL AND r.stability >= ? AND r.stability < ?"
			handTotal := func(low, high float64) int64 {
				var n int64
				db.Raw("SELECT COUNT(*) "+join, 1, low, high).Scan(&n)
				return n
			}
			byLabel := map[string]RetentionBucket{}
			for _, b := range got.Buckets {
				byLabel[b.Label] = b
			}
			checks := []struct {
				label     string
				low, high float64
				wantT     int64
			}{
				{"<1d", 0, 1, 1},
				{"1-7d", 1, 7, 1},
				{"7-30d", 7, 30, 0},
				{"30-90d", 30, 90, 1},
				{"90-180d", 90, 180, 1},
			}
			for _, c := range checks {
				b := byLabel[c.label]
				if b.Total != c.wantT {
					t.Errorf("bucket %s total = %d, want %d", c.label, b.Total, c.wantT)
				}
				if hand := handTotal(c.low, c.high); hand != b.Total {
					t.Errorf("bucket %s total = %d, hand SQL = %d", c.label, b.Total, hand)
				}
			}
			if b := byLabel["<1d"]; b.Passed != 0 || b.Rate != 0 {
				t.Errorf("bucket <1d = %+v, want passed 0 rate 0", b)
			}
		})
	}
}

// TestStatsTimeSpentMatchesHandSQL cross-checks the time-invested average and median.
func TestStatsTimeSpentMatchesHandSQL(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			seedStatsFixture(t, db)
			got, err := NewStatsStore(db).TimeSpent(context.Background(), 1, "2026-01-01", "2026-12-31")
			if err != nil {
				t.Fatalf("TimeSpent() error = %v", err)
			}
			var wantTotal, wantN int64
			db.Raw("SELECT COALESCE(SUM(elapsed_ms), 0), COUNT(elapsed_ms) FROM reviews WHERE user_id = ? AND elapsed_ms IS NOT NULL", 1).Row().Scan(&wantTotal, &wantN)
			if got.TotalMS != wantTotal || got.Count != wantN {
				t.Errorf("TimeSpent() total/count = %d/%d, hand SQL = %d/%d", got.TotalMS, got.Count, wantTotal, wantN)
			}
			if got.TotalMS != 6500 || got.Count != 4 || got.AvgMS != 1625 {
				t.Errorf("TimeSpent() = %+v, want total 6500 count 4 avg 1625", got)
			}
			// The lower median of [500,1000,2000,3000] in ascending order is 1000.
			if got.MedianMS != 1000 {
				t.Errorf("MedianMS = %d, want 1000", got.MedianMS)
			}
		})
	}
}

// TestStatsGradeSourceDistribution cross-checks the grading source distribution.
func TestStatsGradeSourceDistribution(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			seedStatsFixture(t, db)
			got, err := NewStatsStore(db).GradeSourceDistribution(context.Background(), 1)
			if err != nil {
				t.Fatalf("GradeSourceDistribution() error = %v", err)
			}
			want := map[string]int64{"self": 3, "typed": 1, "llm": 1}
			if len(got) != len(want) {
				t.Fatalf("got %d sources %+v, want %d", len(got), got, len(want))
			}
			for _, row := range got {
				if want[row.Source] != row.Count {
					t.Errorf("source %s count = %d, want %d", row.Source, row.Count, want[row.Source])
				}
			}
			var hand int64
			db.Raw("SELECT COUNT(*) FROM reviews WHERE user_id = ? AND grade_source = ?", 1, "typed").Scan(&hand)
			if hand != 1 {
				t.Errorf("hand SQL typed count = %d, want 1", hand)
			}
		})
	}
}

// TestStatsDeckBreakdownMatchesHandSQL cross-checks the deck dimension.
func TestStatsDeckBreakdownMatchesHandSQL(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			fx := seedStatsFixture(t, db)
			got, err := NewStatsStore(db).DeckBreakdown(context.Background(), 1, statsNow)
			if err != nil {
				t.Fatalf("DeckBreakdown() error = %v", err)
			}
			if len(got) != 1 || got[0].DeckID != fx.deckID || got[0].Name != "Deck A" {
				t.Fatalf("DeckBreakdown() = %+v, want one row for deck %d", got, fx.deckID)
			}
			// The retention denominator/numerator use the true retention definition: only due reviews with state_before=Review count, while the review volume still counts everything.
			var wantReviews, wantRetTotal, wantPassed, wantDue, wantElapsed int64
			db.Raw(`SELECT COUNT(*),
				COALESCE(SUM(CASE WHEN r.state_before = 2 AND r.stability IS NOT NULL THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN r.state_before = 2 AND r.stability IS NOT NULL AND r.rating <> 1 THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(r.elapsed_ms), 0)
				FROM reviews r
				JOIN cards c ON c.id = r.card_id AND c.deleted_at IS NULL
				JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL
				WHERE r.user_id = ?
				  AND n.deck_id IN (SELECT id FROM decks WHERE owner_user_id = ? OR id IN (SELECT deck_id FROM deck_grants WHERE user_id = ?))`,
				1, 1, 1).Row().Scan(&wantReviews, &wantRetTotal, &wantPassed, &wantElapsed)
			db.Raw(`SELECT COUNT(*) FROM cards c
				JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL
				LEFT JOIN card_states cs ON cs.card_id = c.id AND cs.user_id = ?
				WHERE c.deleted_at IS NULL AND cs.suspended_at IS NULL
				  AND n.deck_id IN (SELECT id FROM decks WHERE owner_user_id = ? OR id IN (SELECT deck_id FROM deck_grants WHERE user_id = ?))
				  AND (cs.card_id IS NULL OR cs.state = 'new' OR cs.due_at IS NULL OR cs.due_at <= ?)`,
				1, 1, 1, statsNow).Scan(&wantDue)
			row := got[0]
			if row.Reviews != wantReviews || row.RetentionTotal != wantRetTotal || row.Passed != wantPassed || row.ElapsedMS != wantElapsed || row.DueCount != wantDue {
				t.Errorf("DeckBreakdown() = %+v, hand SQL = reviews %d retention total %d passed %d elapsed %d due %d", row, wantReviews, wantRetTotal, wantPassed, wantElapsed, wantDue)
			}
			if wantReviews != 5 || wantRetTotal != 4 || wantPassed != 3 || wantDue != 2 {
				t.Errorf("fixture drifted: hand reviews/retention total/passed/due = %d/%d/%d/%d, want 5/4/3/2", wantReviews, wantRetTotal, wantPassed, wantDue)
			}
			if row.Retention != 0.75 {
				t.Errorf("DeckBreakdown() retention = %v, want 0.75 (3 of 4 due reviews)", row.Retention)
			}
		})
	}
}

// TestStatsTagBreakdown cross-checks the tag dimension: tags_json is decoded and aggregated on the Go side.
func TestStatsTagBreakdown(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			seedStatsFixture(t, db)
			got, err := NewStatsStore(db).TagBreakdown(context.Background(), 1, "2026-01-01", "2026-12-31")
			if err != nil {
				t.Fatalf("TagBreakdown() error = %v", err)
			}
			// go covers all 5 reviews, of which 4 are due reviews and 3 are not Again; fsrs only covers card1's
			// 3 (r1 r2 r4), r4 is a learning step and does not count toward retention, leaving 2 due reviews and 1 not Again.
			want := map[string]TagStat{
				"go":   {Tag: "go", Reviews: 5, RetentionTotal: 4, Passed: 3, Retention: 0.75},
				"fsrs": {Tag: "fsrs", Reviews: 3, RetentionTotal: 2, Passed: 1, Retention: 0.5},
			}
			if len(got) != len(want) {
				t.Fatalf("got %d tags %+v, want %d", len(got), got, len(want))
			}
			for _, row := range got {
				w := want[row.Tag]
				if row.Reviews != w.Reviews || row.RetentionTotal != w.RetentionTotal || row.Passed != w.Passed {
					t.Errorf("tag %s = %+v, want reviews %d retention total %d passed %d", row.Tag, row, w.Reviews, w.RetentionTotal, w.Passed)
				}
				if diff := row.Retention - w.Retention; diff > 1e-9 || diff < -1e-9 {
					t.Errorf("tag %s retention = %v, want %v", row.Tag, row.Retention, w.Retention)
				}
			}
		})
	}
}

// seedStreakDays builds a minimal dataset holding only the given review days: one review log entry per day,
// for the streak's date-distribution assertions (seedStatsFixture is not reused, so its dates do not interfere).
func seedStreakDays(t *testing.T, db *gorm.DB, days ...string) {
	t.Helper()
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	p := NewPreset(1, "streak")
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	d := Deck{OwnerUserID: 1, Name: "Streak deck", PresetID: p.ID, CreatedAt: statsNow}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	n := Note{DeckID: d.ID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`, TagsJSON: `[]`, CreatedAt: statsNow, UpdatedAt: statsNow}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	cardID := createStatsCard(t, db, n.ID, "forward")
	for _, day := range days {
		seedReview(t, db, cardID, day, 3, 2, 5, 1000, "self", statsNow)
	}
}

// TestStatsStreakBoundaryAtCutoff is the acceptance boundary: the streak breaks only when "a whole review day
// is skipped". On the same wall-clock date 2026-10-04, 03:30 still counts as review day 10-03 (streak unbroken),
// and at 04:30, past the 04:00 cutoff, 10-03 has been skipped whole (streak drops to zero).
func TestStatsStreakBoundaryAtCutoff(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			seedStreakDays(t, db, "2026-10-01", "2026-10-02")
			ctx := context.Background()

			// The cutoff itself: 03:59:59 belongs to the previous day, 04:00:00 to the current one.
			if got := reviewDayString(time.Date(2026, 10, 3, 3, 59, 59, 0, time.UTC), time.UTC, 4); got != "2026-10-02" {
				t.Errorf("reviewDayString(03:59:59) = %q, want 2026-10-02", got)
			}
			if got := reviewDayString(time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC), time.UTC, 4); got != "2026-10-03" {
				t.Errorf("reviewDayString(04:00:00) = %q, want 2026-10-03", got)
			}

			before, err := NewStatsStore(db).Streak(ctx, 1, time.Date(2026, 10, 4, 3, 30, 0, 0, time.UTC), time.UTC, 4)
			if err != nil {
				t.Fatalf("Streak(before cutoff) error = %v", err)
			}
			if before.Current != 2 || before.Longest != 2 {
				t.Errorf("Streak(before cutoff) = %+v, want current 2 longest 2 (today is still 10-03)", before)
			}

			after, err := NewStatsStore(db).Streak(ctx, 1, time.Date(2026, 10, 4, 4, 30, 0, 0, time.UTC), time.UTC, 4)
			if err != nil {
				t.Fatalf("Streak(after cutoff) error = %v", err)
			}
			if after.Current != 0 || after.Longest != 2 {
				t.Errorf("Streak(after cutoff) = %+v, want current 0 longest 2 (a whole review day was skipped)", after)
			}
		})
	}
}

// TestStatsStreakCurrentAndLongest asserts the current/longest run: the longest run is the historical maximum,
// and the current run tolerates "no review yet today" without breaking (only a whole skipped day yesterday zeroes it).
func TestStatsStreakCurrentAndLongest(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			seedStreakDays(t, db,
				"2026-09-25", "2026-09-26", "2026-09-27",
				"2026-10-01", "2026-10-02")
			ctx := context.Background()
			store := NewStatsStore(db)

			cases := []struct {
				now         time.Time
				wantCurrent int
				wantLongest int
			}{
				{time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), 2, 3}, // 10-02,10-01
				{time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC), 2, 3}, // not reviewed today, 10-02 is there from yesterday, unbroken
				{time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), 0, 3}, // 10-03 skipped entirely
				{time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC), 3, 3}, // 09-25..27 right up to today
			}
			for _, tc := range cases {
				got, err := store.Streak(ctx, 1, tc.now, time.UTC, 4)
				if err != nil {
					t.Fatalf("Streak(%s) error = %v", tc.now.Format("2006-01-02"), err)
				}
				if got.Current != tc.wantCurrent || got.Longest != tc.wantLongest {
					t.Errorf("Streak(%s) = %+v, want current %d longest %d",
						tc.now.Format("2006-01-02"), got, tc.wantCurrent, tc.wantLongest)
				}
			}
		})
	}
}

// TestStatsLearningCurveMatchesHandSQL cross-checks the learning curve: each day's newly introduced
// count is state_before=New, everything else counts as review volume. All expected values are hard-coded, asserting both numbers day by day.
func TestStatsLearningCurveMatchesHandSQL(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			fx := seedStatsFixture(t, db)
			// Add two "newly introduced" entries: one on 10-02, one on 10-03.
			seedReview(t, db, fx.card1, "2026-10-02", 3, 0, 5, 1000, "self", statsNow)
			seedReview(t, db, fx.card2, "2026-10-03", 3, 0, 5, 1000, "self", statsNow)

			got, err := NewStatsStore(db).LearningCurve(context.Background(), 1, "2026-10-01", "2026-10-03")
			if err != nil {
				t.Fatalf("LearningCurve() error = %v", err)
			}
			want := []LearningCurvePoint{
				{Day: "2026-10-01", New: 0, Review: 1},
				{Day: "2026-10-02", New: 1, Review: 2},
				{Day: "2026-10-03", New: 1, Review: 0},
			}
			if len(got) != len(want) {
				t.Fatalf("LearningCurve() = %+v, want %d points %+v", got, len(want), want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("LearningCurve()[%d] = %+v, want %+v", i, got[i], want[i])
				}
				// Independent SQL cross-checks the mutually exclusive buckets for the same day.
				var handNew, handReview int64
				db.Raw(`SELECT
					COALESCE(SUM(CASE WHEN state_before = 0 THEN 1 ELSE 0 END), 0),
					COALESCE(SUM(CASE WHEN state_before <> 0 THEN 1 ELSE 0 END), 0)
					FROM reviews WHERE user_id = ? AND review_day = ?`, 1, want[i].Day).Row().Scan(&handNew, &handReview)
				if got[i].New != handNew || got[i].Review != handReview {
					t.Errorf("LearningCurve()[%d] = %+v, hand SQL = new %d review %d", i, got[i], handNew, handReview)
				}
			}
		})
	}
}

// seedForeignDeck builds a deck owned by owner with cards new cards that have no card_states row,
// to construct "someone else's deck" (to the current user these cards look like new cards never reviewed).
func seedForeignDeck(t *testing.T, db *gorm.DB, owner uint64, name string, cards int) uint64 {
	t.Helper()
	p := NewPreset(owner, name)
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset %s: %v", name, err)
	}
	d := Deck{OwnerUserID: owner, Name: name, PresetID: p.ID, CreatedAt: statsNow}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("create deck %s: %v", name, err)
	}
	n := Note{DeckID: d.ID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`, TagsJSON: `[]`, CreatedAt: statsNow, UpdatedAt: statsNow}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create note in %s: %v", name, err)
	}
	for i := 0; i < cards; i++ {
		createStatsCard(t, db, n.ID, "t"+strconv.Itoa(i))
	}
	return d.ID
}

// TestStatsScopesToVisibleDecks is the regression case for deck privilege escalation (deck dimension and due forecast).
//
// The shape of the defect: DeckBreakdown's due volume and DueForecast's new-card count both start from cards and only join
// card_states on user_id, with no deck-scope filter at all -- cards in someone else's deck have no
// card_states row for the current user (cs.card_id IS NULL), so they were all counted as "due / new not yet due", then grouped by
// deck_id, and other people's private decks appeared on my stats page, name and all.
//
// Definition = the decks "visible" to the user (their own ∪ granted through deck_grants, the same set as the deck list page and the whole-database queue):
// other people's decks never appear, whatever their name; a deck "once granted, now revoked" does not appear either.
func TestStatsScopesToVisibleDecks(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			fx := seedStatsFixture(t, db)
			ctx := context.Background()

			foreignOne := seedForeignDeck(t, db, 2, "Other deck A", 2)
			foreignTwo := seedForeignDeck(t, db, 2, "Other deck B", 3)
			revoked := seedForeignDeck(t, db, 2, "Revoked grant", 2)

			// User 1 once reviewed in the revoked deck (there is a state row and a log entry), and then the grant was revoked:
			// the deck is no longer visible, so the stats page must not show it and must not count its cards in the due forecast.
			if err := db.Create(&DeckGrant{DeckID: revoked, UserID: 1, Role: "reader", CreatedAt: statsNow}).Error; err != nil {
				t.Fatalf("create grant: %v", err)
			}
			var revokedCards []Card
			if err := db.Where("note_id IN (SELECT id FROM notes WHERE deck_id = ?)", revoked).Find(&revokedCards).Error; err != nil {
				t.Fatalf("load revoked cards: %v", err)
			}
			if len(revokedCards) != 2 {
				t.Fatalf("revoked deck has %d cards, want 2", len(revokedCards))
			}
			for _, c := range revokedCards {
				setState(t, db, c.ID, "review", statsNow.Add(-time.Hour), 5)
				seedReview(t, db, c.ID, "2026-10-02", 3, 2, 5, 1000, "self", statsNow)
			}
			if err := db.Where("deck_id = ? AND user_id = ?", revoked, 1).Delete(&DeckGrant{}).Error; err != nil {
				t.Fatalf("delete grant: %v", err)
			}

			// Deck dimension: only their own deck, and the due volume is right.
			got, err := NewStatsStore(db).DeckBreakdown(ctx, 1, statsNow)
			if err != nil {
				t.Fatalf("DeckBreakdown() error = %v", err)
			}
			wantDue := map[uint64]int64{fx.deckID: 2}
			if len(got) != len(wantDue) {
				t.Fatalf("DeckBreakdown() returned %d decks (%+v), want %d (own only)", len(got), got, len(wantDue))
			}
			for _, row := range got {
				want, ok := wantDue[row.DeckID]
				if !ok {
					t.Errorf("DeckBreakdown() leaked deck %d (%q): not visible to the user", row.DeckID, row.Name)
					continue
				}
				if row.DueCount != want {
					t.Errorf("DeckBreakdown() deck %d due = %d, want %d", row.DeckID, row.DueCount, want)
				}
			}
			// Named explicitly: not a single card of someone else's deck may appear (the direct symptom of the defect).
			for _, row := range got {
				if row.DeckID == foreignOne || row.DeckID == foreignTwo {
					t.Errorf("DeckBreakdown() leaked another user's deck %d (%q)", row.DeckID, row.Name)
				}
			}
			// Due forecast: new cards not yet due only count visible decks (the fixture's own 1).
			due, err := NewStatsStore(db).DueForecast(ctx, 1, 0, statsNow, time.UTC, 4)
			if err != nil {
				t.Fatalf("DueForecast() error = %v", err)
			}
			if due.NewNotDue != 1 {
				t.Errorf("DueForecast().NewNotDue = %d, want 1 (own cards only)", due.NewNotDue)
			}
			// User 1 has two due state rows in the revoked-grant deck: without the filter this would become 3.
			if due.Today != 1 {
				t.Errorf("DueForecast().Today = %d, want 1 (revoked-grant deck must not count)", due.Today)
			}
		})
	}
}

// seedTaggedDeck builds a deck owned by owner, holding one note with a single tag and cards cards,
// for the tag-dimension visibility cases to construct both "someone else's deck" and "revoked grant".
func seedTaggedDeck(t *testing.T, db *gorm.DB, owner uint64, name, tag string, cards int) uint64 {
	t.Helper()
	p := NewPreset(owner, name)
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset %s: %v", name, err)
	}
	d := Deck{OwnerUserID: owner, Name: name, PresetID: p.ID, CreatedAt: statsNow}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("create deck %s: %v", name, err)
	}
	n := Note{DeckID: d.ID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`, TagsJSON: `["` + tag + `"]`, CreatedAt: statsNow, UpdatedAt: statsNow}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create note in %s: %v", name, err)
	}
	for i := 0; i < cards; i++ {
		createStatsCard(t, db, n.ID, "t"+strconv.Itoa(i))
	}
	return d.ID
}

// addUser1Reviews adds one review by user 1 on every card in the given deck (a state row + a 2026-10-02 log entry),
// to construct the escalation scenario "history remains after the grant is revoked"; want is the expected card count, and a mismatch means the fixture drifted.
func addUser1Reviews(t *testing.T, db *gorm.DB, deckID uint64, want int) {
	t.Helper()
	var cards []Card
	if err := db.Where("note_id IN (SELECT id FROM notes WHERE deck_id = ?)", deckID).Find(&cards).Error; err != nil {
		t.Fatalf("load cards of deck %d: %v", deckID, err)
	}
	if len(cards) != want {
		t.Fatalf("deck %d has %d cards, want %d", deckID, len(cards), want)
	}
	for _, c := range cards {
		setState(t, db, c.ID, "review", statsNow.Add(-time.Hour), 5)
		seedReview(t, db, c.ID, "2026-10-02", 3, 2, 5, 1000, "self", statsNow)
	}
}

// TestStatsTagBreakdownScopesToVisibleDecks is the regression case for tag-dimension escalation (the tag dimension).
//
// The shape of the defect: TagBreakdown starts from reviews and JOINs notes to take tags_json, aggregating on the Go side.
// Tags are note-level content metadata, and the reviews.user_id row-level guard does not stop them: after A revokes B's grant,
// B's stats page still lists the former deck's tags with their review volume/retention (a real defect in this repo).
//
// Definition: the tag dimension comes from the same source as the deck dimension and only aggregates tags of "currently visible decks"; the other pure review aggregates
// (ReviewVolume and the like) deliberately stay whole-history, asymmetrically. This case, in one fixture,
// pins both definitions down together with a positive control, preventing an implementation from casually adding the predicate to other aggregates.
func TestStatsTagBreakdownScopesToVisibleDecks(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			seedStatsFixture(t, db)
			ctx := context.Background()

			// Someone else's deck: not visible, its tags must not appear.
			otherDeck := seedTaggedDeck(t, db, 2, "Other deck", "shared", 1)
			// Another of someone else's decks: likewise invisible (even though user 1 has historical reviews).
			privateDeck := seedTaggedDeck(t, db, 2, "Other private", "leaked-private", 2)
			// A deck once granted to user 1 and now revoked: invisible, its tags must not appear -- the direct symptom of the defect.
			revokedDeck := seedTaggedDeck(t, db, 2, "Revoked grant", "leaked-revoked", 2)
			if err := db.Create(&DeckGrant{DeckID: revokedDeck, UserID: 1, Role: "reader", CreatedAt: statsNow}).Error; err != nil {
				t.Fatalf("create grant: %v", err)
			}

			addUser1Reviews(t, db, otherDeck, 1)
			addUser1Reviews(t, db, privateDeck, 2)
			addUser1Reviews(t, db, revokedDeck, 2)

			if err := db.Where("deck_id = ? AND user_id = ?", revokedDeck, 1).Delete(&DeckGrant{}).Error; err != nil {
				t.Fatalf("delete grant: %v", err)
			}

			got, err := NewStatsStore(db).TagBreakdown(ctx, 1, "2026-01-01", "2026-12-31")
			if err != nil {
				t.Fatalf("TagBreakdown() error = %v", err)
			}
			// Only the tags of their own deck may appear: go, fsrs. Other people's tags never appear.
			want := map[string]int64{"go": 5, "fsrs": 3}
			gotTags := make(map[string]int64, len(got))
			for _, row := range got {
				gotTags[row.Tag] = row.Reviews
			}
			if len(gotTags) != len(want) {
				t.Fatalf("TagBreakdown() returned %d tags %+v, want %d (%v)", len(gotTags), got, len(want), want)
			}
			for tag, reviews := range want {
				if gotTags[tag] != reviews {
					t.Errorf("tag %q reviews = %d, want %d", tag, gotTags[tag], reviews)
				}
			}
			// The three escalating tags are named explicitly so a failure points straight at the defect.
			for _, leak := range []string{"shared", "leaked-private", "leaked-revoked"} {
				if _, ok := gotTags[leak]; ok {
					t.Errorf("TagBreakdown() leaked tag %q from a deck not visible to the user", leak)
				}
			}

			// Positive control: ReviewVolume is a pure aggregate and stays whole-history; revoking a grant must not shrink it.
			// The fixture's own 2 on 10-02 + the added other 1, private 2 and revoked 2 = 7 today;
			// the last 7 days add one on 10-01 = 8; the last 30 days add one more on 09-20 = 9.
			// If an implementation casually adds the visible-deck predicate to ReviewVolume too, this drops to 3/4/5.
			vol, err := NewStatsStore(db).ReviewVolume(ctx, 1, "2026-10-02")
			if err != nil {
				t.Fatalf("ReviewVolume() error = %v", err)
			}
			if vol.Today != 7 || vol.Last7Days != 8 || vol.Last30Days != 9 {
				t.Errorf("ReviewVolume() = %+v, want {Today:7 Last7Days:8 Last30Days:9} (pure aggregation stays full history)", vol)
			}
		})
	}
}

// TestStatsTagBreakdownCountsOnlyReviewedNotes pins down the tag dimension's definition:
// this dimension answers "which content is weak" and the retention rate has to come from review records, so it only counts the tags
// on **reviewed cards**. Tags on notes with no review record in the same deck do not appear -- that is deliberate, not an omission; the page also says
// so in a line, otherwise it would look like a duplicate of the deck dimension.
func TestStatsTagBreakdownCountsOnlyReviewedNotes(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate: %v", err)
			}
			p := NewPreset(1, "tag scope")
			if err := db.Create(&p).Error; err != nil {
				t.Fatalf("create preset: %v", err)
			}
			d := Deck{OwnerUserID: 1, Name: "Tag scope deck", PresetID: p.ID, CreatedAt: statsNow}
			if err := db.Create(&d).Error; err != nil {
				t.Fatalf("create deck: %v", err)
			}
			reviewed := Note{DeckID: d.ID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`,
				TagsJSON: `["studied"]`, CreatedAt: statsNow, UpdatedAt: statsNow}
			untouched := Note{DeckID: d.ID, Kind: "basic", FieldsJSON: `{"front":"q2","back":"a2"}`,
				TagsJSON: `["never-reviewed"]`, CreatedAt: statsNow, UpdatedAt: statsNow}
			if err := db.Create(&reviewed).Error; err != nil {
				t.Fatalf("create reviewed note: %v", err)
			}
			if err := db.Create(&untouched).Error; err != nil {
				t.Fatalf("create untouched note: %v", err)
			}
			cardID := createStatsCard(t, db, reviewed.ID, "forward")
			seedReview(t, db, cardID, "2026-10-02", 3, 2, 5, 1000, "self", statsNow)

			got, err := NewStatsStore(db).TagBreakdown(context.Background(), 1, "2026-01-01", "2026-12-31")
			if err != nil {
				t.Fatalf("TagBreakdown() error = %v", err)
			}
			if len(got) != 1 || got[0].Tag != "studied" {
				t.Fatalf("TagBreakdown() = %+v, want only the tag on the reviewed note", got)
			}
			if got[0].Reviews != 1 || got[0].Passed != 1 {
				t.Errorf("tag row = %+v, want 1 review passed (hand-computable)", got[0])
			}
		})
	}
}

// TestStatsTrueRetentionIgnoresLearningSteps pins down the single definition of "retention rate": the overall value, deck rows and
// tag rows all count only due reviews with state_before=Review; new cards' first reviews and the learning/relearning steps only enter the review volume.
// A user with only learning steps has a zero denominator, so the UI shows "no data" instead of an inflated percentage.
func TestStatsTrueRetentionIgnoresLearningSteps(t *testing.T) {
	type review struct{ rating, stateBefore int }
	cases := []struct {
		name      string
		reviews   []review
		wantAll   int64
		wantTotal int64
		wantPass  int64
		wantRate  float64
	}{
		{
			name: "mixed states count only due reviews",
			reviews: []review{
				{3, 0}, {1, 1}, {3, 3}, // New / Learning(Again) / Relearning: not counted toward retention
				{3, 2}, {1, 2}, {2, 2}, // Review: Good / Again / Hard
			},
			wantAll: 6, wantTotal: 3, wantPass: 2, wantRate: 2.0 / 3.0,
		},
		{
			name:    "learning steps only yield no retention data",
			reviews: []review{{3, 0}, {3, 1}, {1, 3}},
			wantAll: 3, wantTotal: 0, wantPass: 0, wantRate: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for driver, db := range testDatabases(t) {
				t.Run(driver, func(t *testing.T) {
					if err := db.AutoMigrate(AllModels()...); err != nil {
						t.Fatalf("AutoMigrate: %v", err)
					}
					p := NewPreset(1, "true retention")
					if err := db.Create(&p).Error; err != nil {
						t.Fatalf("create preset: %v", err)
					}
					d := Deck{OwnerUserID: 1, Name: "Retention deck", PresetID: p.ID, CreatedAt: statsNow}
					if err := db.Create(&d).Error; err != nil {
						t.Fatalf("create deck: %v", err)
					}
					n := Note{DeckID: d.ID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`,
						TagsJSON: `["topic"]`, CreatedAt: statsNow, UpdatedAt: statsNow}
					if err := db.Create(&n).Error; err != nil {
						t.Fatalf("create note: %v", err)
					}
					cardID := createStatsCard(t, db, n.ID, "forward")
					for i, rv := range tc.reviews {
						seedReview(t, db, cardID, "2026-10-02", rv.rating, rv.stateBefore, 5, 1000, "self", statsNow.Add(time.Duration(i)*time.Minute))
					}
					ctx := context.Background()
					st := NewStatsStore(db)

					ret, err := st.RetentionByStability(ctx, 1, 0)
					if err != nil {
						t.Fatalf("RetentionByStability() error = %v", err)
					}
					if ret.Total != tc.wantTotal || ret.Passed != tc.wantPass || ret.Rate != tc.wantRate {
						t.Errorf("RetentionByStability() = %d/%d/%v, want %d/%d/%v", ret.Total, ret.Passed, ret.Rate, tc.wantTotal, tc.wantPass, tc.wantRate)
					}

					decks, err := st.DeckBreakdown(ctx, 1, statsNow)
					if err != nil {
						t.Fatalf("DeckBreakdown() error = %v", err)
					}
					if len(decks) != 1 {
						t.Fatalf("DeckBreakdown() = %+v, want one row", decks)
					}
					if dr := decks[0]; dr.Reviews != tc.wantAll || dr.RetentionTotal != tc.wantTotal || dr.Passed != tc.wantPass || dr.Retention != tc.wantRate {
						t.Errorf("deck row = %+v, want reviews %d retention %d/%d rate %v", dr, tc.wantAll, tc.wantPass, tc.wantTotal, tc.wantRate)
					}

					tags, err := st.TagBreakdown(ctx, 1, "2026-01-01", "2026-12-31")
					if err != nil {
						t.Fatalf("TagBreakdown() error = %v", err)
					}
					if len(tags) != 1 {
						t.Fatalf("TagBreakdown() = %+v, want one row", tags)
					}
					if tr := tags[0]; tr.Reviews != tc.wantAll || tr.RetentionTotal != tc.wantTotal || tr.Passed != tc.wantPass || tr.Retention != tc.wantRate {
						t.Errorf("tag row = %+v, want reviews %d retention %d/%d rate %v", tr, tc.wantAll, tc.wantPass, tc.wantTotal, tc.wantRate)
					}
				})
			}
		})
	}
}

// TestResolveLearnAhead asserts the parsing of the learn-ahead window: unset (NULL) is off at the default 0, an explicit value takes effect as given,
// and out-of-range values clamp to [0, cap].
func TestResolveLearnAhead(t *testing.T) {
	ptr := func(v int) *int { return &v }
	cases := []struct {
		name string
		in   *int
		want time.Duration
	}{
		{"unset defaults to off", nil, 0},
		{"explicit zero stays off", ptr(0), 0},
		{"explicit value is used", ptr(20), 20 * time.Minute},
		{"negative clamps to zero", ptr(-5), 0},
		{"too large clamps to the maximum", ptr(MaxLearnAheadMinutes + 1), MaxLearnAheadMinutes * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveLearnAhead(tc.in); got != tc.want {
				t.Errorf("ResolveLearnAhead() = %v, want %v", got, tc.want)
			}
		})
	}
}
