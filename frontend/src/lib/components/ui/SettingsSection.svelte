<script lang="ts">
  import type { Snippet } from 'svelte';
  import { cn } from './utils';

  /**
   * 设置页的一个分区：左侧标题与一行说明，右侧控件；分区之间只有一条横线。
   *
   * 取代「每组设置一张卡片」：卡片把标签、输入框、按钮从上往下堆，一页四五张卡片互相嵌套时
   * 层级全靠边框区分，看起来很乱。左右两栏让说明与控件并排，扫一眼左栏就知道这页能改什么。
   * 窄屏下两栏自动上下排列。
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
    // 只按 section 计「第一个」：页头、提示行等其它元素在前面时，第一个分区上方同样不画线。
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
