package views

// ReviewPageData 是复习页（M3-5）整页的渲染数据；所有用户可见文案都已本地化。
type ReviewPageData struct {
	Layout LayoutData
	// JSURL 是复习交互脚本（键盘/滑动）的内容哈希 URL；为空时模板跳过引用。
	JSURL string
	Area  ReviewAreaData
}

// ReviewRating 是一档评分按钮；Value 是 1–4，Label 已本地化。
type ReviewRating struct {
	Value int
	Label string
}

// ReviewCardView 是当前卡片的两面渲染结果与标识。
// FrontHTML / BackHTML 一定来自 internal/render 的白名单清洗，模板可安全地按原样嵌入。
type ReviewCardView struct {
	CardID string
	NoteID string
	DeckID string
	// ExpectedVersion 是提交时携带的乐观锁版本（新卡为 "0"）。
	ExpectedVersion string
	Template        string
	FrontHTML       string
	BackHTML        string
	// EditHref 指向该 note 的编辑页，供 e 键跳转。
	EditHref string
}

// ReviewAreaData 是复习页主区域（htmx 交换单元）的渲染数据。
// 评分响应只返回这一部分，因此这里自包含计数器、当前卡片、评分与动作控件。
type ReviewAreaData struct {
	RemainingLabel string
	RemainingCount string
	DoneLabel      string
	DoneCount      string
	// Empty 为 true 时队列已空，只展示 EmptyText。
	Empty     bool
	EmptyText string
	// ErrorText 非空时在主区域顶部显示一条错误（提交失败/渲染失败），不静默。
	ErrorText string
	// NetworkErrorText 是离线横幅文案，默认隐藏，由前端在 htmx sendError 时揭开。
	NetworkErrorText string

	Card *ReviewCardView
	// CardFaceLabel / BackLabel 是正反面小标题。
	FrontLabel string
	BackLabel  string

	ShowAnswerLabel string
	Ratings         []ReviewRating
	ShortcutsHint   string

	// 动作控件文案（u/e/s/b）。
	UndoLabel    string
	EditLabel    string
	SuspendLabel string
	BuryLabel    string

	// 表单目标与状态。
	AnswerURL   string
	ActionURL   string
	CSRF        string
	DeckValue   string
	DoneValue   string
	ElapsedName string
	// CardEditHref 是主区域 data-edit-href 的取值，供 e 键跳转；无卡片时为空串。
	CardEditHref string
	// HasCard 为 true 时渲染表单；动作用同一份隐藏字段。
	HasCard bool
}

// ratingValue 把 1–4 的评分渲染成按钮 value/data 属性的字符串。
func ratingValue(v int) string {
	if v <= 0 {
		return "0"
	}
	if v >= 10 {
		return string([]byte{byte('0' + v/10), byte('0' + v%10)})
	}
	return string([]byte{byte('0' + v)})
}
