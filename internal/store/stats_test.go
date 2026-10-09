package store

import (
	"context"
	"strconv"
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

// TestStatsReviewVolumeMatchesHandSQL 用独立 SQL 对拍复习量（第一条）。
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

// TestStatsDueForecastMatchesHandSQL 用独立 SQL 对拍到预测分桶。
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

// TestStatsRetentionByStabilityMatchesHandSQL 用独立 SQL 对拍留存率：
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

// TestStatsTimeSpentMatchesHandSQL 对拍时间投入的日均与中位数。
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

// TestStatsGradeSourceDistribution 对拍判分来源分布。
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

// TestStatsDeckBreakdownMatchesHandSQL 对拍卡组维度。
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
			db.Raw(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN r.rating <> 1 THEN 1 ELSE 0 END), 0), COALESCE(SUM(r.elapsed_ms), 0)
				FROM reviews r
				JOIN cards c ON c.id = r.card_id AND c.deleted_at IS NULL
				JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL
				WHERE r.user_id = ?
				  AND n.deck_id IN (SELECT id FROM decks WHERE owner_user_id = ? OR id IN (SELECT deck_id FROM deck_grants WHERE user_id = ?))`,
				1, 1, 1).Row().Scan(&wantReviews, &wantPassed, &wantElapsed)
			db.Raw(`SELECT COUNT(*) FROM cards c
				JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL
				LEFT JOIN card_states cs ON cs.card_id = c.id AND cs.user_id = ?
				WHERE c.deleted_at IS NULL AND cs.suspended_at IS NULL
				  AND n.deck_id IN (SELECT id FROM decks WHERE owner_user_id = ? OR id IN (SELECT deck_id FROM deck_grants WHERE user_id = ?))
				  AND (cs.card_id IS NULL OR cs.state = 'new' OR cs.due_at IS NULL OR cs.due_at <= ?)`,
				1, 1, 1, statsNow).Scan(&wantDue)
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

// TestStatsTagBreakdown 对拍标签维度：tags_json 在 Go 侧解码后聚合。
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

// seedStreakDays 建出只含指定复习日的最小数据集：每个 day 写一条复习日志，
// 用于连续打卡的日期分布断言（不复用 seedStatsFixture，避免它的日期干扰）。
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

// TestStatsStreakBoundaryAtCutoff 是验收边界：连续天数只在「整整一个复习日
// 被跳过」时中断。同一个挂钟日期 2026-10-04，03:30 仍算复习日 10-03（连续未断），
// 04:30 跨过 04:00 切点后 10-03 已被整天跳过（连续归零）。
func TestStatsStreakBoundaryAtCutoff(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			seedStreakDays(t, db, "2026-10-01", "2026-10-02")
			ctx := context.Background()

			// 切点本身：03:59:59 属前一天，04:00:00 属当天。
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

// TestStatsStreakCurrentAndLongest 断言当前/最长连续段：最长段取历史最长，
// 当前段允许「今天还没复习」而不算断（只有昨天整天被跳过才归零）。
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
				{time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC), 2, 3}, // 今天未复习，昨天 10-02 在，未断
				{time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), 0, 3}, // 10-03 整天被跳过
				{time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC), 3, 3}, // 09-25..27 正好当天
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

// TestStatsLearningCurveMatchesHandSQL 对拍学习曲线：每日新引入
// 是 state_before=New 的次数，其余算复习量。期望值全部硬编码，逐日断言两个数字。
func TestStatsLearningCurveMatchesHandSQL(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			fx := seedStatsFixture(t, db)
			// 补两条「新引入」：10-02 一张、10-03 一张。
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
				// 独立 SQL 对拍同一天的互斥分桶。
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

// seedForeignDeck 建一个属于 owner 的卡组，含 cards 张没有 card_states 行的新卡，
// 用来构造「别人的卡组」（对当前用户而言这些卡看起来就是没复习过的新卡）。
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

// TestStatsScopesToVisibleDecks 是卡组越权的回归用例（卡组维度与到期预测）。
//
// 缺陷形态：DeckBreakdown 的到期量与 DueForecast 的新卡计数都以 cards 为起点、只按
// user_id 左连接 card_states，没有任何卡组范围过滤——别人卡组里的卡对当前用户没有
// card_states 行（cs.card_id IS NULL），于是被整体算成「到期 / 新卡未到期」，再按
// deck_id 分组，别人的私有卡组连名字一起出现在我的统计页上。
//
// 口径＝该用户「可见」的卡组（自有 ∪ 被 deck_grants 授权，与卡组列表页、全库队列同一集合）：
// 别人的卡组一律不出现，无论它叫什么名字；「曾授权、现已撤销」的卡组同样不出现。
func TestStatsScopesToVisibleDecks(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			fx := seedStatsFixture(t, db)
			ctx := context.Background()

			foreignOne := seedForeignDeck(t, db, 2, "Other deck A", 2)
			foreignTwo := seedForeignDeck(t, db, 2, "Other deck B", 3)
			revoked := seedForeignDeck(t, db, 2, "Revoked grant", 2)

			// 用户 1 曾在 revoked 卡组里复习过（有状态行与复习日志），随后授权被撤销：
			// 该卡组当前不可见，统计页不该再出现它，也不该把它的卡算进到期预测。
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

			// 卡组维度：只有自己的卡组，且到期量正确。
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
			// 显式点名：别人的卡组一张都不能出现（缺陷的直接症状）。
			for _, row := range got {
				if row.DeckID == foreignOne || row.DeckID == foreignTwo {
					t.Errorf("DeckBreakdown() leaked another user's deck %d (%q)", row.DeckID, row.Name)
				}
			}
			// 到期预测：新卡未到期只数可见卡组（夹具自有 1 张）。
			due, err := NewStatsStore(db).DueForecast(ctx, 1, 0, statsNow, time.UTC, 4)
			if err != nil {
				t.Fatalf("DueForecast() error = %v", err)
			}
			if due.NewNotDue != 1 {
				t.Errorf("DueForecast().NewNotDue = %d, want 1 (own cards only)", due.NewNotDue)
			}
			// 撤销授权的卡组里用户 1 有两张已到期的状态行：不过滤时这里会变成 3。
			if due.Today != 1 {
				t.Errorf("DueForecast().Today = %d, want 1 (revoked-grant deck must not count)", due.Today)
			}
		})
	}
}

// seedTaggedDeck 建一个属于 owner 的卡组，含一张带单个标签的笔记与 cards 张卡，
// 供标签维度的可见性用例构造「他人卡组 / 已撤销授权」两种卡组。
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

// addUser1Reviews 在指定卡组内的每张卡上补一条用户 1 的复习（状态行 + 2026-10-02 的日志），
// 用来构造「撤销授权后仍有历史复习」的越权场景；want 是预期的卡数，卡数不符即夹具漂移。
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

// TestStatsTagBreakdownScopesToVisibleDecks 是标签维度越权的回归用例（标签维度）。
//
// 缺陷形态：TagBreakdown 以 reviews 为起点、JOIN notes 取 tags_json 在 Go 侧聚合。
// 标签是 note 级内容元数据，reviews.user_id 这道行级安全挡不住它：A 撤销对 B 的授权后，
// B 的统计页仍会列出原卡组的标签及其复习量/留存率（本仓真实缺陷）。
//
// 口径：标签维度与卡组维度同源，只聚合「当前可见卡组」的标签；其余 reviews 纯聚合
// （ReviewVolume 等）按刻意不对称的口径保持全史。本用例在同一个夹具里
// 用正向对照把这两条口径一起钉死，防止实现顺手给别的聚合也加上谓词。
func TestStatsTagBreakdownScopesToVisibleDecks(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			seedStatsFixture(t, db)
			ctx := context.Background()

			// 他人卡组：不可见，标签不得出现。
			otherDeck := seedTaggedDeck(t, db, 2, "Other deck", "shared", 1)
			// 另一个他人卡组：同样不可见（即使存在用户 1 的历史复习）。
			privateDeck := seedTaggedDeck(t, db, 2, "Other private", "leaked-private", 2)
			// 曾授权给用户 1、现已撤销的卡组：不可见，标签不得出现——缺陷的直接症状。
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
			// 只应出现自有卡组的标签：go、fsrs。别人的标签一律不出现。
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
			// 显式点名三个越权标签，失败信息直接指认缺陷。
			for _, leak := range []string{"shared", "leaked-private", "leaked-revoked"} {
				if _, ok := gotTags[leak]; ok {
					t.Errorf("TagBreakdown() leaked tag %q from a deck not visible to the user", leak)
				}
			}

			// 正向对照：ReviewVolume 是纯聚合，保持全史，撤销授权不得让它减少。
			// 夹具自有 10-02 两条 + 追加的 other 1、private 2、已撤销 2 = 今日 7；
			// 近 7 日加 10-01 一条 = 8；近 30 日再加 09-20 一条 = 9。
			// 若实现顺手给 ReviewVolume 也加了可见卡组谓词，这里会掉到 3/4/5。
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

// TestStatsTagBreakdownCountsOnlyReviewedNotes 把标签维度的口径钉死：
// 这个维度回答的是「哪块内容弱」，留存率要由复习记录算出来，所以它只统计**已复习卡片上**
// 的标签。同一个卡组里没有复习记录的标签不会出现——这是刻意口径，不是漏算；页面上也写了
// 这行说明，否则它会看起来像在重复卡组维度。
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
