<script lang="ts">
  import type { Snippet } from 'svelte';
  import { X } from '@lucide/svelte';
  import { t } from '../../i18n';

  /**
   * A batch action bar that floats at the bottom of the viewport after list items are selected.
   *
   * Fixed position, out of the document flow: previously the batch buttons sat in a toolbar above the list, and selecting anything made the toolbar taller,
   * pushing the whole page below it downward. On desktop it corrects its horizontal position by the sidebar width (--app-sidebar-w set by AppShell)
   * so the bar centers on the content area rather than the whole window.
   */
  interface Props {
    count: number;
    onClear: () => void;
    testId?: string;
    /** Buttons inside the bar; use SelectionBar's own dark-styled button (see selectionBarButton). */
    children: Snippet;
  }

  // No errors are shown inside the bar: batch operations are confirmed in a dialog and failures stay in that dialog, while other results go through toast.
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
