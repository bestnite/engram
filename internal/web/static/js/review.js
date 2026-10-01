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

  function onTouchStart(e) {
    if (!e.touches || e.touches.length !== 1) {
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

  function markElapsed() {
    var elapsed = document.getElementById("review-elapsed");
    if (elapsed) {
      elapsed.value = String(Date.now() - startedAt);
    }
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

  document.addEventListener("click", function (e) {
    var target = e.target;
    if (target && target.closest && target.closest("#review-show-answer")) {
      revealAnswer();
    }
  });

  // 评分/动作请求发出前记录耗时；换卡后复位问答状态与计时。
  document.addEventListener("htmx:beforeRequest", markElapsed);
  document.addEventListener("htmx:afterSwap", function (e) {
    if (e.target && e.target.id === "review-area") {
      resetCardState();
    }
  });
  // 断网/请求无法送达时明确提示，不静默（DESIGN.md §8.2）。
  document.addEventListener("htmx:sendError", showOffline);
})();
