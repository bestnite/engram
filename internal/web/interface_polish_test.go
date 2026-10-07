package web

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// 本文件是 M8-7（界面打磨）的可核实部分：导航统一、品牌不误高亮、页脚项目名、
// 图标与 favicon、新建卡组对话框。真正的观感（对话框动画、移动端手感）仍需人工在
// 浏览器里确认，见最终报告里的真实实例检查。

// desktopNavSegment 截出桌面端导航容器（base.templ 里的 `hidden sm:flex items-center gap-1`）
// 的内容片段；导航项在这里按固定顺序渲染，便于逐项比对。
func desktopNavSegment(t *testing.T, body string) string {
	t.Helper()
	const marker = "hidden sm:flex items-center gap-1"
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("page has no desktop navigation container: %s", snippet(body))
	}
	rest := body[i:]
	j := strings.Index(rest, "</div>")
	if j < 0 {
		t.Fatalf("desktop navigation container is not closed: %s", snippet(body))
	}
	return rest[:j]
}

// topNavItems 返回桌面端导航项的 `href|label` 列表（顺序即渲染顺序）。
func topNavItems(t *testing.T, body string) []string {
	t.Helper()
	seg := desktopNavSegment(t, body)
	re := regexp.MustCompile(`href="([^"]+)">([^<]*)</a>`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(seg, -1) {
		out = append(out, m[1]+"|"+m[2])
	}
	return out
}

// activeNavHref 返回桌面端导航里带 aria-current="page" 的项；必须恰好一个。
func activeNavHref(t *testing.T, body string) string {
	t.Helper()
	seg := desktopNavSegment(t, body)
	re := regexp.MustCompile(`<a aria-current="page"[^>]*href="([^"]+)"`)
	ms := re.FindAllStringSubmatch(seg, -1)
	if len(ms) != 1 {
		t.Fatalf("desktop nav has %d item(s) marked aria-current=\"page\", want exactly 1: %s", len(ms), snippet(seg))
	}
	return ms[0][1]
}

// TestTopNavigationIsIdenticalAcrossPages 是 M8-7 (b) 的验收：首页、卡组、统计、预设、
// 设置与管理页渲染同一份顶部导航（此前四个外壳各拼一份，管理入口只在部分页面出现）。
//
// 用登录态夹具：newNotesServer 提供管理员会话，但它的作业执行器未装配，/presets 路由因此
// 不注册（registerPresetRoutes 的依赖门），故 /presets 用同样带会话的 newPresetsServer。
func TestTopNavigationIsIdenticalAcrossPages(t *testing.T) {
	notesSrv, _, _, cookies, _ := newNotesServer(t)
	presetsSrv, _, _, presetsCookies, _, _ := newPresetsServer(t)
	// GET /stats 已切到 SPA 应用壳，不再是 SSR 页面；禁用 SPA 让本用例继续覆盖
	// SSR 回退页（降级路径）的顶部导航，与其余 SSR 页面逐项一致。
	notesSrv.spa = nil
	// GET /presets 同样已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 回退页的顶部导航。
	presetsSrv.spa = nil

	pages := []struct {
		name    string
		srv     *Server
		path    string
		cookies []*http.Cookie
	}{
		{"home", notesSrv, "/", cookies},
		{"decks", notesSrv, "/decks", cookies},
		{"stats", notesSrv, "/stats", cookies},
		{"presets", presetsSrv, "/presets", presetsCookies},
		{"settings", notesSrv, "/settings", cookies},
		{"admin", notesSrv, "/admin", cookies},
	}

	var want []string
	for _, p := range pages {
		rec := getWithCookies(t, p.srv, p.path, p.cookies)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200 (body %s)", p.path, rec.Code, snippet(rec.Body.String()))
		}
		items := topNavItems(t, rec.Body.String())
		if want == nil {
			want = items
			continue
		}
		if !reflect.DeepEqual(items, want) {
			t.Errorf("GET %s top nav = %v, want the same as other pages %v", p.path, items, want)
		}
	}

	expected := []string{"/|今日", "/decks|卡组", "/stats|统计", "/admin|管理", "/presets|调度预设", "/settings|设置"}
	if !reflect.DeepEqual(want, expected) {
		t.Errorf("top nav = %v, want %v", want, expected)
	}
}

// activeNavAnchor 返回桌面端导航里带 aria-current="page" 的整段 <a> 标签。
func activeNavAnchor(t *testing.T, body string) string {
	t.Helper()
	seg := desktopNavSegment(t, body)
	re := regexp.MustCompile(`<a aria-current="page"[^>]*>[^<]*</a>`)
	ms := re.FindAllString(seg, -1)
	if len(ms) != 1 {
		t.Fatalf("desktop nav has %d item(s) marked aria-current=\"page\", want exactly 1: %s", len(ms), snippet(seg))
	}
	return ms[0]
}

// TestBrandNeverActiveAndCurrentItemMarked 是 M8-7 (a) 的验收：品牌链接永不带活动态样式，
// 当前页对应的导航项带 aria-current="page"（服务端渲染，不再由 JS 按 href 猜测）。
func TestBrandNeverActiveAndCurrentItemMarked(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	// /stats 已切到 SPA；禁用 SPA 以覆盖 SSR 回退页的导航高亮（降级路径）。
	srv.spa = nil

	brandRe := regexp.MustCompile(`<a class="([^"]*font-bold tracking-tight[^"]*)" href="/">`)
	for _, tc := range []struct{ path, wantActive string }{
		{path: "/", wantActive: "/"},
		{path: "/decks", wantActive: "/decks"},
		{path: "/stats", wantActive: "/stats"},
	} {
		body := getWithCookies(t, srv, tc.path, cookies).Body.String()

		m := brandRe.FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("GET %s: brand link not found: %s", tc.path, snippet(body))
		}
		for _, bad := range []string{"bg-zinc-100", "dark:bg-zinc-800", "font-semibold"} {
			if strings.Contains(m[1], bad) {
				t.Errorf("GET %s: brand link carries active class %q: %s", tc.path, bad, m[1])
			}
		}
		if strings.Contains(m[0], "aria-current") {
			t.Errorf("GET %s: brand link is marked as the current page: %s", tc.path, m[0])
		}

		if got := activeNavHref(t, body); got != tc.wantActive {
			t.Errorf("GET %s: nav item marked current = %q, want %q", tc.path, got, tc.wantActive)
		}

		// 活动项必须同时给出浅色/深色文字色，且不带会被生成顺序反超的灰色文字类
		// （Tailwind 在深色组里把 dark:text-zinc-400 排在 dark:text-zinc-100 之后）。
		active := activeNavAnchor(t, body)
		for _, want := range []string{"bg-zinc-100", "dark:bg-zinc-800", "text-zinc-950", "dark:text-zinc-100", "font-semibold"} {
			if !strings.Contains(active, want) {
				t.Errorf("GET %s: active nav item is missing %q: %s", tc.path, want, active)
			}
		}
		for _, bad := range []string{"text-zinc-600", "dark:text-zinc-400", "text-zinc-700", "dark:text-zinc-300"} {
			if strings.Contains(active, bad) {
				t.Errorf("GET %s: active nav item keeps conflicting class %q (it would win the cascade): %s", tc.path, bad, active)
			}
		}
	}
}

// TestPWAScriptDoesNotHighlightByHref 是 M8-7 (a) 的回归闸：高亮逻辑必须留在服务端，
// 脚本里不得再出现按 window.location.pathname 匹配导航项并加活动类样的代码。
func TestPWAScriptDoesNotHighlightByHref(t *testing.T) {
	raw, err := os.ReadFile("static/js/pwa.js")
	if err != nil {
		t.Fatalf("read pwa.js: %v", err)
	}
	js := string(raw)
	if strings.Contains(js, "window.location.pathname") {
		t.Errorf("pwa.js still inspects window.location.pathname for nav highlighting")
	}
	if strings.Contains(js, `classList.add("bg-zinc-100"`) {
		t.Errorf("pwa.js still adds the active-tab background class at runtime")
	}
}

// TestFooterShowsProjectName 是 M8-7 (c) 的验收：页脚文本是项目名 Engram，而不是口号。
func TestFooterShowsProjectName(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	// GET /decks 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 回退列表页（DESIGN.md §8.5）。
	srv.spa = nil
	body := getWithCookies(t, srv, "/decks", cookies).Body.String()
	if !strings.Contains(body, `>Engram</a>`) {
		t.Errorf("footer does not render the project name: %s", snippet(body))
	}
	if strings.Contains(body, "数据留在你自己的机器上") {
		t.Errorf("footer still renders the old slogan: %s", snippet(body))
	}
}

// TestDeckListUsesNewDeckDialog 是 M8-7 (e) 的验收：新建卡组是原生对话框，列表头部有打开按钮，
// 表单内容与错误回显都在对话框内，且带无 JS 时的 noscript 兜底。
func TestDeckListUsesNewDeckDialog(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	// GET /decks 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 回退列表页（DESIGN.md §8.5）。
	srv.spa = nil
	body := getWithCookies(t, srv, "/decks", cookies).Body.String()

	for _, want := range []string{
		`<dialog id="new-deck-dialog"`,
		`data-dialog-open="new-deck-dialog"`,
		`data-dialog-close`,
		`method="post" action="/decks"`,
		`<noscript>`,
		`#new-deck-dialog{display:block`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("deck list is missing %q: %s", want, snippet(body))
		}
	}
	// 旧的整块 section 表单必须消失。
	if strings.Contains(body, `<!-- 新建卡组表单 -->`) {
		t.Errorf("deck list still renders the old full-width create form block")
	}
}

// TestFaviconRedirectsToHashedIcon 是 M8-7 (f) 的验收：/favicon.ico 不再 404，
// 而是重定向到内容哈希化的 SVG 图标。
func TestFaviconRedirectsToHashedIcon(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/favicon.ico", nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("GET /favicon.ico status = %d, want 302 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	loc := rec.Header().Get("Location")
	icon := srv.assets.URL("icons/icon.svg")
	if icon == "" {
		t.Fatal("icons/icon.svg is not embedded")
	}
	if loc != icon {
		t.Errorf("GET /favicon.ico Location = %q, want the hashed icon %q", loc, icon)
	}
	if !strings.HasPrefix(loc, staticPathPrefix) {
		t.Errorf("favicon target %q is not a content-hashed static path", loc)
	}
}

// TestPagesDeclareIconLinks 断言每个页面都在 <head> 声明 SVG favicon 与 apple-touch-icon，
// 两者都走内容哈希路径（M8-7 (f)）。
func TestPagesDeclareIconLinks(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	// GET /decks 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 回退列表页（DESIGN.md §8.5）。
	srv.spa = nil
	icon := srv.assets.URL("icons/icon.svg")
	apple := srv.assets.URL("icons/apple-touch-icon.png")
	if icon == "" || apple == "" {
		t.Fatalf("icon assets are not embedded: icon=%q apple=%q", icon, apple)
	}

	for _, path := range []string{"/", "/decks"} {
		body := getWithCookies(t, srv, path, cookies).Body.String()
		if !strings.Contains(body, `rel="icon" type="image/svg+xml" href="`+icon+`"`) {
			t.Errorf("GET %s does not declare the hashed SVG icon: %s", path, snippet(body))
		}
		if !strings.Contains(body, `rel="apple-touch-icon" href="`+apple+`"`) {
			t.Errorf("GET %s does not declare the hashed apple-touch-icon: %s", path, snippet(body))
		}
	}
}

// TestManifestDeclaresAdaptiveAndMaskableIcons 断言 manifest 声明两个图标：自适应 any 的
// icon.svg 与白底 maskable 的 icon-maskable.svg，且 maskable 走 512x512（M8-7 (f)）。
func TestManifestDeclaresAdaptiveAndMaskableIcons(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, manifestPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", manifestPath, rec.Code)
	}
	var body struct {
		Icons []struct {
			Src, Sizes, Type, Purpose string
		} `json:"icons"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}

	anyIcon := srv.assets.URL("icons/icon.svg")
	maskableIcon := srv.assets.URL("icons/icon-maskable.svg")
	if anyIcon == "" || maskableIcon == "" {
		t.Fatalf("manifest icon assets are not embedded: any=%q maskable=%q", anyIcon, maskableIcon)
	}

	var haveAny, haveMaskable bool
	for _, ic := range body.Icons {
		if ic.Src == anyIcon && ic.Sizes == "any" && ic.Type == "image/svg+xml" && ic.Purpose == "any" {
			haveAny = true
		}
		if ic.Src == maskableIcon && ic.Sizes == "512x512" && ic.Type == "image/svg+xml" && ic.Purpose == "maskable" {
			haveMaskable = true
		}
	}
	if !haveAny {
		t.Errorf("manifest is missing the adaptive icon (any, any size): %+v", body.Icons)
	}
	if !haveMaskable {
		t.Errorf("manifest is missing the maskable icon (512x512): %+v", body.Icons)
	}
}

// TestServiceWorkerCachesIcons 断言两个图标与 apple-touch-icon 都进了外壳缓存清单（M8-7 (f)）。
func TestServiceWorkerCachesIcons(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	body := getWithCookies(t, srv, serviceWorkerPath, nil).Body.String()
	for _, logical := range []string{"icons/icon.svg", "icons/icon-maskable.svg", "icons/apple-touch-icon.png"} {
		url := srv.assets.URL(logical)
		if url == "" {
			t.Fatalf("asset %q is not embedded", logical)
		}
		if !strings.Contains(body, `"`+url+`"`) {
			t.Errorf("service worker cache list is missing %q (%s)", logical, url)
		}
	}
}
