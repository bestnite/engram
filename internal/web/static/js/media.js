// 编辑器媒体上传（M2-9）：把上传成功返回的 /media/:id 以 Markdown 图片语法插入当前
// 聚焦的字段，并触发一次预览刷新。所有用户可见文案由模板经 data-* 传入，本文件不含文案。
//
// 为什么不用 htmx 直接换 DOM：上传成功要把引用插进 textarea 的光标处，纯 htmx 换片段
// 做不到；这里保留 htmx 负责预览刷新（派发 change 事件即可），上传本身走 fetch。
(function () {
  "use strict";

  // codeToData 把存储层返回的稳定英文 code 映射到表单上的本地化文案 data 属性。
  var codeToData = {
    media_too_large: "msgTooLarge",
    media_mime_not_allowed: "msgMime",
    media_magic_mismatch: "msgMagic"
  };

  function activeField(form) {
    var el = document.activeElement;
    if (el && el.matches && el.matches('[name^="field."]')) {
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
      insertRef(form, res.body.url);
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
})();