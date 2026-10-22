<script lang="ts">
  import type { Snippet } from 'svelte';
  import { ChevronLeft } from '@lucide/svelte';
  import { cn } from './utils';

  /**
   * Page header: back link / title / one-line description / right-side actions, with the same structure and spacing site-wide.
   *
   * Only one primary button per page, placed in actions; other actions use outline or ghost.
   * The description is at most one line, and if it can be deleted it should be - the header is a signpost, not a manual.
   */
  interface Props {
    title: string;
    description?: string;
    /** data-testid of the title (tests locate a page by its title). */
    testId?: string;
    back?: { href: string; label: string; testId?: string };
    class?: string;
    /** Extra content below the title and after the description (e.g. a row of stats). */
    meta?: Snippet;
    /** Custom title area (e.g. an inline-editable title and description); when given it replaces the default rendering of title and description. */
    heading?: Snippet;
    actions?: Snippet;
  }

  let { title, description, testId, back, class: klass = '', meta, heading, actions }: Props = $props();
</script>

<header class={cn('mb-6 space-y-3', klass)}>
  {#if back}
    <a
      href={back.href}
      data-testid={back.testId}
      class="-ml-1 inline-flex items-center gap-0.5 rounded-md px-1 py-0.5 text-[13px] text-muted-foreground transition-colors hover:text-foreground"
    >
      <ChevronLeft class="size-4" aria-hidden="true" />
      {back.label}
    </a>
  {/if}
  <div class="flex flex-wrap items-end justify-between gap-x-6 gap-y-3">
    <div class="min-w-0">
      {#if heading}
        {@render heading()}
      {:else}
        <h1 class="text-2xl font-semibold tracking-tight text-foreground" data-testid={testId}>{title}</h1>
        {#if description}
          <p class="mt-1 text-sm text-muted-foreground">{description}</p>
        {/if}
      {/if}
      {#if meta}
        <div class="mt-3">{@render meta()}</div>
      {/if}
    </div>
    {#if actions}
      <div class="flex flex-wrap items-center gap-2">{@render actions()}</div>
    {/if}
  </div>
</header>
