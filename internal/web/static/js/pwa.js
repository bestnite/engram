// PWA shell registration (M8-2), dark mode and mobile navigation interactions.
// Zero dependencies, no build chain; touches no answer data whatsoever.
//
// Division of labor for dark mode: **the toggle button lives on the SPA side** (ThemeToggle and UserMenu in `frontend/src/lib/theme.ts` + `frontend/src/lib/components/shell/`), because
// the SPA is client-rendered, while this file's bindings run once at load time and cannot reach buttons mounted afterwards. This file only handles
// "apply once at load time + follow system preference changes while the user has made no choice".
(function () {
  "use strict";

  // ── dark mode management ───────────────────────────────────────
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
    // color-scheme and the canvas background must match the first-paint inline bootstrap (internal/web/theme.go) exactly:
    // switching only the class name leaves native control colors and the canvas background stuck at their pre-switch state.
    root.style.colorScheme = isDark ? "dark" : "light";
    root.style.backgroundColor = isDark ? "#09090b" : "#f8fafc";
    var meta = document.querySelector('meta[name="theme-color"]');
    if (meta) {
      meta.setAttribute("content", isDark ? "#09090b" : "#ffffff");
    }
  }

  // apply right away from the preference, to avoid a flash
  applyTheme(isDarkTheme());

  if (window.matchMedia) {
    window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", function (e) {
      if (!localStorage.getItem("engram-theme")) {
        applyTheme(e.matches);
      }
    });
  }

  // ── mobile navigation and page-ready enhancements ──────────────
  function setupPageInteractions() {
    applyTheme(isDarkTheme());

    // mobile collapsible navigation drawer
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

    // Navigation item highlighting is server-rendered (M8-7): no guessing the current page from the href any more — the old approach used
    // a `header nav a` selector, which counted the brand link too, so on "/" the brand got highlighted.
    // All that remains here is "collapse the mobile drawer after clicking any navigation link".
    var navLinks = document.querySelectorAll("header nav a, #mobile-nav-menu a");
    navLinks.forEach(function (link) {
      link.addEventListener("click", function () {
        closeMobileNav();
      });
    });

    // Native dialogs (new deck and the like, M8-7).
    setupDialogs();

    // Custom dropdown menus (a site-wide shared component).
    setupCustomSelects();

    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape") {
        closeMobileNav();
      }
    });
  }

  // setupCustomSelects implements expand/collapse, option highlighting, form/HTMX integration and keyboard accessibility for custom dropdowns.
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

  // setupDialogs connects buttons carrying data-dialog-open to the matching <dialog>, and handles closing and backdrop clicks.
  // No user-facing copy lives here: accessible names come from the templates (language packs). On error echo the template puts the
  // open attribute on the dialog (non-modal); this strips the attribute and opens it modally instead, so the backdrop and centering are consistent.
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
      // Clicking the dialog itself (outside the inner panel) counts as a backdrop click and closes it.
      dialog.addEventListener("click", function (e) {
        if (e.target === dialog) {
          dialog.close();
        }
      });
      // Error echo: the template already carries the open attribute, so upgrade it to modal (Escape and the backdrop then take effect).
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
      // A failed registration does not affect page functionality: shell caching is an enhancement, not a prerequisite for answering.
    });
  });
})();
