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

// ReviewGradedOption 是一个选项控件：Value 是提交用的 0 基索引字符串，Label 已本地化
// （选项文本来自 note 内容是内容本身，索引与题型无关）。
type ReviewGradedOption struct {
	Value string
	Label string
}

// ReviewGradedView 是作答类题型（M3-12）要渲染的输入控件。
//
// TypeAttr 是 <input type> 的取值："text"（typed/numeric）或 "radio"/"checkbox"
// （单选/多选/判断题）。InputMode 非空时补上 inputmode（数值输入走数字键盘）。
// Options 非空时逐个渲染为选项控件。
type ReviewGradedView struct {
	TypeAttr    string
	InputMode   string
	Options     []ReviewGradedOption
	Placeholder string
	SubmitLabel string
	// AnswerLabel 是作答输入框的可访问名称（M8-5）：纯文本/数值输入没有可见标题，
	// 用 <label> 关联，屏幕阅读器与键盘用户都能知道这个输入框是什么。
	AnswerLabel string
}

// ReviewDetailLine 是一条判分细节（标签已本地化，值来自判分结果）。
type ReviewDetailLine struct {
	Label string
	Value string
}

// ReviewResultView 是判分后展示的结果面板：判定、得分、正确答案与细节，
// 用户点“继续”才进入下一张卡（判分结果已在同一请求内写入 reviews）。
type ReviewResultView struct {
	VerdictLabel  string
	ScoreLabel    string
	Score         string
	AnswerLabel   string
	AnswerHTML    string
	DetailLines   []ReviewDetailLine
	ContinueLabel string
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
	// ZeroScoreLabel 是作答类题型"已揭示答案、按 0 分继续"按钮的文案。
	ZeroScoreLabel string
	Ratings        []ReviewRating
	ShortcutsHint  string

	// Graded 非空时（作答类题型，M3-12）用输入控件代替四档自评按钮。
	Graded *ReviewGradedView
	// Result 非空时展示判分结果面板，等待用户点“继续”进入下一张卡。
	Result *ReviewResultView

	// 动作控件文案（u/e/s/b）。
	UndoLabel    string
	EditLabel    string
	SuspendLabel string
	BuryLabel    string

	// 表单目标与状态。
	AnswerURL string
	ActionURL string
	CSRF      string
	// DeckValues 是本次复习范围里的卡组 id（十进制字符串），每个渲染一个隐藏
	// `deck` 字段；空切片表示全库范围（不渲染任何 deck 字段）。DESIGN.md §8.2 要求
	// 每个评分/动作请求把同一范围原样带回，否则跨卡组复习会在首次评分后退化。
	DeckValues  []string
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
