package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// shortcutHintPattern 抓取复习页那一行快捷键提示的正文：三种状态各渲染一处（互斥），
// 用提示行独有的 class 尾巴定位，避免依赖周围结构。
var shortcutHintPattern = regexp.MustCompile(`text-zinc-600 dark:text-zinc-400 font-mono font-medium">([^<]*)<`)

func shortcutHint(body string) string {
	m := shortcutHintPattern.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

// TestReviewShortcutHintFollowsCardState 守卫三态快捷键提示（M3-12）：
//   - 自评卡宣传 1–4 评分；
//   - 判分卡待作答**不能**再宣传 1–4——那几个键在判分卡上既不评分也不揭示答案
//     （落进揭示分支等于误触弃卡），宣传它们等于骗用户按；
//   - 结果面板改成"回车：继续"，且该状态下 b（埋藏）的按钮并未渲染，不能宣传它。
//
// 判据用"有没有数字"而不是 "1–4" 字面量：en dash 在源码里容易写坏，而"带数字"正是
// 1–4 那段独有的特征——判分卡与结果面板的提示里一个数字都没有。
func TestReviewShortcutHintFollowsCardState(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	// GET /review 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 复习页（DESIGN.md §8.5）。
	srv.spa = nil
	loc := srv.i18n.Localizer(srv.i18n.Pick("", "", ""))
	selfHint := loc.T("review.shortcuts")
	gradedHint := loc.T("review.shortcuts_graded")
	resultHint := loc.T("review.shortcuts_result")
	if !strings.ContainsAny(selfHint, "0123456789") {
		t.Fatalf("fixture assumption broken: the self-assessed hint %q carries no rating digits", selfHint)
	}
	if strings.ContainsAny(gradedHint, "0123456789") || strings.ContainsAny(resultHint, "0123456789") {
		t.Fatalf("fixture assumption broken: graded=%q result=%q", gradedHint, resultHint)
	}

	// 自评卡：提示串同时是"服务端用了哪个语言"的探针——对不上说明夹具语言与这里不同。
	selfDeck := seedReviewDeck(t, db, ownerID, "Self-assessed hint deck")
	seedBasic(t, db, selfDeck.ID, "Front", "Back")
	page := getWithCookies(t, srv, "/review?deck="+u64str(selfDeck.ID), cookies)
	if got := shortcutHint(page.Body.String()); got != selfHint {
		t.Fatalf("self-assessed hint = %q, want %q", got, selfHint)
	}

	// 判分卡待作答：提示里不得出现评分数字。
	deck := seedReviewDeck(t, db, ownerID, "Graded hint deck")
	seedGradedNote(t, db, deck.ID, "numeric", map[string]any{
		"prompt": "分数 1/2 化成百分数是多少？", "value": 50.0, "tolerance_absolute": 0.5, "unit": "%",
	})
	page = getWithCookies(t, srv, "/review?deck="+u64str(deck.ID), cookies)
	body := page.Body.String()
	if !strings.Contains(body, `id="review-graded"`) {
		t.Fatalf("graded card did not render graded controls: %s", snippet(body))
	}
	if got := shortcutHint(body); got != gradedHint {
		t.Errorf("graded card hint = %q, want %q", got, gradedHint)
	}
	if strings.ContainsAny(shortcutHint(body), "0123456789") {
		t.Errorf("graded card still advertises the 1-4 ratings: %q", shortcutHint(body))
	}

	// 结果面板：判分提交后的那份响应里，提示必须换成"回车：继续"。
	rec := postForm(t, srv, "/review/answer", url.Values{
		"csrf_token":       {csrf},
		"card_id":          {attrValue(body, "card_id")},
		"deck":             {u64str(deck.ID)},
		"expected_version": {attrValue(body, "expected_version")},
		"done":             {attrValue(body, "done")},
		"elapsed_ms":       {"900"},
		"answer":           {"50"},
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /review/answer status = %d, want 200 (%s)", rec.Code, snippet(rec.Body.String()))
	}
	resultBody := rec.Body.String()
	if !strings.Contains(resultBody, `id="review-result"`) {
		t.Fatalf("graded response has no result panel: %s", snippet(resultBody))
	}
	if got := shortcutHint(resultBody); got != resultHint {
		t.Errorf("result panel hint = %q, want %q", got, resultHint)
	}
	t.Logf("self=%q graded=%q result=%q", selfHint, gradedHint, resultHint)
}
