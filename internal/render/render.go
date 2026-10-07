// Package render 实现卡面内容的渲染管线：
// Markdown + TeX 原文 → goldmark 转 HTML → bluemonday 白名单清洗 → 输出可安全嵌入页面的 HTML。
//
// 为什么独立成包：渲染既被 web 页面使用，也会被将来的卡组包导出复用；
// 放在 internal/web 里会让导出路径反向依赖 HTTP 层（AGENTS.md §2.4）。
//
// 为什么必须经过 bluemonday：卡面允许一个 HTML 子集（末条），
// 所以 goldmark 以 WithUnsafe 放行原始 HTML，安全性完全由本包的白名单清洗保证。
package render

import (
	"bytes"
	"fmt"
	"regexp"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// markdown 是共享的 goldmark 实例。
//
// 启用 Table 与 Strikethrough 扩展，因为卡面白名单允许 table/tbody/del
// （表结构来自 Table 扩展，del 来自 Strikethrough 的 ~~删除线~~）。
// WithUnsafe 是刻意的：raw HTML 要交给 bluemonday 按白名单裁剪，而不是在
// goldmark 阶段被转义成文本——否则 HTML 子集无从谈起。
var markdown = goldmark.New(
	goldmark.WithExtensions(
		extension.Table,
		extension.Strikethrough,
	),
	goldmark.WithRendererOptions(html.WithUnsafe()),
)

// policy 是共享的 bluemonday 清洗策略，只在包初始化时构造一次。
var policy = buildPolicy()

// imgSrcPattern 描述 img.src 允许的两类取值：https 绝对地址，或站内相对路径。
//
// 比 a.href 更严（不允许 http），因为自托管服务不应把图片热链到明文 HTTP；
// 相对路径分支用“不含冒号”来排除 javascript:/data: 等伪协议——RE2 不支持
// 负向前瞻，且 url.Parse 会在 validURL 阶段再做一次 scheme 校验作为双保险。
var imgSrcPattern = regexp.MustCompile(
	`(?i)^https://[^:]*$|^[^:/?#][^:]*$|^\.{1,2}/[^:]*$|^/[^:/][^:]*$`,
)

// RenderMarkdown 把卡面的 Markdown + TeX 原文渲染成清洗后的 HTML 片段。
//
// 传入的 src 是不可信内容；返回值只包含白名单内的标签与属性。
// MathJax 分隔符 \( \) 与 \[ \] 及其中的反斜杠、花括号会原样保留，供浏览器端 MathJax 3 渲染。
func RenderMarkdown(src string) (string, error) {
	// 先保护公式，再渲染 Markdown：goldmark 把 \( 当作 CommonMark 反斜杠转义，
	// 会把分隔符吃成 (，从而彻底破坏 MathJax；详见 math.go。
	protected, spans := protectMath(src)

	var buf bytes.Buffer
	if err := markdown.Convert([]byte(protected), &buf); err != nil {
		return "", fmt.Errorf("render markdown: %w", err)
	}

	clean := policy.SanitizeBytes(buf.Bytes())

	// 清洗之后再还原公式：公式是纯文本，还原时只转义会破坏 HTML 的三个字符，
	// 反斜杠与花括号保持不变，MathJax 才能读到原始 TeX。
	restored := restoreMath(string(clean), spans)

	// F8：还原必须再过一次同一白名单。
	//
	// 占位符是在清洗之前插入的，goldmark 可能把它放进属性值位置（链接目标、
	// 图片 alt、raw HTML 的 class/href）。第一遍 bluemonday 校验的是纯字母数字的
	// 占位符本身，而不是还原后的公式原文；公式里的引号会闭合属性并注入白名单
	// 从未见过的新属性——事件处理属性（onmouseover/onerror），或一个 javascript:
	// 的 src/href（还原后的属性值从未被 URL 校验过）。因此把还原后的 HTML 重新
	// 交给同一策略解析清洗一次，让属性名、事件属性与 URL scheme 都在“还原后的
	// 真实内容”上重新校验。公式文本节点里的 < > & 已被 restoreMath 转义，
	// 二次清洗不会改动 MathJax 需要的反斜杠与花括号。
	return string(policy.SanitizeBytes([]byte(restored))), nil
}

// buildPolicy 构造卡面白名单：允许 img/a/code/pre/table/span[class]，绝不允许脚本。
func buildPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	// 卡面可能出现的元素集合。刻意不包含 heading/h1-h6：标题在卡面里无意义，
	// 不放进白名单时 bluemonday 会剥掉标签而保留文字，不会丢内容。
	p.AllowElements(
		"p", "br", "hr",
		"strong", "em", "del", "blockquote",
		"code", "pre",
		"ul", "ol", "li",
		"table", "thead", "tbody", "tr", "th", "td",
		"a", "img", "span",
	)

	// span 的 class 供站点样式与高亮使用；用 bluemonday 自带的 token 正则收窄取值。
	p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("span")

	// a.href 的取值由下面的全局 scheme 策略与相对路径开关共同把关：
	// 只放行 http/https/mailto 与站内相对路径。
	p.AllowAttrs("href").OnElements("a")

	// img.src 只允许 https 与站内相对路径（不允许 http）；alt 提升无障碍可读性。
	p.AllowAttrs("src").Matching(imgSrcPattern).OnElements("img")
	p.AllowAttrs("alt").OnElements("img")

	// ol.start：有序列表从非 1 开始时 goldmark 会输出 start 属性，
	// 不放行会导致编号错误；取值限制为正整数，无脚本风险。
	p.AllowAttrs("start").Matching(regexp.MustCompile(`^[0-9]+$`)).OnElements("ol")

	// 打开 URL 解析校验，并显式允许相对路径（否则 /media/x 这类站内地址会被清掉）。
	p.RequireParseableURLs(true)
	p.AllowRelativeURLs(true)
	p.AllowURLSchemes("http", "https", "mailto")

	return p
}
