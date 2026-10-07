package web

import (
	"net/url"
	"strings"
	"testing"
)

// cardBodyFragment 取页面里某个卡面正文容器的内部 HTML；occurrence 指定第几个匹配
// （复习页正反面共用同一个 class，靠出现顺序区分：正面在前、反面在后）。
//
// 定位方式是 class 属性的完整值（模板里固定不变），随后读到第一个 </div>：
// 卡面白名单不允许 div，因此正文片段内部不可能再出现 </div>，第一个就是闭合点。
func cardBodyFragment(t *testing.T, body, classAttr string, occurrence int) string {
	t.Helper()
	marker := `class="` + classAttr + `"`
	searchFrom := 0
	for i := 0; ; i++ {
		idx := strings.Index(body[searchFrom:], marker)
		if idx < 0 {
			t.Fatalf("card body marker %q occurrence %d not found in body: %s", marker, occurrence, snippet(body))
		}
		idx += searchFrom
		lt := strings.IndexByte(body[idx:], '>')
		if lt < 0 {
			t.Fatalf("unterminated opening tag for marker %q", marker)
		}
		start := idx + lt + 1
		end := strings.Index(body[start:], "</div>")
		if end < 0 {
			t.Fatalf("no closing </div> for marker %q", marker)
		}
		if i == occurrence {
			return body[start : start+end]
		}
		searchFrom = start + end
	}
}

// 分享页与复习页包裹卡面正文的 class 属性值（模板里各自固定，见 sharebrowse.templ / review.templ）。
const (
	shareFrontClass = "mt-2 text-base text-zinc-950 dark:text-zinc-100 leading-relaxed"
	shareBackClass  = "mt-2 text-base text-zinc-700 dark:text-zinc-300 leading-relaxed"
	reviewBodyClass = "mt-3 text-xl sm:text-2xl font-medium text-zinc-900 dark:text-zinc-100 leading-relaxed break-words"
)

// TestShareBrowseRendersSanitizedCardHTML 锁定 F21：分享页把卡面正文当纯文本转义，
// 用户看到 &lt;span&gt; 而不是粗体/高亮。分享页必须复用复习页同一套已清洗 HTML
// （internal/render.RenderMarkdown），即把渲染结果作为标签注入而不是再转义一次。
func TestShareBrowseRendersSanitizedCardHTML(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	// GET /s/:token 已切到 SPA 应用壳：本用例断言 SSR 回退页的清洗渲染，故置空 SPA（DESIGN.md §8.5）。
	srv.spa = nil
	deck := seedDeck(t, db, ownerID, "Render deck")
	front := `<span class="hl">高亮</span> 与 **加粗** 与 \(x+y\)`
	seedBasic(t, db, deck.ID, front, "背面")

	rec := postForm(t, srv, shareLinksPath(deck.ID), url.Values{"csrf_token": {ownerCSRF}}, ownerCookies)
	if rec.Code != 200 {
		t.Fatalf("create share link status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	token := extractShareToken(t, rec.Body.String())

	body := get(t, srv, "/s/"+token, nil).Body.String()
	frag := cardBodyFragment(t, body, shareFrontClass, 0)

	// 合法标记必须以标签形式出现。
	for _, want := range []string{`<span class="hl">高亮</span>`, `<strong>加粗</strong>`, `\(x+y\)`} {
		if !strings.Contains(frag, want) {
			t.Errorf("shared card body missing rendered markup %q:\n%s", want, frag)
		}
	}
	// 且绝不能再出现被转义的标签或未转换的 Markdown。
	for _, bad := range []string{"&lt;span", "&lt;strong", "&quot;hl&quot;", "**加粗**"} {
		if strings.Contains(frag, bad) {
			t.Errorf("shared card body still shows escaped/raw source %q:\n%s", bad, frag)
		}
	}
}

// TestShareBrowseSanitizesUnsafeCardHTML 是 F21 的安全负例端到端：
// 同一张卡的字段里放脚本、事件属性与 F8 的公式绕过样本，分享页与复习页都不得出现可执行形式。
//
// 清洗本身由 internal/render 的用例穷尽覆盖：
//   - TestRenderMarkdownStripsUnsafe（script / onerror / javascript: URL）
//   - TestRenderMarkdownNeutralizesMathAttributeEscape（F8：公式还原后的属性逃逸）
//
// 这里只证明分享页与复习页确实把内容送进了同一条管线，且两页渲染出的正文 HTML 完全一致。
func TestShareBrowseSanitizesUnsafeCardHTML(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	// GET /review 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 复习页（DESIGN.md §8.5）。
	srv.spa = nil
	deck := seedReviewDeck(t, db, ownerID, "Unsafe deck")
	front := strings.Join([]string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`<span class="\(a"onmouseover="alert(2)//\)">xss-span</span>`,
		`[x](\(javascript:alert(3)\))`,
	}, "\n\n")
	back := `**背面** 与 \(a<b\)`
	seedBasic(t, db, deck.ID, front, back)

	rec := postForm(t, srv, shareLinksPath(deck.ID), url.Values{"csrf_token": {ownerCSRF}}, ownerCookies)
	if rec.Code != 200 {
		t.Fatalf("create share link status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	token := extractShareToken(t, rec.Body.String())

	shareBody := get(t, srv, "/s/"+token, nil).Body.String()
	shareFront := cardBodyFragment(t, shareBody, shareFrontClass, 0)
	shareBack := cardBodyFragment(t, shareBody, shareBackClass, 0)

	reviewBody := getWithCookies(t, srv, "/review?deck="+u64str(deck.ID), ownerCookies).Body.String()
	reviewFront := cardBodyFragment(t, reviewBody, reviewBodyClass, 0)
	reviewBack := cardBodyFragment(t, reviewBody, reviewBodyClass, 1)

	// 安全断言只针对卡面正文：整页外壳本来就带合法的 <script src=...>（主题脚本）。
	for name, frag := range map[string]string{"share front": shareFront, "share back": shareBack, "review front": reviewFront, "review back": reviewBack} {
		for _, bad := range []string{"<script", "onerror", "onmouseover", "onclick", "javascript:", "alert("} {
			if strings.Contains(frag, bad) {
				t.Errorf("%s still contains executable form %q:\n%s", name, bad, frag)
			}
		}
	}
	// 内容不得被整段吞掉：合法残留与文本都要在。
	for _, want := range []string{"xss-span", `<img src="x"`} {
		if !strings.Contains(shareFront, want) {
			t.Errorf("share front lost benign content %q:\n%s", want, shareFront)
		}
	}
	// 一致性：同一 note，分享页与复习页的正文 HTML 逐字相同。
	if shareFront != reviewFront {
		t.Errorf("share/review front HTML differ:\nshare=%q\nreview=%q", shareFront, reviewFront)
	}
	if shareBack != reviewBack {
		t.Errorf("share/review back HTML differ:\nshare=%q\nreview=%q", shareBack, reviewBack)
	}
}
