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

    // 导航项高亮
    var path = window.location.pathname;
    var links = document.querySelectorAll("header nav a, #mobile-nav-menu a");
    links.forEach(function (link) {
      var href = link.getAttribute("href");
      if (href) {
        if (href === path || (href !== "/" && path.indexOf(href) === 0)) {
          link.classList.add("bg-zinc-100", "dark:bg-zinc-800", "text-zinc-950", "dark:text-zinc-100", "font-semibold");
          link.classList.remove("text-zinc-600", "dark:text-zinc-400");
        }
        link.addEventListener("click", function () {
          closeMobileNav();
        });
      }
    });

    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape") {
        closeMobileNav();
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
