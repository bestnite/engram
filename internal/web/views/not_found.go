package views

// NotFoundData 是未知路径回退页（404）的渲染数据。
// 所有用户可见文案由 handler 从语言包取好传入，模板不自己取词（AGENTS.md §2.1）。
type NotFoundData struct {
	Layout LayoutData
	// Code 是 HTTP 状态码的文本形式，作为页面上的大号标记；它是协议数字而非自然语言，
	// 因此不走语言包（语言包只承载需要翻译的文案）。
	Code string
	// Heading / Message / HomeLabel 都是已本地化的文案。
	Heading   string
	Message   string
	HomeLabel string
	// HomeHref 是页面上「返回首页」按钮的目标。
	HomeHref string
}
