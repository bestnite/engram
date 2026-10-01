package views

// 本文件是 M9-4 预设页（/presets）与参数优化卡片的渲染数据。
// 所有面向用户的文字都由 handler 从语言包取好后放入这些字段，模板只做排版
// （AGENTS.md §2.1；scripts/checks/no-template-literals.sh 会强制这一点）。

// PresetListData 是预设页整页的渲染数据。
type PresetListData struct {
	Layout    LayoutData
	Heading   string
	Intro     string
	EmptyText string
	Cards     []PresetCardData
}

// PresetCardData 是单个预设卡片的渲染数据，也是优化轮询时返回的局部片段。
//
// 设计要点：卡片每次渲染都从数据库重读预设，因此页面显示的权重来源
// （默认 / 已优化 + 时间 + 条数 + 原始权重）始终等于库里的真实状态，
// 而不是某次响应的缓存值——这正是 M9-4「完成后页面显示的权重与库里一致」的落地方式。
type PresetCardData struct {
	ID   string
	Name string
	// 调度参数（只读展示；编辑页面属于其它任务）。
	RetentionLabel   string
	RetentionValue   string
	MaxIntervalLabel string
	MaxIntervalValue string
	FuzzLabel        string
	FuzzValue        string

	// 权重来源（DESIGN.md §3.5：默认 / 已优化 + 时间 + 使用条数）。
	WeightsHeading     string
	WeightsSource      string
	WeightsOptimizedAt string
	WeightsReviewCount string
	WeightsRawLabel    string
	WeightsRaw         string

	// 门槛信息（DESIGN.md §3.5：可用复习条数、不足时「还差 N 条」）。
	ReviewsAvailable string
	Threshold        string

	// 优化入口与轮询。
	OptimizeLabel  string
	OptimizeAction string
	StatusURL      string
	// PollTrigger 是 htmx 的 hx-trigger 值：作业在途时是 "every 2s"，否则是 "none"（不轮询）。
	PollTrigger string

	// 作业状态与结果摘要。
	StatusText   string
	StageText    string
	Shortfall    string
	Conflict     string
	ErrorText    string
	LogTailLabel string
	LogTail      string

	ResultTitle   string
	ResultReviews string
	ResultBefore  string
	ResultAfter   string
	ResultVerdict string

	// 回退默认权重与「不重算到期日」的说明。
	RevertLabel    string
	RevertAction   string
	RevertNote     string
	RescheduleNote string

	CSRF string
}
