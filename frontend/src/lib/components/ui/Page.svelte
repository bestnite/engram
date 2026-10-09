<script lang="ts">
  import type { Snippet } from 'svelte';
  import { cn } from './utils';

  /**
   * 页面容器：全站页面宽度与外边距的唯一来源。
   *
   * 此前每个视图自己写 max-w-md / 2xl / 4xl / 5xl / 6xl，切换页面时内容区左右边缘跟着跳。
   * 现在应用内所有页面共用同一宽度（default），左右边缘在页面之间完全不动；
   * 只有登录、注册这类进入应用之前的单表单页面用 narrow。
   * 页面内部需要更窄的阅读宽度（如表单）时，在内容上加 max-w，不改页面容器。
   */
  interface Props {
    width?: 'narrow' | 'default';
    /** 嵌在别的页面里（如卡组详情的标签页）时不再套宽度与外边距。 */
    embedded?: boolean;
    class?: string;
    testId?: string;
    /** 渲染成 section 还是 div；语义上是独立区块的页面用 section。 */
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
