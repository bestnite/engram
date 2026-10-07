<script lang="ts">
  import type { Snippet } from 'svelte';
  import { cn } from './utils';

  /**
   * 页面/区块面板的唯一样式来源：`card-elevated` + 圆角 + 内边距。
   * 全站此前把 `card-elevated rounded-2xl p-6 sm:p-8` 这组类名抄在十几个页面里，
   * 底色或圆角改一次要改十几处（DESIGN.md §8）。
   */
  interface Props {
    /** sm 用于卡内次级区块，md/lg 用于页面级面板。 */
    padding?: 'sm' | 'md' | 'lg';
    class?: string;
    testId?: string;
    children: Snippet;
  }

  let { padding = 'lg', class: klass = '', testId, children }: Props = $props();

  const pad = { sm: 'p-4', md: 'p-5', lg: 'p-6 sm:p-8' } as const;
</script>

<div class={cn('card-elevated rounded-2xl', pad[padding], klass)} data-testid={testId}>
  {@render children()}
</div>
