<script lang="ts">
  /**
   * Loading placeholder (skeleton screen).
   *
   * Loading state uses a skeleton screen instead of blank space or "loading..." text. The copy stays in the DOM (`sr-only`):
   * screen readers still announce "loading", and tests assert on semantics rather than on a particular pixel block.
   *
   * Skeletons fade in only after 150ms (skeleton-reveal in app.css), so a fast request never makes one flash.
   *
   * This is the **only** implementation of the pulsing placeholder: writing another `skeleton-block` in a view means two styles that will drift.
   */
  interface Props {
    testId: string;
    label: string;
    /** lines = a heading plus a few body rows (detail content); cards = repeated cards (list content). */
    variant?: 'lines' | 'cards';
    /** Number of body rows (lines) or cards (cards). */
    lines?: number;
    count?: number;
    /** Column count for the cards shape; 1 is a single column. */
    columns?: number;
  }

  let { testId, label, variant = 'lines', lines = 3, count = 4, columns = 2 }: Props = $props();

  // Row widths decrease, so it reads like a paragraph rather than a few equal stripes.
  const widths = ['100%', '92%', '78%', '64%', '86%'];
</script>

{#if variant === 'cards'}
  <div
    data-testid={testId}
    role="status"
    aria-live="polite"
    aria-busy="true"
    class="skeleton-reveal grid gap-4 {columns >= 2 ? 'grid-cols-1 sm:grid-cols-2' : ''}"
  >
    <span class="sr-only">{label}</span>
    {#each Array.from({ length: count }) as _, index (index)}
      <div class="p-5 rounded-xl border border-border space-y-3">
        <div class="skeleton-block h-5 w-1/3 rounded"></div>
        <div class="skeleton-block h-4 w-3/4 rounded"></div>
        <div class="skeleton-block h-4 w-1/2 rounded"></div>
      </div>
    {/each}
  </div>
{:else}
  <div data-testid={testId} role="status" aria-live="polite" aria-busy="true" class="skeleton-reveal space-y-3 py-2">
    <span class="sr-only">{label}</span>
    <div class="skeleton-block h-5 w-2/5 rounded-md"></div>
    {#each Array.from({ length: lines }) as _, index (index)}
      <div class="skeleton-block h-3.5 rounded-md" style="width: {widths[index % widths.length]}"></div>
    {/each}
  </div>
{/if}
