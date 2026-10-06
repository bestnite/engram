package web

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// registerStatsRoutes 挂载统计页（M7-3，DESIGN.md §8.1、§9）。
//
// 页面上的每个数字都来自 internal/store 的既有聚合查询（M7-1/M7-2）；handler 不写任何
// 统计 SQL。原因：M7-4 的回溯校验（store.RetroCheck）对照的正是这批查询，一旦 handler
// 另写一套 SQL，页面数字就脱离了可校验的路径。
//
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerStatsRoutes(router *gin.Engine) {
	if s.sessions == nil {
		return
	}
	// GET /stats 已切到 SPA 规范路径：statsRoute 返回应用壳，由客户端路由渲染统计页。
	// 统计页没有任何写操作，因此这里只动这一个 GET 路由，其余路由（含 SPA 明细接口）不变。
	router.GET("/stats", s.statsRoute)
	// SPA 统计明细接口：只读、只接受浏览器会话（不走 /api/v1 的 API Key 组），
	// 与 /stats 共用同一批 store 聚合，口径不会分叉（DESIGN.md §8.1、§9）。
	router.GET("/api/v1/stats/detail", s.spaStatsDetail)
}

// statsRoute 提供 GET /stats：SPA 已加载时返回应用壳（DESIGN.md §8.5），由客户端路由
// 渲染统计页，数据仍走同一批 store 聚合的 GET /api/v1/stats/detail（DESIGN.md §8.1、§9）。
//
// 两条路径都先要求已登录会话，与迁移前的 SSR 页面一致：未登录一律重定向到登录页，
// 页面迁移不改动授权判定，也不新增任何写路径。SPA 缺失（降级）时回退 SSR 统计页。
func (s *Server) statsRoute(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	if s.spa != nil {
		s.spa.ServeIndex(c)
		return
	}
	s.statsPage(c)
}

// statsPage 渲染统计页；匿名访问被重定向到登录页。
func (s *Server) statsPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	data, err := s.statsData(c, loc, user)
	if err != nil {
		s.logger.Error("build stats page failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := views.StatsPage(data).Render(c.Request.Context(), c.Writer); err != nil {
		s.logger.Error("render template failed", "error", err, "path", c.Request.URL.Path)
	}
}

// statsData 调用 StatsStore 的聚合查询并把结果排版成渲染数据。
//
// 取数交给 collectStatsMeasures：now 只取一次，所有指标共用同一个时刻与同一个复习日窗口，
// 页面内部不会自相矛盾。日期窗口是 review_day 格式的闭区间：近 30 日 = [today-29, today]
// （DESIGN.md §9）。SPA 明细接口复用同一份编排，两个界面的口径因此逐项一致。
func (s *Server) statsData(c *gin.Context, loc *i18n.Localizer, user *store.User) (views.StatsData, error) {
	m, err := s.collectStatsMeasures(c.Request.Context(), user)
	if err != nil {
		return views.StatsData{}, err
	}
	volume, due, retention, timeSpent := m.Volume, m.Due, m.Retention, m.TimeSpent
	streak, curve, decks, tags, grades := m.Streak, m.Curve, m.Decks, m.Tags, m.Grades

	data := views.StatsData{
		Layout:           s.pageLayout(c, loc, "stats.title"),
		Heading:          loc.T("stats.heading"),
		VolumeHeading:    loc.T("stats.volume.heading"),
		DueHeading:       loc.T("stats.due.heading"),
		RetentionHeading: loc.T("stats.retention.heading"),
		RetentionIntro:   loc.T("stats.retention.intro"),
		TimeHeading:      loc.T("stats.time.heading"),
		StreakHeading:    loc.T("stats.streak.heading"),
		GradeHeading:     loc.T("stats.grade.heading"),
		CurveHeading:     loc.T("stats.curve.heading"),
		CurveIntro:       loc.T("stats.curve.intro"),
		CurveNewLabel:    loc.T("stats.curve.new"),
		CurveReviewLabel: loc.T("stats.curve.review"),
		CurveEmpty:       loc.T("stats.curve.empty"),
		DeckHeading:      loc.T("stats.deck.heading"),
		DeckEmpty:        loc.T("stats.deck.empty"),
		DeckColumns: []string{
			loc.T("stats.deck.col.name"),
			loc.T("stats.deck.col.due"),
			loc.T("stats.deck.col.reviews"),
			loc.T("stats.deck.col.retention"),
			loc.T("stats.deck.col.elapsed"),
		},
		TagHeading: loc.T("stats.tag.heading"),
		TagEmpty:   loc.T("stats.tag.empty"),
		TagColumns: []string{
			loc.T("stats.tag.col.tag"),
			loc.T("stats.tag.col.reviews"),
			loc.T("stats.tag.col.retention"),
		},
	}

	// 复习量：三个窗口共用最大值做柱宽基准，柱长因此可以直接互相比较。
	volumeMax := maxInt64(volume.Today, volume.Last7Days, volume.Last30Days)
	data.VolumeRows = []views.StatsRow{
		scalarRow(loc, "stats.volume.today", "stats.volume.value", volume.Today, volumeMax),
		scalarRow(loc, "stats.volume.last7", "stats.volume.value", volume.Last7Days, volumeMax),
		scalarRow(loc, "stats.volume.last30", "stats.volume.value", volume.Last30Days, volumeMax),
	}

	// 到期预测：各桶互斥（DESIGN.md §9），新卡未排期单独一桶。
	dueMax := maxInt64(due.Today, due.Tomorrow, due.Within7, due.Within30, due.Later, due.NewNotDue)
	data.DueRows = []views.StatsRow{
		scalarRow(loc, "stats.due.today", "stats.due.value", due.Today, dueMax),
		scalarRow(loc, "stats.due.tomorrow", "stats.due.value", due.Tomorrow, dueMax),
		scalarRow(loc, "stats.due.within7", "stats.due.value", due.Within7, dueMax),
		scalarRow(loc, "stats.due.within30", "stats.due.value", due.Within30, dueMax),
		scalarRow(loc, "stats.due.later", "stats.due.value", due.Later, dueMax),
		scalarRow(loc, "stats.due.new_not_due", "stats.due.value", due.NewNotDue, dueMax),
	}

	// 留存率：总体一行 + 每个稳定性桶一行，柱宽直接是比例。
	data.RetentionRows = append(data.RetentionRows, views.StatsRow{
		Label:    loc.T("stats.retention.overall"),
		Value:    loc.Tf("stats.retention.rate", retentionRateData(retention.Passed, retention.Total)),
		WidthCSS: percentWidth(retention.Rate),
	})
	for _, b := range retention.Buckets {
		data.RetentionRows = append(data.RetentionRows, views.StatsRow{
			Label:    loc.T(retentionBucketKey(b.Label)),
			Value:    loc.Tf("stats.retention.rate", retentionRateData(b.Passed, b.Total)),
			WidthCSS: percentWidth(b.Rate),
		})
	}

	// 时间投入：累计/平均/中位/样本数。耗时单位随量级走（elapsedLabel，DESIGN.md §9）。
	data.TimeRows = []views.StatsRow{
		{Label: loc.T("stats.time.total_label"), Value: elapsedLabel(loc, timeSpent.TotalMS)},
		{Label: loc.T("stats.time.avg_label"), Value: elapsedLabel(loc, int64(timeSpent.AvgMS))},
		{Label: loc.T("stats.time.median_label"), Value: elapsedLabel(loc, timeSpent.MedianMS)},
		{Label: loc.T("stats.time.count_label"), Value: loc.Tf("stats.time.count", map[string]any{"count": timeSpent.Count})},
	}

	// 连续打卡。
	data.StreakRows = []views.StatsRow{
		{Label: loc.T("stats.streak.current_label"), Value: loc.Tf("stats.streak.current", map[string]any{"days": streak.Current})},
		{Label: loc.T("stats.streak.longest_label"), Value: loc.Tf("stats.streak.longest", map[string]any{"days": streak.Longest})},
	}

	// 判分来源分布。
	var gradeMax int64
	for _, g := range grades {
		if g.Count > gradeMax {
			gradeMax = g.Count
		}
	}
	for _, g := range grades {
		data.GradeRows = append(data.GradeRows, views.StatsRow{
			Label:    loc.T(gradeSourceKey(g.Source)),
			Value:    loc.Tf("stats.grade.value", map[string]any{"count": g.Count}),
			WidthCSS: barWidth(g.Count, gradeMax),
		})
	}

	// 学习曲线：新引入与复习量共用最大值，两条柱可直接比较。
	var curveMax int64
	for _, p := range curve {
		if p.New > curveMax {
			curveMax = p.New
		}
		if p.Review > curveMax {
			curveMax = p.Review
		}
	}
	for _, p := range curve {
		data.CurveRows = append(data.CurveRows, views.StatsCurveRow{
			Day:            p.Day,
			NewValue:       loc.Tf("stats.curve.value", map[string]any{"count": p.New}),
			NewWidthCSS:    barWidth(p.New, curveMax),
			ReviewValue:    loc.Tf("stats.curve.value", map[string]any{"count": p.Review}),
			ReviewWidthCSS: barWidth(p.Review, curveMax),
		})
	}

	// 卡组维度与标签维度。
	for _, d := range decks {
		data.DeckRows = append(data.DeckRows, []string{
			d.Name,
			loc.Tf("stats.due.value", map[string]any{"count": d.DueCount}),
			loc.Tf("stats.volume.value", map[string]any{"count": d.Reviews}),
			loc.Tf("stats.table.rate", map[string]any{"rate": store.FormatPercent(d.Retention)}),
			elapsedLabel(loc, d.ElapsedMS),
		})
	}
	for _, t := range tags {
		data.TagRows = append(data.TagRows, []string{
			t.Tag,
			loc.Tf("stats.volume.value", map[string]any{"count": t.Reviews}),
			loc.Tf("stats.table.rate", map[string]any{"rate": store.FormatPercent(t.Retention)}),
		})
	}

	// 完全没有数据时只显示一句话，而不是一排全 0 的柱状条。
	if volume.Today == 0 && volume.Last30Days == 0 &&
		due.Today == 0 && due.NewNotDue == 0 && len(decks) == 0 {
		data.Empty = loc.T("stats.empty")
	}
	return data, nil
}

// scalarRow 组装一行「本地化标签 + 本地化数值 + 柱宽」。
func scalarRow(loc *i18n.Localizer, labelKey, valueKey string, value, max int64) views.StatsRow {
	return views.StatsRow{
		Label:    loc.T(labelKey),
		Value:    loc.Tf(valueKey, map[string]any{"count": value}),
		WidthCSS: barWidth(value, max),
	}
}

// retentionRateData 构造留存率的模板数据；Total 为 0 时比例定义为 0（不出现 NaN）。
func retentionRateData(passed, total int64) map[string]any {
	rate := 0.0
	if total > 0 {
		rate = float64(passed) / float64(total)
	}
	return map[string]any{"rate": store.FormatPercent(rate), "passed": passed, "total": total}
}

// retentionBucketKey 把 store 的稳定性桶标签映射到语言包 key。
// 未知标签回退到「更远」之外的中性 key，避免页面出现裸标识符。
func retentionBucketKey(label string) string {
	switch label {
	case "<1d":
		return "stats.retention.bucket.lt1"
	case "1-7d":
		return "stats.retention.bucket.d1_7"
	case "7-30d":
		return "stats.retention.bucket.d7_30"
	case "30-90d":
		return "stats.retention.bucket.d30_90"
	case "90-180d":
		return "stats.retention.bucket.d90_180"
	case "180-365d":
		return "stats.retention.bucket.d180_365"
	case "365d+":
		return "stats.retention.bucket.d365plus"
	default:
		return "stats.retention.overall"
	}
}

// gradeSourceKey 把 reviews.grade_source 映射到语言包 key；未知来源显示「其它」。
func gradeSourceKey(source string) string {
	switch source {
	case "self":
		return "stats.grade.self"
	case "typed":
		return "stats.grade.typed"
	case "llm":
		return "stats.grade.llm"
	default:
		return "stats.grade.other"
	}
}

// userLocation 解析用户时区；回退规则见 store.LoadLocation（空/非法名 → UTC），
// 页面不会因脏数据 500。
func userLocation(user *store.User) *time.Location {
	return store.LoadLocation(user.Timezone)
}

// barWidth 返回柱状条宽度声明；value 非正或 max 非正时返回空串（该行不画柱）。
// 模板用 templ.SafeCSS 渲染它：值完全由本函数从整数算出，不含用户输入；
// 注意 templ.SafeCSSProperty 不在 templ 的样式清洗器支持列表里，会被替换成占位符。
func barWidth(value, max int64) string {
	if value <= 0 || max <= 0 {
		return ""
	}
	return "width:" + store.FormatPercent(float64(value)/float64(max)) + "%;"
}

// percentWidth 把 0–1 的比例转成柱宽声明；0 时返回空串。
func percentWidth(rate float64) string {
	if rate <= 0 {
		return ""
	}
	return "width:" + store.FormatPercent(rate) + "%;"
}

// maxInt64 返回一组整数里的最大值；无参数时返回 0。
func maxInt64(values ...int64) int64 {
	var out int64
	for _, v := range values {
		if v > out {
			out = v
		}
	}
	return out
}
