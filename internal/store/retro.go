package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 本文件实现 ROADMAP.md M7-4「回溯校验」：把统计页展示的数字与直接从原始表
// （reviews / card_states / cards / notes）重算出来的数字逐一对照，任何漂移都被
// 指名道姓地报出来，而不是靠嘴争论。
//
// 与 stats_test.go 的关系：那些用例把聚合函数与「一条手工 SQL」对拍，但期望值仍写死在
// 测试里；回溯校验是运行期可复用的第二实现——它同样从原始表出发，但不经过 StatsStore
// 的任何聚合函数，因此能在真实库上重放（cmd 工具或 `go test -run Retro`）。
//
// 覆盖范围：复习量、到期量（含新卡未到期）、留存（总数/通过数）、时间投入、连续打卡、
// 学习曲线（新引入/复习合计）、卡组维度（复习数/到期数/累计耗时合计）。
// 未覆盖：标签维度与判分来源分布——它们的口径依赖 tags_json 解码与任意来源分组，
// 已由 stats_test.go 的手工 SQL 用例覆盖，此处不重复。

// PageNumbers 是统计页（M7-3）会直接展示的标量数字集合，每个统计指标取一个有代表性的
// 标量；列表型结果（分桶、每卡组、每标签）在这里取合计值，避免逐行比对而让校验脆弱。
type PageNumbers struct {
	ReviewToday int64
	Review7d    int64
	Review30d   int64

	DueToday     int64
	DueNewNotDue int64

	RetentionTotal  int64
	RetentionPassed int64

	TimeTotalMS int64
	TimeCount   int64

	StreakCurrent int
	StreakLongest int

	CurveNew    int64
	CurveReview int64

	DeckReviews   int64
	DeckDue       int64
	DeckElapsedMS int64
}

// PageNumbersFromStats 走统计页使用的同一条代码路径（StatsStore 的聚合函数）构造数字，
// 与 RecomputePageNumbers 的原始表重算互为对照。today/fromDay/toDay 是 review_day 格式
// 的闭区间，now/loc/cutoffHour 决定到期与连续打卡的日边界。
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
	decks, err := s.DeckBreakdown(ctx, userID, now)
	if err != nil {
		return PageNumbers{}, err
	}

	p := PageNumbers{
		ReviewToday:     vol.Today,
		Review7d:        vol.Last7Days,
		Review30d:       vol.Last30Days,
		DueToday:        due.Today,
		DueNewNotDue:    due.NewNotDue,
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
		p.DeckDue += d.DueCount
		p.DeckElapsedMS += d.ElapsedMS
	}
	return p, nil
}

// RecomputePageNumbers 只用原始表上的 SQL 重算同一组数字，刻意不调用 StatsStore：
// 它是独立第二实现，聚合函数一旦被改坏（或口径漂移）就会与这里的重算结果不一致。
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

	// 复习量：按 review_day 直接数行。
	if err := db.WithContext(ctx).Raw(`SELECT
		COALESCE(SUM(CASE WHEN review_day = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN review_day >= ? AND review_day <= ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN review_day >= ? AND review_day <= ? THEN 1 ELSE 0 END), 0)
		FROM reviews WHERE user_id = ?`,
		today, from7, today, from30, today, userID).
		Row().Scan(&p.ReviewToday, &p.Review7d, &p.Review30d); err != nil {
		return p, fmt.Errorf("retro check: review volume: %w", err)
	}

	// 到期与新增：日边界与队列一致（复习日起点 04:00，本地）。
	// 两条都以「可见卡组」为范围，与 StatsStore.DueForecast 同一谓词（visibleDeckIDsQuery）；
	// 少了它，别人 private 卡组里没有状态行的卡会被算成「新卡未到期」。
	e1 := reviewDayStart(now, loc, cutoffHour).Add(24 * time.Hour)
	if err := db.WithContext(ctx).Raw(`SELECT COUNT(*) FROM card_states cs
		JOIN cards c ON c.id = cs.card_id AND c.deleted_at IS NULL AND c.suspended_at IS NULL
		JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL
		WHERE cs.user_id = ? AND n.deck_id IN (?) AND cs.due_at IS NOT NULL AND cs.due_at <= ?`,
		userID, visibleDeckIDsQuery(db, userID), e1).
		Row().Scan(&p.DueToday); err != nil {
		return p, fmt.Errorf("retro check: due today: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT COUNT(c.id) FROM cards c
		JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL
		LEFT JOIN card_states cs ON cs.card_id = c.id AND cs.user_id = ?
		WHERE c.deleted_at IS NULL AND c.suspended_at IS NULL
		  AND n.deck_id IN (?)
		  AND (cs.card_id IS NULL OR (cs.state = 'new' AND (cs.due_at IS NULL OR cs.due_at > ?)))`,
		userID, visibleDeckIDsQuery(db, userID), now.UTC()).
		Row().Scan(&p.DueNewNotDue); err != nil {
		return p, fmt.Errorf("retro check: new not due: %w", err)
	}

	// 留存：到期复习（state_before=Review）里评分非 Again 的比例，只计 stability 非空的行。
	if err := db.WithContext(ctx).Raw(`SELECT COUNT(r.id),
		COALESCE(SUM(CASE WHEN r.rating <> 1 THEN 1 ELSE 0 END), 0)
		FROM reviews r
		JOIN cards c ON c.id = r.card_id AND c.deleted_at IS NULL
		JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL
		WHERE r.user_id = ? AND r.state_before = 2 AND r.stability IS NOT NULL`, userID).
		Row().Scan(&p.RetentionTotal, &p.RetentionPassed); err != nil {
		return p, fmt.Errorf("retro check: retention: %w", err)
	}

	// 时间投入：区间内 elapsed_ms 的总和与样本数。
	if err := db.WithContext(ctx).Raw(`SELECT COALESCE(SUM(elapsed_ms), 0), COUNT(elapsed_ms)
		FROM reviews WHERE user_id = ? AND elapsed_ms IS NOT NULL AND review_day >= ? AND review_day <= ?`,
		userID, fromDay, toDay).Row().Scan(&p.TimeTotalMS, &p.TimeCount); err != nil {
		return p, fmt.Errorf("retro check: time spent: %w", err)
	}

	// 学习曲线合计：新引入（state_before=New）与复习量互斥相加。
	if err := db.WithContext(ctx).Raw(`SELECT
		COALESCE(SUM(CASE WHEN state_before = 0 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN state_before <> 0 THEN 1 ELSE 0 END), 0)
		FROM reviews WHERE user_id = ? AND review_day >= ? AND review_day <= ?`,
		userID, fromDay, toDay).Row().Scan(&p.CurveNew, &p.CurveReview); err != nil {
		return p, fmt.Errorf("retro check: learning curve: %w", err)
	}

	// 卡组维度合计。两条同样以可见卡组为范围（与 StatsStore.DeckBreakdown 一致）。
	if err := db.WithContext(ctx).Raw(`SELECT COUNT(c.id) FROM cards c
		JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL
		LEFT JOIN card_states cs ON cs.card_id = c.id AND cs.user_id = ?
		WHERE c.deleted_at IS NULL AND c.suspended_at IS NULL
		  AND n.deck_id IN (?)
		  AND (cs.card_id IS NULL OR cs.state = 'new' OR cs.due_at IS NULL OR cs.due_at <= ?)`,
		userID, visibleDeckIDsQuery(db, userID), now.UTC()).
		Row().Scan(&p.DeckDue); err != nil {
		return p, fmt.Errorf("retro check: deck due: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT COUNT(r.id), COALESCE(SUM(r.elapsed_ms), 0)
		FROM reviews r
		JOIN cards c ON c.id = r.card_id AND c.deleted_at IS NULL
		JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL
		WHERE r.user_id = ? AND n.deck_id IN (?)`,
		userID, visibleDeckIDsQuery(db, userID)).
		Row().Scan(&p.DeckReviews, &p.DeckElapsedMS); err != nil {
		return p, fmt.Errorf("retro check: deck reviews: %w", err)
	}

	// 连续打卡：直接取不同复习日再跑同一套相邻判定。
	var days []string
	if err := db.WithContext(ctx).Raw(`SELECT DISTINCT review_day FROM reviews
		WHERE user_id = ? AND review_day <= ? ORDER BY review_day ASC`, userID, today).
		Scan(&days).Error; err != nil {
		return p, fmt.Errorf("retro check: streak days: %w", err)
	}
	p.StreakCurrent, p.StreakLongest = streakFromDays(days, today)
	return p, nil
}

// streakFromDays 与 StatsStore.Streak 的口径一致：相邻复习日才算连续；当前连续段只在
// 最后一天是今天或昨天时成立。抽成自由函数，让重算路径不依赖聚合方法。
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
		if i > 0 && d == nextReviewDay(days[i-1]) {
			run++
		} else {
			run = 1
		}
		if run > longest {
			longest = run
		}
	}
	last := days[len(days)-1]
	if last == today || last == prevReviewDay(today) {
		for d := last; present[d]; d = prevReviewDay(d) {
			current++
		}
	}
	return current, longest
}

// DiffPageNumbers 逐字段比较，返回漂移描述；一致时返回空切片。每一项都指名是哪个数字，
// 便于一眼看出是聚合坏了还是分组口径变了。
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
	add("due_forecast.new_not_due", page.DueNewNotDue, raw.DueNewNotDue)
	add("retention.total", page.RetentionTotal, raw.RetentionTotal)
	add("retention.passed", page.RetentionPassed, raw.RetentionPassed)
	add("time_spent.total_ms", page.TimeTotalMS, raw.TimeTotalMS)
	add("time_spent.count", page.TimeCount, raw.TimeCount)
	add("streak.current", page.StreakCurrent, raw.StreakCurrent)
	add("streak.longest", page.StreakLongest, raw.StreakLongest)
	add("learning_curve.new", page.CurveNew, raw.CurveNew)
	add("learning_curve.review", page.CurveReview, raw.CurveReview)
	add("deck_breakdown.reviews", page.DeckReviews, raw.DeckReviews)
	add("deck_breakdown.due", page.DeckDue, raw.DeckDue)
	add("deck_breakdown.elapsed_ms", page.DeckElapsedMS, raw.DeckElapsedMS)
	return drift
}

// RetroCheck 跑一遍完整回溯：页路径数字 vs 原始表重算。返回非空即表示统计页数字漂移。
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
