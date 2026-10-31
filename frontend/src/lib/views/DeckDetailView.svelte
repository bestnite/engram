<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { get } from 'svelte/store';
  import { navigate, replaceAddress, routeStore } from '../router';
  import { withReturn } from '../return-to';
  import { t } from '../i18n';
  import { fieldText } from '../format';
  import { apiClient, ApiClientError } from '../api';
  import type { Deck, DeckQueueCount, DeckTag, Note, BulkNotesResponse } from '../api';
  import DeckSharingView from './DeckSharingView.svelte';
  import DeckSettingsView from './DeckSettingsView.svelte';
  import Dialog from '../components/ui/Dialog.svelte';
  import Checkbox from '../components/ui/Checkbox.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Button from '../components/ui/Button.svelte';
  import Badge from '../components/ui/Badge.svelte';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import SelectionBar from '../components/ui/SelectionBar.svelte';
  import { listClasses, menuClasses, selectionBarButton } from '../components/ui/variants';
  import { toast } from '../components/ui/toast';
  import InlineEdit from '../components/InlineEdit.svelte';
  import CardFrame from '../components/CardFrame.svelte';
  import PackageExportOptions from '../components/PackageExportOptions.svelte';
  import { DropdownMenu } from 'bits-ui';
  import { ChevronLeft, ChevronRight, CirclePause, CirclePlay, Copy, Download, FileText, Link2, MoreHorizontal, Pause, Pencil, Play, Plus, RefreshCw, Search, SearchX, Tags, Trash2, X } from '@lucide/svelte';
  import EmptyState from '../components/ui/EmptyState.svelte';

  interface Props {
    /** Test injection: when provided, nothing is fetched on mount (same convention as HomeView and other views). */
    initialDeck?: Deck | null;
    initialNotes?: Note[];
    initialTotal?: number;
    initialLoading?: boolean;
  }

  let { initialDeck = null, initialNotes = [], initialTotal = 0, initialLoading = true }: Props = $props();

  // State variables (Svelte 5 runes)
  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  let error = $state<ApiClientError | Error | null>(null);
  // svelte-ignore state_referenced_locally
  let deck = $state<Deck | null>(initialDeck);
  // svelte-ignore state_referenced_locally
  let notes = $state<Note[]>(initialNotes);
  // svelte-ignore state_referenced_locally
  let total = $state(initialTotal);
  // The caller's progress through this deck; null until loaded or when the request fails, in which case the header omits it.
  let progress = $state<DeckQueueCount | null>(null);
  let page = $state(1);
  let activeTab = $state<'cards' | 'sharing' | 'settings'>('cards');
  // Opened tabs stay mounted and only toggle visibility: switching back and forth does not refetch, so the loading placeholder does not flash every time.
  let visitedTabs = $state<Record<string, boolean>>({ cards: true });

  // Per-row delete state
  // The note pending delete confirmation: the confirmation lives in a dialog rather than inserting text into the row (inserting would make the row taller and squeeze the text into wrapping).
  let noteToDelete = $state<Note | null>(null);
  let deletingNoteId = $state<string | null>(null);
  let deleteError = $state('');

  // Deck deletion state: the entry point is owner-only, and a successful delete leaves the page.
  let confirmDeleteOpen = $state(false);
  let deletingDeck = $state(false);
  let deleteDeckError = $state(false);

  // Export dialog state
  let showExportModal = $state(false);
  let exporting = $state(false);
  let exportError = $state(false);
  let includeMedia = $state(true);
  let includeProgress = $state(false);
  let includeReviews = $state(false);
  let includeWeights = $state(false);

  const perPage = 50;

  // Filter criteria: input takes effect immediately (there is no "apply" button), text input is debounced 300ms.
  // Deleted cards no longer offer a filter entry (the list only presents non-deleted content).
  // The filters and the page number live in the address as well (q, tag, page), so a reload, the
  // browser's back button and the card editor's return link all come back to the same list. They are read
  // once on mount; afterwards the page writes them back after every load.
  const initialQuery = get(routeStore).query;
  const initialPage = Math.max(1, Number.parseInt(initialQuery.page ?? '', 10) || 1);
  let queryInput = $state(initialQuery.q ?? '');
  let tagInput = $state(initialQuery.tag ?? '');
  // The filters of the list on screen. The inputs can run ahead of it while the debounce is pending, and an
  // address must describe the list the user sees, not the half-typed next one.
  let appliedFilters = $state({ q: '', tag: '' });
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;

  // Study by tag: tags are only selectable within this deck (a same-named tag in another deck is not the same category); multiple selections take the union.
  let tagReviewOpen = $state(false);
  let tagReviewLoading = $state(false);
  let tagReviewError = $state(false);
  let deckTags = $state<DeckTag[]>([]);
  let tagReviewChecked = $state<string[]>([]);

  // The review page takes its queue from the deck / tag params in the address; joining in tag list order means the same set of checkboxes always produces the same address.
  const tagReviewHref = $derived.by(() => {
    const params = new URLSearchParams();
    params.append('deck', deckId);
    for (const item of deckTags) {
      if (tagReviewChecked.includes(item.tag)) params.append('tag', item.tag);
    }
    return params.has('tag') ? `/review?${params.toString()}` : '';
  });

  // Refetched on every open: the note's tags may just have been bulk-edited on this page.
  async function openTagReview(): Promise<void> {
    tagReviewOpen = true;
    tagReviewLoading = true;
    tagReviewError = false;
    tagReviewChecked = [];
    try {
      const res = await apiClient.getDeckTags(deckId);
      deckTags = res.tags;
    } catch {
      deckTags = [];
      tagReviewError = true;
    } finally {
      tagReviewLoading = false;
    }
  }

  // Bulk actions
  let selectedIds = $state<string[]>([]);
  let bulkTagInput = $state('');
  // The in-flight bulk action: only its button spins, the other buttons are disabled along with it.
  let bulkPending = $state<'delete' | 'add_tags' | 'remove_tags' | 'set_tags' | null>(null);
  const bulkBusy = $derived(bulkPending !== null);
  let bulkError = $state('');
  let confirmingBulkDelete = $state(false);
  let bulkTagsOpen = $state(false);
  let bulkResult = $state<{ affected: number; notFound: number; insufficientRole: number } | null>(null);

  const deckId = $derived($routeStore.params.id || '');

  // This list's address, filters and page included: mirrored into the address bar, and handed to the card
  // editor as its return path.
  const listAddress = $derived.by(() => {
    const params = new URLSearchParams();
    if (appliedFilters.q) params.set('q', appliedFilters.q);
    if (appliedFilters.tag) params.set('tag', appliedFilters.tag);
    if (page > 1) params.set('page', String(page));
    const search = params.toString();
    return `${$routeStore.path}${search ? `?${search}` : ''}`;
  });
  const createHref = $derived(withReturn(`/decks/${encodeURIComponent(deckId)}/notes/new`, listAddress));

  /** The editor link of one card, returning to this list as it is now. */
  function editHref(noteId: string): string {
    return withReturn(`/decks/${encodeURIComponent(deckId)}/notes/${encodeURIComponent(noteId)}/edit`, listAddress);
  }
  const totalPages = $derived(Math.max(1, Math.ceil(total / perPage)));
  const hasFilter = $derived(Boolean(queryInput.trim() || tagInput.trim()));
  const allSelected = $derived(notes.length > 0 && notes.every((n) => selectedIds.includes(n.id)));
  // The content-editing entries are shown according to the role the server returns: a reader can view, review, pause and configure study settings,
  // but cannot create/edit/delete cards or run bulk tag actions (the server rejects those writes anyway).
  // While deck is not loaded yet (role unknown) it renders as editable first, avoiding a needless gap in the page title area.
  const canEditContent = $derived(deck?.role !== 'reader');
  // Only the owner can change the name and description (the same criterion on the server); other members only see the text.
  const isOwner = $derived(deck?.role === 'owner');
  // The description is shown in the sandboxed frame, so the server-rendered HTML is the value that reaches it: a Markdown deck
  // arrives already converted, an HTML deck passes its stored HTML through. The source text stays in `description` for editing.
  const descriptionHTML = $derived(deck?.description_html ?? deck?.description ?? '');
  const descriptionValue = $derived(deck?.description ?? '');

  /**
   * Save of the header's in-place edit: name and description go through the same endpoint, and the unchanged one is submitted along with its current value.
   * An empty name is stopped on the frontend first; 400 means an invalid name/description, 403/404 means no permission to modify. The deck name in the sidebar is synced by the change notification from the API layer (AppShell).
   */
  async function saveDeckInfo(patch: { name?: string; description?: string }): Promise<boolean> {
    if (!deck) return false;
    const name = (patch.name ?? deck.name).trim();
    const description = (patch.description ?? deck.description).trim();
    if (!name) {
      toast.error($t('deck.settings.info_error_invalid'));
      return false;
    }
    try {
      const updated = await apiClient.updateDeck(deckId, { name, description });
      deck = {
        ...deck,
        name: updated.name,
        description: updated.description,
        description_markdown: updated.description_markdown ?? deck.description_markdown,
        description_html: updated.description_html ?? updated.description,
      };
      toast.success($t('deck.settings.info_saved'));
      return true;
    } catch (err) {
      const key =
        err instanceof ApiClientError && err.status === 400
          ? 'deck.settings.info_error_invalid'
          : err instanceof ApiClientError && (err.isForbidden || err.isNotFound)
            ? 'deck.settings.error.forbidden'
            : 'deck.settings.info_error_failed';
      toast.error($t(key));
      return false;
    }
  }

  /**
   * Delete this deck after the confirmation dialog: on success the deck no longer exists, so the page cannot stay —
   * navigate to the deck list. A failure keeps the dialog open with its error line, because closing it would look like success.
   */
  async function deleteDeck(): Promise<void> {
    if (!deck || deletingDeck) return;
    deletingDeck = true;
    deleteDeckError = false;
    try {
      await apiClient.deleteDeck(deckId);
      confirmDeleteOpen = false;
      navigate('/decks');
    } catch {
      deleteDeckError = true;
    } finally {
      deletingDeck = false;
    }
  }

  /**
   * Load the deck's card data and the deck metadata
   */
  async function loadData(targetPage = 1): Promise<void> {
    if (!deckId) return;
    loading = true;
    error = null;
    page = targetPage;
    selectedIds = [];
    confirmingBulkDelete = false;

    const filters = { q: queryInput.trim(), tag: tagInput.trim() };

    try {
      if (!deck) {
        try {
          deck = await apiClient.getDeck(deckId);
        } catch {}
      }

      const res = await apiClient.getDeckNotes(deckId, {
        page: targetPage,
        per_page: perPage,
        q: filters.q || undefined,
        tag: filters.tag || undefined,
      });

      notes = res.notes;
      total = res.total;
      page = res.page;
      appliedFilters = filters;
      replaceAddress(listAddress);
    } catch (err) {
      error = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  /**
   * Load this deck's study progress. The counts endpoint answers for every visible deck at once, so this picks out the current one.
   * The numbers are supplementary: a failure leaves the header without them instead of failing the page.
   */
  async function loadProgress(): Promise<void> {
    if (!deckId) return;
    try {
      const res = await apiClient.getDeckQueueCounts();
      progress = res.decks.find((item) => item.deck_id === deckId) ?? null;
    } catch {
      progress = null;
    }
  }

  let pausing = $state(false);

  /**
   * Pause or resume this deck for the caller only. Any member may do it, so the button is not limited to the owner.
   * The progress numbers do not change, so only the deck itself is updated from the response.
   */
  async function togglePaused(): Promise<void> {
    if (!deck || pausing) return;
    const current = deck;
    pausing = true;
    try {
      const res = await apiClient.setDeckPaused(current.id, !current.paused);
      deck = { ...current, paused: res.paused };
      toast.success($t(res.paused ? 'decks.pause.done' : 'decks.resume.done', { name: current.name }));
    } catch {
      toast.error($t('decks.pause.failed'));
    } finally {
      pausing = false;
    }
  }

  /** Text input: refetch after the debounce, so not every keystroke hits the API. */
  function scheduleFilterReload(): void {
    if (debounceTimer) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => loadData(1), 300);
  }

  function handleFilterReset(): void {
    if (debounceTimer) clearTimeout(debounceTimer);
    queryInput = '';
    tagInput = '';
    loadData(1);
  }

  function handlePrevPage(): void {
    if (page > 1) {
      loadData(page - 1);
    }
  }

  function handleNextPage(): void {
    if (page < totalPages) {
      loadData(page + 1);
    }
  }

  function isSelected(id: string): boolean {
    return selectedIds.includes(id);
  }

  function toggleSelect(id: string): void {
    selectedIds = isSelected(id) ? selectedIds.filter((x) => x !== id) : [...selectedIds, id];
  }

  function toggleSelectAll(): void {
    selectedIds = allSelected ? [] : notes.map((n) => n.id);
  }

  function parseTags(raw: string): string[] {
    return raw
      .split(',')
      .map((s) => s.trim())
      .filter((s) => s !== '');
  }

  function summarizeBulk(res: BulkNotesResponse): { affected: number; notFound: number; insufficientRole: number } {
    let notFound = 0;
    let insufficientRole = 0;
    for (const item of res.skipped) {
      if (item.code === 'not_found') notFound += 1;
      else if (item.code === 'insufficient_role') insufficientRole += 1;
    }
    return { affected: res.affected, notFound, insufficientRole };
  }

  async function runBulk(action: 'delete' | 'add_tags' | 'remove_tags' | 'set_tags'): Promise<void> {
    if (bulkBusy || selectedIds.length === 0) return;
    const tags = action === 'delete' ? [] : parseTags(bulkTagInput);
    if (action !== 'delete' && tags.length === 0) {
      bulkError = 'notes.bulk_failed';
      return;
    }
    bulkPending = action;
    bulkError = '';
    bulkResult = null;
    try {
      const res = await apiClient.bulkNotes({ action, note_ids: selectedIds, tags, dry_run: false });
      bulkResult = summarizeBulk(res);
      announceBulkResult(bulkResult);
      bulkTagInput = '';
      // The dialog closes only once the request completes: the confirm button spins while in flight, and on failure the error stays in the dialog.
      bulkTagsOpen = false;
      confirmingBulkDelete = false;
      if (action === 'delete') selectedIds = [];
      await loadData(1);
      if (action === 'delete') void loadProgress();
    } catch (err) {
      bulkError = err instanceof ApiClientError && err.isForbidden ? 'error.forbidden' : 'notes.bulk_failed';
    } finally {
      bulkPending = null;
    }
  }

  // Bulk results are reported with a toast: one line for how many were processed, one line per skip reason.
  function announceBulkResult(result: { affected: number; notFound: number; insufficientRole: number }): void {
    const details = [
      result.notFound > 0 ? $t('notes.bulk_skipped_not_found', { count: result.notFound }) : '',
      result.insufficientRole > 0 ? $t('notes.bulk_skipped_forbidden', { count: result.insufficientRole }) : '',
    ].filter(Boolean);
    toast.success($t('notes.bulk_result', { affected: result.affected, skipped: result.notFound + result.insufficientRole }), {
      description: details.join('；') || undefined,
    });
  }

  // Pause only affects yourself: the toggle in the list changes the caller's own pause state for all cards under this note.
  let suspendingNoteId = $state<string | null>(null);

  async function toggleSuspended(note: Note): Promise<void> {
    suspendingNoteId = note.id;
    try {
      const res = await apiClient.setNoteSuspended(note.id, !note.suspended);
      notes = notes.map((item) => (item.id === note.id ? { ...item, suspended: res.suspended } : item));
      void loadProgress();
    } catch {
      toast.error($t('notes.suspend_failed'));
    } finally {
      suspendingNoteId = null;
    }
  }

  async function deleteNote(note: Note): Promise<void> {
    deletingNoteId = note.id;
    deleteError = '';
    try {
      await apiClient.deleteNote(note.id);
      notes = notes.filter((item) => item.id !== note.id);
      selectedIds = selectedIds.filter((id) => id !== note.id);
      total = Math.max(0, total - 1);
      noteToDelete = null;
      void loadProgress();
      toast.success($t('notes.delete_success'));
    } catch (err) {
      deleteError = err instanceof ApiClientError
        ? err.code === 'insufficient_role' ? 'error.forbidden' : err.code === 'not_found' ? 'error.not_found' : 'error.unknown'
        : 'error.unknown';
    } finally {
      deletingNoteId = null;
    }
  }

  async function exportPackage(): Promise<void> {
    exporting = true;
    exportError = false;
    try {
      const { blob, filename } = await apiClient.downloadDeckPackage(deckId, { includeMedia, includeProgress, includeReviews, includeWeights });
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement('a');
      anchor.href = url;
      anchor.download = filename;
      anchor.click();
      anchor.remove();
      URL.revokeObjectURL(url);
      showExportModal = false;
    } catch {
      exportError = true;
    } finally {
      exporting = false;
    }
  }

  async function copyNoteId(note: Note): Promise<void> {
    try {
      await navigator.clipboard.writeText(note.id);
      toast.success($t('notes.id_copied'));
    } catch {
      toast.error($t('notes.copy_failed'));
    }
  }

  /**
   * The two-line summary of a list row: the first line is the note type's first non-empty field, the second the next
   * distinct one. Newlines and consecutive whitespace are squeezed into a single space — the row only shows one line,
   * the full content is on the edit page.
   */
  function noteSummary(note: Note): { primary: string; secondary: string } {
    const flat = (value: unknown) => fieldText(value).replace(/\s+/g, ' ').trim();
    const filled = Object.entries(note.fields)
      .map(([key, value]) => [key, flat(value)] as const)
      .filter(([, value]) => value !== '');
    const primary = filled[0]?.[1] ?? '';
    const secondary = filled.find(([, value]) => value !== primary)?.[1] ?? '';
    return { primary, secondary };
  }

  onMount(() => {
    if (initialLoading) loadData(initialPage);
    void loadProgress();
  });

  onDestroy(() => {
    if (debounceTimer) clearTimeout(debounceTimer);
  });
</script>

<Page>
  <PageHeader
    title={deck ? deck.name : $t('notes.deck_title', { id: deckId })}
    back={{ href: '/decks', label: $t('decks.list_title'), testId: 'back-to-decks' }}
  >
    {#snippet heading()}
      <h1 class="text-2xl font-semibold tracking-tight text-foreground" data-testid="deck-title">
        {#if deck}
          <InlineEdit
            value={deck.name}
            editable={isOwner}
            label={$t('deck.settings.name')}
            maxlength={200}
            testId="deck-name"
            onSave={(name) => saveDeckInfo({ name })}
          />
        {:else}
          {$t('notes.deck_title', { id: deckId })}
        {/if}
      </h1>
      {#if deck && (deck.description || isOwner)}
        <div class="mt-1 max-w-3xl text-sm text-muted-foreground">
          <InlineEdit
            value={descriptionValue}
            editable={isOwner}
            multiline
            placeholder={$t('deck.add_description')}
            label={$t('deck.settings.description')}
            maxlength={2000}
            testId="deck-description"
            onSave={(description) => saveDeckInfo({ description })}
          >
            {#snippet display()}
              <!-- The description is stored HTML: the frame renders it in its own sandboxed document, never in this page. -->
              <span class="block rounded-md border border-border bg-surface px-3 py-2" data-testid="deck-description-rendered">
                <CardFrame
                  html={descriptionHTML}
                  css=""
                  bodyClass="card"
                  side="question"
                  mediaBase=""
                  title={$t('deck.settings.description')}
                />
              </span>
            {/snippet}
          </InlineEdit>
        </div>
      {/if}
      {#if deck?.paused}
        <p data-testid="deck-paused-hint" class="mt-2 flex max-w-3xl items-start gap-2 text-[13px] text-muted-foreground">
          <Badge variant="warning" class="shrink-0">{$t('decks.paused_badge')}</Badge>
          <span>{$t('deck.paused_hint')}</span>
        </p>
      {/if}
      {#if progress}
        <p data-testid="deck-progress" class="mt-2 text-[13px] tabular-nums text-muted-foreground" title={$t('decks.progress_hint')}>
          {$t('decks.progress', { studied: progress.studied_count, remaining: progress.remaining_count })}
        </p>
      {/if}
    {/snippet}
    {#snippet actions()}
      <Button variant="outline" size="lg" onclick={() => (showExportModal = true)} testId="deck-export-open">
        <Download class="size-4" aria-hidden="true" />
        <span>{$t('package.export.short')}</span>
      </Button>
      {#if canEditContent}
        <Button variant="outline" size="lg" href={createHref} testId="create-note-link">
          <Plus class="size-4" aria-hidden="true" />
          <span>{$t('notes.create')}</span>
        </Button>
      {/if}
      <Button variant="outline" size="lg" onclick={() => void openTagReview()} testId="deck-tag-review-open">
        <Tags class="size-4" aria-hidden="true" />
        <span>{$t('deck.tag_review.open')}</span>
      </Button>
      {#if deck}
        <Button
          variant="outline"
          size="lg"
          onclick={() => void togglePaused()}
          loading={pausing}
          title={$t(deck.paused ? 'decks.resume.action' : 'decks.pause.action')}
          testId="deck-pause-toggle"
        >
          {#if deck.paused}<CirclePlay class="size-4" aria-hidden="true" />{:else}<CirclePause class="size-4" aria-hidden="true" />{/if}
          <span>{$t(deck.paused ? 'deck.resume.short' : 'deck.pause.short')}</span>
        </Button>
      {/if}
      <Button variant="primary" size="lg" href="/review?deck={encodeURIComponent(deckId)}" testId="deck-start-review">
        <Play class="size-4" aria-hidden="true" />
        <span>{$t('home.start_review')}</span>
      </Button>
      {#if isOwner}
        <Button
          variant="ghost"
          size="lg"
          label={$t('decks.delete.action')}
          title={$t('decks.delete.action')}
          testId="deck-delete-open"
          onclick={() => { deleteDeckError = false; confirmDeleteOpen = true; }}
        >
          <Trash2 class="size-4" aria-hidden="true" />
        </Button>
      {/if}
    {/snippet}
  </PageHeader>

  <!-- Tabs: underline style, switching only swaps the content below, the header stays put. -->
  <div role="tablist" aria-label={deck?.name ?? ''} class="mb-6 flex gap-6 overflow-x-auto overflow-y-hidden border-b border-border">
    {#each [
      { id: 'cards', label: $t('deck.tab.notes', { count: total }), testId: 'deck-notes-tab' },
      { id: 'sharing', label: $t('deck.sharing.title'), testId: 'deck-sharing-link' },
      { id: 'settings', label: $t('deck.settings.entry'), testId: 'deck-settings-link' },
    ] as tab (tab.id)}
      <button
        type="button"
        role="tab"
        aria-selected={activeTab === tab.id}
        data-testid={tab.testId}
        onclick={() => { activeTab = tab.id as typeof activeTab; visitedTabs[tab.id] = true; }}
        class="-mb-px whitespace-nowrap border-b-2 py-2.5 text-sm transition-colors cursor-pointer {activeTab === tab.id
          ? 'border-foreground font-medium text-foreground'
          : 'border-transparent text-muted-foreground hover:text-foreground'}"
      >
        {tab.label}
      </button>
    {/each}
  </div>

  {#if visitedTabs.sharing}
    <div hidden={activeTab !== 'sharing'}><DeckSharingView embedded /></div>
  {/if}
  {#if visitedTabs.settings}
    <!-- Study settings are each member's own, every member can open them. -->
    <div hidden={activeTab !== 'settings'}><DeckSettingsView embedded /></div>
  {/if}
  <div hidden={activeTab !== 'cards'}>
    <!-- Search and filters -->
    <form
      onsubmit={(event) => event.preventDefault()}
      class="mb-3 flex flex-wrap items-center gap-2"
      data-testid="notes-filter-form"
    >
      <label class="relative flex w-full items-center sm:w-64">
        <Search class="pointer-events-none absolute left-2.5 size-4 text-muted-foreground" aria-hidden="true" />
        <input
          type="search"
          data-testid="filter-query-input"
          aria-label={$t('notes.search_placeholder')}
          placeholder={$t('notes.search_placeholder')}
          bind:value={queryInput}
          oninput={scheduleFilterReload}
          class="field-input w-full pl-8 text-sm"
        />
      </label>
      <input
        type="text"
        data-testid="filter-tag-input"
        aria-label={$t('notes.tag_placeholder')}
        placeholder={$t('notes.tag_placeholder')}
        bind:value={tagInput}
        oninput={scheduleFilterReload}
        class="field-input w-full text-sm sm:w-36"
      />
      {#if hasFilter}
        <Button variant="ghost" size="lg" testId="filter-reset-btn" onclick={handleFilterReset}>
          <X class="size-4" aria-hidden="true" />
          <span>{$t('notes.filter_reset')}</span>
        </Button>
      {/if}
    </form>

    {#if loading}
      <Skeleton testId="notes-loading" label={$t('common.loading')} lines={6} />
    {:else if error}
      <div
        data-testid={error instanceof ApiClientError && error.isUnauthorized
          ? 'notes-unauthorized'
          : error instanceof ApiClientError && error.isForbidden
            ? 'notes-forbidden'
            : error instanceof ApiClientError && error.isNotFound
              ? 'notes-not-found'
              : 'notes-failed'}
        class="space-y-3 py-16 text-center"
      >
        <p class="text-sm font-medium text-foreground">
          {#if error instanceof ApiClientError && error.isNotFound}
            {$t('notes.not_found')}
          {:else if error instanceof ApiClientError && error.isForbidden}
            {$t('notes.forbidden')}
          {:else if error instanceof ApiClientError && error.isUnauthorized}
            {$t('notes.unauthorized')}
          {:else}
            {$t('notes.failed')}
          {/if}
        </p>
        <Button testId="notes-retry" type="button" onclick={() => loadData(page)} variant="outline" size="lg">
          <RefreshCw class="size-4" aria-hidden="true" />
          <span>{$t('notes.retry')}</span>
        </Button>
      </div>
    {:else if notes.length === 0}
      <EmptyState
        testId="notes-empty"
        icon={hasFilter ? SearchX : FileText}
        text={hasFilter ? $t('notes.empty_filter') : $t('notes.empty')}
      />
    {:else}
      {@const cols = canEditContent
        ? 'grid-cols-[1.25rem_minmax(0,1fr)_4.5rem] md:grid-cols-[1.25rem_minmax(0,1fr)_7rem_10rem_6rem_4.5rem]'
        : 'grid-cols-[minmax(0,1fr)_4.5rem] md:grid-cols-[minmax(0,1fr)_7rem_10rem_6rem_4.5rem]'}
      <div data-testid="notes-list" class={listClasses.root}>
        <div class="grid {cols} h-9 items-center gap-x-3 border-b border-border bg-surface px-4 text-xs font-medium text-muted-foreground">
          {#if canEditContent}
            <Checkbox
              testId="bulk-select-all"
              checked={allSelected}
              indeterminate={selectedIds.length > 0 && !allSelected}
              onCheckedChange={toggleSelectAll}
              label={$t('notes.select_all')}
            />
          {/if}
          <span>{$t('notes.col_content')}</span>
          <span class="hidden md:block">{$t('notes.col_tags')}</span>
          <span class="hidden md:block">{$t('notes.col_created')}</span>
          <span></span>
        </div>
        {#each notes as note (note.id)}
          {@const summary = noteSummary(note)}
          <div
            data-testid={`note-card-${note.id}`}
            class="group relative grid {cols} min-h-14 items-center gap-x-3 border-b border-border px-4 py-2 transition-colors last:border-b-0 hover:bg-muted/50 has-[[data-state=checked]]:bg-brand-soft/60"
          >
            {#if canEditContent}
              <Checkbox
                class={listClasses.rowAction}
                testId="select-note-{note.id}"
                checked={isSelected(note.id)}
                onCheckedChange={() => toggleSelect(note.id)}
                label={$t('notes.select_one')}
              />
            {/if}
            <div class="min-w-0" data-testid={`note-fields-${note.id}`}>
              <div class="flex min-w-0 items-center gap-2">
                {#if canEditContent}
                  <a
                    href={editHref(note.id)}
                    class="{listClasses.rowLink} text-sm {note.suspended ? 'text-muted-foreground' : ''}"
                  >{summary.primary || '—'}</a>
                {:else}
                  <span class="min-w-0 truncate text-sm font-medium text-foreground">{summary.primary || '—'}</span>
                {/if}
                {#if note.suspended}
                  <Badge testId={`note-suspended-${note.id}`} class="shrink-0">
                    <Pause class="size-3" aria-hidden="true" />{$t('notes.suspended')}
                  </Badge>
                {/if}
                {#if note.external_ref}
                  <span
                    class="inline-flex h-5 max-w-40 shrink-0 items-center gap-1 rounded bg-muted px-1.5 font-mono text-[11px] text-muted-foreground"
                    title="{$t('notes.external_ref')}: {note.external_ref}"
                  >
                    <Link2 class="size-3 shrink-0" aria-hidden="true" />
                    <span class="truncate">{note.external_ref}</span>
                  </span>
                {/if}
              </div>
              {#if summary.secondary}
                <p class="truncate text-[13px] text-muted-foreground">{summary.secondary}</p>
              {/if}
            </div>
            <div class="hidden min-w-0 flex-wrap gap-1 md:flex" data-testid={`note-tags-${note.id}`}>
              {#each (note.tags ?? []).slice(0, 3) as tag (tag)}
                <span class="inline-flex h-5 max-w-full items-center truncate rounded-full border border-border px-2 text-xs text-muted-foreground">{tag}</span>
              {/each}
              {#if (note.tags ?? []).length > 3}
                <span class="inline-flex h-5 items-center px-1 text-xs text-muted-foreground" title={note.tags.slice(3).join(', ')}>+{note.tags.length - 3}</span>
              {/if}
            </div>
            <span class="hidden text-[13px] tabular-nums text-muted-foreground md:block">{note.created_at ? note.created_at.slice(0, 10) : ''}</span>
            <div class="{listClasses.rowAction} flex items-center justify-end gap-0.5">
              {#if canEditContent}
                <a
                  data-testid="edit-note-{note.id}"
                  href={editHref(note.id)}
                  class="inline-flex size-8 items-center justify-center rounded-md text-muted-foreground opacity-0 transition hover:bg-muted hover:text-foreground focus-visible:opacity-100 group-hover:opacity-100 max-md:opacity-100"
                  aria-label={$t('note_edit.action')}
                  title={$t('note_edit.action')}
                >
                  <Pencil class="size-4" aria-hidden="true" />
                </a>
              {/if}
              <DropdownMenu.Root>
                <DropdownMenu.Trigger
                  data-testid="note-menu-{note.id}"
                  class="inline-flex size-8 items-center justify-center rounded-md text-muted-foreground transition hover:bg-muted hover:text-foreground data-[state=open]:bg-muted data-[state=open]:text-foreground cursor-pointer"
                  aria-label={$t('notes.more_actions')}
                  title={$t('notes.more_actions')}
                >
                  <MoreHorizontal class="size-4" aria-hidden="true" />
                </DropdownMenu.Trigger>
                <DropdownMenu.Portal>
                  <DropdownMenu.Content class={menuClasses.content} align="end" sideOffset={4}>
                    <DropdownMenu.Item
                      class={menuClasses.item}
                      data-testid="toggle-suspend-{note.id}"
                      disabled={suspendingNoteId === note.id}
                      onSelect={() => toggleSuspended(note)}
                    >
                      {#if note.suspended}<Play aria-hidden="true" />{:else}<Pause aria-hidden="true" />{/if}
                      {$t(note.suspended ? 'notes.unsuspend' : 'notes.suspend')}
                    </DropdownMenu.Item>
                    <DropdownMenu.Item class={menuClasses.item} onSelect={() => copyNoteId(note)}>
                      <Copy aria-hidden="true" />
                      {$t('notes.copy_id')}
                    </DropdownMenu.Item>
                    {#if canEditContent}
                      <DropdownMenu.Separator class={menuClasses.separator} />
                      <DropdownMenu.Item
                        class={menuClasses.destructiveItem}
                        data-testid="delete-note-{note.id}"
                        onSelect={() => { noteToDelete = note; deleteError = ''; }}
                      >
                        <Trash2 aria-hidden="true" />
                        {$t('notes.delete')}…
                      </DropdownMenu.Item>
                    {/if}
                  </DropdownMenu.Content>
                </DropdownMenu.Portal>
              </DropdownMenu.Root>
            </div>
          </div>
        {/each}
      </div>

      <div class="mt-3 flex items-center justify-between text-[13px] text-muted-foreground">
        <span class="tabular-nums">{$t('notes.total_count', { total })}</span>
        {#if totalPages > 1}
          <div data-testid="notes-pagination" class="flex items-center gap-2">
            <span data-testid="notes-page-info" class="tabular-nums">{$t('notes.page_info', { page, totalPages })}</span>
            <Button testId="notes-prev-page" type="button" disabled={page <= 1} onclick={handlePrevPage} variant="outline" size="sm">
              <ChevronLeft class="size-4" aria-hidden="true" />
              <span>{$t('notes.prev_page')}</span>
            </Button>
            <Button testId="notes-next-page" type="button" disabled={page >= totalPages} onclick={handleNextPage} variant="outline" size="sm">
              <span>{$t('notes.next_page')}</span>
              <ChevronRight class="size-4" aria-hidden="true" />
            </Button>
          </div>
        {/if}
      </div>
    {/if}

    <!-- Bulk operations: only roles that can change content ever have a selection (readers have no checkboxes). -->
    <SelectionBar
      count={selectedIds.length}
      onClear={() => (selectedIds = [])}
      testId="notes-bulk-toolbar"
    >
      <button type="button" class={selectionBarButton} disabled={bulkBusy} onclick={() => { bulkError = ''; bulkTagsOpen = true; }} data-testid="bulk-tags-open">
        <Tags aria-hidden="true" />
        {$t('notes.bulk_tags')}
      </button>
      <button
        type="button"
        class={selectionBarButton}
        data-testid="bulk-delete"
        disabled={bulkBusy}
        onclick={() => { bulkError = ''; bulkResult = null; confirmingBulkDelete = true; }}
      >
        <Trash2 aria-hidden="true" />
        {$t('notes.bulk_delete')}
      </button>
    </SelectionBar>
  </div>
</Page>

<!-- Study by tag -->
<Dialog
  bind:open={tagReviewOpen}
  title={$t('deck.tag_review.title')}
  description={$t('deck.tag_review.description')}
  testId="deck-tag-review-dialog"
>
  {#if tagReviewLoading}
    <Skeleton testId="deck-tag-review-loading" label={$t('common.loading')} lines={3} />
  {:else if tagReviewError}
    <p role="alert" class="text-sm text-destructive-foreground" data-testid="deck-tag-review-error">{$t('deck.tag_review.load_failed')}</p>
  {:else if deckTags.length === 0}
    <p class="text-sm text-muted-foreground" data-testid="deck-tag-review-empty">{$t('deck.tag_review.empty')}</p>
  {:else}
    <div class="max-h-72 space-y-2 overflow-y-auto text-sm text-foreground" data-testid="deck-tag-review-list">
      {#each deckTags as item (item.tag)}
        <label class="flex items-center gap-2 cursor-pointer">
          <Checkbox
            checked={tagReviewChecked.includes(item.tag)}
            label={item.tag}
            onCheckedChange={(on) => (tagReviewChecked = on ? [...tagReviewChecked, item.tag] : tagReviewChecked.filter((x) => x !== item.tag))}
          />
          <span class="min-w-0 flex-1 truncate">{item.tag}</span>
          <span class="shrink-0 text-xs text-muted-foreground">{$t('deck.tag_review.notes', { count: item.notes })}</span>
        </label>
      {/each}
    </div>
  {/if}
  <p class="mt-3 text-xs text-muted-foreground">{$t('deck.tag_review.limit_hint')}</p>
  <div class="mt-5 flex flex-wrap items-center justify-end gap-2">
    <Button type="button" variant="outline" size="lg" onclick={() => (tagReviewOpen = false)}>{$t('common.cancel')}</Button>
    {#if tagReviewHref}
      <Button variant="primary" size="lg" href={tagReviewHref} testId="deck-tag-review-start">
        <Play class="size-4" aria-hidden="true" />
        <span>{$t('home.start_review')}</span>
      </Button>
    {:else}
      <Button type="button" variant="primary" size="lg" disabled testId="deck-tag-review-start">
        <Play class="size-4" aria-hidden="true" />
        <span>{$t('home.start_review')}</span>
      </Button>
    {/if}
  </div>
</Dialog>

<!-- Bulk tag edit -->
<Dialog
  bind:open={bulkTagsOpen}
  title={$t('notes.bulk_tags')}
  description={$t('notes.selected_count', { count: selectedIds.length })}
  testId="notes-bulk-tags-dialog"
>
  <label class="block text-sm font-medium text-foreground">
    <span class="sr-only">{$t('notes.bulk_tag_placeholder')}</span>
    <input
      type="text"
      data-testid="bulk-tag-input"
      placeholder={$t('notes.bulk_tag_placeholder')}
      bind:value={bulkTagInput}
      class="field-input w-full text-sm"
    />
  </label>
  <p class="mt-1.5 text-xs text-muted-foreground">{$t('notes.bulk_tags_hint')}</p>
  {#if bulkError}
    <p role="alert" data-testid="notes-bulk-error" class="mt-3 text-sm text-destructive-foreground">{$t(bulkError)}</p>
  {/if}
  <div class="mt-5 flex flex-wrap items-center justify-end gap-2">
    <Button type="button" testId="bulk-remove-tags" loading={bulkPending === 'remove_tags'} disabled={bulkBusy} onclick={() => runBulk('remove_tags')} variant="outline" size="lg">{$t('notes.bulk_remove_tags')}</Button>
    <Button type="button" testId="bulk-set-tags" loading={bulkPending === 'set_tags'} disabled={bulkBusy} onclick={() => runBulk('set_tags')} variant="outline" size="lg">{$t('notes.bulk_set_tags')}</Button>
    <Button type="button" testId="bulk-add-tags" loading={bulkPending === 'add_tags'} disabled={bulkBusy} onclick={() => runBulk('add_tags')} variant="primary" size="lg">
      {$t('notes.bulk_add_tags')}
    </Button>
  </div>
</Dialog>

<!-- Bulk delete confirmation -->
<Dialog
  bind:open={confirmingBulkDelete}
  title={$t('notes.bulk_confirm_delete')}
  description={$t('notes.bulk_delete_desc', { count: selectedIds.length })}
  testId="notes-bulk-delete-dialog"
  size="sm"
>
  {#if bulkError}
    <p role="alert" class="text-sm text-destructive-foreground">{$t(bulkError)}</p>
  {/if}
  <div class="mt-2 flex items-center justify-end gap-2">
    <Button type="button" testId="bulk-cancel-delete" variant="outline" size="lg" disabled={bulkBusy} onclick={() => (confirmingBulkDelete = false)}>{$t('note_edit.cancel')}</Button>
    <Button type="button" testId="bulk-confirm-delete" variant="danger" size="lg" loading={bulkPending === 'delete'} disabled={bulkBusy} onclick={() => runBulk('delete')}>
      <Trash2 class="size-4" aria-hidden="true" />
      <span>{$t('notes.bulk_delete')}</span>
    </Button>
  </div>
</Dialog>

<!-- Single-row delete confirmation -->
<Dialog
  open={noteToDelete !== null}
  onOpenChange={(open) => { if (!open && !deletingNoteId) noteToDelete = null; }}
  title={$t('notes.delete_confirm')}
  description={$t('notes.delete_desc')}
  testId="note-delete-dialog"
  size="sm"
>
  {#if deleteError}
    <p role="alert" data-testid="note-delete-error" class="text-sm text-destructive-foreground">{$t(deleteError)}</p>
  {/if}
  <div class="mt-2 flex items-center justify-end gap-2">
    <Button type="button" variant="outline" size="lg" disabled={deletingNoteId !== null} onclick={() => (noteToDelete = null)}>{$t('note_edit.cancel')}</Button>
    <Button
      type="button"
      variant="danger"
      size="lg"
      testId={noteToDelete ? `confirm-delete-note-${noteToDelete.id}` : undefined}
      loading={deletingNoteId !== null}
      onclick={() => noteToDelete && deleteNote(noteToDelete)}
    >
      <Trash2 class="size-4" aria-hidden="true" />
      <span>{$t('notes.delete')}</span>
    </Button>
  </div>
</Dialog>

<!-- Export package settings -->
<Dialog
  bind:open={showExportModal}
  title={$t('package.export.heading')}
  testId="deck-export-dialog"
>
  <PackageExportOptions bind:includeMedia bind:includeProgress bind:includeReviews bind:includeWeights />

  {#if exportError}
    <p role="alert" class="mt-3 text-sm text-destructive-foreground">{$t('package.export.failed')}</p>
  {/if}

  <div class="mt-5 flex items-center justify-end gap-2">
    <Button type="button" variant="outline" size="lg" onclick={() => (showExportModal = false)}>{$t('note_edit.cancel')}</Button>
    <Button type="button" testId="deck-package-export" loading={exporting} onclick={exportPackage} variant="primary" size="lg">
      <Download class="size-4" aria-hidden="true" />
      <span>{$t('package.export.action')}</span>
    </Button>
  </div>
</Dialog>

<!-- Deck delete confirmation: deleting the deck destroys its cards, so it takes one explicit confirmation step. -->
<Dialog
  bind:open={confirmDeleteOpen}
  title={$t('decks.delete.confirm_title')}
  description={$t('decks.delete.confirm_desc', { name: deck?.name ?? '' })}
  testId="deck-detail-delete-dialog"
  size="sm"
>
  {#if deleteDeckError}
    <p role="alert" class="text-sm text-destructive-foreground" data-testid="deck-delete-error">{$t('decks.delete.failed')}</p>
  {/if}
  <div class="mt-2 flex items-center justify-end gap-2">
    <Button
      type="button"
      variant="outline"
      size="lg"
      disabled={deletingDeck}
      testId="deck-delete-cancel"
      onclick={() => (confirmDeleteOpen = false)}
    >
      {$t('decks.delete.cancel_btn')}
    </Button>
    <Button type="button" variant="danger" size="lg" loading={deletingDeck} testId="deck-delete-confirm" onclick={deleteDeck}>
      <Trash2 class="size-4" aria-hidden="true" />
      <span>{$t('decks.delete.confirm_btn')}</span>
    </Button>
  </div>
</Dialog>
