<script lang="ts">
  import { Toaster as Sonner } from 'svelte-sonner';

  /**
   * 全局轻提示（svelte-sonner）。操作结果（已删除、批量处理了几条、失败原因）用 toast 报告，
   * 不再往页面里插一行文字：插入的文字会把下面的列表往下推，正是「页面抖动」的来源之一。
   * 全站请求反馈的完整规则（进行中、结果、哪些留在原地）写在 ui/toast.ts。
   *
   * 颜色通过 sonner 暴露的 CSS 变量接到应用令牌上，深浅主题跟着 app.css 走。
   */
</script>

<Sonner
  position="bottom-right"
  closeButton
  style="--normal-bg: var(--popover); --normal-text: var(--foreground); --normal-border: var(--border); --border-radius: 0.625rem;"
  toastOptions={{ class: 'text-[13px] shadow-lg shadow-black/8' }}
/>
