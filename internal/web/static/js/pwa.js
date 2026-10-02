// PWA 外壳注册（M8-2）、暗色模式（Dark Mode）与移动端导航交互。
// 零依赖、无构建链；不触碰任何答题数据（DESIGN.md §8.5）。
(function () {
  "use strict";

  // ── 暗色模式管理 ───────────────────────────────────────────────
  function isDarkTheme() {
    var stored = localStorage.getItem("engram-theme");
    if (stored === "dark") return true;
    if (stored === "light") return false;
    return !!(window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches);
  }

  function applyTheme(isDark) {
    if (isDark) {
      document.documentElement.classList.add("dark");
    } else {
      document.documentElement.classList.remove("dark");
    }
    var meta = document.querySelector('meta[name="theme-color"]');
    if (meta) {
      meta.setAttribute("content", isDark ? "#09090b" : "#ffffff");
    }
    updateToggleIcons(isDark);
  }

  function updateToggleIcons(isDark) {
    var sunIcons = document.querySelectorAll(".theme-icon-sun");
    var moonIcons = document.querySelectorAll(".theme-icon-moon");
    sunIcons.forEach(function (el) {
      if (isDark) {
        el.classList.remove("hidden");
      } else {
        el.classList.add("hidden");
      }
    });
    moonIcons.forEach(function (el) {
      if (isDark) {
        el.classList.add("hidden");
      } else {
        el.classList.remove("hidden");
      }
    });
  }

  function toggleTheme() {
    var nextDark = !document.documentElement.classList.contains("dark");
    localStorage.setItem("engram-theme", nextDark ? "dark" : "light");
    applyTheme(nextDark);
  }

  // 立即根据偏好应用，避免闪烁
  applyTheme(isDarkTheme());

  if (window.matchMedia) {
    window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", function (e) {
      if (!localStorage.getItem("engram-theme")) {
        applyTheme(e.matches);
      }
    });
  }

  // ── 移动端导航与页面就绪增强 ────────────────────────────────────
  function setupPageInteractions() {
    applyTheme(isDarkTheme());

    // 绑定主题切换按钮
    var toggles = document.querySelectorAll("[data-theme-toggle]");
    toggles.forEach(function (btn) {
      btn.onclick = function (e) {
        e.preventDefault();
        toggleTheme();
      };
    });

    // 移动端折叠导航抽屉
    var navToggle = document.getElementById("mobile-nav-toggle");
    var navMenu = document.getElementById("mobile-nav-menu");
    var iconBars = document.getElementById("mobile-nav-icon-bars");
    var iconClose = document.getElementById("mobile-nav-icon-close");
    var backdrop = document.getElementById("mobile-nav-backdrop");

    function closeMobileNav() {
      if (navMenu) navMenu.classList.add("hidden");
      if (backdrop) backdrop.classList.add("hidden");
      if (iconBars) iconBars.classList.remove("hidden");
      if (iconClose) iconClose.classList.add("hidden");
    }

    function openMobileNav() {
      if (navMenu) navMenu.classList.remove("hidden");
      if (backdrop) backdrop.classList.remove("hidden");
      if (iconBars) iconBars.classList.add("hidden");
      if (iconClose) iconClose.classList.remove("hidden");
    }

    if (navToggle && navMenu) {
      navToggle.onclick = function (e) {
        e.preventDefault();
        var isOpen = !navMenu.classList.contains("hidden");
        if (isOpen) {
          closeMobileNav();
        } else {
          openMobileNav();
        }
      };
    }

    if (backdrop) {
      backdrop.onclick = closeMobileNav;
    }

    // 导航项高亮由服务端渲染（M8-7）：不再按 href 猜测当前页——旧做法用
    // `header nav a` 选择器，会把品牌链接也算进去，于是 "/" 上品牌被选中。
    // 这里只保留“点任意导航链接后收起移动抽屉”。
    var navLinks = document.querySelectorAll("header nav a, #mobile-nav-menu a");
    navLinks.forEach(function (link) {
      link.addEventListener("click", function () {
        closeMobileNav();
      });
    });

    // 原生对话框（新建卡组等，M8-7）。
    setupDialogs();

    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape") {
        closeMobileNav();
      }
    });
  }

  // setupDialogs 把带 data-dialog-open 的按钮接到对应 <dialog>，并处理关闭与点击遮罩。
  // 这里不含任何用户文案：可访问名称来自模板（语言包）。模板在错误回显时给对话框加
  // open 属性（非模态），这里摘掉属性再以模态打开，保证遮罩与居中一致。
  function setupDialogs() {
    document.querySelectorAll("[data-dialog-open]").forEach(function (trigger) {
      trigger.addEventListener("click", function (e) {
        e.preventDefault();
        var dialog = document.getElementById(trigger.getAttribute("data-dialog-open"));
        if (dialog && typeof dialog.showModal === "function") {
          dialog.showModal();
        }
      });
    });

    document.querySelectorAll("[data-dialog-close]").forEach(function (btn) {
      btn.addEventListener("click", function (e) {
        e.preventDefault();
        var dialog = btn.closest("dialog");
        if (dialog) {
          dialog.close();
        }
      });
    });

    document.querySelectorAll("dialog").forEach(function (dialog) {
      // 点击对话框自身（内层面板之外）即视为点击遮罩，关闭。
      dialog.addEventListener("click", function (e) {
        if (e.target === dialog) {
          dialog.close();
        }
      });
      // 错误回显：模板已带 open 属性，升级为模态（Escape 与遮罩随之生效）。
      if (dialog.hasAttribute("open")) {
        dialog.removeAttribute("open");
        if (typeof dialog.showModal === "function") {
          dialog.showModal();
        }
      }
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", setupPageInteractions);
  } else {
    setupPageInteractions();
  }

  // ── Service Worker ─────────────────────────────────────────────
  if (!("serviceWorker" in navigator)) {
    return;
  }
  window.addEventListener("load", function () {
    navigator.serviceWorker.register("/sw.js").catch(function () {
      // 注册失败不影响页面功能：外壳缓存是增强项，不是答题的前提。
    });
  });
})();
