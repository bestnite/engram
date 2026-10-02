// 复习页交互（M3-5）：键盘快捷键、触屏滑动、离线提示与换卡后的状态复位。
// 保持零依赖、无 SPA：只操作 DOM 与 htmx 事件，逻辑足够小以随静态资源分发。
(function () {
  "use strict";

  function area() {
    return document.getElementById("review-area");
  }

  function answerEl() {
    return document.getElementById("review-answer");
  }

  function revealed() {
    var el = answerEl();
    return !!el && !el.classList.contains("hidden");
  }

  function revealAnswer() {
    var answer = answerEl();
    if (answer) {
      answer.classList.remove("hidden");
    }
    var ratings = document.getElementById("review-ratings");
    if (ratings) {
      ratings.classList.remove("hidden");
    }
    var show = document.getElementById("review-show-answer");
    if (show) {
      show.classList.add("hidden");
    }
  }

  function resetCardState() {
    var answer = answerEl();
    if (answer) {
      answer.classList.add("hidden");
    }
    var ratings = document.getElementById("review-ratings");
    if (ratings) {
      ratings.classList.add("hidden");
    }
    var show = document.getElementById("review-show-answer");
    if (show) {
      show.classList.remove("hidden");
    }
    var elapsed = document.getElementById("review-elapsed");
    if (elapsed) {
      elapsed.value = "";
    }
    startedAt = Date.now();
  }

  function clickRating(value) {
    var btn = document.querySelector('button[data-rating="' + value + '"]');
    if (btn) {
      btn.click();
    }
  }

  function submitAction(name) {
    var btn = document.querySelector('button[data-action="' + name + '"]');
    if (btn) {
      btn.click();
    }
  }

  function gotoEdit() {
    var el = area();
    var href = el && el.getAttribute("data-edit-href");
    if (href) {
      window.location.href = href;
    }
  }

  function onKeyDown(e) {
    var tag = e.target && e.target.tagName;
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") {
      return;
    }
    if (e.key === " " || e.key === "Enter") {
      e.preventDefault();
      if (!revealed()) {
        revealAnswer();
      }
      return;
    }
    if (e.key === "1" || e.key === "2" || e.key === "3" || e.key === "4") {
      if (!revealed()) {
        revealAnswer();
        return;
      }
      e.preventDefault();
      clickRating(e.key);
      return;
    }
    var key = (e.key || "").toLowerCase();
    if (key === "u") {
      submitAction("undo");
    } else if (key === "s") {
      submitAction("suspend");
    } else if (key === "b") {
      submitAction("bury");
    } else if (key === "e") {
      gotoEdit();
    }
  }

  var startedAt = Date.now();
  var startX = 0;
  var startY = 0;
  var tracking = false;

  // 落点在输入控件、按钮或链接上时不当作滑动，避免在作答输入框里误触发评分（M8-1）。
  function interactiveTarget(target) {
    if (!target || !target.tagName) {
      return false;
    }
    var tag = target.tagName;
    return (
      tag === "INPUT" ||
      tag === "TEXTAREA" ||
      tag === "SELECT" ||
      tag === "BUTTON" ||
      tag === "A"
    );
  }

  function onTouchStart(e) {
    if (!e.touches || e.touches.length !== 1 || interactiveTarget(e.target)) {
      tracking = false;
      return;
    }
    tracking = true;
    startX = e.touches[0].clientX;
    startY = e.touches[0].clientY;
  }

  function onTouchEnd(e) {
    if (!tracking || !e.changedTouches || e.changedTouches.length !== 1) {
      tracking = false;
      return;
    }
    tracking = false;
    var dx = e.changedTouches[0].clientX - startX;
    var dy = e.changedTouches[0].clientY - startY;
    if (Math.abs(dx) < 45 || Math.abs(dx) < Math.abs(dy)) {
      return;
    }
    // 未揭示答案时，任一横向滑动都是“显示答案”；已揭示后左滑 Again、右滑 Good。
    if (!revealed()) {
      revealAnswer();
      return;
    }
    if (dx < 0) {
      clickRating("1");
    } else {
      clickRating("3");
    }
  }

  // 评分请求发出前记录本张卡的耗时（DESIGN.md §9 的时间投入）。
  // 必须挂在 htmx:configRequest，而不是 htmx:beforeRequest：htmx 2.x 里 configRequest 触发时
  // 参数**已经收集完毕**，此时再去写隐藏字段已经晚了——请求体里带的仍是空值，服务端存 NULL，
  // 统计里卡组耗时恒为 0（实测踩过）。这里直接改 detail.parameters，并同步回隐藏字段。
  function markElapsed(evt) {
    var field = document.getElementById("review-elapsed");
    var params = evt && evt.detail && evt.detail.parameters;
    if (!field || !field.name || !params) {
      return;
    }
    // 只给带该字段的评分表单补值；撤销/暂停/埋藏/继续这些动作表单不需要耗时。
    var isFormData = typeof FormData !== "undefined" && params instanceof FormData;
    var present = isFormData ? params.has(field.name) : field.name in params;
    if (!present) {
      return;
    }
    var ms = String(Date.now() - startedAt);
    if (isFormData) {
      params.set(field.name, ms);
    } else {
      params[field.name] = ms;
    }
    field.value = ms;
  }

  function showOffline() {
    var banner = document.getElementById("review-offline");
    if (banner) {
      banner.classList.remove("hidden");
    }
  }

  document.addEventListener("keydown", onKeyDown);
  document.addEventListener("touchstart", onTouchStart, { passive: true });
  document.addEventListener("touchend", onTouchEnd, { passive: true });

  // 长按不弹上下文菜单、双击不缩放（M8-1、DESIGN.md §8.2）：连续点击是手机复习的
  // 高频动作，系统菜单一旦弹出就打断节奏。touch-action / user-select 在模板里用
  // touch-manipulation + select-none 生效，这里再挡掉 contextmenu 与 dblclick 的兜底。
  function inReviewArea(target) {
    return !!(target && target.closest && target.closest("#review-area"));
  }

  document.addEventListener("contextmenu", function (e) {
    if (inReviewArea(e.target)) {
      e.preventDefault();
    }
  });

  document.addEventListener("dblclick", function (e) {
    if (inReviewArea(e.target)) {
      e.preventDefault();
    }
  });

  document.addEventListener("click", function (e) {
    var target = e.target;
    if (target && target.closest && target.closest("#review-show-answer")) {
      revealAnswer();
    }
  });

  // 评分/动作请求配置参数时记录耗时；换卡后复位问答状态与计时。
  document.addEventListener("htmx:configRequest", markElapsed);
  document.addEventListener("htmx:afterSwap", function (e) {
    if (e.target && e.target.id === "review-area") {
      resetCardState();
    }
  });
  // 断网/请求无法送达时明确提示，不静默（DESIGN.md §8.2）。
  document.addEventListener("htmx:sendError", showOffline);
})();
