<script lang="ts">
  /**
   * MediaPicker is the editor's "insert from library" dialog: it pages the readable media set and hands one item back to
   * the caller, which registers its deck-scoped name and inserts the markup at the field's saved caret.
   *
   * The list and its filters are the media library endpoint's own contract (the same one the global browser uses): the
   * picker only builds the query, pages by cursor and renders thumbnails. Every request goes through the session cookie,
   * so a thumbnail is loaded from the same-origin `/media/<sha256>` route; nothing here renders stored HTML.
   *
   * The dialog is the site's shared Dialog (bits-ui): focus trapping, Escape/overlay closing and the dialog ARIA wiring are
   * the library's job. Opening it moves focus out of the editing frame, which is exactly what the frame's saved caret is
   * for: the frame keeps the last selection, so the insert command still lands where the author was writing.
   */
  import { untrack } from 'svelte';
  import { t, localeStore } from '../i18n';
  import { apiClient } from '../api';
  import type { MediaItem, MediaPickerClass, MediaPickerFilters } from '../api';
  import { formatBytes } from '../format';
  import Dialog from './ui/Dialog.svelte';
  import Button from './ui/Button.svelte';
  import Select from './ui/Select.svelte';
  import Skeleton from './ui/Skeleton.svelte';
  import EmptyState from './ui/EmptyState.svelte';
  import { FileText, Image, Images, Music, Video } from '@lucide/svelte';

  interface Props {
    open?: boolean;
    /**
     * Public id of the deck being edited. It scopes the "this deck" filter and is never required: without it the picker
     * shows the whole readable set.
     */
    deckId?: string;
    /** Called with the chosen item; the caller registers the name and inserts the markup. Rejecting keeps the dialog open. */
    onchoose: (item: MediaItem) => Promise<void>;
    testId?: string;
  }

  let { open = $bindable(false), deckId = '', onchoose, testId = 'media-picker' }: Props = $props();

  /** Search debounce: a request per keystroke would make paging jump under the author's hands. */
  const SEARCH_DEBOUNCE_MS = 300;

  /** The class filter; `all` sends no class at all. */
  type ClassFilter = 'all' | MediaPickerClass;
  /** The deck filter: the whole readable set, or the deck being edited. */
  type DeckScope = 'all' | 'deck';

  const classOptions: Array<{ value: ClassFilter; label: string }> = [
    { value: 'all', label: 'media.picker.class.all' },
    { value: 'image', label: 'media.picker.class.image' },
    { value: 'audio', label: 'media.picker.class.audio' },
    { value: 'video', label: 'media.picker.class.video' },
    { value: 'font', label: 'media.picker.class.font' },
    { value: 'script', label: 'media.picker.class.script' },
    { value: 'style', label: 'media.picker.class.style' },
    { value: 'other', label: 'media.picker.class.other' },
  ];

  const deckOptions: Array<{ value: DeckScope; label: string }> = [
    { value: 'all', label: 'media.picker.scope.all' },
    { value: 'deck', label: 'media.picker.scope.deck' },
  ];

  let items = $state<MediaItem[]>([]);
  let nextCursor = $state('');
  let search = $state('');
  let classFilter = $state<ClassFilter>('all');
  let deckScope = $state<DeckScope>('all');
  /** First page is in flight: the list area shows the loading state instead of stale items. */
  let loading = $state(false);
  /** A later page is in flight: the list stays and only the "load more" button spins. */
  let loadingMore = $state(false);
  /** The first page failed; the list area offers a retry. */
  let failed = $state(false);
  /** A later page failed; the existing items stay and the footer offers a retry. */
  let moreFailed = $state(false);
  /** The sha of the item being registered, so a slow request blocks a second click without freezing the list. */
  let choosingSha = $state('');
  /** Only the latest query's response may land; every new query bumps this. */
  let generation = 0;
  let searchTimer: ReturnType<typeof setTimeout> | null = null;

  /** The request filters for the current controls; absent values are omitted so the server applies its default. */
  function currentFilters(): MediaPickerFilters {
    const filters: MediaPickerFilters = {};
    if (deckScope === 'deck' && deckId) filters.deck = deckId;
    if (classFilter !== 'all') filters.class = classFilter;
    const q = search.trim();
    if (q) filters.q = q;
    return filters;
  }

  /** Replaces the list with the first page for the current filters. */
  async function loadFirstPage(): Promise<void> {
    const request = ++generation;
    loading = true;
    failed = false;
    moreFailed = false;
    try {
      const page = await apiClient.listMediaForPicker('', currentFilters());
      if (request !== generation) return;
      items = page.items;
      nextCursor = page.next_cursor;
    } catch {
      if (request !== generation) return;
      items = [];
      nextCursor = '';
      failed = true;
    } finally {
      if (request === generation) loading = false;
    }
  }

  /** Appends the next page; a failure leaves the current items in place and offers a retry. */
  async function loadMore(): Promise<void> {
    if (!nextCursor || loading || loadingMore) return;
    const request = generation;
    loadingMore = true;
    moreFailed = false;
    try {
      const page = await apiClient.listMediaForPicker(nextCursor, currentFilters());
      if (request !== generation) return;
      items = [...items, ...page.items];
      nextCursor = page.next_cursor;
    } catch {
      if (request === generation) moreFailed = true;
    } finally {
      if (request === generation) loadingMore = false;
    }
  }

  function onSearchInput(event: Event): void {
    search = (event.currentTarget as HTMLInputElement).value;
    if (searchTimer) clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      searchTimer = null;
      void loadFirstPage();
    }, SEARCH_DEBOUNCE_MS);
  }

  function onClassChange(value: string): void {
    classFilter = value as ClassFilter;
    void loadFirstPage();
  }

  function onDeckScopeChange(value: string): void {
    deckScope = value as DeckScope;
    void loadFirstPage();
  }

  /** Hands the item to the caller; a rejection keeps the dialog open so the author can choose again. */
  async function choose(item: MediaItem): Promise<void> {
    if (choosingSha !== '') return;
    choosingSha = item.sha256;
    try {
      await onchoose(item);
    } catch {
      // The caller shows the failure; the dialog stays open so another item can be chosen.
    } finally {
      choosingSha = '';
    }
  }

  /** A thumbnail is only fetched from the same-origin media route the API builds; anything else shows a class icon. */
  function thumbnailURL(item: MediaItem): string {
    return /^\/media\/[0-9a-f]{64}$/.test(item.url) ? item.url : '';
  }

  function isImage(item: MediaItem): boolean {
    return item.mime.startsWith('image/');
  }

  function isAudio(item: MediaItem): boolean {
    return item.mime.startsWith('audio/');
  }

  function isVideo(item: MediaItem): boolean {
    return item.mime.startsWith('video/');
  }

  const hasFilter = $derived(search.trim() !== '' || classFilter !== 'all' || (deckScope === 'deck' && deckId !== ''));

  // Opening resets the controls and loads the first page; closing abandons any in-flight query.
  $effect(() => {
    if (!open) {
      generation += 1;
      return;
    }
    untrack(() => {
      search = '';
      classFilter = 'all';
      deckScope = 'all';
      items = [];
      nextCursor = '';
      void loadFirstPage();
    });
    return () => {
      if (searchTimer) {
        clearTimeout(searchTimer);
        searchTimer = null;
      }
    };
  });
</script>

<Dialog
  bind:open
  title={$t('media.heading')}
  testId={testId}
  size="lg"
  class="max-w-2xl"
>
  <div class="space-y-3">
    <div class="flex flex-col gap-2 sm:flex-row sm:items-center">
      <input
        type="search"
        class="field-input block w-full text-sm sm:flex-1"
        data-testid="{testId}-search"
        aria-label={$t('media.picker.search')}
        placeholder={$t('media.picker.search')}
        spellcheck="false"
        value={search}
        oninput={onSearchInput}
      />
      <div class="flex gap-2">
        <Select
          class="w-36"
          size="sm"
          testId="{testId}-class"
          ariaLabel={$t('media.picker.class.label')}
          value={classFilter}
          onValueChange={onClassChange}
          options={classOptions.map((option) => ({ value: option.value, label: $t(option.label) }))}
        />
        {#if deckId}
          <Select
            class="w-36"
            size="sm"
            testId="{testId}-deck-scope"
            ariaLabel={$t('media.picker.scope.label')}
            value={deckScope}
            onValueChange={onDeckScopeChange}
            options={deckOptions.map((option) => ({ value: option.value, label: $t(option.label) }))}
          />
        {/if}
      </div>
    </div>

    {#if loading}
      <Skeleton testId="{testId}-loading" label={$t('media.loading')} variant="cards" count={6} columns={3} />
    {:else if failed}
      <div class="rounded-lg border border-dashed border-border py-12 text-center" data-testid="{testId}-error">
        <p class="text-sm text-muted-foreground">{$t('media.failed')}</p>
        <div class="mt-3">
          <Button type="button" variant="outline" size="sm" testId="{testId}-retry" onclick={() => void loadFirstPage()}>
            {$t('media.retry')}
          </Button>
        </div>
      </div>
    {:else if items.length === 0}
      <EmptyState
        icon={Images}
        testId="{testId}-empty"
        text={hasFilter ? $t('media.picker.no_results') : $t('media.empty')}
      />
    {:else}
      <ul class="grid max-h-[55vh] grid-cols-2 gap-2 overflow-y-auto sm:grid-cols-3 md:grid-cols-4" data-testid="{testId}-items">
        {#each items as item (item.sha256)}
          {@const thumb = thumbnailURL(item)}
          <li>
            <button
              type="button"
              class="group w-full cursor-pointer overflow-hidden rounded-lg border border-border bg-card text-left transition-colors hover:border-ring focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50"
              data-testid="{testId}-item-{item.sha256}"
              aria-label={$t('media.picker.insert', { name: item.sha256 })}
              disabled={choosingSha !== ''}
              onclick={() => void choose(item)}
            >
              <span class="flex h-20 items-center justify-center overflow-hidden bg-muted">
                {#if isImage(item) && thumb}
                  <img src={thumb} alt="" loading="lazy" class="size-full object-cover" data-testid="{testId}-thumb-{item.sha256}" />
                {:else if isAudio(item)}
                  <Music class="size-7 text-muted-foreground" aria-hidden="true" />
                {:else if isVideo(item)}
                  <Video class="size-7 text-muted-foreground" aria-hidden="true" />
                {:else if isImage(item)}
                  <Image class="size-7 text-muted-foreground" aria-hidden="true" />
                {:else}
                  <FileText class="size-7 text-muted-foreground" aria-hidden="true" />
                {/if}
              </span>
              <span class="block px-2 py-1.5">
                <span class="block truncate text-xs font-medium text-foreground">{item.mime}</span>
                <span class="block text-xs text-muted-foreground">{formatBytes(item.bytes, $localeStore)}</span>
              </span>
            </button>
          </li>
        {/each}
      </ul>
      <div class="flex items-center justify-between gap-2">
        <div class="min-w-0">
          {#if moreFailed}
            <p role="alert" class="text-sm text-destructive-foreground" data-testid="{testId}-more-error">
              {$t('media.picker.more_failed')}
            </p>
          {/if}
        </div>
        {#if nextCursor}
          <Button
            type="button"
            variant="outline"
            size="sm"
            testId="{testId}-next"
            loading={loadingMore}
            onclick={() => void loadMore()}
          >
            {$t('media.next')}
          </Button>
        {/if}
      </div>
    {/if}

    <div class="flex justify-end">
      <Button type="button" variant="ghost" size="sm" testId="{testId}-close" onclick={() => (open = false)}>
        {$t('media.picker.close')}
      </Button>
    </div>
  </div>
</Dialog>
