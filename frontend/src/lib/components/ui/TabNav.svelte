<script lang="ts">
  import { routeStore } from '../../router';

  /**
   * 页面级的链接标签栏：每个标签是一个独立页面的链接（管理面板、个人设置）。
   * 与卡组详情页的页内标签同一种下划线样式；一行排开，窄屏横向滚动。
   * 当前页或其下级路径命中时高亮。
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
    /** 只在地址完全相同时才高亮的链接（它同时是其它标签的路径前缀时用）。 */
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
