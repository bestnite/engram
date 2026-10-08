<script lang="ts">
  import { t } from '../i18n';
  import { apiClient } from '../api';
  import type { MediaItem } from '../api';
  import Button from './ui/Button.svelte';

  // 媒体库选择器：数据来自 GET /api/v1/media 的同源 JSON，不再拉取已删除的
  // SSR HTML 片段。缩略预览直接用原图（服务端不生成缩略图），由 CSS 限尺寸 + loading="lazy"
  // 承担；每一页的翻页游标也来自 JSON 的 next_cursor。
  //
  // 只负责取数与展示：把选中项的 url（/media/<sha256>）交回宿主，由宿主把它以 Markdown
  // 图片语法插入当前聚焦字段（与上传路径共用同一段插入逻辑）。
  interface Props {
    /** 选中一项时回调，参数是可直接引用的媒体 url（/media/<sha256>）。 */
    onselect: (url: string) => void;
  }

  let { onselect }: Props = $props();

  let open = $state(false);
  let loading = $state(false);
  let failed = $state(false);
  let items = $state<MediaItem[]>([]);
  let cursor = $state('');
  let hasMore = $state(false);

  async function load(reset = false): Promise<void> {
    loading = true;
    failed = false;
    try {
      const page = await apiClient.listMedia(reset ? '' : cursor);
      items = reset ? page.items : [...items, ...page.items];
      cursor = page.next_cursor;
      hasMore = Boolean(page.next_cursor);
    } catch {
      failed = true;
    } finally {
      loading = false;
    }
  }

  async function toggle(): Promise<void> {
    open = !open;
    if (open && items.length === 0 && !loading) await load(true);
  }
</script>

<Button testId="media-picker-toggle" variant="outline" size="lg" onclick={toggle}>{$t('media.open')}</Button>
{#if open}
  <section data-testid="media-picker" class="rounded-xl border border-zinc-200 dark:border-zinc-700 p-4">
    <h2 class="text-lg font-semibold">{$t('media.heading')}</h2>
    {#if loading}<p role="status">{$t('media.loading')}</p>{/if}
    {#if failed}<p role="alert">{$t('media.failed')}</p><button type="button" onclick={() => load(!cursor)}>{$t('media.retry')}</button>{/if}
    {#if !loading && !failed && items.length === 0}<p>{$t('media.empty')}</p>{/if}
    {#if items.length > 0}
      <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
        {#each items as item (item.sha256)}
          <button data-testid="media-item-{item.sha256}" type="button" onclick={() => onselect(item.url)} class="overflow-hidden rounded-lg border border-zinc-200 dark:border-zinc-700 focus-visible:outline-2 focus-visible:outline-offset-2">
            <img src={item.url} alt={item.sha256} loading="lazy" class="h-24 w-full object-contain" />
            <span class="block truncate px-2 py-1 text-xs">{item.sha256}</span>
          </button>
        {/each}
      </div>
    {/if}
    {#if hasMore}<Button testId="media-next" variant="outline" size="lg" class="mt-3" disabled={loading} onclick={() => load(false)}>{$t('media.next')}</Button>{/if}
  </section>
{/if}
