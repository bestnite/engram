// PWA 外壳注册（M8-2）与基础视觉增强（M8-5）。
// 零依赖、无构建链；只做注册与高亮当前导航，不触碰任何答题数据（DESIGN.md §8.5）。
(function () {
  "use strict";

  // 高亮当前顶部导航链接
  function highlightActiveNav() {
    var path = window.location.pathname;
    var links = document.querySelectorAll("header nav a");
    links.forEach(function (link) {
      var href = link.getAttribute("href");
      if (href) {
        if (href === path || (href !== "/" && path.indexOf(href) === 0)) {
          link.classList.add("bg-zinc-100", "text-zinc-950", "font-semibold");
          link.classList.remove("text-zinc-600");
        }
      }
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", highlightActiveNav);
  } else {
    highlightActiveNav();
  }

  if (!("serviceWorker" in navigator)) {
    return;
  }
  window.addEventListener("load", function () {
    navigator.serviceWorker.register("/sw.js").catch(function () {
      // 注册失败不影响页面功能：外壳缓存是增强项，不是答题的前提。
    });
  });
})();
