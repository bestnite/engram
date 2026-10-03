package views

// DeckSettingsData 是卡组设置页的渲染数据。
//
// 当前上限与今日已用 / 剩余都由 handler 从 schedule.DeckBudgets 取好（与复习队列同源），
// 模板只负责展示，不读库、不算额度公式。Left 值在「不限」时是 UnlimitedValue 文案，
// 由 handler 决定，模板不需要知道不限的判定。
type DeckSettingsData struct {
	Layout LayoutData
	// Heading 是页面标题；DeckName 是当前卡组名。
	Heading  string
	DeckName string
	// BackLabel / BackHref 是返回卡组列表的链接。
	BackLabel string
	BackHref  string
	// Notice 是保存成功提示（来自 ?notice=）；ErrorMessage 是校验失败的局部化文案。二者至多一个非空。
	Notice       string
	ErrorMessage string
	// Action 是表单提交地址；CSRF 是会话绑定的 token。
	Action string
	CSRF   string
	// NewPerDayLabel / ReviewsPerDayLabel 是两个数字输入的名称；UnlimitedHint 说明 0 表示不限。
	NewPerDayLabel     string
	ReviewsPerDayLabel string
	UnlimitedHint      string
	// NewPerDayValue / ReviewsPerDayValue 是输入框初值（库里真实的列值，0 原样显示）。
	NewPerDayValue     string
	ReviewsPerDayValue string
	// UsageHeading 是「今日额度」区块标题。
	UsageHeading string
	// 四个标签与四个值：已用（New/Review）与剩余（New/Review）。
	NewUsedLabel    string
	NewLeftLabel    string
	ReviewUsedLabel string
	ReviewLeftLabel string
	NewUsedValue    string
	NewLeftValue    string
	ReviewUsedValue string
	ReviewLeftValue string
	// UnlimitedValue 是「不限」的显示文案（剩余为不限时用它替代数字）。
	UnlimitedValue string
	// SubmitLabel 是保存按钮文案。
	SubmitLabel string
}
