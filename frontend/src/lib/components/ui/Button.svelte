<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { HTMLButtonAttributes } from 'svelte/elements';
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
    class: klass = '',
    testId,
    label,
    title,
    onclick,
    children,
  }: Props = $props();

  const classes = $derived(cn(buttonVariants({ variant, size }), klass));
</script>

{#if href}
  <a {href} class={classes} data-testid={testId} aria-label={label} {title}>
    {@render children?.()}
  </a>
{:else}
  <button
    {type}
    {disabled}
    class={classes}
    data-testid={testId}
    aria-label={label}
    {title}
    onclick={onclick}
  >
    {@render children?.()}
  </button>
{/if}
