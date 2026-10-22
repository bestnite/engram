<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { HTMLButtonAttributes } from 'svelte/elements';
  import { LoaderCircle } from '@lucide/svelte';
  import { cn } from './utils';
  import { buttonVariants } from './variants';

  type Variant = 'primary' | 'outline' | 'ghost' | 'danger' | 'danger-outline';
  type Size = 'xs' | 'sm' | 'md' | 'lg' | 'icon';

  interface Props {
    /** When href is present it renders as <a> (navigation-style buttons), otherwise as <button>. */
    href?: string;
    variant?: Variant;
    size?: Size;
    type?: HTMLButtonAttributes['type'];
    disabled?: boolean;
    /**
     * Request in progress: the button is disabled and spins in place, keeping its text. That is the only in-progress signal;
     * the text is no longer swapped for "saving..."; results always go through toast (see ui/toast.ts).
     */
    loading?: boolean;
    class?: string;
    testId?: string;
    /** Icon-only buttons must have a label: it replaces visible text for screen readers. */
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

  // Keep it opaque and switch the cursor to wait while in progress: it is "working on it", not "unavailable".
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
