<script lang="ts">
  import { onMount } from 'svelte';
  import { routeStore } from '../router';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { Deck, Note, BulkNotesResponse } from '../api';
  import DeckSharingView from './DeckSharingView.svelte';
  import DeckSettingsView from './DeckSettingsView.svelte';

  // 状态变量（Svelte 5 runes）
  let loading = $state(true);
  let error = $state<ApiClientError | Error | null>(null);
  let deck = $state<Deck | null>(null);
  let notes = $state<Note[]>([]);
  let total = $state(0);
  let page = $state(1);
  let activeTab = $state<'cards' | 'sharing' | 'settings'>('cards');

  // 逐条删除状态
  let confirmingDeleteId = $state<number | null>(null);
  let deletingNoteId = $state<number | null>(null);
  let deleteError = $state('');
  let deleteSuccess = $state(false);

  // 导出对话框状态
  let showExportModal = $state(false);
  let exporting = $state(false);
  let exportError = $state(false);
  let includeMedia = $state(true);
  let includeProgress = $state(false);
  let includeReviews = $state(false);

  // owner 才显示卡组设置入口：以服务端是否返回卡组设置判定
  let isOwner = $state(false);
  const perPage = 50;

  // 筛选与搜索输入
  let queryInput = $state('');
  let tagInput = $state('');
  let kindSelect = $state('');
  let statusSelect = $state<'active' | 'deleted'>('active');

  // 已经应用的筛选条件
  let appliedQ = $state('');
  let appliedTag = $state('');
  let appliedKind = $state('');
  let appliedStatus = $state<'active' | 'deleted'>('active');

  // 批量动作
  let selectedIds = $state<number[]>([]);
  let bulkTagInput = $state('');
  let bulkBusy = $state(false);
  let bulkError = $state('');
  let confirmingBulkDelete = $state(false);
  let bulkResult = $state<{ affected: number; notFound: number; insufficientRole: number } | null>(null);

  const deckId = $derived($routeStore.params.id || '');
  const totalPages = $derived(Math.max(1, Math.ceil(total / perPage)));
  const hasFilter = $derived(Boolean(appliedQ || appliedTag || appliedKind) || appliedStatus !== 'active');
  const allSelected = $derived(notes.length > 0 && notes.every((n) => selectedIds.includes(n.id)));

  /**
   * 加载卡组卡片数据及卡组元数据
   */
  async function loadData(targetPage = 1): Promise<void> {
    if (!deckId) return;
    loading = true;
    error = null;
    page = targetPage;
    selectedIds = [];
    confirmingBulkDelete = false;

    try {
      if (!deck) {
        try {
          deck = await apiClient.getDeck(deckId);
        } catch {}
      }

      const res = await apiClient.getDeckNotes(deckId, {
        page: targetPage,
        per_page: perPage,
        q: appliedQ || undefined,
        tag: appliedTag || undefined,
        kind: appliedKind || undefined,
        status: appliedStatus,
      });

      notes = res.notes;
      total = res.total;
      page = res.page;
    } catch (err) {
      error = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  function handleFilterSubmit(e?: Event): void {
    if (e) e.preventDefault();
    appliedQ = queryInput.trim();
    appliedTag = tagInput.trim();
    appliedKind = kindSelect;
    appliedStatus = statusSelect;
    loadData(1);
  }

  function handleFilterReset(): void {
    queryInput = '';
    tagInput = '';
    kindSelect = '';
    statusSelect = 'active';
    appliedQ = '';
    appliedTag = '';
    appliedKind = '';
    appliedStatus = 'active';
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

  function isSelected(id: number): boolean {
    return selectedIds.includes(id);
  }

  function toggleSelect(id: number): void {
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
    bulkBusy = true;
    bulkError = '';
    bulkResult = null;
    confirmingBulkDelete = false;
    try {
      const res = await apiClient.bulkNotes({ action, note_ids: selectedIds, tags, dry_run: false });
      bulkResult = summarizeBulk(res);
      bulkTagInput = '';
      await loadData(1);
    } catch (err) {
      bulkError = err instanceof ApiClientError && err.isForbidden ? 'error.forbidden' : 'notes.bulk_failed';
    } finally {
      bulkBusy = false;
    }
  }

  async function deleteNote(note: Note): Promise<void> {
    deletingNoteId = note.id;
    deleteError = '';
    try {
      await apiClient.deleteNote(note.id);
      notes = notes.filter((item) => item.id !== note.id);
      total = Math.max(0, total - 1);
      confirmingDeleteId = null;
      deleteSuccess = true;
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
      const { blob, filename } = await apiClient.downloadDeckPackage(deckId, { includeMedia, includeProgress, includeReviews });
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

  function formatFieldValue(val: unknown): string {
    if (val === null || val === undefined) return '';
    if (typeof val === 'string') return val;
    if (typeof val === 'number' || typeof val === 'boolean') return String(val);
    if (Array.isArray(val)) {
      return val
        .map((item) => (typeof item === 'object' ? JSON.stringify(item) : String(item)))
        .join(', ');
    }
    return JSON.stringify(val, null, 2);
  }

  async function loadOwnerFlag(): Promise<void> {
    if (!deckId) return;
    try {
      await apiClient.getDeckSettings(deckId);
      isOwner = true;
    } catch {
      isOwner = false;
    }
  }

  onMount(() => {
    loadData(1);
    loadOwnerFlag();
  });
</script>

<div class="py-10 max-w-4xl mx-auto px-4 space-y-6">
  <!-- 顶栏精炼导航与卡组信息 -->
  <div class="card-elevated p-6 sm:p-8 rounded-2xl">
    <div class="flex items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400 mb-3">
      <a href="/decks" data-testid="back-to-decks" class="hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors inline-flex items-center gap-1 font-medium">
        <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="15 18 9 12 15 6"/></svg>
        <span>卡组列表</span>
      </a>
      <span>/</span>
      <span class="text-zinc-800 dark:text-zinc-200 font-medium truncate max-w-xs">{deck ? deck.name : `#${deckId}`}</span>
    </div>

    <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
      <div>
        <div class="flex items-center gap-2.5">
          <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100" data-testid="deck-title">
            {deck ? deck.name : $t('notes.deck_title', { id: deckId })}
          </h1>
          {#if deck?.visibility}
            <span class="px-2 py-0.5 rounded-full text-xs font-mono font-medium bg-zinc-100 dark:bg-zinc-800 text-zinc-600 dark:text-zinc-400 border border-zinc-200/60 dark:border-zinc-700/60">
              {deck.visibility}
            </span>
          {/if}
        </div>
        {#if deck?.description}
          <p class="text-xs sm:text-sm text-zinc-600 dark:text-zinc-400 mt-1.5 max-w-2xl leading-relaxed">
            {deck.description}
          </p>
        {/if}
      </div>

      <!-- 右侧主要动作区 -->
      <div class="flex items-center gap-2.5 self-start sm:self-auto shrink-0">
        <button
          type="button"
          onclick={() => showExportModal = true}
          class="inline-flex items-center gap-1.5 px-3 py-2 rounded-xl border border-zinc-200 dark:border-zinc-700/80 bg-white dark:bg-zinc-800 hover:bg-zinc-50 dark:hover:bg-zinc-700 text-xs font-semibold text-zinc-700 dark:text-zinc-200 transition-colors cursor-pointer"
        >
          <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
          <span>导出</span>
        </button>

        <a
          href="/decks/{encodeURIComponent(deckId)}/notes/new"
          data-testid="create-note-link"
          class="inline-flex items-center gap-1.5 rounded-xl bg-zinc-900 px-3.5 py-2 text-xs font-semibold text-white hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-zinc-200 transition-colors shadow-xs cursor-pointer"
        >
          <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
          <span>{$t('notes.create')}</span>
        </a>
      </div>
    </div>

    <!-- 统一 Tab 标签栏导航 -->
    <div class="mt-6 pt-2 border-t border-zinc-100 dark:border-zinc-800/80 flex items-center gap-2">
      <button
        type="button"
        onclick={() => activeTab = 'cards'}
        class="px-3.5 py-1.5 text-xs font-semibold rounded-lg transition-colors cursor-pointer {activeTab === 'cards' ? 'bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900' : 'text-zinc-600 dark:text-zinc-400 hover:bg-zinc-100 dark:hover:bg-zinc-800'}"
      >
        卡片列表 ({total})
      </button>

      <button
        type="button"
        data-testid="deck-sharing-link"
        onclick={() => activeTab = 'sharing'}
        class="px-3.5 py-1.5 text-xs font-semibold rounded-lg transition-colors cursor-pointer {activeTab === 'sharing' ? 'bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900' : 'text-zinc-600 dark:text-zinc-400 hover:bg-zinc-100 dark:hover:bg-zinc-800'}"
      >
        {$t('deck.sharing.title')}
      </button>

      {#if isOwner}
        <button
          type="button"
          data-testid="deck-settings-link"
          onclick={() => activeTab = 'settings'}
          class="px-3.5 py-1.5 text-xs font-semibold rounded-lg transition-colors cursor-pointer {activeTab === 'settings' ? 'bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900' : 'text-zinc-600 dark:text-zinc-400 hover:bg-zinc-100 dark:hover:bg-zinc-800'}"
        >
          {$t('deck.settings.entry')}
        </button>
      {/if}
    </div>
  </div>

  <!-- Tab 内容区域 -->
  {#if activeTab === 'sharing'}
    <DeckSharingView embedded />
  {:else if activeTab === 'settings'}
    <DeckSettingsView embedded />
  {:else}
    <!-- 卡片管理 Tab -->
    <div class="card-elevated p-6 sm:p-8 rounded-2xl space-y-5">
      <!-- 紧凑搜索与筛选栏 -->
      <form
        onsubmit={handleFilterSubmit}
        class="flex flex-wrap gap-2 items-center pb-4 border-b border-zinc-100 dark:border-zinc-800/80"
        data-testid="notes-filter-form"
      >
        <input
          type="text"
          data-testid="filter-query-input"
          placeholder={$t('notes.search_placeholder')}
          bind:value={queryInput}
          class="text-xs rounded-xl border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 px-3 py-1.5 w-full sm:w-44"
        />
        <input
          type="text"
          data-testid="filter-tag-input"
          placeholder={$t('notes.tag_placeholder')}
          bind:value={tagInput}
          class="text-xs rounded-xl border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 px-3 py-1.5 w-full sm:w-32"
        />
        <select
          data-testid="filter-kind-select"
          bind:value={kindSelect}
          class="text-xs rounded-xl border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 px-2.5 py-1.5 cursor-pointer"
        >
          <option value="">{$t('notes.all_kinds')}</option>
          <option value="basic">basic</option>
          <option value="basic_both">basic_both</option>
          <option value="cloze">cloze</option>
          <option value="list">list</option>
          <option value="typed">typed</option>
          <option value="numeric">numeric</option>
          <option value="choice_single">choice_single</option>
          <option value="choice_multi">choice_multi</option>
          <option value="true_false">true_false</option>
          <option value="short_answer">short_answer</option>
        </select>
        <select
          data-testid="filter-status-select"
          bind:value={statusSelect}
          class="text-xs rounded-xl border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 px-2.5 py-1.5 cursor-pointer"
        >
          <option value="active">{$t('notes.status_active')}</option>
          <option value="deleted">{$t('notes.status_deleted')}</option>
        </select>
        <button
          type="submit"
          data-testid="filter-apply-btn"
          class="text-xs px-3 py-1.5 rounded-xl font-medium bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors btn-press cursor-pointer"
        >
          {$t('notes.filter_apply')}
        </button>
        {#if hasFilter}
          <button
            type="button"
            data-testid="filter-reset-btn"
            class="text-xs px-2.5 py-1.5 rounded-xl font-medium text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors cursor-pointer"
            onclick={handleFilterReset}
          >
            {$t('notes.filter_reset')}
          </button>
        {/if}
      </form>

      {#if appliedStatus === 'deleted'}
        <p role="note" data-testid="notes-deleted-notice" class="text-xs text-amber-700 dark:text-amber-400 bg-amber-50 dark:bg-amber-950/40 px-3 py-2 rounded-lg border border-amber-200 dark:border-amber-800/80">
          {$t('notes.deleted_readonly')}
        </p>
      {/if}

      <!-- 批量操作工具条 -->
      {#if appliedStatus === 'active' && !loading && !error && notes.length > 0}
        <div
          data-testid="notes-bulk-toolbar"
          class="flex flex-wrap items-center gap-2 rounded-xl border border-zinc-200/80 dark:border-zinc-800/80 bg-zinc-50 dark:bg-zinc-900/60 px-3 py-2 text-xs"
        >
          <label class="flex items-center gap-1.5 font-medium text-zinc-600 dark:text-zinc-400 cursor-pointer select-none">
            <input type="checkbox" data-testid="bulk-select-all" checked={allSelected} onchange={toggleSelectAll} class="rounded text-blue-600 focus:ring-blue-500" />
            {$t('notes.select_all')}
          </label>
          <span data-testid="bulk-selected-count" class="text-zinc-400 dark:text-zinc-500">
            {$t('notes.selected_count', { count: selectedIds.length })}
          </span>

          <input
            type="text"
            data-testid="bulk-tag-input"
            placeholder={$t('notes.bulk_tag_placeholder')}
            bind:value={bulkTagInput}
            class="rounded-lg border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-2.5 py-1 text-xs w-36"
          />
          <button type="button" data-testid="bulk-add-tags" disabled={bulkBusy || selectedIds.length === 0} onclick={() => runBulk('add_tags')} class="px-2.5 py-1 rounded-lg border border-zinc-300 dark:border-zinc-700 disabled:opacity-40 cursor-pointer">{$t('notes.bulk_add_tags')}</button>
          <button type="button" data-testid="bulk-remove-tags" disabled={bulkBusy || selectedIds.length === 0} onclick={() => runBulk('remove_tags')} class="px-2.5 py-1 rounded-lg border border-zinc-300 dark:border-zinc-700 disabled:opacity-40 cursor-pointer">{$t('notes.bulk_remove_tags')}</button>
          <button type="button" data-testid="bulk-set-tags" disabled={bulkBusy || selectedIds.length === 0} onclick={() => runBulk('set_tags')} class="px-2.5 py-1 rounded-lg border border-zinc-300 dark:border-zinc-700 disabled:opacity-40 cursor-pointer">{$t('notes.bulk_set_tags')}</button>

          {#if confirmingBulkDelete}
            <span class="text-zinc-600 dark:text-zinc-400">{$t('notes.bulk_confirm_delete')}</span>
            <button type="button" data-testid="bulk-confirm-delete" disabled={bulkBusy} class="text-rose-700 dark:text-rose-400 font-semibold underline cursor-pointer" onclick={() => runBulk('delete')}>{$t(bulkBusy ? 'notes.bulk_applying' : 'notes.bulk_delete')}</button>
            <button type="button" data-testid="bulk-cancel-delete" class="underline cursor-pointer" onclick={() => confirmingBulkDelete = false}>{$t('note_edit.cancel')}</button>
          {:else}
            <button type="button" data-testid="bulk-delete" disabled={bulkBusy || selectedIds.length === 0} class="px-2.5 py-1 rounded-lg border border-rose-200 dark:border-rose-900 text-rose-700 dark:text-rose-400 disabled:opacity-40 cursor-pointer" onclick={() => { confirmingBulkDelete = true; bulkError = ''; bulkResult = null; }}>{$t('notes.bulk_delete')}</button>
          {/if}
          {#if bulkBusy}<span class="text-zinc-400">{$t('notes.bulk_applying')}</span>{/if}
        </div>
      {/if}

      {#if bulkError}
        <p role="alert" data-testid="notes-bulk-error" class="text-xs text-rose-600 dark:text-rose-400">{$t(bulkError)}</p>
      {/if}
      {#if bulkResult}
        <div role="status" data-testid="notes-bulk-result" class="text-xs text-zinc-700 dark:text-zinc-300">
          <p>{$t('notes.bulk_result', { affected: bulkResult.affected, skipped: bulkResult.notFound + bulkResult.insufficientRole })}</p>
          {#if bulkResult.notFound > 0}
            <p data-testid="notes-bulk-skipped-not-found" class="text-zinc-400">{$t('notes.bulk_skipped_not-found', { count: bulkResult.notFound })}</p>
          {/if}
          {#if bulkResult.insufficientRole > 0}
            <p data-testid="notes-bulk-skipped-forbidden" class="text-zinc-400">{$t('notes.bulk_skipped_forbidden', { count: bulkResult.insufficientRole })}</p>
          {/if}
        </div>
      {/if}

      {#if deleteSuccess}
        <p role="status" data-testid="note-delete-success" class="text-xs text-emerald-700 dark:text-emerald-400">{$t('notes.delete_success')}</p>
      {/if}
      {#if deleteError}
        <p role="alert" data-testid="note-delete-error" class="text-xs text-rose-600 dark:text-rose-400">{$t(deleteError)}</p>
      {/if}

      {#if loading}
        <div data-testid="notes-loading" class="space-y-3">
          {#each [1, 2, 3] as item (item)}
            <div class="p-4 rounded-xl border border-zinc-200/60 dark:border-zinc-800/60 space-y-2 skeleton-block">
              <div class="h-4 w-28 bg-zinc-200 dark:bg-zinc-700/60 rounded"></div>
              <div class="h-4 w-3/4 bg-zinc-200 dark:bg-zinc-700/60 rounded"></div>
            </div>
          {/each}
        </div>
      {:else if error}
        <div
          data-testid={error instanceof ApiClientError && error.isUnauthorized
            ? 'notes-unauthorized'
            : error instanceof ApiClientError && error.isForbidden
              ? 'notes-forbidden'
              : error instanceof ApiClientError && error.isNotFound
                ? 'notes-not-found'
                : 'notes-failed'}
          class="py-12 text-center space-y-3"
        >
          <p class="text-sm font-medium text-zinc-900 dark:text-zinc-100">
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
          <button
            data-testid="notes-retry"
            type="button"
            class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-semibold rounded-lg bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors cursor-pointer"
            onclick={() => loadData(page)}
          >
            <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M3 12a9 9 0 0 1 15-6.7L21 8" /><path d="M21 3v5h-5" /><path d="M21 12a9 9 0 0 1-15 6.7L3 16" /><path d="M3 21v-5h5" /></svg>
            <span>{$t('notes.retry')}</span>
          </button>
        </div>
      {:else if notes.length === 0}
        <div data-testid="notes-empty" class="py-16 text-center text-zinc-500 dark:text-zinc-400">
          <p class="text-sm font-medium">
            {hasFilter ? $t('notes.empty_filter') : $t('notes.empty')}
          </p>
        </div>
      {:else}
        <!-- 扁平化独立卡片项：消除四层嵌套与大色块包裹 -->
        <div data-testid="notes-list" class="space-y-3">
          {#each notes as note (note.id)}
            <div data-testid={`note-card-${note.id}`} class="p-4 rounded-xl border border-zinc-200/80 dark:border-zinc-800 bg-white dark:bg-zinc-900/40 hover:border-zinc-300 dark:hover:border-zinc-700 transition-colors space-y-2.5">
              <div class="flex items-center justify-between text-xs pb-2 border-b border-zinc-100 dark:border-zinc-800/60">
                <div class="flex items-center gap-2">
                  {#if appliedStatus === 'active'}
                    <input
                      type="checkbox"
                      data-testid="select-note-{note.id}"
                      checked={isSelected(note.id)}
                      onchange={() => toggleSelect(note.id)}
                      aria-label={$t('notes.select_all')}
                      class="rounded text-blue-600 focus:ring-blue-500 cursor-pointer"
                    />
                  {/if}
                  <span class="font-mono font-medium text-zinc-500 dark:text-zinc-400">#{note.id}</span>
                  <span class="px-2 py-0.5 rounded font-mono text-xs bg-zinc-100 dark:bg-zinc-800 text-zinc-700 dark:text-zinc-300 font-semibold">
                    {note.kind}
                  </span>
                  {#if note.external_ref}
                    <span class="text-zinc-400 dark:text-zinc-500 font-mono text-xs" title="External Ref">
                      [{note.external_ref}]
                    </span>
                  {/if}
                </div>

                <div class="flex items-center gap-3">
                  <span class="text-zinc-400 text-xs">
                    {note.created_at ? note.created_at.slice(0, 10) : ''}
                  </span>
                  {#if appliedStatus === 'active'}
                    <a data-testid="edit-note-{note.id}" href="/decks/{deckId}/notes/{note.id}/edit" class="text-blue-600 dark:text-blue-400 hover:underline font-medium">{$t('note_edit.action')}</a>
                    {#if confirmingDeleteId === note.id}
                      <span class="text-zinc-500">{$t('notes.delete_confirm')}</span>
                      <button data-testid="confirm-delete-note-{note.id}" type="button" disabled={deletingNoteId === note.id} class="text-rose-700 dark:text-rose-400 font-semibold underline disabled:opacity-50 cursor-pointer" onclick={() => deleteNote(note)}>{$t(deletingNoteId === note.id ? 'notes.deleting' : 'notes.delete')}</button>
                      <button type="button" class="underline cursor-pointer" onclick={() => confirmingDeleteId = null}>{$t('note_edit.cancel')}</button>
                    {:else}
                      <button data-testid="delete-note-{note.id}" type="button" class="text-rose-700 dark:text-rose-400 hover:underline cursor-pointer" onclick={() => { confirmingDeleteId = note.id; deleteError = ''; deleteSuccess = false; }}>{$t('notes.delete')}</button>
                    {/if}
                  {:else}
                    <span data-testid="note-deleted-badge-{note.id}" class="px-2 py-0.5 rounded text-xs bg-amber-100 dark:bg-amber-950/50 text-amber-800 dark:text-amber-400">{$t('notes.deleted_badge')}</span>
                  {/if}
                </div>
              </div>

              <!-- 字段内容展示：自然排版，不套多余深色框 -->
              <div class="space-y-1.5 text-xs" data-testid={`note-fields-${note.id}`}>
                {#each Object.entries(note.fields) as [fieldName, fieldValue] (fieldName)}
                  <div class="flex flex-col sm:flex-row sm:items-baseline gap-1 sm:gap-3">
                    <span class="font-semibold text-zinc-500 dark:text-zinc-400 sm:w-20 shrink-0 capitalize">
                      {fieldName}:
                    </span>
                    <span class="font-mono text-zinc-800 dark:text-zinc-200 whitespace-pre-wrap break-words flex-1 leading-relaxed">
                      {formatFieldValue(fieldValue)}
                    </span>
                  </div>
                {/each}
              </div>

              <!-- 标签展示 -->
              {#if note.tags && note.tags.length > 0}
                <div class="flex flex-wrap gap-1.5 pt-1.5 border-t border-zinc-100 dark:border-zinc-800/40" data-testid={`note-tags-${note.id}`}>
                  {#each note.tags as tag (tag)}
                    <span class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-zinc-100 dark:bg-zinc-800 text-zinc-600 dark:text-zinc-400">
                      #{tag}
                    </span>
                  {/each}
                </div>
              {/if}
            </div>
          {/each}
        </div>

        <!-- 分页栏 -->
        {#if totalPages > 1}
          <div data-testid="notes-pagination" class="flex items-center justify-between pt-4 border-t border-zinc-100 dark:border-zinc-800 text-xs">
            <button
              data-testid="notes-prev-page"
              type="button"
              disabled={page <= 1}
              class="px-3 py-1.5 font-medium rounded-lg border border-zinc-300 dark:border-zinc-700 disabled:opacity-40 cursor-pointer"
              onclick={handlePrevPage}
            >
              {$t('notes.prev_page')}
            </button>
            <span class="text-zinc-500 dark:text-zinc-400" data-testid="notes-page-info">
              {$t('notes.page_info', { page, totalPages })}
            </span>
            <button
              data-testid="notes-next-page"
              type="button"
              disabled={page >= totalPages}
              class="px-3 py-1.5 font-medium rounded-lg border border-zinc-300 dark:border-zinc-700 disabled:opacity-40 cursor-pointer"
              onclick={handleNextPage}
            >
              {$t('notes.next_page')}
            </button>
          </div>
        {/if}
      {/if}
    </div>
  {/if}
</div>

<!-- 导出包设置对话框（Modal） -->
{#if showExportModal}
  <div class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50 backdrop-blur-xs animate-in fade-in duration-150" role="dialog" aria-modal="true">
    <div
      role="document"
      class="card-elevated w-full max-w-md p-6 rounded-2xl shadow-xl space-y-4 animate-in zoom-in-95 duration-150"
    >
      <div class="flex items-center justify-between pb-3 border-b border-zinc-100 dark:border-zinc-800">
        <h2 class="text-base font-bold text-zinc-900 dark:text-zinc-100">导出卡组包 (.edeck)</h2>
        <button type="button" class="p-1 rounded-lg text-zinc-400 hover:text-zinc-700 dark:hover:text-zinc-200 cursor-pointer" onclick={() => showExportModal = false} aria-label="关闭">
          <svg class="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
        </button>
      </div>

      <div class="space-y-3 text-xs text-zinc-700 dark:text-zinc-300">
        <label class="flex items-center gap-2 cursor-pointer">
          <input type="checkbox" bind:checked={includeMedia} class="rounded text-blue-600 focus:ring-blue-500" />
          <span>{$t('package.export.include_media')}</span>
        </label>
        <label class="flex items-center gap-2 cursor-pointer">
          <input type="checkbox" bind:checked={includeProgress} onchange={() => { if (!includeProgress) includeReviews = false; }} class="rounded text-blue-600 focus:ring-blue-500" />
          <span>{$t('package.export.include_progress')}</span>
        </label>
        {#if includeProgress}
          <label class="flex items-center gap-2 pl-5 cursor-pointer">
            <input type="checkbox" bind:checked={includeReviews} class="rounded text-blue-600 focus:ring-blue-500" />
            <span>{$t('package.export.include_reviews')}</span>
          </label>
        {/if}
      </div>

      {#if exportError}
        <p role="alert" class="text-xs text-rose-600 dark:text-rose-400">{$t('package.export.failed')}</p>
      {/if}

      <div class="pt-2 flex items-center justify-end gap-3">
        <button type="button" onclick={() => showExportModal = false} class="px-3.5 py-1.5 text-xs font-medium rounded-xl border border-zinc-200 dark:border-zinc-700 hover:bg-zinc-50 dark:hover:bg-zinc-800 cursor-pointer">
          取消
        </button>
        <button
          type="button"
          data-testid="deck-package-export"
          disabled={exporting}
          onclick={exportPackage}
          class="px-3.5 py-1.5 text-xs font-semibold rounded-xl bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 hover:bg-zinc-800 dark:hover:bg-zinc-200 disabled:opacity-50 transition-colors btn-press cursor-pointer"
        >
          {exporting ? $t('package.export.exporting') : $t('package.export.action')}
        </button>
      </div>
    </div>
  </div>
{/if}
