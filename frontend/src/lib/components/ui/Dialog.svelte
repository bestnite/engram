<script lang="ts">
  import type { Snippet } from 'svelte';
  import { Dialog as BitsDialog } from 'bits-ui';
  import { cn } from './utils';

  /**
   * 全站唯一的模态对话框（交互件用成熟组件库）。
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
    <BitsDialog.Overlay
      class="fixed inset-0 z-50 bg-overlay backdrop-blur-[2px] data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=closed]:animate-out data-[state=closed]:fade-out-0"
    />
    <BitsDialog.Content
      data-testid={testId}
      class={cn(
        'fixed left-1/2 top-1/2 z-50 max-h-[90vh] w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-xl border border-border bg-popover p-6 text-foreground shadow-2xl shadow-black/10',
        // 进出场：淡入 + 轻微放大，时长短到不拖慢操作，但足以让弹窗「出现」而不是「闪现」。
        'duration-150 data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95',
        width[size],
        klass
      )}
    >
      <BitsDialog.Title class="text-base font-semibold text-foreground">
        {title}
      </BitsDialog.Title>
      {#if description}
        <BitsDialog.Description class="mt-1 text-sm text-muted-foreground">
          {description}
        </BitsDialog.Description>
      {/if}
      <div class="mt-4">
        {@render children()}
      </div>
    </BitsDialog.Content>
  </BitsDialog.Portal>
</BitsDialog.Root>
