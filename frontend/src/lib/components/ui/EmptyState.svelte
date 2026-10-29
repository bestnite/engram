<script lang="ts">
  import type { Component, Snippet } from 'svelte';
  import { cn } from './utils';

  /**
   * The placeholder a list or page shows when it has nothing to show: a dashed frame, a muted icon naming what is missing,
   * one line of text, and optionally the action that fills it (for example "create a deck").
   *
   * Ten views had their own copy of the dashed frame with only a grey line inside. The icon tells at a glance which kind of
   * thing is absent before the text is read; it is decorative, so it stays hidden from screen readers and the text alone
   * carries the meaning.
   */
  interface Props {
    icon: Component<{ class?: string; strokeWidth?: number; 'aria-hidden'?: boolean | 'true' | 'false' }>;
    text: string;
    testId?: string;
    class?: string;
    /** The action that fills the empty place, rendered below the text. */
    children?: Snippet;
  }

  let { icon: Icon, text, testId, class: klass = '', children }: Props = $props();
</script>

<div data-testid={testId} class={cn('rounded-lg border border-dashed border-border py-16 text-center', klass)}>
  <Icon class="mx-auto mb-3 size-8 text-muted-foreground/60" strokeWidth={1.5} aria-hidden="true" />
  <p class="text-sm text-muted-foreground">{text}</p>
  {#if children}
    <div class="mt-4">{@render children()}</div>
  {/if}
</div>
