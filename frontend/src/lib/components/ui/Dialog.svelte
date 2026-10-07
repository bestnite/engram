<script lang="ts">
  import type { Snippet } from 'svelte';
  import { Dialog as BitsDialog } from 'bits-ui';
  import { cn } from './utils';

  /**
   * 全站唯一的模态对话框（DESIGN.md §8：交互件用成熟组件库）。
   *
   * 此前 4 处自写模态（新建卡组、删卡组、删预设、导出）各自手搓遮罩、ESC、焦点与
   * 点击外部关闭，行为与样式都不完全一致。底层换成 bits-ui 的 Dialog 后，
   * 焦点陷阱、ESC/遮罩关闭、滚动锁定、aria 关联都由库负责。
   */
  interface Props {
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
    title: string;
    description?: string;
    /** 与 title 分开给：读屏要先读标题、再读说明。 */
    testId?: string;
    size?: 'sm' | 'md' | 'lg';
    class?: string;
    children: Snippet;
  }

  let { open = $bindable(false), onOpenChange, title, description, testId, size = 'md', class: klass = '', children }: Props = $props();

  const width = { sm: 'max-w-sm', md: 'max-w-md', lg: 'max-w-lg' } as const;
</script>

<BitsDialog.Root bind:open {onOpenChange}>
  <BitsDialog.Portal>
    <BitsDialog.Overlay class="fixed inset-0 z-50 bg-black/50 backdrop-blur-xs" />
    <BitsDialog.Content
      data-testid={testId}
      class={cn(
        'card-elevated fixed left-1/2 top-1/2 z-50 max-h-[90vh] w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-2xl p-6 shadow-xl',
        width[size],
        klass
      )}
    >
      <BitsDialog.Title class="text-base font-bold text-zinc-900 dark:text-zinc-100">
        {title}
      </BitsDialog.Title>
      {#if description}
        <BitsDialog.Description class="mt-1 text-xs text-zinc-500 dark:text-zinc-400">
          {description}
        </BitsDialog.Description>
      {/if}
      <div class="mt-4">
        {@render children()}
      </div>
    </BitsDialog.Content>
  </BitsDialog.Portal>
</BitsDialog.Root>
