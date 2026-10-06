// 笔记编辑页（新建/编辑）预览区的局部刷新增强（M2-12）。
//
// 预览由 htmx 把 /decks/:id/preview 的返回片段换进 #note-preview；换入的卡片正文里可能
// 含 \(…\) 公式，而 MathJax 只在页面加载时扫描一次 DOM，新片段里的公式会原样留着。这里在
// document 上委托 htmx:afterSwap，事件目标命中 #note-preview 时补排一次公式。
//
// 为什么不用模板上的 hx-on::after-swap：hx-on 的值由 htmx 在运行时用 new Function 编译，
// 会迫使 CSP 的 script-src 长期保留 'unsafe-eval'（DESIGN.md §11）。改成委托监听不需要
// 任何 eval，策略即可去掉 'unsafe-eval'。review.js 对复习页换卡用的是同一手法。
(function () {
  "use strict";

  // 交给 MathJax 补排新节点。typesetPromise 返回 Promise，失败必须吞掉：渲染失败不该
  // 打断编辑页（与 review.js 一致）。
  function typesetPreview(node) {
    if (window.MathJax && window.MathJax.typesetPromise) {
      window.MathJax.typesetPromise([node])["catch"](function () {});
    }
  }

  document.addEventListener("htmx:afterSwap", function (e) {
    if (e.target && e.target.id === "note-preview") {
      typesetPreview(e.target);
    }
  });
})();
