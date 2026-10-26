<script lang="ts">
  import Skeleton from './ui/Skeleton.svelte';
  import { t } from '../i18n';
  import { apiClient } from '../api';
  import type { MediaItem } from '../api';
  import { Music } from '@lucide/svelte';
  import Button from './ui/Button.svelte';

  // Media library picker: data comes from the same-origin JSON of GET /api/v1/media and no longer fetches the removed
  // SSR HTML fragment. Thumbnail previews use the original image directly (the server generates no thumbnails), handled by CSS size limits + loading="lazy"
  // and the paging cursor for each page also comes from the JSON's next_cursor.
  //
  // Only fetches and displays: it hands the selected item back to the host, which decides what to write into the
  // focused field — an image reference for a picture, the audio shortcode for a sound file. The mime travels with the
  // item because the host cannot see it otherwise, and an audio file rendered as an <img> is a broken picture.
  interface Props {
    /** Called when an item is selected; the item carries the media url (/media/<sha256>) and its mime type. */
    onselect: (item: MediaItem) => void;
    /** Only items whose mime starts with this prefix are listed, e.g. "audio/" for the audio shortcode panel. */
    mimePrefix?: string;
    /** Prefix for every data-testid value: `<prefix>-picker-toggle`, `<prefix>-picker`, `<prefix>-item-<sha256>`, `<prefix>-next`. */
    idPrefix?: string;
    /** Toggle button size; the shortcode panel uses the compact one. */
    size?: 'sm' | 'md' | 'lg';
    /** Toggle button label; defaults to the generic "choose from library". */
    openLabel?: string;
    /** Copy shown when a loaded page holds no item matching `mimePrefix`. */
    emptyLabel?: string;
  }

  let {
    onselect,
    mimePrefix = '',
    idPrefix = 'media',
    size = 'lg',
    openLabel = '',
    emptyLabel = '',
  }: Props = $props();

  let open = $state(false);
  let loading = $state(false);
  let failed = $state(false);
  let items = $state<MediaItem[]>([]);
  let cursor = $state('');
  let hasMore = $state(false);

  // Filtering is done on the loaded page rather than in the query: the endpoint pages by cursor only,
  // so a page can legitimately hold no matching item and the "load more" control stays reachable.
  const visible = $derived(items.filter((item) => item.mime.startsWith(mimePrefix)));

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

  function isAudio(item: MediaItem): boolean {
    return item.mime.startsWith('audio/');
  }
</script>

<div class="space-y-2">
  <Button testId="{idPrefix}-picker-toggle" variant="outline" {size} onclick={toggle}>
    {openLabel || $t('media.open')}
  </Button>
  {#if open}
    <section data-testid="{idPrefix}-picker" class="rounded-xl border border-input p-4">
      <h2 class="text-lg font-semibold">{$t('media.heading')}</h2>
      {#if loading}<Skeleton testId="{idPrefix}-loading" label={$t('media.loading')} lines={2} />{/if}
      {#if failed}<p role="alert">{$t('media.failed')}</p><button type="button" onclick={() => load(!cursor)}>{$t('media.retry')}</button>{/if}
      {#if !loading && !failed && items.length === 0}<p>{$t('media.empty')}</p>{/if}
      {#if !loading && !failed && items.length > 0 && visible.length === 0}
        <p data-testid="{idPrefix}-empty" class="text-sm text-muted-foreground">{emptyLabel || $t('media.empty')}</p>
      {/if}
      {#if visible.length > 0}
        <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
          {#each visible as item (item.sha256)}
            <button
              data-testid="{idPrefix}-item-{item.sha256}"
              type="button"
              onclick={() => onselect(item)}
              class="overflow-hidden rounded-lg border border-input focus-visible:outline-2 focus-visible:outline-offset-2"
            >
              {#if isAudio(item)}
                <!-- A sound file has no picture: the tile shows a sound mark and the mime type instead of a broken image. -->
                <span class="flex h-24 w-full flex-col items-center justify-center gap-1 bg-muted text-muted-foreground">
                  <Music class="size-6" aria-hidden="true" />
                  <span class="text-[11px]">{item.mime}</span>
                </span>
              {:else}
                <img src={item.url} alt={item.sha256} loading="lazy" class="h-24 w-full object-contain" />
              {/if}
              <span class="block truncate px-2 py-1 text-xs">{item.sha256}</span>
            </button>
          {/each}
        </div>
      {/if}
      {#if hasMore}
        <Button testId="{idPrefix}-next" variant="outline" size="lg" class="mt-3" disabled={loading} onclick={() => load(false)}>
          {$t('media.next')}
        </Button>
      {/if}
    </section>
  {/if}
</div>
