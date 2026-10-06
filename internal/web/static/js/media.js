// 编辑器媒体面（M2-9 及扩展）：把 /media/<sha256> 以 Markdown 图片语法插入当前聚焦的字段，
// 并触发一次预览刷新。两条来源共用同一段插入逻辑：
//   1. 上传控件（[data-media-upload]）上传成功后插入返回的 url；
//   2. 媒体选择器（[data-media-insert] 按钮）点击后用按钮上的 url 插入。
// 所有用户可见文案由模板经 data-* 传入，本文件不含文案。
//
// 为什么不用 htmx 直接换 DOM：插入要落在 textarea 的光标处，纯 htmx 换片段做不到；这里保留
// htmx 负责预览刷新（派发 change 事件即可）、选择器片段的拉取与翻页，插入本身走原生 DOM。
//
// 为什么用事件委托而不是 hx-on：hx-on 由 htmx 在运行时用 new Function 编译，会迫使 CSP 的
// script-src 长期保留 'unsafe-eval'（DESIGN.md §11）。委托监听不需要任何 eval。
(function () {
  "use strict";

  // codeToData 把存储层返回的稳定英文 code 映射到表单上的本地化文案 data 属性。
  var codeToData = {
    media_too_large: "msgTooLarge",
    media_mime_not_allowed: "msgMime",
    media_magic_mismatch: "msgMagic"
  };

  // lastField 记住最近一次聚焦的字段输入框：点击上传按钮或选择器按钮会把焦点从 textarea
  // 移走，若不记住就只剩「第一个字段」这个兜底，用户会看到引用插错位置。
  var lastField = null;
  document.addEventListener("focusin", function (e) {
    var el = e.target;
    if (el && el.matches && el.matches('[name^="field."]')) {
      lastField = el;
    }
  });

  // noteFormFor 找到引用应插入的卡片表单：字段输入框在卡片表单里，而上传控件/选择器是它的
  // 兄弟节点（不是子节点），所以要显式找 [data-note-form]。
  function noteFormFor(form) {
    if (form && form.closest) {
      var owner = form.closest("[data-note-form]");
      if (owner) {
        return owner;
      }
    }
    return document.querySelector("[data-note-form]") || form;
  }

  function activeField(form) {
    if (!form) {
      return null;
    }
    if (lastField && form.contains(lastField)) {
      return lastField;
    }
    var el = document.activeElement;
    if (el && el.matches && el.matches('[name^="field."]') && form.contains(el)) {
      return el;
    }
    return form.querySelector('[name^="field."]');
  }

  // insertRef 在光标处插入 Markdown 图片引用；随后派发 change，让 htmx 重新渲染预览。
  function insertRef(form, url) {
    var field = activeField(form);
    if (!field) {
      return;
    }
    var ref = "![](" + url + ")";
    var start = typeof field.selectionStart === "number" ? field.selectionStart : field.value.length;
    var end = typeof field.selectionEnd === "number" ? field.selectionEnd : field.value.length;
    field.value = field.value.slice(0, start) + ref + field.value.slice(end);
    var caret = start + ref.length;
    if (field.setSelectionRange) {
      field.setSelectionRange(caret, caret);
    }
    field.dispatchEvent(new Event("change", { bubbles: true }));
  }

  function messageFor(form, code) {
    var attr = codeToData[code] || "msgGeneric";
    return form.dataset[attr] || form.dataset.msgGeneric || "";
  }

  function showStatus(form, text, isError) {
    var box = form.querySelector("[data-media-status]");
    if (!box) {
      return;
    }
    box.textContent = text;
    box.classList.toggle("text-red-700", !!isError);
    box.classList.toggle("text-emerald-700", !isError);
  }

  function submit(form) {
    var input = form.querySelector('input[type="file"]');
    if (!input || !input.files || input.files.length === 0) {
      return;
    }
    fetch(form.getAttribute("action"), {
      method: "POST",
      body: new FormData(form),
      credentials: "same-origin"
    }).then(function (resp) {
      return resp.json().catch(function () { return {}; }).then(function (body) {
        return { ok: resp.ok, body: body };
      });
    }).then(function (res) {
      if (!res.ok) {
        var err = res.body.error || {};
        // 配额超限的文案要点名限额与已用量：具体数字只有服务端知道，直接采用它返回的
        // 本地化 message，而不是走静态 data-* 映射。
        var text = err.code === "media_quota_exceeded" && err.message
          ? err.message
          : messageFor(form, err.code);
        showStatus(form, text, true);
        return;
      }
      insertRef(noteFormFor(form), res.body.url);
      showStatus(form, form.dataset.msgInserted || "", false);
      input.value = "";
    }).catch(function () {
      showStatus(form, form.dataset.msgGeneric || "", true);
    });
  }

  document.addEventListener("submit", function (e) {
    var form = e.target;
    if (!form || !form.matches || !form.matches("[data-media-upload]")) {
      return;
    }
    e.preventDefault();
    submit(form);
  });

  // 媒体选择器：点击片段里的一项，把它携带的 /media/<sha256> 插入当前聚焦字段。
  // 与上传路径调用同一个 insertRef，不另写一套插入逻辑。
  document.addEventListener("click", function (e) {
    var target = e.target;
    if (!target || !target.closest) {
      return;
    }
    var item = target.closest("[data-media-insert]");
    if (!item) {
      return;
    }
    e.preventDefault();
    insertRef(noteFormFor(item), item.getAttribute("data-media-insert"));
  });
})();
