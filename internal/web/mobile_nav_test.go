package web

// 本文件只保留移动端抽屉缺陷的**标记检查器**及其负例。
//
// 用户报告的缺陷（抽屉用 relative 把页头撑高、遮罩放在带 backdrop-filter 的 <header> 内
// 导致 fixed inset-0 只覆盖页头一条）此前由 SSR 页头渲染的 HTML 断言。页面层已切到 SPA
// 应用壳：抽屉与遮罩标记改由前端（Svelte）渲染，Go 侧不再产生这段 HTML，页面级与组件级
// 断言随之删除（相关回归由前端测试覆盖）。
//
// 检查器本身仍可独立验证：下面用缺陷时的标记与修好后的标记各跑一遍，确保检查逻辑不会
// 静默假绿。

import (
	"regexp"
	"strings"
	"testing"
)

var reHeaderElement = regexp.MustCompile(`(?s)<header\b[^>]*>.*?</header>`)

// openingTagByID 返回 id 等于 id 的元素起始标签；找不到返回空串。
// 属性名必须以空白开头，避免 "data-id" 被当成 "id"。
func openingTagByID(html, id string) string {
	re := regexp.MustCompile(`<[a-zA-Z][^>]*\sid="` + regexp.QuoteMeta(id) + `"[^>]*>`)
	return re.FindString(html)
}

// classListOf 返回起始标签上 class 属性的类名集合（没有 class 时为空集）。
func classListOf(tag string) map[string]bool {
	set := map[string]bool{}
	m := regexp.MustCompile(`\sclass="([^"]*)"`).FindStringSubmatch(tag)
	if m == nil {
		return set
	}
	for _, c := range strings.Fields(m[1]) {
		set[c] = true
	}
	return set
}

// mobileNavDrawerInFlowProblem 返回抽屉仍参与普通文档流的证据；没有则返回空串。
func mobileNavDrawerInFlowProblem(html string) string {
	tag := openingTagByID(html, "mobile-nav-menu")
	if tag == "" {
		return "the rendered page has no #mobile-nav-menu element"
	}
	classes := classListOf(tag)
	for _, inFlow := range []string{"relative", "static"} {
		if classes[inFlow] {
			return "#mobile-nav-menu uses position:" + inFlow + ", so opening it grows the header and pushes <main> down: " + tag
		}
	}
	if !classes["absolute"] && !classes["fixed"] {
		return "#mobile-nav-menu is neither absolute nor fixed, so it stays in normal flow: " + tag
	}
	return ""
}

// mobileNavScrimInsideHeaderProblem 返回遮罩位于 <header> 内部的证据；没有则返回空串。
func mobileNavScrimInsideHeaderProblem(html string) string {
	header := reHeaderElement.FindString(html)
	if header == "" {
		return "the rendered page has no <header> element"
	}
	if strings.Contains(header, `id="mobile-nav-backdrop"`) {
		return "the mobile nav scrim sits inside <header>; the header carries backdrop-filter, which makes it a containing block for fixed descendants, so fixed inset-0 covers only the header strip instead of the viewport"
	}
	return ""
}

// TestMobileNavProblemCheckersFlagBrokenMarkup 是检查函数自身的负例：缺陷当时的标记
// （抽屉 relative、遮罩在页头内部）必须被抓出来，修好后的标记必须放行。
func TestMobileNavProblemCheckersFlagBrokenMarkup(t *testing.T) {
	broken := `<header class="sticky top-0 backdrop-blur-md">` +
		`<div id="mobile-nav-backdrop" class="fixed inset-0 z-40 hidden sm:hidden"></div>` +
		`<div id="mobile-nav-menu" class="hidden sm:hidden relative z-50"></div>` +
		`</header>`
	if got := mobileNavDrawerInFlowProblem(broken); got == "" {
		t.Error("the in-flow drawer check accepted a relative drawer: the reported defect would pass unnoticed")
	}
	if got := mobileNavScrimInsideHeaderProblem(broken); got == "" {
		t.Error("the scrim check accepted a scrim inside <header>: the reported defect would pass unnoticed")
	}

	fixed := `<header class="sticky top-0 backdrop-blur-md">` +
		`<div id="mobile-nav-menu" class="hidden sm:hidden absolute inset-x-0 top-full z-50"></div>` +
		`</header>` +
		`<div id="mobile-nav-backdrop" class="fixed inset-0 z-20 hidden sm:hidden"></div>`
	if got := mobileNavDrawerInFlowProblem(fixed); got != "" {
		t.Errorf("the in-flow drawer check rejected the fixed markup: %s", got)
	}
	if got := mobileNavScrimInsideHeaderProblem(fixed); got != "" {
		t.Errorf("the scrim check rejected the fixed markup: %s", got)
	}
	t.Logf("checkers: flag the reported markup, accept the fixed markup")
}
