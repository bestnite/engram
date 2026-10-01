package render

import (
	"fmt"
	"regexp"
	"strings"
)

// mathTokenPrefix 是公式占位符的前缀。
//
// 占位符必须是纯字母数字：goldmark 不会改写它，bluemonday 也不会把它当标签处理。
// 正常情况下用户的 TeX 里不会出现这个前缀；即便出现，最坏结果是还原位置错乱，
// 不会造成安全问题。
const mathTokenPrefix = "MJXFORMULATOKEN"

// mathPattern 匹配 MathJax 的行内与块级分隔符：\( ... \) 与 \[ ... \]。
//
// (?s) 让 . 跨越换行，块级公式可多行。
// (?:\\.|[^\\])*? 允许公式里出现反斜杠命令（\dfrac、\int），
// 同时用非贪婪匹配在第一个未转义的 \) 或 \] 处收束。
var mathPattern = regexp.MustCompile(
	`(?s)\\\((?:\\.|[^\\])*?\\\)|\\\[(?:\\.|[^\\])*?\\\]`,
)

// mathSpan 记录一段被占位符替换掉的公式原文。
type mathSpan struct {
	token string
	raw   string
}

// escapeMath 在把公式写回 HTML 文本节点时，只转义会破坏 HTML 结构的三个字符。
//
// 关键是“不转义反斜杠、圆括号与花括号”：它们正是 MathJax 的分隔符与 TeX 语法字符，
// 一旦变成 &#92; 之类的实体，浏览器 textContent 里就不是原始 TeX 了。
// HTML 解析器会把 &lt; 还原成 <，所以 MathJax 最终读到的仍是原始 TeX。
var mathEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

// protectMath 把 src 里的公式整段替换成占位符，返回替换后的字符串与占位符表。
func protectMath(src string) (string, []mathSpan) {
	spans := make([]mathSpan, 0, 4)
	protected := mathPattern.ReplaceAllStringFunc(src, func(match string) string {
		token := fmt.Sprintf("%s%06d", mathTokenPrefix, len(spans))
		spans = append(spans, mathSpan{token: token, raw: match})
		return token
	})
	return protected, spans
}

// restoreMath 把渲染并清洗后的 HTML 里的占位符换回公式原文。
func restoreMath(out string, spans []mathSpan) string {
	for _, s := range spans {
		out = strings.ReplaceAll(out, s.token, mathEscaper.Replace(s.raw))
	}
	return out
}
