// Package texmath 识别卡面原文里的 MathJax 公式分隔符。
//
// 为什么独立成包：render 渲染 Markdown 前要把公式整段保护起来，cardtype 渲染挖空时
// 要知道挖空是否落在公式里。两边必须用同一条规则判断“哪里是公式”，而 render 的测试
// 依赖 cardtype，规则只能放在两者都能引用的第三处；各写一份正则迟早会不一致。
package texmath

import "regexp"

// pattern 匹配 MathJax 的行内与块级分隔符：\( ... \) 与 \[ ... \]。
//
// (?s) 让 . 跨越换行，块级公式可多行。
// (?:\\.|[^\\])*? 允许公式里出现反斜杠命令（\dfrac、\int），
// 同时用非贪婪匹配在第一个未转义的 \) 或 \] 处收束。
var pattern = regexp.MustCompile(
	`(?s)\\\((?:\\.|[^\\])*?\\\)|\\\[(?:\\.|[^\\])*?\\\]`,
)

// Ranges 按出现顺序返回 s 中每段公式的字节区间 [start, end)，区间包含分隔符本身。
// 没有公式时返回 nil。
func Ranges(s string) [][]int {
	return pattern.FindAllStringIndex(s, -1)
}

// Contains 判断字节区间 [start, end) 是否完整落在某一段公式之内。
// 只跨进公式一半的区间不算在内。
func Contains(ranges [][]int, start, end int) bool {
	for _, r := range ranges {
		if r[0] <= start && end <= r[1] {
			return true
		}
	}
	return false
}
