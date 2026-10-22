<script lang="ts">
  import { routeStore } from '../../router';

  /**
   * Page-level link tab bar: each tab is a link to a separate page (admin panel, personal settings).
   * Uses the same underline style as the in-page tabs on the deck detail page; laid out in one row, horizontally scrollable on narrow screens.
   * Highlighted when the current page or a descendant path matches.
   */
  interface Item {
    href: string;
    label: string;
    testId?: string;
  }

  interface Props {
    items: Item[];
    ariaLabel: string;
    testId?: string;
    /** A link highlighted only when the address matches exactly (used when it is also the path prefix of other tabs). */
    exact?: string[];
  }

  let { items, ariaLabel, testId, exact = [] }: Props = $props();

  function isActive(href: string, current: string): boolean {
    if (current === href) return true;
    return !exact.includes(href) && current.startsWith(href + '/');
  }
</script>

<nav data-testid={testId} aria-label={ariaLabel} class="mb-6 border-b border-border">
  <ul class="-mb-px flex gap-6 overflow-x-auto overflow-y-hidden">
    {#each items as item (item.href)}
      {@const active = isActive(item.href, $routeStore.path)}
      <li class="shrink-0">
        <a
          href={item.href}
          data-testid={item.testId}
          aria-current={active ? 'page' : undefined}
          class="block whitespace-nowrap border-b-2 py-2.5 text-sm transition-colors {active
            ? 'border-foreground font-medium text-foreground'
            : 'border-transparent text-muted-foreground hover:text-foreground'}"
        >
          {item.label}
        </a>
      </li>
    {/each}
  </ul>
</nav>
