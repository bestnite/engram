<script lang="ts">
  import type { Snippet } from 'svelte';
  import { X } from '@lucide/svelte';
  import { t } from '../../i18n';

  /**
   * 选中列表项后浮在视口底部的批量操作条。
   *
   * 固定定位、不占文档流：此前批量按钮插在列表上方的工具栏里，一选中就把工具栏撑高，
   * 下面整页内容跟着往下跳。桌面端按侧边栏宽度（AppShell 设置的 --app-sidebar-w）
   * 校正水平位置，使操作条居中于内容区而不是整个窗口。
   */
  interface Props {
    count: number;
    onClear: () => void;
    testId?: string;
    /** 操作条里的按钮；用 SelectionBar 自带的深色样式按钮（见 selectionBarButton）。 */
    children: Snippet;
  }

  // 操作条里不显示错误：批量操作在对话框里确认，失败留在对话框内，其余结果走 toast。
  let { count, onClear, testId, children }: Props = $props();
</script>

{#if count > 0}
  <div
    role="toolbar"
    aria-label={$t('selection.toolbar')}
    data-testid={testId}
    class="fixed bottom-6 left-1/2 z-40 flex max-w-[calc(100vw-2rem)] -translate-x-1/2 items-center gap-1 rounded-xl bg-primary p-1.5 text-primary-foreground shadow-2xl shadow-black/25 duration-200 animate-in fade-in-0 slide-in-from-bottom-4 md:left-[calc(50%+var(--app-sidebar-w,0px)/2)]"
  >
    <span class="whitespace-nowrap px-2.5 text-[13px] tabular-nums">{$t('selection.count', { count })}</span>
    <span class="h-5 w-px bg-primary-foreground/20" aria-hidden="true"></span>
    <div class="flex min-w-0 items-center gap-0.5 overflow-x-auto">
      {@render children()}
    </div>
    <span class="h-5 w-px bg-primary-foreground/20" aria-hidden="true"></span>
    <button
      type="button"
      onclick={onClear}
      class="inline-flex size-8 shrink-0 items-center justify-center rounded-md text-primary-foreground/60 transition-colors hover:bg-primary-foreground/10 hover:text-primary-foreground cursor-pointer"
      aria-label={$t('selection.clear')}
      title={$t('selection.clear')}
    >
      <X class="size-4" aria-hidden="true" />
    </button>
  </div>
{/if}
