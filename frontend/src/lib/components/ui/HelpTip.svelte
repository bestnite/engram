<script lang="ts">
  import { Popover } from 'bits-ui';
  import { CircleHelp } from '@lucide/svelte';
  import { t } from '../../i18n';
  import { cn } from './utils';

  /**
   * 指标旁的「这是什么意思？」说明：一个小问号按钮，点开后在浮层里显示一两句口径说明。
   *
   * 用 bits-ui 的 Popover 而不是 Tooltip：手机上没有悬停，纯悬停提示在触屏上根本打不开；
   * Popover 点按打开、点外部或按 Esc 关闭，触发器是原生 button，Enter/Space 天然可用。
   * 指针设备额外开启悬停打开（库在触摸事件上会忽略悬停），桌面上扫一眼就能看到说明。
   */
  interface Props {
    /** 已本地化的说明正文。 */
    text: string;
    /** 被说明的指标名（已本地化）；拼进按钮的 aria-label，读屏时知道问的是哪一项。 */
    label: string;
    testId?: string;
    class?: string;
  }

  let { text, label, testId, class: klass = '' }: Props = $props();

  let open = $state(false);
</script>

<Popover.Root bind:open>
  <Popover.Trigger
    type="button"
    data-testid={testId}
    aria-label={$t('help.trigger', { label })}
    openOnHover
    openDelay={250}
    class={cn(
      // 视觉上只是 14px 的图标，按钮本身 24px 方便手指点中；负外边距抵消多出的高度，不撑高所在行。
      'inline-flex size-6 -my-1 shrink-0 cursor-pointer items-center justify-center rounded-full align-middle text-muted-foreground/70 transition-colors hover:text-foreground data-[state=open]:text-foreground',
      klass
    )}
  >
    <CircleHelp class="size-3.5" aria-hidden="true" />
  </Popover.Trigger>
  <Popover.Portal>
    <Popover.Content
      aria-label={label}
      sideOffset={4}
      collisionPadding={16}
      class="z-50 w-max max-w-[min(20rem,calc(100vw-2rem))] rounded-lg border border-border bg-popover px-3 py-2 text-left text-xs leading-relaxed font-normal text-foreground shadow-lg shadow-black/5 outline-hidden data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95"
    >
      <!-- pre-line：多条规则的说明用换行分条，单句说明不受影响。 -->
      <p class="whitespace-pre-line" data-testid={testId ? `${testId}-text` : undefined}>{text}</p>
    </Popover.Content>
  </Popover.Portal>
</Popover.Root>
