// M8-1 触屏序列模拟：用零依赖的最小 DOM 替身在 Node 里加载 review.js，
// 回放“滑动揭示 → 左滑评分 → 长按 contextmenu”的真实事件序列。
//
// 目的：在无法开真机/浏览器的 CI 里，给出一段可复现的脚本化触屏证据。
// 用法：node review_touch_sim.mjs <path-to-review.js>
// 全部断言通过时退出码为 0；任一失败打印 FAIL 并以 1 退出。

import { readFileSync } from "node:fs";

const reviewPath = process.argv[2];
if (!reviewPath) {
  console.error("usage: node review_touch_sim.mjs <path-to-review.js>");
  process.exit(2);
}

let failures = 0;
function check(name, condition) {
  if (condition) {
    console.log(`ok   - ${name}`);
  } else {
    console.log(`FAIL - ${name}`);
    failures++;
  }
}

class ClassList {
  constructor(classes) {
    this.set = new Set(classes);
  }
  add(...names) {
    names.forEach((n) => this.set.add(n));
  }
  remove(...names) {
    names.forEach((n) => this.set.delete(n));
  }
  contains(name) {
    return this.set.has(name);
  }
}

class Element {
  constructor(tag, attrs = {}) {
    // 真实 DOM 里 tagName 恒为大写；review.js 的 interactiveTarget 依赖这一点。
    this.tagName = tag.toUpperCase();
    this.attrs = attrs;
    this.classList = new ClassList(attrs.class ? attrs.class.split(/\s+/) : []);
    this.listeners = {};
    this.clicks = 0;
    this.area = null; // 最近的可命中的 #review-area 祖先
  }
  addEventListener(type, fn) {
    (this.listeners[type] = this.listeners[type] || []).push(fn);
  }
  click() {
    this.clicks++;
  }
  getAttribute(name) {
    return Object.prototype.hasOwnProperty.call(this.attrs, name) ? this.attrs[name] : null;
  }
  closest(selector) {
    if (selector === "#review-area") {
      return this.area || (this.attrs.id === "review-area" ? this : null);
    }
    return null;
  }
}

// 元素注册表：模拟 getElementById 与 querySelector('button[data-rating="N"]')。
const byId = new Map();
const ratingButtons = new Map();

function register(el) {
  if (el.attrs.id) {
    byId.set(el.attrs.id, el);
  }
  if (el.attrs["data-rating"]) {
    ratingButtons.set(el.attrs["data-rating"], el);
  }
  return el;
}

const docListeners = {};
const document = {
  getElementById: (id) => byId.get(id) || null,
  querySelector: (selector) => {
    const m = /^button\[data-rating="(\d)"\]$/.exec(selector);
    if (m) {
      return ratingButtons.get(m[1]) || null;
    }
    return null;
  },
  addEventListener: (type, fn) => {
    (docListeners[type] = docListeners[type] || []).push(fn);
  },
  dispatch: (type, event) => {
    (docListeners[type] || []).forEach((fn) => fn(event));
  },
};

globalThis.document = document;

// 复习区域及其子元素，结构对应 review.templ 里的 id/class。
const area = register(new Element("div", { id: "review-area", class: "touch-manipulation select-none" }));
const card = register(new Element("article", { id: "review-card" }));
card.area = area;
const answer = register(new Element("div", { id: "review-answer", class: "mt-4 hidden" }));
const ratings = register(new Element("div", { id: "review-ratings", class: "mt-1 hidden" }));
const show = register(new Element("button", { id: "review-show-answer", type: "button" }));
const input = register(new Element("input", { id: "review-answer-input" }));
for (const value of ["1", "2", "3", "4"]) {
  register(new Element("button", { "data-rating": value, name: "rating", value }));
}

// 加载被测脚本（IIFE，只依赖 document / Date）。
const code = readFileSync(reviewPath, "utf8");
new Function(code)();

function touch(x, y, target) {
  return { touches: [{ clientX: x, clientY: y }], target };
}
function endTouch(x, y, target) {
  return { changedTouches: [{ clientX: x, clientY: y }], target };
}

// 1) 未揭示答案时右滑 = 显示答案。
document.dispatch("touchstart", touch(80, 300, card));
document.dispatch("touchend", endTouch(240, 305, card));
check("swipe while hidden reveals the answer", !answer.classList.contains("hidden"));
check("swipe while hidden shows the ratings", !ratings.classList.contains("hidden"));
check("swipe while hidden hides the show-answer button", show.classList.contains("hidden"));

// 2) 已揭示后左滑 = Again（评分 1），且不触发其它评分。
document.dispatch("touchstart", touch(260, 300, card));
document.dispatch("touchend", endTouch(90, 300, card));
check("swipe left after reveal rates Again once", ratingButtons.get("1").clicks === 1);
check("swipe left does not rate other grades", ["2", "3", "4"].every((v) => ratingButtons.get(v).clicks === 0));

// 3) 长按（contextmenu）在复习区域内被阻止，不弹系统菜单。
const menuEvent = {
  target: card,
  defaultPrevented: false,
  preventDefault() {
    this.defaultPrevented = true;
  },
};
document.dispatch("contextmenu", menuEvent);
check("long-press contextmenu inside review area is prevented", menuEvent.defaultPrevented === true);

// 4) 双击（dblclick）兜底也被阻止，避免双击缩放。
const dblEvent = {
  target: card,
  defaultPrevented: false,
  preventDefault() {
    this.defaultPrevented = true;
  },
};
document.dispatch("dblclick", dblEvent);
check("double-tap dblclick inside review area is prevented", dblEvent.defaultPrevented === true);

// 5) 滑动起点在输入控件上时不评分（避免作答输入框里误触发）。
ratings.classList.add("hidden"); // 复位成“未揭示”以外的干净状态无必要，这里只验证输入框保护。
document.dispatch("touchstart", touch(260, 300, input));
document.dispatch("touchend", endTouch(90, 300, input));
check("swipe starting on an input is ignored", ratingButtons.get("1").clicks === 1);

if (failures > 0) {
  console.error(`\n${failures} assertion(s) failed`);
  process.exit(1);
}
console.log("\nall touch-sequence assertions passed");
