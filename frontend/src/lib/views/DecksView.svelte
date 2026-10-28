<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { BulkDecksResponse, Deck, DeckQueueCount, DeckShareInvite } from '../api';
  import Dialog from '../components/ui/Dialog.svelte';
  import Button from '../components/ui/Button.svelte';
  import { toast } from '../components/ui/toast';
  import Badge from '../components/ui/Badge.svelte';
  import Select from '../components/ui/Select.svelte';
  import Checkbox from '../components/ui/Checkbox.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import SelectionBar from '../components/ui/SelectionBar.svelte';
  import PackageExportOptions from '../components/PackageExportOptions.svelte';
  import { listClasses, selectionBarButton } from '../components/ui/variants';
  import { CirclePlay, Download, LogOut, Plus, RefreshCw, Trash2, Upload } from '@lucide/svelte';
  import { deckActionKind, presetSelectOptions } from '../labels';

  // View reactive state definitions (Svelte 5 runes)
  let loading = $state(true);
  let error = $state<ApiClientError | Error | null>(null);
  let decks = $state<Deck[]>([]);
  let queueCounts = $state<Record<string, DeckQueueCount>>({});

  // New deck dialog state
  let showCreateModal = $state(false);
  let name = $state('');
  let description = $state('');
  // The scheduling preset selectable when creating a deck. The dropdown lists only real presets (without the "default preset" sentinel entry),
  // it preselects the default preset once loading finishes; when the fetch fails createPresetId stays empty and the deck is submitted with the server's default preset
  // (an empty string as preset_id in the request body).
  let createPresets = $state<Array<{ value: string; label: string }>>([]);
  let createPresetDefault = $state('');
  let createPresetId = $state('');
  let creating = $state(false);
  let createError = $state<string | null>(null);

  // Bulk selection and export state. The export options match the single-deck export defaults: media included, personal progress and weights excluded.
  let selectedDeckIds = $state<string[]>([]);
  let showBatchExport = $state(false);
  let batchExporting = $state(false);
  let batchExportError = $state<string | null>(null);
  let includeMedia = $state(true);
  let includeProgress = $state(false);
  let includeReviews = $state(false);
  let includeWeights = $state(false);

  // Bulk delete state. The ids are captured when the dialog opens, so the confirmation text and the request
  // always describe the same set even if the selection were to change behind the dialog.
  let showBatchDelete = $state(false);
  let batchDeleteIDs = $state<string[]>([]);
  let batchDeleting = $state(false);
  let batchDeleteError = $state<string | null>(null);

  // Share invitations pending acceptance (consent model): sharing creates an invitation first, and only the accept step writes the grant.
  // A failed fetch does not set error — failing to load invitations should not turn the whole page into an error page, it merely hides this block.
  let invites = $state<DeckShareInvite[]>([]);
  // An in-flight invitation response: the deck id decides which row is disabled, accept decides which button on that row spins.
  let inviteBusy = $state<{ deckId: string; accept: boolean } | null>(null);

  // State of the confirm dialog for dangerous deck operations: delete (own deck) and leave share (shared deck) share one dialog,
  // only the copy and the endpoint called differ, avoiding two duplicated confirmation UIs.
  let deckAction = $state<{ deck: Deck; kind: 'delete' | 'leave' } | null>(null);
  let acting = $state(false);
  let actionError = $state<string | null>(null);

  /**
   * Request the backend deck list (GET /api/v1/decks)
   */
  async function fetchDecks(): Promise<void> {
    loading = true;
    error = null;
    try {
      const res = await apiClient.getDecks();
      decks = res.decks;
      const counts = await apiClient.getDeckQueueCounts();
      queueCounts = Object.fromEntries(counts.decks.map((count) => [count.deck_id, count]));
    } catch (err) {
      error = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  /**
   * Read share invitations pending acceptance (GET /api/v1/sharing/invites).
   * Being logged out or an API failure both just mean "no invitations to show", and are not escalated into a page-level error.
   */
  async function fetchInvites(): Promise<void> {
    try {
      const res = await apiClient.getShareInvites();
      invites = res.invites;
    } catch {
      invites = [];
    }
  }

  /**
   * Accept or decline an invitation. After a successful accept, refetch the deck list — that step is exactly when the deck appears here.
   * Failures go through toast and leave the whole page's state untouched.
   */
  async function respondToInvite(deckId: string, accept: boolean): Promise<void> {
    inviteBusy = { deckId, accept };
    try {
      if (accept) {
        await apiClient.acceptShareInvite(deckId);
      } else {
        await apiClient.rejectShareInvite(deckId);
      }
      invites = invites.filter((invite) => invite.deck_id !== deckId);
      if (accept) {
        await fetchDecks();
      }
    } catch {
      toast.error($t('decks.invites.failed'));
    } finally {
      inviteBusy = null;
    }
  }

  function openCreateModal(): void {
    name = '';
    description = '';
    createPresetId = createPresetDefault;
    createError = null;
    showCreateModal = true;
  }

  function closeCreateModal(): void {
    if (!creating) {
      showCreateModal = false;
    }
  }

  /** Fetch the preset list only when the dialog opens: most users never open this dialog, so there is no reason to fetch it every time. */
  $effect(() => {
    if (showCreateModal && createPresets.length === 0) {
      apiClient
        .listPresets()
        .then((response) => {
          const { options, selected } = presetSelectOptions(response.presets, $t);
          createPresets = options;
          createPresetDefault = selected;
          if (!createPresetId) {
            createPresetId = selected;
          }
        })
        .catch(() => {
          // Failing to get the preset list does not block deck creation: leaving it empty falls back to the server's default preset.
          createPresets = [];
        });
    }
  });

  async function createDeck(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    creating = true;
    createError = null;
    try {
      const deck = await apiClient.createDeck({
        name,
        description,
        preset_id: createPresetId,
      });
      decks = [deck, ...decks];
      queueCounts = { ...queueCounts, [deck.id]: { deck_id: deck.id, new_count: 0, review_count: 0, studied_count: 0, remaining_count: 0 } };
      name = '';
      description = '';
      createPresetId = createPresetDefault;
      showCreateModal = false;
    } catch (err) {
      if (err instanceof ApiClientError && err.code === 'deck_name_invalid') {
        createError = 'decks.create.name_invalid';
      } else if (err instanceof ApiClientError && err.code === 'deck_description_invalid') {
        createError = 'decks.create.description_invalid';
      } else if (err instanceof ApiClientError && err.code === 'invalid_request') {
        createError = 'decks.create.invalid_request';
      } else {
        createError = 'decks.create.failed';
      }
    } finally {
      creating = false;
    }
  }

  function toggleSelectDeck(deckId: string): void {
    if (selectedDeckIds.includes(deckId)) {
      selectedDeckIds = selectedDeckIds.filter((id) => id !== deckId);
    } else {
      selectedDeckIds = [...selectedDeckIds, deckId];
    }
  }

  function toggleSelectAll(): void {
    if (selectedDeckIds.length === decks.length) {
      selectedDeckIds = [];
    } else {
      selectedDeckIds = decks.map((d) => d.id);
    }
  }

  // Bulk review jumps straight to the review page: that page already takes a multi-deck queue from the repeatable deck param in the address, all this does is build the address.
  // Joined in list order rather than selection order, so the same set of decks yields the same address however they were checked.
  const batchReviewHref = $derived.by(() => {
    const params = new URLSearchParams();
    for (const deck of decks) {
      if (selectedDeckIds.includes(deck.id)) params.append('deck', deck.id);
    }
    return `/review?${params.toString()}`;
  });

  async function handleBatchExport(): Promise<void> {
    if (selectedDeckIds.length === 0 || batchExporting) return;
    batchExporting = true;
    batchExportError = null;
    try {
      const { blob, filename } = await apiClient.exportDecksZip(selectedDeckIds, { includeMedia, includeProgress, includeReviews, includeWeights });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
      showBatchExport = false;
    } catch {
      batchExportError = 'package.batch_export_failed';
    } finally {
      batchExporting = false;
    }
  }

  /** Open the bulk delete confirmation dialog with the current selection. */
  function openBatchDelete(): void {
    if (selectedDeckIds.length === 0) return;
    batchDeleteIDs = [...selectedDeckIds];
    batchDeleteError = null;
    showBatchDelete = true;
  }

  function closeBatchDelete(): void {
    if (!batchDeleting) {
      showBatchDelete = false;
      batchDeleteError = null;
    }
  }

  // How many of the decks captured for deletion are only shared with the caller: the server will skip those,
  // so the confirmation warns before the request instead of after.
  const sharedSelectedCount = $derived.by(
    () => decks.filter((deck) => batchDeleteIDs.includes(deck.id) && deck.role !== 'owner').length
  );

  /**
   * Delete every captured deck in one request. The server decides ownership per deck: ids reported in skipped stay
   * in the list, every other id is dropped from the list, the queue counts and the selection.
   * On success the dialog closes; on failure it stays open with the error and the list is untouched.
   */
  async function handleBatchDelete(): Promise<void> {
    if (batchDeleteIDs.length === 0 || batchDeleting) return;
    batchDeleting = true;
    batchDeleteError = null;
    try {
      const res = await apiClient.bulkDecks({ action: 'delete', deck_ids: batchDeleteIDs, dry_run: false });
      const skipped = new Set(res.skipped.map((row) => row.deck_id));
      const removed = new Set(batchDeleteIDs.filter((id) => !skipped.has(id)));
      decks = decks.filter((deck) => !removed.has(deck.id));
      queueCounts = Object.fromEntries(Object.entries(queueCounts).filter(([deckID]) => !removed.has(deckID)));
      selectedDeckIds = [];
      showBatchDelete = false;
      announceBatchDelete(res);
    } catch {
      batchDeleteError = 'decks.bulk_delete.failed';
    } finally {
      batchDeleting = false;
    }
  }

  // Bulk results are reported with a toast: one line for the deleted count, one line per skip reason,
  // following the note bulk result pattern so both pages read the same way.
  function announceBatchDelete(res: BulkDecksResponse): void {
    const notFound = res.skipped.filter((row) => row.code === 'not_found').length;
    const forbidden = res.skipped.filter((row) => row.code === 'forbidden').length;
    const details = [
      notFound > 0 ? $t('decks.bulk_delete.skipped_not_found', { count: notFound }) : '',
      forbidden > 0 ? $t('decks.bulk_delete.skipped_forbidden', { count: forbidden }) : '',
    ].filter(Boolean);
    // ' · ' separates the reasons in both locales; a full-width semicolon would read as a typo in the English UI.
    toast.success($t('decks.bulk_delete.result', { count: res.affected }), {
      description: details.join(' · ') || undefined,
    });
  }

  /** Open the confirmation dialog. kind decides whether this is "delete own deck" or "leave shared deck". */
  function promptDeckAction(deck: Deck, kind: 'delete' | 'leave', e: Event): void {
    e.stopPropagation();
    deckAction = { deck, kind };
    actionError = null;
  }

  function closeDeckAction(): void {
    if (!acting) {
      deckAction = null;
      actionError = null;
    }
  }

  /**
   * Perform the confirmed dangerous operation: delete goes through deleteDeck, leaving a share through leaveDeck.
   * On success both remove the deck from the local list — after leaving a share it is no longer visible to you, the effect equals deletion.
   */
  async function confirmDeckAction(): Promise<void> {
    if (!deckAction || acting) return;
    const { deck, kind } = deckAction;
    acting = true;
    actionError = null;
    try {
      if (kind === 'delete') {
        await apiClient.deleteDeck(deck.id);
      } else {
        await apiClient.leaveDeck(deck.id);
      }
      decks = decks.filter((d) => d.id !== deck.id);
      selectedDeckIds = selectedDeckIds.filter((id) => id !== deck.id);
      deckAction = null;
    } catch {
      actionError = kind === 'delete' ? 'decks.delete.failed' : 'decks.leave.failed';
    } finally {
      acting = false;
    }
  }

  function handleKeydown(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      if (showCreateModal) closeCreateModal();
      if (deckAction) closeDeckAction();
      if (showBatchDelete) closeBatchDelete();
    }
  }

  onMount(() => {
    fetchDecks();
    fetchInvites();
    window.addEventListener('keydown', handleKeydown);
    return () => window.removeEventListener('keydown', handleKeydown);
  });
</script>

<Page>
  <PageHeader title={$t('decks.title')} description={$t('decks.count', { count: decks.length })}>
    {#snippet actions()}
      {#if !loading && !error && decks.length > 0}
        <Button variant="ghost" size="icon" label={$t('decks.retry')} title={$t('decks.retry')} onclick={fetchDecks}>
          <RefreshCw class="size-4" aria-hidden="true" />
        </Button>
      {/if}
      <!-- Deck package import entry; "import" in the sidebar is the same page, this is the nearby entry point. -->
      <Button variant="outline" size="lg" href="/import" testId="decks-import-open">
        <Upload class="size-4" aria-hidden="true" />
        <span>{$t('package.import.title')}</span>
      </Button>
      <Button type="button" testId="deck-create-open" onclick={openCreateModal} variant="primary" size="lg">
        <Plus class="size-4" aria-hidden="true" />
        <span>{$t('decks.create.heading')}</span>
      </Button>
    {/snippet}
  </PageHeader>

  <!-- Share invitations pending acceptance: the consent-model entry. The whole block is not rendered when there are no invitations. -->
  {#if invites.length > 0}
    <section data-testid="deck-invites" class="mb-6">
      <div class="mb-2 flex items-baseline gap-2">
        <h2 class="text-sm font-semibold text-foreground">{$t('decks.invites.title')}</h2>
        <p class="text-xs text-muted-foreground">{$t('decks.invites.hint')}</p>
      </div>
      <ul class={listClasses.root}>
        {#each invites as invite (invite.deck_id)}
          <li class="{listClasses.row} flex-wrap bg-brand-soft/40">
            <div class="min-w-0 flex-1">
              <p class="truncate text-sm font-medium text-foreground">{invite.deck_name}</p>
              <p class="text-xs text-muted-foreground">
                {$t('decks.invites.from', { name: invite.inviter_name || invite.username || '—' })}
                ·
                {$t('decks.invites.role', { role: $t(`deck.sharing.role.${invite.role}`) })}
              </p>
            </div>
            <div class="flex items-center gap-2">
              <Button
                testId="deck-invite-reject-{invite.deck_id}"
                variant="ghost"
                size="sm"
                loading={inviteBusy?.deckId === invite.deck_id && !inviteBusy.accept}
                disabled={inviteBusy?.deckId === invite.deck_id}
                onclick={() => respondToInvite(invite.deck_id, false)}
              >
                {$t('decks.invites.reject')}
              </Button>
              <Button
                testId="deck-invite-accept-{invite.deck_id}"
                variant="primary"
                size="sm"
                loading={inviteBusy?.deckId === invite.deck_id && inviteBusy.accept}
                disabled={inviteBusy?.deckId === invite.deck_id}
                onclick={() => respondToInvite(invite.deck_id, true)}
              >
                {$t('decks.invites.accept')}
              </Button>
            </div>
          </li>
        {/each}
      </ul>
    </section>
  {/if}

  <!-- List content area -->
  {#if loading}
    <Skeleton testId="decks-loading" label={$t('common.loading')} lines={5} />
  {:else if error}
    <div
      data-testid={error instanceof ApiClientError && error.isUnauthorized ? 'decks-unauthorized' : 'decks-failed'}
      class="space-y-4 py-16 text-center"
    >
      <p class="text-base font-medium text-foreground">
        {#if error instanceof ApiClientError && error.isUnauthorized}
          {$t('decks.unauthorized')}
        {:else}
          {$t('decks.failed')}
        {/if}
      </p>
      <Button testId="decks-retry" type="button" onclick={fetchDecks} variant="outline" size="lg">
        <RefreshCw class="size-4" aria-hidden="true" />
        <span>{$t('decks.retry')}</span>
      </Button>
    </div>
  {:else if decks.length === 0}
    <div data-testid="decks-empty" class="space-y-4 rounded-lg border border-dashed border-border py-16 text-center">
      <p class="text-sm text-muted-foreground">{$t('decks.empty')}</p>
      <Button variant="outline" size="lg" onclick={openCreateModal}>
        <Plus class="size-4" aria-hidden="true" />
        {$t('decks.create.heading')}
      </Button>
    </div>
  {:else}
    <div data-testid="decks-list" class={listClasses.root}>
      <!-- The header doubles as select-all: a selection state change only alters this row's checkbox, without inserting any toolbar that would make the page taller. -->
      <div class={listClasses.header}>
        <Checkbox
          checked={selectedDeckIds.length > 0 && selectedDeckIds.length === decks.length}
          indeterminate={selectedDeckIds.length > 0 && selectedDeckIds.length < decks.length}
          onCheckedChange={toggleSelectAll}
          label={selectedDeckIds.length === decks.length ? $t('decks.deselect_all') : $t('decks.select_all')}
        />
        <span class="flex-1">{$t('list.col.name')}</span>
        <span class="hidden w-36 sm:block">{$t('list.col.today')}</span>
        <span class="hidden w-40 lg:block" title={$t('decks.progress_hint')}>{$t('list.col.progress')}</span>
        <span class="hidden w-24 md:block" title={$t('home.deck_limits')}>{$t('list.col.limits')}</span>
        <span class="w-8" aria-hidden="true"></span>
      </div>
      {#each decks as deck (deck.id)}
        <div data-testid={`deck-card-${deck.id}`} class={listClasses.row}>
          <Checkbox
            class={listClasses.rowAction}
            checked={selectedDeckIds.includes(deck.id)}
            onCheckedChange={() => toggleSelectDeck(deck.id)}
            label={$t('decks.select_deck', { name: deck.name })}
          />
          <div class="flex min-w-0 flex-1 flex-col">
            <div class="flex min-w-0 items-center gap-2">
              <a href="/decks/{encodeURIComponent(deck.id)}" class={listClasses.rowLink}>{deck.name}</a>
              {#if deckActionKind(deck.role) === 'leave'}
                <Badge testId={`deck-shared-badge-${deck.id}`}>{$t('decks.shared_badge')}</Badge>
              {/if}
            </div>
            {#if deck.description}
              <span class="truncate text-[13px] text-muted-foreground">{deck.description}</span>
            {/if}
          </div>
          <span data-testid={`deck-queue-count-${deck.id}`} class="hidden w-36 text-[13px] tabular-nums text-foreground/80 sm:block">
            {$t('decks.queue_counts', { new: queueCounts[deck.id]?.new_count ?? 0, review: queueCounts[deck.id]?.review_count ?? 0 })}
          </span>
          <span data-testid={`deck-progress-${deck.id}`} class="hidden w-40 text-[13px] tabular-nums text-muted-foreground lg:block" title={$t('decks.progress_hint')}>
            {$t('decks.progress', { studied: queueCounts[deck.id]?.studied_count ?? 0, remaining: queueCounts[deck.id]?.remaining_count ?? 0 })}
          </span>
          <span class="hidden w-24 text-[13px] tabular-nums text-muted-foreground md:block" title={$t('home.deck_limits')}>
            {deck.new_per_day} / {deck.reviews_per_day}
          </span>
          <div class="{listClasses.rowAction} flex w-8 justify-end">
            {#if deckActionKind(deck.role) === 'delete'}
              <button
                type="button"
                data-testid={`deck-delete-btn-${deck.id}`}
                title={$t('decks.delete.action')}
                aria-label={$t('decks.delete.action')}
                class="inline-flex size-8 items-center justify-center rounded-md text-muted-foreground opacity-60 transition hover:bg-destructive-soft hover:text-destructive-foreground hover:opacity-100 focus-visible:opacity-100 group-hover:opacity-100 cursor-pointer"
                onclick={(e) => promptDeckAction(deck, 'delete', e)}
              >
                <Trash2 class="size-4" aria-hidden="true" />
              </button>
            {:else if deckActionKind(deck.role)}
              <button
                type="button"
                data-testid={`deck-leave-btn-${deck.id}`}
                title={$t('decks.leave.action')}
                aria-label={$t('decks.leave.action')}
                class="inline-flex size-8 items-center justify-center rounded-md text-muted-foreground opacity-60 transition hover:bg-muted hover:text-foreground hover:opacity-100 focus-visible:opacity-100 group-hover:opacity-100 cursor-pointer"
                onclick={(e) => promptDeckAction(deck, 'leave', e)}
              >
                <LogOut class="size-4" aria-hidden="true" />
              </button>
            {/if}
          </div>
        </div>
      {/each}
    </div>
  {/if}

  <SelectionBar
    count={selectedDeckIds.length}
    onClear={() => (selectedDeckIds = [])}
  >
    <a data-testid="decks-batch-review" class={selectionBarButton} href={batchReviewHref}>
      <CirclePlay aria-hidden="true" />
      {$t('decks.batch_review')}
    </a>
    <button
      type="button"
      data-testid="decks-batch-export"
      class={selectionBarButton}
      onclick={() => { batchExportError = null; showBatchExport = true; }}
    >
      <Download aria-hidden="true" />
      {$t('package.export.short')}
    </button>
    <button
      type="button"
      data-testid="decks-batch-delete"
      class={selectionBarButton}
      onclick={openBatchDelete}
    >
      <Trash2 aria-hidden="true" />
      {$t('decks.bulk_delete')}
    </button>
  </SelectionBar>
</Page>

<!-- Bulk export settings: options match single-deck export, all selected decks are packed into one zip with the same option set -->
<Dialog
  bind:open={showBatchExport}
  title={$t('package.export.heading')}
  testId="decks-batch-export-dialog"
>
  <PackageExportOptions bind:includeMedia bind:includeProgress bind:includeReviews bind:includeWeights />

  {#if batchExportError}
    <p role="alert" class="mt-3 text-sm text-destructive-foreground">{$t(batchExportError)}</p>
  {/if}

  <div class="mt-5 flex items-center justify-end gap-2">
    <Button type="button" variant="outline" size="lg" onclick={() => (showBatchExport = false)}>{$t('note_edit.cancel')}</Button>
    <Button type="button" testId="decks-batch-export-submit" loading={batchExporting} onclick={handleBatchExport} variant="primary" size="lg">
      {$t('package.batch_export', { count: selectedDeckIds.length })}
    </Button>
  </div>
</Dialog>

<!-- Bulk delete confirmation: the same dialog shape as single-deck delete, with one extra line when some selected decks are only shared. -->
<Dialog
  bind:open={showBatchDelete}
  title={$t('decks.bulk_delete.confirm_title', { count: batchDeleteIDs.length })}
  description={$t('decks.bulk_delete.confirm_desc')}
  testId="decks-batch-delete-dialog"
>
  {#if sharedSelectedCount > 0}
    <p data-testid="decks-batch-delete-shared-hint" class="mt-3 text-sm text-muted-foreground">
      {$t('decks.bulk_delete.shared_hint', { count: sharedSelectedCount })}
    </p>
  {/if}

  {#if batchDeleteError}
    <p role="alert" class="mt-3 text-sm text-destructive-foreground">{$t(batchDeleteError)}</p>
  {/if}

  <div class="mt-5 flex items-center justify-end gap-2">
    <Button type="button" variant="outline" size="lg" disabled={batchDeleting} onclick={closeBatchDelete}>{$t('note_edit.cancel')}</Button>
    <Button type="button" testId="decks-batch-delete-confirm" variant="danger" size="lg" loading={batchDeleting} onclick={handleBatchDelete}>
      {$t('decks.bulk_delete.confirm_btn')}
    </Button>
  </div>
</Dialog>

<!-- New deck dialog (Modal) -->
{#if showCreateModal}
  <Dialog
    open={true}
    onOpenChange={(open) => { if (!open) closeCreateModal(); }}
    title={$t('decks.create.heading')}
    size="lg"
    testId="deck-create-dialog"
  >
      <form onsubmit={createDeck} class="space-y-4">
        <div>
          <label for="deck-name-input" class="block text-sm font-medium text-foreground/80 mb-1">
            {$t('decks.create.name')}
          </label>
          <input
            id="deck-name-input"
            data-testid="deck-create-name"
            bind:value={name}
            required
            maxlength="200"
            placeholder={$t('decks.create.name_placeholder')}
            class="field-input text-sm block w-full"
          />
        </div>

        <div>
          <label for="deck-desc-input" class="block text-sm font-medium text-foreground/80 mb-1">
            {$t('decks.create.description')}
          </label>
          <textarea
            id="deck-desc-input"
            data-testid="deck-create-description"
            bind:value={description}
            maxlength="2000"
            rows="3"
            placeholder={$t('decks.create.description_placeholder')}
            class="field-input text-sm block w-full"
          ></textarea>
          <div>
            <label class="block text-sm font-medium text-foreground/80 mb-1" for="deck-create-preset">
              {$t('decks.create.preset')}
            </label>
            <Select
              class="w-full"
              testId="deck-create-preset"
              value={createPresetId}
              placeholder={$t('decks.preset_default')}
              onValueChange={(value: string) => (createPresetId = value)}
              options={createPresets}
            />
          </div>
        </div>

        {#if createError}
          <p role="alert" class="text-sm text-destructive-foreground">{$t(createError)}</p>
        {/if}

        <div class="pt-2 flex items-center justify-end gap-3">
          <Button variant="outline" size="lg" onclick={closeCreateModal}>{$t('note_edit.cancel')}</Button>
          <Button type="submit" size="lg" testId="deck-create-submit" loading={creating}>
            {$t('decks.create.submit')}
          </Button>
        </div>
      </form>
  </Dialog>
{/if}

<!-- Second-step confirmation dialog for dangerous deck operations (Modal): delete own deck / leave shared deck share one -->
{#if deckAction}
  <Dialog
    open={true}
    onOpenChange={(open) => { if (!open) closeDeckAction(); }}
    title={$t(deckAction.kind === 'delete' ? 'decks.delete.confirm_title' : 'decks.leave.confirm_title')}
    description={$t(deckAction.kind === 'delete' ? 'decks.delete.confirm_desc' : 'decks.leave.confirm_desc', { name: deckAction.deck.name })}
    testId={deckAction.kind === 'delete' ? 'deck-delete-dialog' : 'deck-leave-dialog'}
  >
      {#if actionError}
        <p role="alert" class="text-sm text-destructive-foreground">{$t(actionError)}</p>
      {/if}

      <div class="pt-2 flex items-center justify-end gap-3">
        <Button variant="outline" size="lg" disabled={acting} onclick={closeDeckAction}>
          {$t(deckAction.kind === 'delete' ? 'decks.delete.cancel_btn' : 'decks.leave.cancel_btn')}
        </Button>
        <Button
          variant={deckAction.kind === 'delete' ? 'danger' : 'danger-outline'}
          size="lg"
          loading={acting}
          onclick={confirmDeckAction}
          testId={deckAction.kind === 'delete' ? 'deck-delete-confirm' : 'deck-leave-confirm'}
        >
          {$t(deckAction.kind === 'delete' ? 'decks.delete.confirm_btn' : 'decks.leave.confirm_btn')}
        </Button>
      </div>
  </Dialog>
{/if}
