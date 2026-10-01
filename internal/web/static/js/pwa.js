// PWA 外壳注册（M8-2）：注册 service worker，让页面可“添加到主屏幕”。
// 零依赖、无构建链；只做注册与降级，不触碰任何答题数据（DESIGN.md §8.5）。
(function () {
  "use strict";
  if (!("serviceWorker" in navigator)) {
    return;
  }
  window.addEventListener("load", function () {
    navigator.serviceWorker.register("/sw.js").catch(function () {
      // 注册失败不影响页面功能：外壳缓存是增强项，不是答题的前提。
    });
  });
})();
