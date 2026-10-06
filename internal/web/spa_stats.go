package web

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件实现 SPA 统计明细接口（GET /api/v1/stats/detail，DESIGN.md §8.1、§9）。
//
// 它与 SSR 统计页（internal/web/stats.go 的 statsData）共用 collectStatsMeasures：
// 复习日、时区、日切点、可见卡组范围与每个聚合函数都与页面逐项相同，因此「迁移到 SPA」
// 不会改变任何统计定义。差别只在呈现：SSR 直接拼好语言包文案与 CSS 柱宽，这里返回原始
// 计数、比例与毫秒，本地化与柱宽交给前端。
//
// 字段名与数值口径一经发布即稳定，供 SPA 与后续客户端复用；不返回任何语言包文案。

// statsMeasures 是一次统计请求的原始聚合结果；Now 是所有指标共用的唯一时刻快照。
type statsMeasures struct {
	Now       time.Time
	Volume    store.ReviewVolume
	Due       store.DueForecast
	Retention store.RetentionStats
	TimeSpent store.TimeSpent
	Streak    store.StreakStats
	Curve     []store.LearningCurvePoint
	Decks     []store.DeckStat
	Tags      []store.TagStat
	Grades    []store.GradeSourceStat
}

// collectStatsMeasures 调用 §9 的全部既有聚合查询（M7-1/M7-2）；handler 不写任何统计 SQL。
//
// now 只取一次：所有指标共用同一个时刻与同一个复习日窗口（近 30 日 = [today-29, today]，
// DESIGN.md §9）。SSR 统计页与 SPA 明细接口都从这里取数，两边因此不会对同一请求给出不同
// 口径的数字，也不会各自复制一份编排逻辑。
func (s *Server) collectStatsMeasures(ctx context.Context, user *store.User) (statsMeasures, error) {
	now := time.Now()
	locTZ := userLocation(user)
	cutoff := store.ResolveCutoff(user.DayCutoffHour)
	today := store.ReviewDayString(now, locTZ, cutoff)
	from30 := store.ShiftReviewDay(today, -29)

	stats := store.NewStatsStore(s.db)

	volume, err := stats.ReviewVolume(ctx, user.ID, today)
	if err != nil {
		return statsMeasures{}, err
	}
	due, err := stats.DueForecast(ctx, user.ID, 0, now, locTZ, cutoff)
	if err != nil {
		return statsMeasures{}, err
	}
	retention, err := stats.RetentionByStability(ctx, user.ID, 0)
	if err != nil {
		return statsMeasures{}, err
	}
	timeSpent, err := stats.TimeSpent(ctx, user.ID, from30, today)
	if err != nil {
		return statsMeasures{}, err
	}
	streak, err := stats.Streak(ctx, user.ID, now, locTZ, cutoff)
	if err != nil {
		return statsMeasures{}, err
	}
	curve, err := stats.LearningCurve(ctx, user.ID, from30, today)
	if err != nil {
		return statsMeasures{}, err
	}
	decks, err := stats.DeckBreakdown(ctx, user.ID, now)
	if err != nil {
		return statsMeasures{}, err
	}
	tags, err := stats.TagBreakdown(ctx, user.ID, from30, today)
	if err != nil {
		return statsMeasures{}, err
	}
	grades, err := stats.GradeSourceDistribution(ctx, user.ID)
	if err != nil {
		return statsMeasures{}, err
	}

	return statsMeasures{
		Now:       now,
		Volume:    volume,
		Due:       due,
		Retention: retention,
		TimeSpent: timeSpent,
		Streak:    streak,
		Curve:     curve,
		Decks:     decks,
		Tags:      tags,
		Grades:    grades,
	}, nil
}

// spaStatsDetail 是 GET /api/v1/stats/detail 的响应体。
type spaStatsDetail struct {
	// GeneratedAt 是所有指标共用的 now 快照（RFC3339，UTC）。
	GeneratedAt string `json:"generated_at"`
	// Empty 与 SSR 统计页同一判据：窗口内毫无复习、无到期卡、无卡组数据。
	Empty     bool                  `json:"empty"`
	Volume    spaStatsVolume        `json:"volume"`
	Due       spaStatsDue           `json:"due"`
	Retention spaStatsRetention     `json:"retention"`
	TimeSpent spaStatsTimeSpent     `json:"time_spent"`
	Streak    spaStatsStreak        `json:"streak"`
	Curve     []spaStatsCurvePoint  `json:"curve"`
	Decks     []spaStatsDeck        `json:"decks"`
	Tags      []spaStatsTag         `json:"tags"`
	Grades    []spaStatsGradeSource `json:"grades"`
}

// spaStatsVolume 是复习量指标（今日 / 近 7 日 / 近 30 日）。
type spaStatsVolume struct {
	Today      int64 `json:"today"`
	Last7Days  int64 `json:"last_7_days"`
	Last30Days int64 `json:"last_30_days"`
}

// spaStatsDue 是到期预测的互斥分桶；NewNotDue 是新卡未排期（DESIGN.md §9）。
type spaStatsDue struct {
	Today     int64 `json:"today"`
	Tomorrow  int64 `json:"tomorrow"`
	Within7   int64 `json:"within_7_days"`
	Within30  int64 `json:"within_30_days"`
	Later     int64 `json:"later"`
	NewNotDue int64 `json:"new_not_due"`
}

// spaStatsRetentionBucket 是一个稳定性桶的留存情况；Rate 是 Passed/Total（Total 为 0 时为 0）。
type spaStatsRetentionBucket struct {
	Label  string  `json:"label"`
	Total  int64   `json:"total"`
	Passed int64   `json:"passed"`
	Rate   float64 `json:"rate"`
}

// spaStatsRetention 是留存率总体值加每个稳定性桶。
type spaStatsRetention struct {
	Total   int64                     `json:"total"`
	Passed  int64                     `json:"passed"`
	Rate    float64                   `json:"rate"`
	Buckets []spaStatsRetentionBucket `json:"buckets"`
}

// spaStatsTimeSpent 是时间投入指标（毫秒）。
type spaStatsTimeSpent struct {
	TotalMS  int64   `json:"total_ms"`
	Count    int64   `json:"count"`
	AvgMS    float64 `json:"avg_ms"`
	MedianMS int64   `json:"median_ms"`
}

// spaStatsStreak 是连续打卡天数。
type spaStatsStreak struct {
	Current int `json:"current"`
	Longest int `json:"longest"`
}

// spaStatsCurvePoint 是学习曲线上的一个复习日：New 为新引入，Review 为复习量。
type spaStatsCurvePoint struct {
	Day    string `json:"day"`
	New    int64  `json:"new"`
	Review int64  `json:"review"`
}

// spaStatsDeck 是一个卡组的统计行。
type spaStatsDeck struct {
	DeckID    uint64  `json:"deck_id"`
	Name      string  `json:"name"`
	DueCount  int64   `json:"due_count"`
	Reviews   int64   `json:"reviews"`
	Retention float64 `json:"retention"`
	ElapsedMS int64   `json:"elapsed_ms"`
}

// spaStatsTag 是一个标签的统计行。
type spaStatsTag struct {
	Tag       string  `json:"tag"`
	Reviews   int64   `json:"reviews"`
	Retention float64 `json:"retention"`
}

// spaStatsGradeSource 是一种判分来源的计数（self/typed/llm）。
type spaStatsGradeSource struct {
	Source string `json:"source"`
	Count  int64  `json:"count"`
}

// spaStatsDetail 是会话专用的只读接口，只接受浏览器会话，不接受 bearer / API Key。
//
// 挂在 /api/v1 之外（见 registerStatsRoutes），因此不受 API Key 组中间件影响；这里显式
// 要求活跃会话，未登录一律 401，与 SSR 页面的「重定向到登录页」在语义上一致（JSON 客户端
// 不该收到 HTML 重定向）。GET 是安全方法，无需 CSRF。
func (s *Server) spaStatsDetail(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok || user.Status != store.StatusActive {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{
			"code": api.CodeUnauthorized, "message": api.ErrorMessage(c.Request.Context(), api.CodeUnauthorized),
		}})
		return
	}
	detail, err := s.buildSPAStatsDetail(c.Request.Context(), user)
	if err != nil {
		s.logger.Error("build SPA stats detail failed", "user_id", user.ID, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": api.CodeInternal, "message": api.ErrorMessage(c.Request.Context(), api.CodeInternal),
		}})
		return
	}
	c.JSON(http.StatusOK, detail)
}

// buildSPAStatsDetail 把原始聚合结果映射成稳定的 JSON 形态；切片一律初始化为非 nil，
// 让空结果序列化成 [] 而不是 null，前端无需为 null 单独分支。
func (s *Server) buildSPAStatsDetail(ctx context.Context, user *store.User) (spaStatsDetail, error) {
	m, err := s.collectStatsMeasures(ctx, user)
	if err != nil {
		return spaStatsDetail{}, err
	}
	detail := spaStatsDetail{
		GeneratedAt: m.Now.UTC().Format(time.RFC3339),
		// 与 statsData 完全相同的空态判据：没有任何复习量、没有今日到期、没有卡组数据。
		Empty: m.Volume.Today == 0 && m.Volume.Last30Days == 0 &&
			m.Due.Today == 0 && m.Due.NewNotDue == 0 && len(m.Decks) == 0,
		Volume: spaStatsVolume{
			Today:      m.Volume.Today,
			Last7Days:  m.Volume.Last7Days,
			Last30Days: m.Volume.Last30Days,
		},
		Due: spaStatsDue{
			Today:     m.Due.Today,
			Tomorrow:  m.Due.Tomorrow,
			Within7:   m.Due.Within7,
			Within30:  m.Due.Within30,
			Later:     m.Due.Later,
			NewNotDue: m.Due.NewNotDue,
		},
		Retention: spaStatsRetention{
			Total:   m.Retention.Total,
			Passed:  m.Retention.Passed,
			Rate:    m.Retention.Rate,
			Buckets: make([]spaStatsRetentionBucket, 0, len(m.Retention.Buckets)),
		},
		TimeSpent: spaStatsTimeSpent{
			TotalMS:  m.TimeSpent.TotalMS,
			Count:    m.TimeSpent.Count,
			AvgMS:    m.TimeSpent.AvgMS,
			MedianMS: m.TimeSpent.MedianMS,
		},
		Streak: spaStatsStreak{Current: m.Streak.Current, Longest: m.Streak.Longest},
		Curve:  make([]spaStatsCurvePoint, 0, len(m.Curve)),
		Decks:  make([]spaStatsDeck, 0, len(m.Decks)),
		Tags:   make([]spaStatsTag, 0, len(m.Tags)),
		Grades: make([]spaStatsGradeSource, 0, len(m.Grades)),
	}
	for _, b := range m.Retention.Buckets {
		detail.Retention.Buckets = append(detail.Retention.Buckets, spaStatsRetentionBucket{
			Label: b.Label, Total: b.Total, Passed: b.Passed, Rate: b.Rate,
		})
	}
	for _, p := range m.Curve {
		detail.Curve = append(detail.Curve, spaStatsCurvePoint{Day: p.Day, New: p.New, Review: p.Review})
	}
	for _, d := range m.Decks {
		detail.Decks = append(detail.Decks, spaStatsDeck{
			DeckID: d.DeckID, Name: d.Name, DueCount: d.DueCount,
			Reviews: d.Reviews, Retention: d.Retention, ElapsedMS: d.ElapsedMS,
		})
	}
	for _, tg := range m.Tags {
		detail.Tags = append(detail.Tags, spaStatsTag{Tag: tg.Tag, Reviews: tg.Reviews, Retention: tg.Retention})
	}
	for _, g := range m.Grades {
		detail.Grades = append(detail.Grades, spaStatsGradeSource{Source: g.Source, Count: g.Count})
	}
	return detail, nil
}
