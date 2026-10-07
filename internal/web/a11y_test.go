package web

import (
	"regexp"
	"testing"
)

// 本文件保留无障碍审计逻辑本身的可测部分：表单控件的可访问名称检查。
//
// 逐页的表单控件标签审计曾经针对 SSR 页面渲染的 HTML；页面层已切到 SPA 应用壳（表单在客户端
// 渲染），页面级断言由前端测试覆盖。可见焦点样式（static/css/input.css）与复习快捷键
// （static/js/review.js）随 SSR 静态资源一并删除，对应的静态断言随之删除。
// 这里只保留纯函数审计及其负例。

var (
	// reLabel 匹配一个 <label ...>...</label> 块（含包裹式标签）。模板里的 label 不嵌套。
	reLabel = regexp.MustCompile(`(?s)<label\b([^>]*)>(.*?)</label>`)
	// reControl 匹配 input/select/textarea 起始标签。
	reControl = regexp.MustCompile(`(?s)<(input|select|textarea)\b([^>]*)>`)
)

// attrOf 从一个起始标签的属性串里取 name="value" 的 value；取不到返回空串。
// 属性名前必须紧跟空白或串首，避免 "data-id" 被当成 "id"。
func attrOf(attrs, name string) string {
	m := regexp.MustCompile(`(?:^|\s)` + regexp.QuoteMeta(name) + `="([^"]*)"`).FindStringSubmatch(attrs)
	if m == nil {
		return ""
	}
	return m[1]
}

// unlabelledControls 返回 body 里缺少可访问名称的非 hidden 表单控件片段。
// 判定「有名称」的三条途径：被 <label> 包裹、有 aria-label/aria-labelledby、
// 其 id 被某个 label 的 for 指向。抽成纯函数便于对它本身做负例测试。
func unlabelledControls(body string) []string {
	type labelBlock struct {
		start, end int
		forID      string
	}
	var labels []labelBlock
	for _, m := range reLabel.FindAllStringSubmatchIndex(body, -1) {
		labels = append(labels, labelBlock{start: m[0], end: m[1], forID: attrOf(body[m[2]:m[3]], "for")})
	}

	var bad []string
	for _, m := range reControl.FindAllStringSubmatchIndex(body, -1) {
		tag := body[m[2]:m[3]]
		attrs := body[m[4]:m[5]]
		start, end := m[0], m[1]

		if tag == "input" && attrOf(attrs, "type") == "hidden" {
			continue
		}
		if attrOf(attrs, "aria-label") != "" || attrOf(attrs, "aria-labelledby") != "" {
			continue
		}

		labelled := false
		for _, b := range labels {
			if start >= b.start && end <= b.end {
				labelled = true
				break
			}
		}
		if !labelled {
			if id := attrOf(attrs, "id"); id != "" {
				for _, b := range labels {
					if b.forID == id {
						labelled = true
						break
					}
				}
			}
		}
		if !labelled {
			bad = append(bad, body[start:end])
		}
	}
	return bad
}

// TestUnlabelledControlsDetectsBareInput 是审计逻辑自身的负例：没有标签的输入必须被抓出，
// 被包裹式 label、有 for= 目标或有 aria-label 的输入则放行。防止审计静默假绿。
func TestUnlabelledControlsDetectsBareInput(t *testing.T) {
	bare := `<form><input type="text" name="q" placeholder="Search"/></form>`
	if got := unlabelledControls(bare); len(got) != 1 {
		t.Fatalf("bare input: got %d unlabelled control(s), want 1 (%v)", len(got), got)
	}

	labelled := []string{
		`<label><span>Name</span><input type="text" name="name"/></label>`,
		`<label for="tz">Timezone</label><input id="tz" name="tz"/>`,
		`<input type="text" name="x" aria-label="Answer"/>`,
		`<input type="hidden" name="csrf" value="t"/>`,
	}
	for _, html := range labelled {
		if got := unlabelledControls(html); len(got) != 0 {
			t.Errorf("labelled control flagged: %s -> %v", html, got)
		}
	}
	t.Logf("audit self-check: bare input flagged; wrapped/for=/aria-label/hidden controls pass")
}
