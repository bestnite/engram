package web

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
)

// 本文件是 M8-5（无障碍过一遍）的可核实部分：把「每个表单控件都有标签 / 评分按钮有
// 可读名称 / 键盘快捷键与可见焦点样式存在」这些能从渲染出的 HTML 与静态资源断言的事实
// 固化成测试。真正需要人在浏览器里用键盘体验的部分（Tab 顺序手感、焦点环对比度、
// 屏幕阅读器读音）无法在这里断言，见 docs/accessibility-checklist.md 的待办项。

var (
	// reLabel 匹配一个 <label ...>...</label> 块（含包裹式标签）。模板里的 label 不嵌套。
	reLabel = regexp.MustCompile(`(?s)<label\b([^>]*)>(.*?)</label>`)
	// reControl 匹配 input/select/textarea 起始标签。
	reControl = regexp.MustCompile(`(?s)<(input|select|textarea)\b([^>]*)>`)
	// reRatingButton 匹配带 data-rating 的评分按钮，并捕获其内部文本。
	reRatingButton = regexp.MustCompile(`(?s)<button[^>]*\bdata-rating="[^"]*"[^>]*>(.*?)</button>`)
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

// auditControlLabels 断言 body 里每个非 hidden 表单控件都有可访问名称，返回表单控件总数。
// 复习页普通卡只有隐藏字段与按钮，因此可见控件数可能为 0。
func auditControlLabels(t *testing.T, page, body string) int {
	t.Helper()
	for _, bad := range unlabelledControls(body) {
		t.Errorf("%s: a form control has no associated <label>, for= target or aria-label: %s", page, snippet(bad))
	}
	n := len(reControl.FindAllStringSubmatchIndex(body, -1))
	t.Logf("%s: audited %d form control(s); no unlabelled visible control found", page, n)
	return n
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

// TestAccessibilityFormControlsHaveLabels 是 M8-5 的量化验收：复习页（普通卡与作答类卡）、
// 卡组页、设置页、卡片列表页渲染出的每个可见表单控件都能被关联到标签。
func TestAccessibilityFormControlsHaveLabels(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	// GET /decks 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 回退列表页（DESIGN.md §8.5）。
	srv.spa = nil

	basicDeck := seedReviewDeck(t, db, ownerID, "A11y basic deck")
	seedBasic(t, db, basicDeck.ID, "Q1", "A1")

	gradedDeck := seedReviewDeck(t, db, ownerID, "A11y graded deck")
	seedGradedNote(t, db, gradedDeck.ID, "numeric", map[string]any{
		"prompt": "分数 1/2 化成百分数是多少？", "value": 50.0, "tolerance_absolute": 0.5, "unit": "%",
	})

	pages := []struct{ name, path string }{
		{"review-basic", "/review?deck=" + u64str(basicDeck.ID)},
		{"review-graded", "/review?deck=" + u64str(gradedDeck.ID)},
		{"decks", "/decks"},
		{"settings", "/settings"},
		{"notes-list", "/decks/" + u64str(basicDeck.ID) + "/notes"},
	}
	total := 0
	for _, p := range pages {
		rec := getWithCookies(t, srv, p.path, cookies)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200 (body %s)", p.path, rec.Code, snippet(rec.Body.String()))
		}
		body := rec.Body.String()
		total += auditControlLabels(t, p.name, body)
	}
	if total == 0 {
		t.Fatalf("the audit found no visible form controls on any page; it is not exercising the UI")
	}

	// 作答类卡片的输入框必须有可见标签，而不是只有 placeholder（M8-5 的主战场之一）。
	graded := getWithCookies(t, srv, "/review?deck="+u64str(gradedDeck.ID), cookies).Body.String()
	if !strings.Contains(graded, `id="review-graded"`) {
		t.Fatalf("numeric card did not render graded controls: %s", snippet(graded))
	}
	if !strings.Contains(graded, `id="review-answer-input"`) {
		t.Errorf("graded answer input has no id to anchor a label: %s", snippet(graded))
	}
	if !strings.Contains(graded, `for="review-answer-input"`) {
		t.Errorf("graded answer input has no <label for=...>: %s", snippet(graded))
	}
	t.Logf("review-graded: graded answer input carries id=\"review-answer-input\" and a matching <label for=...>")
}

// TestAccessibilityRatingButtonsHaveReadableNames 断言复习页的四个评分按钮有可读文本，
// 而不是只有图标：每个 data-rating 按钮的内部文本非空，且四个档位齐全。
func TestAccessibilityRatingButtonsHaveReadableNames(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "A11y rating deck")
	seedBasic(t, db, deck.ID, "Q1", "A1")

	body := getWithCookies(t, srv, "/review?deck="+u64str(deck.ID), cookies).Body.String()

	seen := map[string]bool{}
	for _, m := range reRatingButton.FindAllStringSubmatch(body, -1) {
		text := strings.TrimSpace(m[1])
		if text == "" {
			t.Errorf("a rating button has no readable text (icon-only): %s", snippet(m[0]))
			continue
		}
		seen[text] = true
	}
	if len(seen) != 4 {
		t.Errorf("rating buttons with readable text = %d distinct labels, want 4: %v", len(seen), seen)
	}

	// 「显示答案」按钮同样必须有可读文本。
	if !regexp.MustCompile(`(?s)<button[^>]*id="review-show-answer"[^>]*>\s*[^<\s]`).MatchString(body) {
		t.Errorf("show-answer button has no readable text: %s", snippet(body))
	}
	t.Logf("review: %d rating button(s) with readable labels: %v", len(seen), seen)
}

// TestAccessibilityFocusVisibleStyle 断言键盘可见焦点的样式来源存在（M8-5 的「可见的焦点」）。
// Tailwind 产物是构建时生成且被 gitignore，因此这里断言的是提交进仓库的入口样式。
func TestAccessibilityFocusVisibleStyle(t *testing.T) {
	raw, err := os.ReadFile("static/css/input.css")
	if err != nil {
		t.Fatalf("read input.css: %v", err)
	}
	css := string(raw)
	if !strings.Contains(css, ":focus-visible") {
		t.Errorf("input.css has no :focus-visible rule; keyboard focus would be invisible")
	}
	if !strings.Contains(css, "outline") {
		t.Errorf("input.css :focus-visible rule does not set an outline")
	}
	t.Logf("input.css: :focus-visible rule with outline present (keyboard focus is visible)")
}

// TestAccessibilityReviewKeyboardShortcuts 断言复习页的键盘快捷键确实绑定在脚本里，
// 覆盖 DESIGN.md §8.2 列出的空格/Enter、1–4、u/e/s/b。JS 的真实手感仍需人工确认。
func TestAccessibilityReviewKeyboardShortcuts(t *testing.T) {
	raw, err := os.ReadFile("static/js/review.js")
	if err != nil {
		t.Fatalf("read review.js: %v", err)
	}
	js := string(raw)

	mustContain := map[string]string{
		"keydown listener": `addEventListener("keydown"`,
		"space/enter":      `e.key === " " || e.key === "Enter"`,
		"rating keys":      `e.key === "1"`,
		"bury key":         `key === "b"`,
		"edit key":         `key === "e"`,
	}
	for name, want := range mustContain {
		if !strings.Contains(js, want) {
			t.Errorf("review.js is missing the %s binding (%q)", name, want)
		}
	}
	// 撤销与暂停是调试能力，不该再出现在复习页的键盘绑定里（DESIGN.md §8.2）。
	for _, gone := range []string{`submitAction("undo")`, `submitAction("suspend")`} {
		if strings.Contains(js, gone) {
			t.Errorf("review.js still binds the debug-only action %q", gone)
		}
	}
	t.Logf("review.js: keydown handler covers space/Enter, 1–4, e, b")
}
