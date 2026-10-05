package views

// 本文件是 M7-3 统计页（/stats）的渲染数据。
//
// 设计要点：页面的每一个数字都由 handler 从 internal/store 的既有聚合查询（M7-1/M7-2）
// 取好后放入这些字段，模板只负责排版与画柱状条。模板不读数据库，也不做任何聚合，
// 这样 M7-4 的回溯校验（RecomputePageNumbers）对照的就是页面真正展示的那批查询
// （AGENTS.md §2.1、DESIGN.md §9）。
//
// 所有面向用户的文字都由 handler 从语言包取好；模板里没有硬编码文案
// （AGENTS.md §2.1）。

// StatsRow 是「标签 + 数值 + 可选柱状条」的一行，用于复习量、到期预测、留存、耗时、
// 打卡、判分来源这些标量指标。
type StatsRow struct {
	Label string
	Value string
	// WidthCSS 是柱状条的宽度声明（例如 "width:42%"）；为空串表示该行不画柱。
	WidthCSS string
}

// StatsCurveRow 是学习曲线的一天：新引入与复习量各画一条柱。
type StatsCurveRow struct {
	Day            string
	NewValue       string
	NewWidthCSS    string
	ReviewValue    string
	ReviewWidthCSS string
}

// StatsData 是统计页整页的渲染数据。
type StatsData struct {
	Layout  LayoutData
	Heading string
	// Empty 非空时页面只显示这句话（还没有任何可统计的数据）。
	Empty string

	VolumeHeading string
	VolumeRows    []StatsRow

	DueHeading string
	DueRows    []StatsRow

	RetentionHeading string
	RetentionIntro   string
	RetentionRows    []StatsRow

	TimeHeading string
	TimeRows    []StatsRow

	StreakHeading string
	StreakRows    []StatsRow

	GradeHeading string
	GradeRows    []StatsRow

	CurveHeading     string
	CurveIntro       string
	CurveNewLabel    string
	CurveReviewLabel string
	CurveEmpty       string
	CurveRows        []StatsCurveRow

	DeckHeading string
	DeckEmpty   string
	DeckColumns []string
	DeckRows    [][]string

	TagHeading string
	TagEmpty   string
	TagColumns []string
	TagRows    [][]string
}
