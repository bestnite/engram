package web

// 本文件锁定用户报告的移动端抽屉缺陷（回归测试，M8-7 之后的修复）：
//
//	(a) 抽屉必须脱离文档流。它原先用 relative，展开后把页头撑高、<main> 被整体挤到下方；
//	(b) 遮罩必须位于 <header> 之外。页头带 backdrop-blur-md，而 backdrop-filter 会让元素成为
//	    fixed/absolute 后代的包含块，遮罩放在页头内部时 fixed inset-0 只覆盖页头那一条，
//	    于是点击页面空白处关不掉抽屉，也没有压暗效果。
//
// 浏览器里的观感无法在这里断言（CI 不跑浏览器），可断言的是渲染出的 HTML 结构：
// 抽屉脱离文档流且锚在页头下沿，遮罩在页头之外并覆盖视口。

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/web/views"
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

// TestMobileNavDrawerFloatsAndScrimCoversViewport 是本次缺陷的验收测试：真实页面渲染出的
// 抽屉必须脱离文档流、锚在页头下沿，遮罩必须在页头之外并覆盖整个视口。
func TestMobileNavDrawerFloatsAndScrimCoversViewport(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	html := rec.Body.String()

	if !strings.Contains(html, `id="mobile-nav-menu"`) {
		t.Fatalf("GET / did not render the mobile drawer, so the assertions below would be vacuous: %s", snippet(html))
	}
	if problem := mobileNavDrawerInFlowProblem(html); problem != "" {
		t.Errorf("GET /: %s", problem)
	}
	if problem := mobileNavScrimInsideHeaderProblem(html); problem != "" {
		t.Errorf("GET /: %s", problem)
	}

	// 抽屉要锚在页头下沿（top-full）。少了它抽屉会盖住导航栏本身或漂到页头上方。
	drawerTag := openingTagByID(html, "mobile-nav-menu")
	if !classListOf(drawerTag)["top-full"] {
		t.Errorf("#mobile-nav-menu is not anchored to the bottom edge of the header (top-full missing): %s", drawerTag)
	}

	// 遮罩要覆盖整个视口。它在页头之外（上面已断言），因此 fixed inset-0 以视口为包含块。
	scrimTag := openingTagByID(html, "mobile-nav-backdrop")
	if scrimTag == "" {
		t.Fatalf("the rendered page has no #mobile-nav-backdrop element: %s", snippet(html))
	}
	scrimClasses := classListOf(scrimTag)
	if !scrimClasses["fixed"] || !scrimClasses["inset-0"] {
		t.Errorf("#mobile-nav-backdrop is not a viewport-sized overlay (fixed + inset-0 missing): %s", scrimTag)
	}
	t.Logf("mobile drawer: out of flow and anchored with top-full; scrim: outside <header>, fixed inset-0")
}

// TestMobileNavHeaderComponentRendersDrawer 断言页头组件本身（不经 HTTP 栈）就带着修好的标记。
// 页面级测试依赖 handler 传入的导航数据，这一条不依赖。
func TestMobileNavHeaderComponentRendersDrawer(t *testing.T) {
	data := views.LayoutData{
		Brand:        "Engram",
		HomeURL:      "/",
		Nav:          []views.NavItem{{Label: "Decks", Href: "/decks", Active: true}},
		SessionLabel: "Log out",
		SessionHref:  "/logout",
		SessionForm:  true,
		CSRF:         "test-token",
	}
	var sb strings.Builder
	if err := views.Header(data).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render Header: %v", err)
	}
	html := sb.String()

	if problem := mobileNavDrawerInFlowProblem(html); problem != "" {
		t.Errorf("Header component: %s", problem)
	}
	if problem := mobileNavScrimInsideHeaderProblem(html); problem != "" {
		t.Errorf("Header component: %s", problem)
	}
	if !strings.Contains(html, `id="mobile-nav-toggle"`) || !strings.Contains(html, "mobile-nav-icon-close") {
		t.Errorf("Header component no longer renders the drawer toggle and both icons: %s", snippet(html))
	}
	t.Logf("Header component: drawer markup carries the out-of-flow positioning and the scrim is a sibling of <header>")
}

// TestMobileNavProblemCheckersFlagBrokenMarkup 是检查函数自身的负例：缺陷当时的标记
// （抽屉 relative、遮罩在页头内部）必须被抓出来，修好后的标记必须放行。
// 否则上面两条正例测试会静默假绿。
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
