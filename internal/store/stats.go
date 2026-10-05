package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"
)

// 本文件实现 DESIGN.md §9「统计与洞察」的只读聚合查询。
//
// 为什么放在 internal/store 而不是新建 internal/stats 包：这些指标全部是对 reviews /
// card_states / cards / notes / decks 的纯聚合，口径与表结构强绑定；放进 store 可以直接
// 复用同一批模型与可见性规则（软删除、暂停卡排除），也符合 AGENTS.md §2.4「concrete Store
// 类型包住 GORM 访问」的既有约定。新建包只会多一层对 store 模型的转发，没有收益。
//
// 每个函数都接受调用方传入的 now / today / 日期窗口，绝不读取挂钟：页面（M7-3）与
// 手算对拍测试因此能在固定时间上复现同一组数字。

// StatsStore 提供 §9 的统计聚合；只读，不写任何表。
type StatsStore struct {
	db *gorm.DB
}

// NewStatsStore 构造统计存储。
func NewStatsStore(db *gorm.DB) *StatsStore { return &StatsStore{db: db} }

// ReviewVolume 是复习量指标：今日 / 近 7 日 / 近 30 日的 count(*)（DESIGN.md §9）。
// 窗口按 review_day（复习日字符串）闭区间计算，today 是调用方算好的复习日。
type ReviewVolume struct {
	Today      int64
	Last7Days  int64
	Last30Days int64
}

// ReviewVolume 按 review_day 聚合复习量。today 采用 review_day 的格式（YYYY-MM-DD），
// 近 7 日 = [today-6, today]，近 30 日 = [today-29, today]，与 §9 的「按 review_day 聚合」一致。
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

// DueForecast 是到期预测的分桶计数（DESIGN.md §9）：今日（含已过期）/明日/7 日内/30 日内/
// 更远，以及「新卡未到期」（尚无状态行、或状态为 new 但到期日未到）。
//
// 日边界取复习日起点：本地 (now - day_cutoff_hour) 所在日期的切点时刻，见 Design §3.3。
// 「今日」桶包含所有 due_at <= 今日结束 的卡，因此逾期的卡不会被漏掉。
type DueForecast struct {
	Today     int64
	Tomorrow  int64
	Within7   int64
	Within30  int64
	Later     int64
	NewNotDue int64
}

// DueForecast 按 card_states.due_at 分桶。deckID 为 0 时统计该用户可见的全部卡组。
//
// 卡组范围一律收在该用户「可见」的卡组内（自有 ∪ 被 deck_grants 授权 ∪ 他人 public，
// 与卡组列表页、队列的全库口径同一集合）。缺少这道过滤时，别人 private 卡组里的卡对当前
// 用户没有 card_states 行（cs.card_id IS NULL），会被整体算成「新卡未到期」——本仓真实缺陷。
func (s *StatsStore) DueForecast(ctx context.Context, userID, deckID uint64, now time.Time, loc *time.Location, cutoffHour int) (DueForecast, error) {
	if userID == 0 {
		return DueForecast{}, fmt.Errorf("due forecast: user id is required")
	}
	dayStart := reviewDayStart(now, loc, cutoffHour)
	e1 := dayStart.Add(24 * time.Hour) // 今日结束
	e2 := e1.Add(24 * time.Hour)       // 明日结束
	e7 := e1.Add(7 * 24 * time.Hour)   // 7 日结束
	e30 := e1.Add(30 * 24 * time.Hour) // 30 日结束

	visible := `card_states AS cs
		JOIN cards AS c ON c.id = cs.card_id AND c.deleted_at IS NULL AND c.suspended_at IS NULL
		JOIN notes AS n ON n.id = c.note_id AND n.deleted_at IS NULL`
	where := "cs.user_id = ? AND n.deck_id IN (?)"
	args := []any{userID, visibleDeckIDsQuery(s.db, userID)}
	if deckID != 0 {
		where += " AND n.deck_id = ?"
		args = append(args, deckID)
	}

	var row struct {
		Today    int64 `gorm:"column:b_today"`
		Tomorrow int64 `gorm:"column:b_tomorrow"`
		Within7  int64 `gorm:"column:b_within7"`
		Within30 int64 `gorm:"column:b_within30"`
		Later    int64 `gorm:"column:b_later"`
	}
	bucketSQL := fmt.Sprintf(`SELECT
		COALESCE(SUM(CASE WHEN cs.due_at IS NOT NULL AND cs.due_at <= ? THEN 1 ELSE 0 END), 0) AS b_today,
		COALESCE(SUM(CASE WHEN cs.due_at > ? AND cs.due_at <= ? THEN 1 ELSE 0 END), 0) AS b_tomorrow,
		COALESCE(SUM(CASE WHEN cs.due_at > ? AND cs.due_at <= ? THEN 1 ELSE 0 END), 0) AS b_within7,
		COALESCE(SUM(CASE WHEN cs.due_at > ? AND cs.due_at <= ? THEN 1 ELSE 0 END), 0) AS b_within30,
		COALESCE(SUM(CASE WHEN cs.due_at > ? THEN 1 ELSE 0 END), 0) AS b_later
		FROM %s WHERE %s`, visible, where)
	args = append([]any{e1, e1, e2, e2, e7, e7, e30, e30}, args...)
	if err := s.db.WithContext(ctx).Raw(bucketSQL, args...).Scan(&row).Error; err != nil {
		return DueForecast{}, fmt.Errorf("due forecast: bucket: %w", err)
	}

	// 新卡未到期：以 cards 为起点 LEFT JOIN，才能包含还没有 card_states 行的新卡。
	// 必须带可见卡组范围：不加的话别人 private 卡组里的卡（对当前用户没有状态行）会被
	// 全部数成「新卡未到期」。
	newSQL := fmt.Sprintf(`SELECT COALESCE(COUNT(c.id), 0) AS n FROM cards AS c
		JOIN notes AS n ON n.id = c.note_id AND n.deleted_at IS NULL
		LEFT JOIN card_states AS cs ON cs.card_id = c.id AND cs.user_id = ?
		WHERE c.deleted_at IS NULL AND c.suspended_at IS NULL
		  AND n.deck_id IN (?)
		  AND (cs.card_id IS NULL OR (cs.state = 'new' AND (cs.due_at IS NULL OR cs.due_at > ?)))`)
	newArgs := []any{userID, visibleDeckIDsQuery(s.db, userID), now.UTC()}
	if deckID != 0 {
		newSQL += " AND n.deck_id = ?"
		newArgs = append(newArgs, deckID)
	}
	var newRow struct {
		N int64 `gorm:"column:n"`
	}
	if err := s.db.WithContext(ctx).Raw(newSQL, newArgs...).Scan(&newRow).Error; err != nil {
		return DueForecast{}, fmt.Errorf("due forecast: new not due: %w", err)
	}

	return DueForecast{
		Today:     row.Today,
		Tomorrow:  row.Tomorrow,
		Within7:   row.Within7,
		Within30:  row.Within30,
		Later:     row.Later,
		NewNotDue: newRow.N,
	}, nil
}

// retentionBuckets 是留存率的分桶边界（天）：[0,1) [1,7) [7,30) [30,90) [90,180)
// [180,365) [365,+∞)。边界取稳定性（stability）值。
var retentionBuckets = []struct {
	Label string
	Low   float64
	High  float64 // 0 表示无上界
}{
	{"<1d", 0, 1},
	{"1-7d", 1, 7},
	{"7-30d", 7, 30},
	{"30-90d", 30, 90},
	{"90-180d", 90, 180},
	{"180-365d", 180, 365},
	{"365d+", 365, 0},
}

// RetentionBucket 是一个稳定性桶的留存情况；Passed 是评分不为 Again 的次数。
type RetentionBucket struct {
	Label  string
	Total  int64
	Passed int64
	// Rate 是 Passed/Total；Total 为 0 时定义为 0，避免出现 NaN。
	Rate float64
}

// RetentionStats 是留存率结果（DESIGN.md §9）：「到期时首次评分不是 Again」的比例，
// 按 stability 分桶。
type RetentionStats struct {
	Buckets []RetentionBucket
	Total   int64
	Passed  int64
	Rate    float64
}

// RetentionByStability 统计到期复习的留存率。
//
// 口径（§9）：只取 state_before = Review(2) 的日志 —— 那是「到期时」的一次复习；
// 判「记住」的条件是 rating != Again(1)。stability 取该次评分产生的新稳定性（reviews.stability），
// NULL 的旧行没有可用的分桶依据，排除。
func (s *StatsStore) RetentionByStability(ctx context.Context, userID, deckID uint64) (RetentionStats, error) {
	if userID == 0 {
		return RetentionStats{}, fmt.Errorf("retention: user id is required")
	}
	join := `reviews AS r
		JOIN cards AS c ON c.id = r.card_id AND c.deleted_at IS NULL
		JOIN notes AS n ON n.id = c.note_id AND n.deleted_at IS NULL`
	where := "r.user_id = ? AND r.state_before = 2 AND r.stability IS NOT NULL"
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
			fmt.Sprintf("COALESCE(SUM(CASE WHEN %s AND r.rating <> 1 THEN 1 ELSE 0 END), 0) AS p%d", cond, i))
	}
	var raw map[string]any
	sql := fmt.Sprintf("SELECT %s FROM %s WHERE %s", joinStrings(cols, ", "), join, where)
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

// stabilityRangeSQL 生成 stability 的区间条件；High 为 0 表示无上界。
func stabilityRangeSQL(low, high float64) string {
	cond := fmt.Sprintf("r.stability >= %g", low)
	if high > 0 {
		cond += fmt.Sprintf(" AND r.stability < %g", high)
	}
	return cond
}

// TimeSpent 是时间投入指标（DESIGN.md §9）：elapsed_ms 的总量、日均与中位数。
type TimeSpent struct {
	TotalMS int64
	Count   int64
	// AvgMS 是 TotalMS/Count；Count 为 0 时为 0。
	AvgMS float64
	// MedianMS 是下中位数（偶数个样本取第 (n-1)/2 个，升序）；Count 为 0 时为 0。
	MedianMS int64
}

// TimeSpent 统计 [fromDay, toDay] 复习日区间内 reviews.elapsed_ms 的日均与中位数。
// 中位数在 Go 侧计算：两库的 SQL 都没有可移植的 median，排序取中值反而更容易与手算对拍。
// elapsed_ms 为 NULL 的日志（旧数据或未计时）不计入，但会被如实排除在 Count 之外。
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

// DeckStat 是一个卡组的统计行（DESIGN.md §9「卡组维度」）。
type DeckStat struct {
	DeckID    uint64
	Name      string
	DueCount  int64
	Reviews   int64
	Passed    int64
	Retention float64
	ElapsedMS int64
}

// DeckBreakdown 返回该用户每个有关联数据的卡组的到期量、留存率与累计投入。
//
// 卡组范围取「该用户可见的卡组」中有关联数据的那些（可见性谓词与卡组列表页、队列同一份：
// 自有 ∪ 被 deck_grants 授权 ∪ 他人 public），因此共享卡组会出现，而与该用户无关的卡组
// （含别人的 private）不会。now 决定到期判定。
func (s *StatsStore) DeckBreakdown(ctx context.Context, userID uint64, now time.Time) ([]DeckStat, error) {
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

	// 到期量：新卡（无状态行或 state=new）与 due_at 已过的卡；暂停卡排除。
	// 必须带可见卡组范围：别人 private 卡组里的卡对当前用户没有 card_states 行
	// （cs.card_id IS NULL），不过滤就会被整体算成「到期」并按 deck_id 分组，
	// 卡组维度于是冒出别人的卡组（本仓真实缺陷）。
	var dueRows []struct {
		DeckID uint64 `gorm:"column:deck_id"`
		N      int64  `gorm:"column:n"`
	}
	dueSQL := `SELECT n.deck_id AS deck_id, COUNT(c.id) AS n FROM cards AS c
		JOIN notes AS n ON n.id = c.note_id AND n.deleted_at IS NULL
		LEFT JOIN card_states AS cs ON cs.card_id = c.id AND cs.user_id = ?
		WHERE c.deleted_at IS NULL AND c.suspended_at IS NULL
		  AND n.deck_id IN (?)
		  AND (cs.card_id IS NULL OR cs.state = 'new' OR cs.due_at IS NULL OR cs.due_at <= ?)
		GROUP BY n.deck_id`
	if err := s.db.WithContext(ctx).Raw(dueSQL, userID, deckScope, now.UTC()).Scan(&dueRows).Error; err != nil {
		return nil, fmt.Errorf("deck breakdown: due: %w", err)
	}
	for _, r := range dueRows {
		get(r.DeckID).DueCount = r.N
	}

	// 复习量、留存率、累计投入：按卡组分组聚合 reviews。同样收在可见卡组内：
	// 授权被撤销后，那些卡组的复习不应再出现在统计页的卡组维度里。
	var revRows []struct {
		DeckID    uint64 `gorm:"column:deck_id"`
		Reviews   int64  `gorm:"column:reviews"`
		Passed    int64  `gorm:"column:passed"`
		ElapsedMS *int64 `gorm:"column:elapsed_ms"`
	}
	revSQL := `SELECT n.deck_id AS deck_id, COUNT(r.id) AS reviews,
		COALESCE(SUM(CASE WHEN r.rating <> 1 THEN 1 ELSE 0 END), 0) AS passed,
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
		row.Passed = r.Passed
		if r.ElapsedMS != nil {
			row.ElapsedMS = *r.ElapsedMS
		}
		if row.Reviews > 0 {
			row.Retention = float64(row.Passed) / float64(row.Reviews)
		}
	}

	if len(stats) == 0 {
		return []DeckStat{}, nil
	}
	ids := make([]uint64, 0, len(stats))
	for id := range stats {
		ids = append(ids, id)
	}
	var decks []Deck
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&decks).Error; err != nil {
		return nil, fmt.Errorf("deck breakdown: deck names: %w", err)
	}
	for _, d := range decks {
		if row, ok := stats[d.ID]; ok {
			row.Name = d.Name
		}
	}
	out := make([]DeckStat, 0, len(stats))
	for _, row := range stats {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeckID < out[j].DeckID })
	return out, nil
}

// TagStat 是一个标签的统计行（DESIGN.md §9「标签维度」）。
type TagStat struct {
	Tag       string
	Reviews   int64
	Passed    int64
	Retention float64
}

// TagBreakdown 按 notes.tags_json 聚合复习量、留存率与「遗忘」（rating=Again）次数。
// tags_json 是 TEXT（双库兼容约定禁止 jsonb/array），SQL 无法跨库解析 JSON，
// 因此在 Go 侧解码后聚合；数据量是单个用户的复习日志，可接受。
func (s *StatsStore) TagBreakdown(ctx context.Context, userID uint64, fromDay, toDay string) ([]TagStat, error) {
	if userID == 0 {
		return nil, fmt.Errorf("tag breakdown: user id is required")
	}
	var rows []struct {
		Rating   int    `gorm:"column:rating"`
		TagsJSON string `gorm:"column:tags_json"`
	}
	sql := `SELECT r.rating AS rating, n.tags_json AS tags_json FROM reviews AS r
		JOIN cards AS c ON c.id = r.card_id AND c.deleted_at IS NULL
		JOIN notes AS n ON n.id = c.note_id AND n.deleted_at IS NULL
		WHERE r.user_id = ? AND r.review_day >= ? AND r.review_day <= ?`
	if err := s.db.WithContext(ctx).Raw(sql, userID, fromDay, toDay).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("tag breakdown: %w", err)
	}
	acc := map[string]*TagStat{}
	for _, row := range rows {
		var tags []string
		if err := json.Unmarshal([]byte(row.TagsJSON), &tags); err != nil {
			// 非法 JSON 不是统计的失败条件：跳过该行而不是让整个页面 500。
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
			if row.Rating != 1 {
				t.Passed++
			}
		}
	}
	out := make([]TagStat, 0, len(acc))
	for _, t := range acc {
		if t.Reviews > 0 {
			t.Retention = float64(t.Passed) / float64(t.Reviews)
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

// GradeSourceStat 是一种判分来源的计数（DESIGN.md §9「判分来源分布」）。
type GradeSourceStat struct {
	Source string
	Count  int64
}

// GradeSourceDistribution 按 grade_source 聚合，按次数倒序（self/typed/llm）。
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

// StreakStats 是连续打卡结果（DESIGN.md §9「连续打卡」）。
type StreakStats struct {
	// Current 是到「今天」为止仍未中断的连续复习天数；今天尚未复习不算断，
	// 但整整一个复习日被跳过（今天与昨天都没有复习）则为 0。
	Current int
	// Longest 是历史最长连续复习天数。
	Longest int
}

// Streak 计算连续复习天数。口径按 review_day（复习日字符串，已由调用方按切点算好）：
// 相邻两个复习日都出现过复习才算连续。判定「今天」时用 now 与切点现算，因此凌晨
// 03:59 的一次复习仍算前一天 —— 只有整整一个复习日被跳过，连续才会中断
// （AGENTS.md M7-2 验收）。now/loc/cutoffHour 由调用方传入，函数不读挂钟。
func (s *StatsStore) Streak(ctx context.Context, userID uint64, now time.Time, loc *time.Location, cutoffHour int) (StreakStats, error) {
	if userID == 0 {
		return StreakStats{}, fmt.Errorf("streak: user id is required")
	}
	today := reviewDayString(now, loc, cutoffHour)

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

	// 最长连续段：扫描升序日期，遇到不连续的日期就重开一段。
	longest, run := 0, 0
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

	// 当前连续段：从最后一天往回数，但仅当最后一天是今天或昨天时才算「未中断」。
	current := 0
	last := days[len(days)-1]
	if last == today || last == prevReviewDay(today) {
		for d := last; present[d]; d = prevReviewDay(d) {
			current++
		}
	}
	return StreakStats{Current: current, Longest: longest}, nil
}

// LearningCurvePoint 是学习曲线上的一个复习日（DESIGN.md §9「学习曲线」）。
type LearningCurvePoint struct {
	Day string
	// New 是当天「新引入」的卡数：state_before = New 的首次复习。
	New int64
	// Review 是当天对已见过卡（state_before != New）的复习次数。
	Review int64
}

// LearningCurve 返回 [fromDay, toDay] 内每个有复习记录的复习日的「新引入 vs 复习量」，
// 按日期升序。新引入口径是 state_before = New，其余算复习量，两者互斥、相加即当天总量。
// fromDay/toDay 是 review_day 格式的闭区间，与 §9 其余指标一致。
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

// reviewStateNew 是 reviews.state_before 的 New 取值（DESIGN.md §2.2：0=New）。
const reviewStateNew = 0

// reviewDayString 返回 now 所在复习日（YYYY-MM-DD）；口径与 reviewDayStart 一致，
// 即本地时间减切点小时取日期，默认切点 04:00。
func reviewDayString(now time.Time, loc *time.Location, cutoffHour int) string {
	return reviewDayStart(now, loc, cutoffHour).In(locOrUTC(loc)).Format("2006-01-02")
}

// ReviewDayString 导出复习日计算，供统计页（M7-3）等调用方复用同一口径。
// 页面必须用与聚合查询相同的日界，否则「今日复习量」会与库里的 review_day 对不上。
func ReviewDayString(now time.Time, loc *time.Location, cutoffHour int) string {
	return reviewDayString(now, loc, cutoffHour)
}

// prevReviewDay / nextReviewDay 是复习日字符串的相邻日运算。
func prevReviewDay(day string) string { return shiftReviewDay(day, -1) }
func nextReviewDay(day string) string { return shiftReviewDay(day, 1) }

func shiftReviewDay(day string, delta int) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	return t.AddDate(0, 0, delta).Format("2006-01-02")
}

// locOrUTC 归一化时区，避免 reviewDayStart 在 loc 为 nil 时回退 UTC、这里却用 nil。
func locOrUTC(loc *time.Location) *time.Location {
	if loc == nil {
		return time.UTC
	}
	return loc
}

// reviewDayStart 返回 now 所在复习日的起点（本地日期 + 切点小时），转成 UTC。
// 与 schedule.ReviewDay 的口径一致（本地时间减切点取日期）；store 不能 import schedule
// （schedule 依赖 store，会成环），因此在这里保留一份最小实现。
func reviewDayStart(now time.Time, loc *time.Location, cutoffHour int) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	if cutoffHour < 0 || cutoffHour > 23 {
		cutoffHour = 4
	}
	local := now.In(loc)
	day := local.Add(-time.Duration(cutoffHour) * time.Hour)
	return time.Date(day.Year(), day.Month(), day.Day(), cutoffHour, 0, 0, 0, loc).UTC()
}

// mapInt64 把 GORM map 扫描出来的聚合值转成 int64；驱动可能给出 int64 或其他数值类型。
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

// joinStrings 用 sep 拼接字符串片段；标准库 strings.Join 会多一个 import，
// 这里只需要固定用法，保持本文件依赖最小。
func joinStrings(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}
