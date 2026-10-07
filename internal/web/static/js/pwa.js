// PWA 外壳注册（M8-2）、暗色模式（Dark Mode）与移动端导航交互。
// 零依赖、无构建链；不触碰任何答题数据（DESIGN.md §8.5）。
//
// 暗色模式分工：**切换按钮在 SPA 侧**（`frontend/src/lib/theme.ts` + NavHeader），因为
// SPA 是客户端渲染的，而本文件的绑定只在加载期跑一次、拿不到之后才挂载的按钮。本文件只负责
// 「加载期应用一次 + 用户未做选择时跟随系统偏好变化」。
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
    var root = document.documentElement;
    if (isDark) {
      root.classList.add("dark");
    } else {
      root.classList.remove("dark");
    }
    // color-scheme 与画布底色必须与首帧的内联引导（internal/web/theme.go）逐字一致：
    // 只切类名会让原生控件配色与画布底色停留在切换前的状态。
    root.style.colorScheme = isDark ? "dark" : "light";
    root.style.backgroundColor = isDark ? "#09090b" : "#f8fafc";
    var meta = document.querySelector('meta[name="theme-color"]');
    if (meta) {
      meta.setAttribute("content", isDark ? "#09090b" : "#ffffff");
    }
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

    // 自定义下拉菜单（全站通用组件）。
    setupCustomSelects();

    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape") {
        closeMobileNav();
      }
    });
  }

  // setupCustomSelects 实现自定义下拉框的展开/收起、选项高亮、表单/HTMX 联动与键盘无障碍。
  function setupCustomSelects() {
    function closeAllSelects(except) {
      document.querySelectorAll("[data-custom-select]").forEach(function (container) {
        if (container !== except) {
          var menu = container.querySelector("[data-select-menu]");
          var chevron = container.querySelector(".chevron");
          var btn = container.querySelector("[data-select-trigger]");
          if (menu) menu.classList.add("hidden");
          if (chevron) chevron.classList.remove("rotate-180");
          if (btn) btn.setAttribute("aria-expanded", "false");
        }
      });
    }

    function selectOption(container, option) {
      var val = option.getAttribute("data-value") || "";
      var labelSpan = option.querySelector("span");
      var label = labelSpan ? labelSpan.textContent.trim() : option.textContent.trim();

      var input = container.querySelector('input[type="hidden"]');
      var displaySpan = container.querySelector("[data-select-label]");
      var menu = container.querySelector("[data-select-menu]");
      var chevron = container.querySelector(".chevron");
      var btn = container.querySelector("[data-select-trigger]");

      if (input && input.value !== val) {
        input.value = val;
        input.dispatchEvent(new Event("input", { bubbles: true }));
        input.dispatchEvent(new Event("change", { bubbles: true }));
      }
      if (displaySpan) {
        displaySpan.textContent = label;
      }

      container.querySelectorAll('[role="option"]').forEach(function (opt) {
        var isSel = (opt === option);
        opt.setAttribute("aria-selected", isSel ? "true" : "false");
        var check = opt.querySelector(".check-icon");
        opt.classList.toggle("select-option-active", isSel);
        opt.classList.toggle("text-zinc-600", !isSel);
        opt.classList.toggle("dark:text-zinc-400", !isSel);
        if (check) check.classList.toggle("hidden", !isSel);
      });

      if (menu) menu.classList.add("hidden");
      if (chevron) chevron.classList.remove("rotate-180");
      if (btn) {
        btn.setAttribute("aria-expanded", "false");
        btn.focus();
      }
    }

    document.addEventListener("click", function (e) {
      var trigger = e.target.closest("[data-select-trigger]");
      if (trigger) {
        e.preventDefault();
        var container = trigger.closest("[data-custom-select]");
        if (!container) return;
        var menu = container.querySelector("[data-select-menu]");
        var chevron = container.querySelector(".chevron");
        var isOpen = menu && !menu.classList.contains("hidden");

        closeAllSelects(container);

        if (menu) {
          if (isOpen) {
            menu.classList.add("hidden");
            if (chevron) chevron.classList.remove("rotate-180");
            trigger.setAttribute("aria-expanded", "false");
          } else {
            menu.classList.remove("hidden");
            if (chevron) chevron.classList.add("rotate-180");
            trigger.setAttribute("aria-expanded", "true");
            var sel = container.querySelector('[role="option"][aria-selected="true"]');
            if (sel) {
              sel.scrollIntoView({ block: "nearest" });
            }
          }
        }
        return;
      }

      var option = e.target.closest('[role="option"]');
      if (option) {
        e.preventDefault();
        var container = option.closest("[data-custom-select]");
        if (container) {
          selectOption(container, option);
        }
        return;
      }

      if (!e.target.closest("[data-custom-select]")) {
        closeAllSelects();
      }
    });

    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape") {
        closeAllSelects();
        return;
      }

      var target = e.target;
      var container = target.closest ? target.closest("[data-custom-select]") : null;
      if (!container) return;

      var menu = container.querySelector("[data-select-menu]");
      var trigger = container.querySelector("[data-select-trigger]");
      var chevron = container.querySelector(".chevron");
      var isOpen = menu && !menu.classList.contains("hidden");
      var options = Array.prototype.slice.call(container.querySelectorAll('[role="option"]'));
      if (options.length === 0) return;

      var currentIndex = -1;
      for (var i = 0; i < options.length; i++) {
        if (options[i].getAttribute("aria-selected") === "true") {
          currentIndex = i;
          break;
        }
      }
      if (currentIndex === -1) currentIndex = 0;

      if (trigger && (trigger === target || trigger.contains(target))) {
        if (!isOpen) {
          if (e.key === "ArrowDown" || e.key === "ArrowUp" || e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            menu.classList.remove("hidden");
            if (chevron) chevron.classList.add("rotate-180");
            trigger.setAttribute("aria-expanded", "true");
            if (options[currentIndex]) options[currentIndex].scrollIntoView({ block: "nearest" });
            return;
          }
        } else {
          if (e.key === "ArrowDown") {
            e.preventDefault();
            var nextIdx = (currentIndex + 1) % options.length;
            selectOption(container, options[nextIdx]);
            menu.classList.remove("hidden");
            if (chevron) chevron.classList.add("rotate-180");
            trigger.setAttribute("aria-expanded", "true");
            options[nextIdx].scrollIntoView({ block: "nearest" });
          } else if (e.key === "ArrowUp") {
            e.preventDefault();
            var prevIdx = (currentIndex - 1 + options.length) % options.length;
            selectOption(container, options[prevIdx]);
            menu.classList.remove("hidden");
            if (chevron) chevron.classList.add("rotate-180");
            trigger.setAttribute("aria-expanded", "true");
            options[prevIdx].scrollIntoView({ block: "nearest" });
          } else if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            menu.classList.add("hidden");
            if (chevron) chevron.classList.remove("rotate-180");
            trigger.setAttribute("aria-expanded", "false");
          } else if (e.key === "Tab") {
            closeAllSelects();
          }
        }
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
