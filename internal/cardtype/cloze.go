package cardtype

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ClozeDeletion 是一次挖空：{{cN::text}} 或 {{cN::text::hint}}。
type ClozeDeletion struct {
	Index int    // cN 中的 N，是卡片标识 cloze:<index> 的来源。
	Text  string // 挖空内容原文。
	Hint  string // 可选提示；无提示为空串。
	Start int    // 起始字节偏移，指向 "{{"。
	End   int    // 结束字节偏移，正好在闭合 "}}" 之后。
}

// ParseCloze 解析 cloze 文本，按首次出现顺序返回最外层挖空项。
//
// 语法与边界：
//   - {{cN::text}} 与 {{cN::text::hint}}；N 为一个或多个十进制数字。
//   - 内容里成对的 {{ ... }} 按深度匹配，不会提前闭合（嵌套花括号）。
//     嵌套里的 {{cM::...}} 作为普通内容原样保留，不单独成卡 —— 只解析最外层。
//   - \{ \} \\ 是转义：被转义的字符不参与结构解析，但按原文保留（TeX 安全，
//     例如 \frac 里的反斜杠不受影响）。
//   - 未闭合、内容为空的挖空项返回可读的英文错误。
//
// 没有任何挖空项时返回空切片与 nil 错误，由调用方（Validate）决定如何报告。
func ParseCloze(text string) ([]ClozeDeletion, error) {
	var out []ClozeDeletion
	i := 0
	for i < len(text) {
		if escaped, next := skipEscape(text, i); escaped {
			i = next
			continue
		}
		if !strings.HasPrefix(text[i:], "{{") {
			i++
			continue
		}
		index, contentStart, ok := parseClozeOpener(text, i)
		if !ok {
			// 不是挖空标记；"{{" 当普通文本，继续向前。
			i += 2
			continue
		}
		del, end, err := scanClozeContent(text, i, contentStart, index)
		if err != nil {
			return nil, err
		}
		out = append(out, del)
		i = end
	}
	return out, nil
}

// skipEscape 处理 \{ \} \\ 三种转义：返回是否命中，以及跳过后的位置。
// 只对这三种字符生效，避免破坏 TeX 里 \frac 之类的反斜杠。
func skipEscape(s string, i int) (bool, int) {
	if s[i] != '\\' || i+1 >= len(s) {
		return false, i
	}
	switch s[i+1] {
	case '{', '}', '\\':
		return true, i + 2
	}
	return false, i
}

// parseClozeOpener 判断 s[i:] 是否以 {{cN:: 开头；是则返回序号与内容起点。
func parseClozeOpener(s string, i int) (index, contentStart int, ok bool) {
	j := i + 2
	if j >= len(s) || s[j] != 'c' {
		return 0, 0, false
	}
	j++
	digitStart := j
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == digitStart {
		return 0, 0, false
	}
	if !strings.HasPrefix(s[j:], "::") {
		return 0, 0, false
	}
	n, err := strconv.Atoi(s[digitStart:j])
	if err != nil {
		// 数字大到无法表示：当作普通文本，而不是让解析失败。
		return 0, 0, false
	}
	return n, j + 2, true
}

// scanClozeContent 从内容起点扫描到最外层的 "}}"，返回挖空项与结束位置。
// depth 跟踪嵌套的 {{ }}；内容里首个顶层 "::" 分隔 text 与 hint。
func scanClozeContent(s string, start, contentStart, index int) (ClozeDeletion, int, error) {
	depth := 0
	hintSep := -1
	j := contentStart
	for j < len(s) {
		if escaped, next := skipEscape(s, j); escaped {
			j = next
			continue
		}
		switch {
		case strings.HasPrefix(s[j:], "{{"):
			depth++
			j += 2
		case strings.HasPrefix(s[j:], "}}"):
			if depth > 0 {
				depth--
				j += 2
				continue
			}
			text, hint := splitClozeContent(s[contentStart:j], hintSep, contentStart)
			if text == "" {
				return ClozeDeletion{}, 0, fmt.Errorf("cloze: cloze deletion c%d has empty text", index)
			}
			return ClozeDeletion{Index: index, Text: text, Hint: hint, Start: start, End: j + 2}, j + 2, nil
		case depth == 0 && hintSep < 0 && strings.HasPrefix(s[j:], "::"):
			hintSep = j
			j += 2
		default:
			j++
		}
	}
	return ClozeDeletion{}, 0, fmt.Errorf("cloze: unclosed cloze deletion starting at offset %d", start)
}

// splitClozeContent 按顶层 "::" 把内容切成 text 与 hint（hintSep 为 -1 表示无提示）。
func splitClozeContent(content string, hintSep, contentStart int) (text, hint string) {
	if hintSep < 0 {
		return content, ""
	}
	rel := hintSep - contentStart
	return content[:rel], content[rel+2:]
}

// clozeType 是挖空卡：每个不同序号产出一张卡。
// 字段：text（必填，至少含一个 {{cN::…}}），extra / source_url（可选）。
type clozeType struct{}

// Label 返回语言包键名。
func (clozeType) Label() string { return "cardtype.cloze" }

// Validate 校验 text 且必须至少含一个挖空项。
func (clozeType) Validate(fields map[string]any) error {
	text, err := stringField(fields, "text")
	if err != nil {
		return fmt.Errorf("cloze: %w", err)
	}
	dels, err := ParseCloze(text)
	if err != nil {
		return err
	}
	if len(dels) == 0 {
		// schema 的 pattern 只能表达 {{cN::，这里给出更清楚的可读错误。
		return errors.New(`cloze: field "text" must contain at least one cloze deletion like {{c1::text}}`)
	}
	if err := validateCommonOptional(fields); err != nil {
		return fmt.Errorf("cloze: %w", err)
	}
	return nil
}

// Cards 按序号的首次出现顺序各产出一张卡；重复序号只产出一张。
func (clozeType) Cards(note Note) []Card {
	text, _ := note.Fields["text"].(string)
	dels, _ := ParseCloze(text)
	seen := make(map[int]bool, len(dels))
	cards := make([]Card, 0, len(dels))
	ordinal := 0
	for _, d := range dels {
		if seen[d.Index] {
			continue
		}
		seen[d.Index] = true
		cards = append(cards, Card{
			Template: fmt.Sprintf("cloze:%d", d.Index),
			Ordinal:  ordinal,
			Fields:   note.Fields,
		})
		ordinal++
	}
	return cards
}

// Render 正面掩盖目标序号、背面揭示并高亮；其它序号显示挖空内容（不带 {{cN::}} 标记）。
func (clozeType) Render(card Card, side Side) (RenderResult, error) {
	index, ok := parseClozeTemplate(card.Template)
	if !ok {
		return RenderResult{}, errUnknownTemplate(card.Template)
	}
	text, _ := card.Fields["text"].(string)
	dels, err := ParseCloze(text)
	if err != nil {
		return RenderResult{}, err
	}
	return RenderResult{Body: renderCloze(text, dels, index, side == SideBack)}, nil
}

// parseClozeTemplate 解析 "cloze:<index>" 形式的模板。
func parseClozeTemplate(template string) (int, bool) {
	const prefix = "cloze:"
	if !strings.HasPrefix(template, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(template[len(prefix):])
	if err != nil {
		return 0, false
	}
	return n, true
}

// renderCloze 按字节偏移重建文本，挖空之外的部分一字不差地保留 Markdown/TeX 原文。
//
// 挖空标记本身绝不出现在卡面上：目标序号正面换成占位、背面换成内容，两者都包在
// <span class="cloze"> 里供样式高亮；其它序号只显示内容。span[class] 在 render 包的
// 白名单内，Markdown 行内原始 HTML 会原样通过 goldmark，内容里的 Markdown 照常渲染。
func renderCloze(text string, dels []ClozeDeletion, target int, reveal bool) string {
	var b strings.Builder
	last := 0
	for _, d := range dels {
		if d.Start < last {
			// 防御：只解析最外层，偏移不会重叠；真出现重叠时跳过以免重复写入。
			continue
		}
		b.WriteString(text[last:d.Start])
		switch {
		case d.Index != target:
			b.WriteString(stripCloze(d.Text))
		case reveal:
			b.WriteString(`<span class="cloze">` + stripCloze(d.Text) + `</span>`)
		default:
			b.WriteString(`<span class="cloze">` + clozeBlank(d) + `</span>`)
		}
		last = d.End
	}
	b.WriteString(text[last:])
	return b.String()
}

// stripCloze 把内容里嵌套的 {{cM::…}} 换成它的内容：嵌套标记不单独成卡，
// 但也不能以原始语法出现在卡面上。解析失败时按原文返回（外层已校验过，不应发生）。
func stripCloze(text string) string {
	dels, err := ParseCloze(text)
	if err != nil || len(dels) == 0 {
		return text
	}
	return renderCloze(text, dels, -1, true)
}

// clozeBlank 生成正面的占位：有提示时用 [hint]，否则用 […]。
func clozeBlank(d ClozeDeletion) string {
	if d.Hint != "" {
		return "[" + d.Hint + "]"
	}
	return "[…]"
}
