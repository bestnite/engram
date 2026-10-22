<script lang="ts">
  import type { Snippet } from 'svelte';
  import { cn } from './utils';

  /**
   * One section of a settings page: title and a one-line description on the left, controls on the right; only a rule between sections.
   *
   * Replaces "one card per group of settings": cards stack label, input and button top to bottom, and with four or five cards nested on a page
   * the hierarchy rests entirely on borders and looks messy. Two columns put the description and controls side by side, so a glance at the left column shows what the page can change.
   * On narrow screens the two columns stack.
   */
  interface Props {
    title: string;
    description?: string;
    testId?: string;
    class?: string;
    children: Snippet;
  }

  let { title, description, testId, class: klass = '', children }: Props = $props();
</script>

<section
  class={cn(
    // Count "first" only among sections: when a page header, notice row or other element comes before it, the first section gets no line above it either.
    'grid gap-x-10 gap-y-4 border-t border-border py-8 first-of-type:border-t-0 first-of-type:pt-2 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]',
    klass
  )}
  data-testid={testId}
>
  <div>
    <h2 class="text-sm font-semibold text-foreground">{title}</h2>
    {#if description}
      <p class="mt-1 text-[13px] leading-relaxed text-muted-foreground">{description}</p>
    {/if}
  </div>
  <div class="min-w-0">
    {@render children()}
  </div>
</section>
