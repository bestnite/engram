<script lang="ts">
  import type { Snippet } from 'svelte';
  import { cn } from './utils';

  /**
   * Page container: the single source of page width and horizontal padding across the site.
   *
   * Previously each view wrote its own max-w-md / 2xl / 4xl / 5xl / 6xl, so the content area's left and right edges jumped when switching pages.
   * Now all in-app pages share one width (default), and the left and right edges never move between pages;
   * only single-form pages before entering the app, such as login and registration, use narrow.
   * When a page needs a narrower reading width inside (e.g. a form), add max-w to the content instead of changing the page container.
   */
  interface Props {
    width?: 'narrow' | 'default';
    /** When nested inside another page (e.g. a tab of the deck detail page), do not apply width and padding again. */
    embedded?: boolean;
    class?: string;
    testId?: string;
    /** Whether to render a section or a div; pages that are semantically standalone blocks use section. */
    as?: 'div' | 'section';
    children: Snippet;
  }

  let { width = 'default', embedded = false, class: klass = '', testId, as = 'div', children }: Props = $props();

  const widths = {
    narrow: 'max-w-md pt-10 sm:pt-16',
    default: 'max-w-6xl',
  } as const;
</script>

<svelte:element
  this={as}
  class={cn(!embedded && 'mx-auto w-full px-4 pt-6 pb-16 sm:px-8 sm:pt-8', !embedded && widths[width], klass)}
  data-testid={testId}
>
  {@render children()}
</svelte:element>
