package views

// 本文件是 M9-4 预设页（/presets）与参数优化卡片的渲染数据。
// 所有面向用户的文字都由 handler 从语言包取好后放入这些字段，模板只做排版
// （AGENTS.md §2.1）。

// PresetFormLabels 是新建/编辑预设表单的本地化标签集合（M3-14）。
// 列表页的新建对话框与每张卡片的编辑对话框共用同一组标签，避免两处文案漂移。
type PresetFormLabels struct {
	Name            string
	Retention       string
	LearningSteps   string
	RelearningSteps string
	MaxInterval     string
	Fuzz            string
	Save            string
	Cancel          string
	Close           string
	EditNote        string
}

// PresetFormValues 是新建/编辑表单的回显值；提交被拒时原样回填，用户不必重打。
type PresetFormValues struct {
	Name            string
	Retention       string
	LearningSteps   string
	RelearningSteps string
	MaxInterval     string
	Fuzz            bool
}

// PresetListData 是预设页整页的渲染数据。
type PresetListData struct {
	Layout    LayoutData
	Heading   string
	EmptyText string
	Cards     []PresetCardData

	// 新建预设入口与对话框（M3-14）。CreateAction 固定为 POST /presets。
	NewButtonLabel string
	NewHeading     string
	CreateAction   string
	Labels         PresetFormLabels
	NewValues      PresetFormValues
	// ErrorMessage 非空时新建对话框自动展开并显示本地化错误（校验失败回显）。
	ErrorMessage string
	// CSRF 是新建表单需要的会话绑定 token（DESIGN.md §4.3）。
	CSRF string
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
	// ResultSampleInsufficient 非空时渲染「样本不足，无法判定」，且不渲染 before/after 与结论。
	ResultSampleInsufficient string

	// 回退默认权重与「不重算到期日」的说明。
	RevertLabel    string
	RevertAction   string
	RevertNote     string
	RescheduleNote string

	// 编辑入口与对话框（M3-14）。EditAction 固定为 POST /presets/<id>。
	EditLabel   string
	EditHeading string
	EditAction  string
	Labels      PresetFormLabels
	EditValues  PresetFormValues
	// EditError 非空时该卡片的编辑对话框自动展开并显示本地化错误（校验失败回显）。
	EditError string

	CSRF string
}
