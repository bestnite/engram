<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { HTMLButtonAttributes } from 'svelte/elements';
  import { LoaderCircle } from '@lucide/svelte';
  import { cn } from './utils';
  import { buttonVariants } from './variants';

  type Variant = 'primary' | 'outline' | 'ghost' | 'danger' | 'danger-outline';
  type Size = 'xs' | 'sm' | 'md' | 'lg' | 'icon';

  interface Props {
    /** href 存在时渲染成 <a>（导航类按钮），否则渲染 <button>。 */
    href?: string;
    variant?: Variant;
    size?: Size;
    type?: HTMLButtonAttributes['type'];
    disabled?: boolean;
    /**
     * 请求进行中：按钮禁用并在原位转圈，文字不换。进行中的唯一表示就是它，
     * 不再把文字换成「保存中…」；请求结果统一走 toast（见 ui/toast.ts）。
     */
    loading?: boolean;
    class?: string;
    testId?: string;
    /** 图标按钮必须给 label：它替代可见文字供读屏使用。 */
    label?: string;
    title?: string;
    onclick?: (event: MouseEvent) => void;
    children?: Snippet;
  }

  let {
    href,
    variant = 'primary',
    size = 'md',
    type = 'button',
    disabled = false,
    loading = false,
    class: klass = '',
    testId,
    label,
    title,
    onclick,
    children,
  }: Props = $props();

  // 进行中保持不透明、光标改为等待：它是「正在处理」，不是「不可用」。
  const classes = $derived(
    cn(buttonVariants({ variant, size }), klass, loading && 'relative disabled:cursor-wait disabled:opacity-100')
  );
</script>

{#if href}
  <a {href} class={classes} data-testid={testId} aria-label={label} {title}>
    {@render children?.()}
  </a>
{:else}
  <button
    {type}
    disabled={disabled || loading}
    aria-busy={loading || undefined}
    class={classes}
    data-testid={testId}
    aria-label={label}
    {title}
    onclick={onclick}
  >
    {#if loading}
      <!-- 文字用透明占位而不是移除：按钮宽度不变，读屏也仍能读到按钮名称。 -->
      <span class="inline-flex items-center justify-center gap-1.5 opacity-0">{@render children?.()}</span>
      <span class="absolute inset-0 flex items-center justify-center" aria-hidden="true">
        <LoaderCircle class="size-4 animate-spin" />
      </span>
    {:else}
      {@render children?.()}
    {/if}
  </button>
{/if}
