package store

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"
)

// statsNow 是所有统计用例的固定观测时刻；固定它才能把分桶边界写成常量。
var statsNow = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

// statsFixture 保存 seedStatsFixture 建好的行 id，供断言引用。
type statsFixture struct {
	deckID uint64
	card1  uint64 // note1 ["go","fsrs"]，被复习的主力
	card2  uint64 // note2 ["go"]
}

// seedStatsFixture 建出统计用例的完整数据集。数值刻意手算：
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
	d := Deck{OwnerUserID: 1, Name: "Deck A", Visibility: DeckVisibilityPrivate, PresetID: p.ID, CreatedAt: statsNow}
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

	// 到期预测分桶用的卡：今日 / 明日 / 7 日内 / 30 日内 / 更远 / 新卡无状态行。
	setState(t, db, card1, "review", statsNow.Add(-2*time.Hour), 5)
	setState(t, db, card2, "review", time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), 40)
	b5 := createStatsCard(t, db, n1.ID, "b5")
	setState(t, db, b5, "review", time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC), 30)
	b6 := createStatsCard(t, db, n1.ID, "b6")
	setState(t, db, b6, "review", time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC), 20)
	b7 := createStatsCard(t, db, n1.ID, "b7")
	setState(t, db, b7, "review", time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC), 15)
	_ = createStatsCard(t, db, n1.ID, "b8") // 新卡：无 card_states 行

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

// seedReview 写一条复习日志；elapsedMS 传 -1 表示 NULL。
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

// TestStatsReviewVolumeMatchesHandSQL 用独立 SQL 对拍复习量（DESIGN.md §9 第一条）。
// 期望值同时对照固定数据集：今日 2、近 7 日 3、近 30 日 4。
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

// TestStatsDueForecastMatchesHandSQL 用独立 SQL 对拍到预测分桶（DESIGN.md §9）。
// 日边界取复习日起点 04:00 UTC：今日结束 10-03 04:00、明日结束 10-04 04:00、
// 7 日结束 10-09 04:00、30 日结束 11-01 04:00。
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
			join := "FROM card_states cs JOIN cards c ON c.id = cs.card_id AND c.deleted_at IS NULL AND c.suspended_at IS NULL JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL WHERE cs.user_id = ?"
			wantToday := hand("SELECT COUNT(*) "+join+" AND cs.due_at <= ?", 1, edges[0])
			wantTomorrow := hand("SELECT COUNT(*) "+join+" AND cs.due_at > ? AND cs.due_at <= ?", 1, edges[0], edges[1])
			want7 := hand("SELECT COUNT(*) "+join+" AND cs.due_at > ? AND cs.due_at <= ?", 1, edges[1], edges[2])
			want30 := hand("SELECT COUNT(*) "+join+" AND cs.due_at > ? AND cs.due_at <= ?", 1, edges[2], edges[3])
			wantLater := hand("SELECT COUNT(*) "+join+" AND cs.due_at > ?", 1, edges[3])
			wantNew := hand(`SELECT COUNT(*) FROM cards c JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL LEFT JOIN card_states cs ON cs.card_id = c.id AND cs.user_id = ? WHERE c.deleted_at IS NULL AND c.suspended_at IS NULL AND (cs.card_id IS NULL OR (cs.state = 'new' AND (cs.due_at IS NULL OR cs.due_at > ?)))`, 1, statsNow)

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

// TestStatsRetentionByStabilityMatchesHandSQL 用独立 SQL 对拍留存率（DESIGN.md §9）：
// 到期复习（state_before=Review）中「评分不是 Again」的比例，按 stability 分桶。
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

// TestStatsTimeSpentMatchesHandSQL 对拍时间投入的日均与中位数（DESIGN.md §9）。
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
			// 升序 [500,1000,2000,3000] 的下中位数是 1000。
			if got.MedianMS != 1000 {
				t.Errorf("MedianMS = %d, want 1000", got.MedianMS)
			}
		})
	}
}

// TestStatsGradeSourceDistribution 对拍判分来源分布（DESIGN.md §9）。
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

// TestStatsDeckBreakdownMatchesHandSQL 对拍卡组维度（DESIGN.md §9）。
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
			var wantReviews, wantPassed, wantDue, wantElapsed int64
			db.Raw("SELECT COUNT(*), COALESCE(SUM(CASE WHEN rating <> 1 THEN 1 ELSE 0 END), 0), COALESCE(SUM(elapsed_ms), 0) FROM reviews WHERE user_id = ?", 1).Row().Scan(&wantReviews, &wantPassed, &wantElapsed)
			db.Raw(`SELECT COUNT(*) FROM cards c JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL LEFT JOIN card_states cs ON cs.card_id = c.id AND cs.user_id = ? WHERE c.deleted_at IS NULL AND c.suspended_at IS NULL AND (cs.card_id IS NULL OR cs.state = 'new' OR cs.due_at IS NULL OR cs.due_at <= ?)`, 1, statsNow).Scan(&wantDue)
			row := got[0]
			if row.Reviews != wantReviews || row.Passed != wantPassed || row.ElapsedMS != wantElapsed || row.DueCount != wantDue {
				t.Errorf("DeckBreakdown() = %+v, hand SQL = reviews %d passed %d elapsed %d due %d", row, wantReviews, wantPassed, wantElapsed, wantDue)
			}
			if wantReviews != 5 || wantPassed != 4 || wantDue != 2 {
				t.Errorf("fixture drifted: hand reviews/passed/due = %d/%d/%d, want 5/4/2", wantReviews, wantPassed, wantDue)
			}
		})
	}
}

// TestStatsTagBreakdown 对拍标签维度（DESIGN.md §9）：tags_json 在 Go 侧解码后聚合。
func TestStatsTagBreakdown(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			seedStatsFixture(t, db)
			got, err := NewStatsStore(db).TagBreakdown(context.Background(), 1, "2026-01-01", "2026-12-31")
			if err != nil {
				t.Fatalf("TagBreakdown() error = %v", err)
			}
			want := map[string]TagStat{
				"go":   {Tag: "go", Reviews: 5, Passed: 4, Retention: 0.8},
				"fsrs": {Tag: "fsrs", Reviews: 3, Passed: 2, Retention: 2.0 / 3.0},
			}
			if len(got) != len(want) {
				t.Fatalf("got %d tags %+v, want %d", len(got), got, len(want))
			}
			for _, row := range got {
				w := want[row.Tag]
				if row.Reviews != w.Reviews || row.Passed != w.Passed {
					t.Errorf("tag %s = %+v, want reviews %d passed %d", row.Tag, row, w.Reviews, w.Passed)
				}
				if diff := row.Retention - w.Retention; diff > 1e-9 || diff < -1e-9 {
					t.Errorf("tag %s retention = %v, want %v", row.Tag, row.Retention, w.Retention)
				}
			}
		})
	}
}
