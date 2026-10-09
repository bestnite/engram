<script lang="ts">
  /**
   * 加载占位（骨架屏）。
   *
   * 加载态用骨架屏而不是空白或「加载中…」文字。文案仍留在 DOM 里（`sr-only`）：
   * 屏幕阅读器照旧宣布「正在加载」，测试也据此断言语义而不是断言某个像素块。
   *
   * 骨架屏在 150ms 后才淡入（app.css 的 skeleton-reveal）：快速返回的请求不会让它闪一下。
   *
   * 这是脉冲占位的**唯一**实现：视图里再写一份 `skeleton-block` 就等于两份会漂移的样式。
   */
  interface Props {
    testId: string;
    label: string;
    /** lines＝一段标题加若干行正文（详情型内容）；cards＝重复的卡片（列表型内容）。 */
    variant?: 'lines' | 'cards';
    /** 正文行数（lines）或卡片个数（cards）。 */
    lines?: number;
    count?: number;
    /** cards 形态的列数；1 为单列。 */
    columns?: number;
  }

  let { testId, label, variant = 'lines', lines = 3, count = 4, columns = 2 }: Props = $props();

  // 行宽递减，读起来像一段正文，而不是几根等长条纹。
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
