package web

import (
	"os/exec"
	"strings"
	"testing"
)

// M8-1 触屏交互的验证分三层，能真机做到的部分明确写清手法：
//  1. 模板契约（本文件）：复习区域带 touch-manipulation + select-none，按钮点击区 ≥ 44px。
//  2. 脚本契约（本文件）：review.js 绑定 touchstart/touchend/contextmenu/dblclick 并阻止
//     默认行为，且忽略落在输入控件上的滑动。
//  3. 脚本化触屏与键盘序列（testdata/review_touch_sim.mjs）：用零依赖 DOM 替身在 Node 里
//     回放“滑动揭示 → 左滑 Again → 长按无菜单”，以及判分卡（M3-12）的三种状态按键
//     （待作答时 1–4 不评分也不揭示、已揭示时输入被锁、结果面板上回车/1–4 都点继续）
//     与换卡后作答控件获得焦点；有 node 时执行，无 node 时跳过。
//
// 仍需真机/浏览器手动确认的部分（无法在 CI 断言）：iOS Safari 与 Android Chrome 上
// 双击缩放与长按选中确实被系统抑制。手测清单：
//
//	a. 手机打开 /review，连续快速点两次评分按钮，页面不缩放；
//	b. 长按卡片正文 1 秒，不弹“拷贝/查询”菜单、不选中文字；
//	c. 未揭示时右滑显示答案；已揭示后左滑记 Again、右滑记 Good；
//	d. 在作答输入框内横向拖动，不触发评分，光标与输入不受影响。
func TestReviewScriptBindsTouchAndBlocksNativeGestures(t *testing.T) {
	assets, err := LoadAssets()
	if err != nil {
		t.Fatalf("LoadAssets() error = %v", err)
	}
	js := assets.body("js/review.js")
	if js == "" {
		t.Fatalf("js/review.js is not embedded")
	}
	for _, want := range []string{
		`"touchstart"`,
		`"touchend"`,
		`"contextmenu"`,
		`"dblclick"`,
		"preventDefault",
		"interactiveTarget",
		"revealAnswer",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("review.js is missing %q", want)
		}
	}
}

// TestReviewTouchSequenceRatesCard 运行脚本化触屏序列（需要 node）。
// 无 node 的环境跳过并打印原因，保证 CI 不因缺少前端运行时失败。
func TestReviewTouchSequenceRatesCard(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found; skipping scripted touch sequence (see manual checklist above)")
	}
	cmd := exec.Command(node, "testdata/review_touch_sim.mjs", "static/js/review.js")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("scripted touch sequence failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "all touch-sequence assertions passed") {
		t.Fatalf("scripted touch sequence did not report success:\n%s", out)
	}
}

// TestReviewPageTouchTargets 断言复习页的触屏类与点击区尺寸（M8-1）。
func TestReviewPageTouchTargets(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	// GET /review 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 复习页（DESIGN.md §8.5）。
	srv.spa = nil
	deck := seedReviewDeck(t, db, ownerID, "Touch deck")
	seedBasic(t, db, deck.ID, "Front", "Back")

	rec := getWithCookies(t, srv, "/review?deck="+u64str(deck.ID), cookies)
	if rec.Code != 200 {
		t.Fatalf("GET /review status = %d, want 200 (%s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()

	if !strings.Contains(body, `id="review-area" class="touch-manipulation select-none"`) {
		t.Errorf("review area must disable double-tap zoom and long-press selection")
	}
	if !strings.Contains(body, `min-h-11 rounded-lg border border-slate-300 px-2 py-3`) {
		t.Errorf("rating buttons must be at least 44px tall (min-h-11)")
	}
	if !strings.Contains(body, `id="review-show-answer"`) || !strings.Contains(body, `min-h-11 w-full rounded-lg bg-blue-600`) {
		t.Errorf("show-answer button must be at least 44px tall (min-h-11)")
	}
	if !strings.Contains(body, "/manifest.webmanifest") {
		t.Errorf("base layout must link the PWA manifest (M8-2)")
	}
}
