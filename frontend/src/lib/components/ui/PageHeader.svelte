<script lang="ts">
  import type { Snippet } from 'svelte';
  import { ChevronLeft } from '@lucide/svelte';
  import { cn } from './utils';

  /**
   * 页头：返回链接 / 标题 / 一行说明 / 右侧操作区，全站同一结构与间距。
   *
   * 一页只放一个 primary 按钮，放在 actions 里；其余操作用 outline 或 ghost。
   * 说明最多一行，能删就删——页头是导向，不是使用说明书。
   */
  interface Props {
    title: string;
    description?: string;
    /** 标题的 data-testid（测试按标题定位页面）。 */
    testId?: string;
    back?: { href: string; label: string; testId?: string };
    class?: string;
    /** 标题下方、说明之后的补充内容（如统计数字一行）。 */
    meta?: Snippet;
    actions?: Snippet;
  }

  let { title, description, testId, back, class: klass = '', meta, actions }: Props = $props();
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
      <h1 class="text-2xl font-semibold tracking-tight text-foreground" data-testid={testId}>{title}</h1>
      {#if description}
        <p class="mt-1 text-sm text-muted-foreground">{description}</p>
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
