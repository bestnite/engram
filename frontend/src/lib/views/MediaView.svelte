<script lang="ts">
  import { onMount } from 'svelte';
  import { t, localeStore } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { Deck, MediaClass, MediaItem, MediaListParams, MediaState } from '../api';
  import Button from '../components/ui/Button.svelte';
  import Select from '../components/ui/Select.svelte';
  import Badge from '../components/ui/Badge.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import EmptyState from '../components/ui/EmptyState.svelte';
  import type { SelectOption } from '../components/ui/options';
  import { formatBytes, formatDate } from '../format';
  import { FileText, Image as ImageIcon, Music, RefreshCw, Search, SearchX } from '@lucide/svelte';

  /**
   * Global media browser: every medium the signed-in user may read across all their decks, on one page.
   *
   * The page owns only presentation and filter state. The readable set, the reference state and cleanup_at are the
   * server's decisions (the listing endpoint computes them from the same definition the media collector reclaims by),
   * so this view never infers a state from the labels it happens to receive.
   */
  const pageSize = 24;
  const stateValues: MediaState[] = ['referenced', 'pending', 'restorable', 'unreferenced'];
  const classValues: MediaClass[] = ['image', 'audio', 'video', 'font', 'script', 'style', 'other'];

  let loading = $state(true);
  let error = $state<ApiClientError | Error | null>(null);
  let items = $state<MediaItem[]>([]);
  let nextCursor = $state('');
  let loadingMore = $state(false);
  let moreFailed = $state(false);

  // Deck candidates for the deck filter: the same visible deck list the rest of the app uses.
  let decks = $state<Deck[]>([]);

  // Filter state. queryInput runs ahead of appliedQuery while the search debounce is pending, so a request never
  // reflects a half-typed next filter; the select filters apply immediately.
  let deckFilter = $state('');
  let classFilter = $state('');
  let stateFilter = $state('');
  let queryInput = $state('');
  let appliedQuery = $state('');
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;

  // Every list load gets a generation: filter changes and paging can overlap, and a response that lands after a newer
  // load started must not overwrite the newer list.
  let generation = 0;

  const deckOptions = $derived<SelectOption[]>([
    { value: '', label: $t('media.browser.filter.all_decks') },
    ...decks.map((deck) => ({ value: deck.id, label: deck.name })),
  ]);
  const classOptions = $derived<SelectOption[]>([
    { value: '', label: $t('media.browser.filter.all_classes') },
    ...classValues.map((value) => ({ value, label: $t(`media.browser.class.${value}`) })),
  ]);
  const stateOptions = $derived<SelectOption[]>([
    { value: '', label: $t('media.browser.filter.all_states') },
    ...stateValues.map((value) => ({ value, label: $t(`media.browser.state.${value}`) })),
  ]);
  const hasFilter = $derived(
    deckFilter !== '' || classFilter !== '' || stateFilter !== '' || appliedQuery.trim() !== ''
  );

  /** The request parameters for the filters on screen. */
  function currentParams(): MediaListParams {
    const params: MediaListParams = { limit: pageSize };
    if (deckFilter) params.deck = deckFilter;
    if (classFilter) params.class = classFilter as MediaClass;
    if (stateFilter) params.state = stateFilter as MediaState;
    if (appliedQuery.trim()) params.q = appliedQuery.trim();
    return params;
  }

  /** Load the first page for the filters on screen, replacing the current list. */
  async function load(): Promise<void> {
    generation += 1;
    const current = generation;
    loading = true;
    error = null;
    moreFailed = false;
    try {
      const page = await apiClient.listMedia(currentParams());
      if (current !== generation) return;
      items = page.items;
      nextCursor = page.next_cursor;
    } catch (err) {
      if (current !== generation) return;
      error = err instanceof Error ? err : new Error(String(err));
      items = [];
      nextCursor = '';
    } finally {
      if (current === generation) loading = false;
    }
  }

  /** Append the next page; a failure keeps the current list and can be retried with the same button. */
  async function loadMore(): Promise<void> {
    if (!nextCursor || loading || loadingMore) return;
    const current = generation;
    loadingMore = true;
    moreFailed = false;
    try {
      const page = await apiClient.listMedia({ ...currentParams(), cursor: nextCursor });
      if (current !== generation) return;
      items = [...items, ...page.items];
      nextCursor = page.next_cursor;
    } catch {
      if (current !== generation) return;
      moreFailed = true;
    } finally {
      if (current === generation) loadingMore = false;
    }
  }

  /** The deck list only feeds the filter select; failing to load it must not turn the page into an error page. */
  async function loadDecks(): Promise<void> {
    try {
      decks = (await apiClient.getDecks()).decks;
    } catch {
      decks = [];
    }
  }

  /** Search input: refetch after the debounce, so not every keystroke hits the API. */
  function scheduleSearch(): void {
    if (debounceTimer) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      debounceTimer = null;
      appliedQuery = queryInput;
      void load();
    }, 300);
  }

  /** Select filter changed: apply immediately. */
  function applyFilters(): void {
    if (debounceTimer) {
      clearTimeout(debounceTimer);
      debounceTimer = null;
    }
    appliedQuery = queryInput;
    void load();
  }

  function resetFilters(): void {
    if (debounceTimer) {
      clearTimeout(debounceTimer);
      debounceTimer = null;
    }
    deckFilter = '';
    classFilter = '';
    stateFilter = '';
    queryInput = '';
    appliedQuery = '';
    void load();
  }

  const stateBadgeVariant: Record<MediaState, 'success' | 'warning' | 'info' | 'neutral'> = {
    referenced: 'success',
    pending: 'warning',
    restorable: 'info',
    unreferenced: 'neutral',
  };

  function isImage(mime: string): boolean {
    return mime.startsWith('image/');
  }

  function isAudio(mime: string): boolean {
    return mime.startsWith('audio/');
  }

  function isVideo(mime: string): boolean {
    return mime.startsWith('video/');
  }

  /** All deck-scoped names of an item, or the explicit "no visible name" copy. */
  function namesText(item: MediaItem): string {
    return item.names.length > 0 ? item.names.join(', ') : $t('media.browser.no_names');
  }

  /** The visible decks the medium belongs to, or the explicit "no visible deck" copy. */
  function decksText(item: MediaItem): string {
    return item.decks.length > 0 ? item.decks.map((deck) => deck.name).join(', ') : $t('media.browser.no_decks');
  }

  /**
   * The cleanup line. Only a pending medium has a cleanup time; an elapsed grace is still pending until a sweep runs,
   * and the page says so instead of pretending the bytes are gone.
   */
  function cleanupText(item: MediaItem): string {
    if (!item.cleanup_at) return $t('media.browser.cleanup.none');
    const at = new Date(item.cleanup_at);
    if (Number.isNaN(at.getTime())) return item.cleanup_at;
    if (at.getTime() <= Date.now()) return $t('media.browser.cleanup.overdue');
    return $t('media.browser.cleanup.pending', { date: formatDate(at, $localeStore) });
  }

  onMount(() => {
    void loadDecks();
    void load();
    return () => {
      if (debounceTimer) clearTimeout(debounceTimer);
      generation += 1;
    };
  });
</script>

<Page testId="media-page">
  <PageHeader title={$t('media.browser.title')} description={$t('media.browser.description')} testId="media-title" />

  <form
    onsubmit={(event) => event.preventDefault()}
    class="mb-4 flex flex-wrap items-end gap-2"
    data-testid="media-filters"
  >
    <label class="relative flex w-full items-center sm:w-64">
      <Search class="pointer-events-none absolute left-2.5 size-4 text-muted-foreground" aria-hidden="true" />
      <input
        type="search"
        data-testid="media-search"
        aria-label={$t('media.browser.search_label')}
        placeholder={$t('media.browser.search_placeholder')}
        bind:value={queryInput}
        oninput={scheduleSearch}
        class="field-input w-full pl-8 text-sm"
      />
    </label>
    <div class="w-full sm:w-44">
      <Select
        testId="media-filter-deck"
        ariaLabel={$t('media.browser.filter.deck')}
        options={deckOptions}
        bind:value={deckFilter}
        onValueChange={applyFilters}
        allowDeselect={false}
      />
    </div>
    <div class="w-full sm:w-40">
      <Select
        testId="media-filter-class"
        ariaLabel={$t('media.browser.filter.class')}
        options={classOptions}
        bind:value={classFilter}
        onValueChange={applyFilters}
        allowDeselect={false}
      />
    </div>
    <div class="w-full sm:w-44">
      <Select
        testId="media-filter-state"
        ariaLabel={$t('media.browser.filter.state')}
        options={stateOptions}
        bind:value={stateFilter}
        onValueChange={applyFilters}
        allowDeselect={false}
      />
    </div>
    {#if hasFilter}
      <Button variant="ghost" size="lg" testId="media-filter-reset" onclick={resetFilters}>
        {$t('media.browser.filter.reset')}
      </Button>
    {/if}
  </form>

  {#if loading}
    <Skeleton testId="media-loading" label={$t('media.browser.loading')} variant="cards" count={6} columns={3} />
  {:else if error}
    <div data-testid="media-failed" class="space-y-3 py-16 text-center">
      <p class="text-sm font-medium text-foreground">{$t('media.browser.failed')}</p>
      <Button testId="media-retry" variant="outline" size="lg" onclick={() => void load()}>
        <RefreshCw class="size-4" aria-hidden="true" />
        <span>{$t('media.browser.retry')}</span>
      </Button>
    </div>
  {:else if items.length === 0}
    <EmptyState
      testId="media-empty"
      icon={hasFilter ? SearchX : ImageIcon}
      text={hasFilter ? $t('media.browser.empty_filter') : $t('media.browser.empty')}
    />
  {:else}
    <ul data-testid="media-grid" class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
      {#each items as item (item.sha256)}
        <li
          data-testid="media-item"
          data-sha={item.sha256}
          class="flex min-w-0 flex-col overflow-hidden rounded-xl border border-border bg-card"
        >
          <div class="flex h-36 items-center justify-center bg-muted/40">
            {#if isImage(item.mime)}
              <!-- The thumbnail is the existing public media URL; the bytes are served only to a caller who may read them. -->
              <img
                src={item.url}
                alt=""
                loading="lazy"
                data-testid="media-thumbnail"
                class="h-full w-full object-contain"
              />
            {:else if isAudio(item.mime)}
              <!-- The URL is the same session-authorized media route the thumbnail uses, so the browser plays the bytes in place. -->
              <div class="flex w-full flex-col items-center gap-1.5 px-3">
                <Music class="size-6 text-muted-foreground/50" aria-hidden="true" />
                <audio
                  controls
                  preload="metadata"
                  src={item.url}
                  aria-label={$t('media.browser.preview.audio')}
                  data-testid="media-audio"
                  class="w-full"
                ></audio>
              </div>
            {:else if isVideo(item.mime)}
              <!-- svelte-ignore a11y_media_has_caption (stored video carries no caption track; the card frame has the same limit) -->
              <video
                controls
                preload="metadata"
                playsinline
                src={item.url}
                aria-label={$t('media.browser.preview.video')}
                data-testid="media-video"
                class="h-full w-full object-contain"
              ></video>
            {:else}
              <div class="flex flex-col items-center gap-2 px-4 text-center">
                <FileText class="size-8 text-muted-foreground/50" aria-hidden="true" />
                <a
                  href={item.url}
                  download
                  data-testid="media-download"
                  class="text-xs font-medium text-foreground underline underline-offset-2"
                >
                  {$t('media.browser.download')}
                </a>
              </div>
            {/if}
          </div>
          <div class="flex flex-1 flex-col gap-1.5 p-4">
            <div class="flex items-start justify-between gap-2">
              <p class="min-w-0 break-all text-sm font-medium text-foreground" data-testid="media-names">{namesText(item)}</p>
              <Badge variant={stateBadgeVariant[item.state]} testId="media-state">{$t(`media.browser.state.${item.state}`)}</Badge>
            </div>
            <p class="text-xs text-muted-foreground" data-testid="media-meta">
              {item.mime} · {formatBytes(item.bytes, $localeStore)}
            </p>
            <p class="text-xs text-muted-foreground" data-testid="media-created">
              {$t('media.browser.created', { date: formatDate(item.created_at, $localeStore) })}
            </p>
            <p class="text-xs text-muted-foreground" data-testid="media-decks">
              {$t('media.browser.decks')}: {decksText(item)}
            </p>
            <p class="text-xs text-muted-foreground" data-testid="media-cleanup">{cleanupText(item)}</p>
          </div>
        </li>
      {/each}
    </ul>
    {#if nextCursor}
      <div class="mt-6 flex flex-col items-center gap-2">
        <Button testId="media-load-more" variant="outline" size="lg" loading={loadingMore} onclick={() => void loadMore()}>
          {$t('media.browser.load_more')}
        </Button>
        {#if moreFailed}
          <p data-testid="media-load-more-failed" class="text-xs text-destructive-foreground">
            {$t('media.browser.load_more_failed')}
          </p>
        {/if}
      </div>
    {/if}
  {/if}
</Page>
