<script lang="ts">
  import { onMount } from 'svelte';
  import { routeStore } from '../router';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { Deck, Note } from '../api';

  // 状态变量（Svelte 5 runes）
  let loading = $state(true);
  let error = $state<ApiClientError | Error | null>(null);
  let deck = $state<Deck | null>(null);
  let notes = $state<Note[]>([]);
  let total = $state(0);
  let page = $state(1);
  let confirmingDeleteId = $state<number | null>(null);
  let deletingNoteId = $state<number | null>(null);
  let deleteError = $state('');
  let deleteSuccess = $state(false);
  let exporting = $state(false);
  let exportError = $state(false);
  let cloneSubmitting = $state(false);
  let cloneError = $state(false);
  let includeMedia = $state(true);
  let includeProgress = $state(false);
  let includeReviews = $state(false);
  // owner 才显示卡组设置入口：以服务端是否返回卡组设置为准（非 owner 会 403/404），不靠客户端猜测。
  let isOwner = $state(false);
  const perPage = 50;

  // 筛选与搜索输入
  let queryInput = $state('');
  let tagInput = $state('');
  let kindSelect = $state('');

  // 已经应用的筛选条件
  let appliedQ = $state('');
  let appliedTag = $state('');
  let appliedKind = $state('');

  const deckId = $derived($routeStore.params.id || '');
  const totalPages = $derived(Math.max(1, Math.ceil(total / perPage)));

  /**
   * 加载卡组卡片数据及卡组元数据
   * 严格遵守只读约定：仅调用 GET /api/v1/decks/:id/notes 与 GET /api/v1/decks
   */
  async function loadData(targetPage = 1): Promise<void> {
    if (!deckId) return;
    loading = true;
    error = null;
    page = targetPage;

    try {
      // 若卡组元数据尚未加载，尝试拉取
      if (!deck) {
        try {
          deck = await apiClient.getDeck(deckId);
        } catch {
          // getDeck 失败时不中断笔记加载，服务端 GET /api/v1/decks/:id/notes 会进行独立鉴权
        }
      }

      const res = await apiClient.getDeckNotes(deckId, {
        page: targetPage,
        per_page: perPage,
        q: appliedQ || undefined,
        tag: appliedTag || undefined,
        kind: appliedKind || undefined,
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
    loadData(1);
  }

  function handleFilterReset(): void {
    queryInput = '';
    tagInput = '';
    kindSelect = '';
    appliedQ = '';
    appliedTag = '';
    appliedKind = '';
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
      URL.revokeObjectURL(url);
    } catch {
      exportError = true;
    } finally {
      exporting = false;
    }
  }

  async function cloneDeck(): Promise<void> {
    cloneSubmitting = true;
    cloneError = false;
    try {
      const id = encodeURIComponent(String(deckId));
      await apiClient.request<{ id: number; name: string }>(`/api/v1/decks/${id}/clone`, { method: 'POST', body: '{}', headers: { Accept: 'application/json' } });
    } catch {
      cloneError = true;
    } finally {
      cloneSubmitting = false;
    }
  }

  /**
   * 将任意字段值安全转换为纯文本字符串
   * 绝不使用原始 HTML 注入，天然防止 XSS
   */
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

  onMount(() => {
    loadData(1);
    loadOwnerFlag();
  });

  /** 探测 owner 身份：只有 owner 能读到 GET /api/v1/decks/:id/settings，其余一律隐藏设置入口。 */
  async function loadOwnerFlag(): Promise<void> {
    if (!deckId) return;
    try {
      await apiClient.getDeckSettings(deckId);
      isOwner = true;
    } catch {
      isOwner = false;
    }
  }
</script>

<div class="py-10 max-w-5xl mx-auto px-4">
  <div class="mb-6">
    <a
      href="/decks"
      data-testid="back-to-decks"
      class="inline-flex items-center text-sm font-medium text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors"
    >
      &larr; {$t('notes.back_to_decks')}
    </a>
  </div>

  <div class="card-elevated p-8 rounded-xl mb-6">
    <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-4">
      <div>
        <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100" data-testid="deck-title">
          {deck ? deck.name : $t('notes.deck_title', { id: deckId })}
        </h1>
        {#if deck?.description}
          <p class="text-sm text-zinc-600 dark:text-zinc-400 mt-1">
            {deck.description}
          </p>
        {/if}
      </div>

      {#if deck}
        <div class="flex items-center gap-2 text-xs">
          {#if deck.visibility}
            <span class="px-2.5 py-1 rounded bg-zinc-100 dark:bg-zinc-800 text-zinc-600 dark:text-zinc-400 font-mono">
              {deck.visibility}
            </span>
          {/if}
          <span class="px-2.5 py-1 rounded bg-zinc-100 dark:bg-zinc-800 text-zinc-600 dark:text-zinc-400">
            {deck.new_per_day} / {deck.reviews_per_day}
          </span>
        </div>
      {/if}
    </div>

    <div class="mb-4 flex flex-wrap items-center gap-3">
      <a
        href="/decks/{encodeURIComponent(deckId)}/notes/new"
        data-testid="create-note-link"
        class="inline-flex items-center rounded-md bg-zinc-900 px-3 py-2 text-sm font-medium text-white hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-zinc-200"
      >
        {$t('notes.create')}
      </a>
      <a
        href="/decks/{encodeURIComponent(deckId)}/sharing"
        data-testid="deck-sharing-link"
        class="rounded-md border border-zinc-300 dark:border-zinc-700 px-3 py-2 text-sm font-medium"
      >{$t('deck.sharing.title')}</a>
      {#if isOwner}
        <a
          href="/spa/decks/{encodeURIComponent(deckId)}/settings"
          data-testid="deck-settings-link"
          class="rounded-md border border-zinc-300 dark:border-zinc-700 px-3 py-2 text-sm font-medium"
        >{$t('deck.settings.entry')}</a>
      {/if}
      {#if cloneError}<span role="alert" class="text-sm text-rose-700 dark:text-rose-400">{$t('deck.clone.failed')}</span>{/if}
      <button type="button" data-testid="deck-clone" disabled={cloneSubmitting || !deck} onclick={cloneDeck} class="rounded-md border border-zinc-300 dark:border-zinc-700 px-3 py-2 text-sm font-medium disabled:opacity-50">{$t(cloneSubmitting ? 'deck.clone.submitting' : 'deck.clone.action')}</button>
      <label class="flex items-center gap-2 text-xs text-zinc-600 dark:text-zinc-400"><input type="checkbox" bind:checked={includeMedia} />{$t('package.export.include_media')}</label>
      <label class="flex items-center gap-2 text-xs text-zinc-600 dark:text-zinc-400"><input type="checkbox" bind:checked={includeProgress} onchange={() => { if (!includeProgress) includeReviews = false; }} />{$t('package.export.include_progress')}</label>
      {#if includeProgress}<label class="flex items-center gap-2 text-xs text-zinc-600 dark:text-zinc-400"><input type="checkbox" bind:checked={includeReviews} />{$t('package.export.include_reviews')}</label>{/if}
      <button type="button" data-testid="deck-package-export" disabled={exporting || !deck} onclick={exportPackage} class="rounded-md border border-zinc-300 dark:border-zinc-700 px-3 py-2 text-sm font-medium disabled:opacity-50">{$t(exporting ? 'package.export.exporting' : 'package.export.action')}</button>
      {#if exportError}<span role="alert" class="text-sm text-rose-700 dark:text-rose-400">{$t('package.export.failed')}</span>{/if}
    </div>

    <!-- 搜索与筛选表单 -->
    <form
      onsubmit={handleFilterSubmit}
      class="pt-4 border-t border-zinc-200 dark:border-zinc-800 flex flex-wrap gap-2 items-center"
      data-testid="notes-filter-form"
    >
      <input
        type="text"
        data-testid="filter-query-input"
        placeholder={$t('notes.search_placeholder')}
        bind:value={queryInput}
        class="text-xs rounded-md border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 px-3 py-1.5 focus:outline-hidden focus:ring-1 focus:ring-zinc-500 w-full sm:w-48"
      />
      <input
        type="text"
        data-testid="filter-tag-input"
        placeholder={$t('notes.tag_placeholder')}
        bind:value={tagInput}
        class="text-xs rounded-md border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 px-3 py-1.5 focus:outline-hidden focus:ring-1 focus:ring-zinc-500 w-full sm:w-36"
      />
      <select
        data-testid="filter-kind-select"
        bind:value={kindSelect}
        class="text-xs rounded-md border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 px-2.5 py-1.5 focus:outline-hidden focus:ring-1 focus:ring-zinc-500"
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
      <button
        type="submit"
        data-testid="filter-apply-btn"
        class="text-xs px-3 py-1.5 rounded-md font-medium bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors btn-press cursor-pointer"
      >
        {$t('notes.filter_apply')}
      </button>
      {#if appliedQ || appliedTag || appliedKind}
        <button
          type="button"
          data-testid="filter-reset-btn"
          class="text-xs px-2.5 py-1.5 rounded-md font-medium text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors cursor-pointer"
          onclick={handleFilterReset}
        >
          {$t('notes.filter_reset')}
        </button>
      {/if}
      <div class="ml-auto text-xs text-zinc-500 dark:text-zinc-400">
        {$t('notes.total_count', { total })}
      </div>
    </form>
  </div>

  <!-- 卡片列表主体内容 -->
  <div class="card-elevated p-8 rounded-xl">
    {#if deleteSuccess}
      <p role="status" data-testid="note-delete-success" class="mb-4 text-sm text-emerald-700 dark:text-emerald-400">{$t('notes.delete_success')}</p>
    {/if}
    {#if deleteError}
      <p role="alert" data-testid="note-delete-error" class="mb-4 text-sm text-rose-700 dark:text-rose-400">{$t(deleteError)}</p>
    {/if}
    {#if loading}
      <div data-testid="notes-loading" class="py-12 text-center text-zinc-500 dark:text-zinc-400">
        <div class="inline-block animate-spin w-6 h-6 border-2 border-current border-t-transparent rounded-full mb-3" aria-hidden="true"></div>
        <p class="text-sm">{$t('notes.loading')}</p>
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
        class="py-10 text-center"
      >
        <div class="inline-flex items-center justify-center w-12 h-12 rounded-full bg-rose-100 dark:bg-rose-950/50 text-rose-600 dark:text-rose-400 mb-3">
          <svg class="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
          </svg>
        </div>
        <p class="text-base font-medium text-zinc-900 dark:text-zinc-100 mb-2">
          {#if error instanceof ApiClientError && error.isUnauthorized}
            {$t('notes.unauthorized')}
          {:else if error instanceof ApiClientError && error.isForbidden}
            {$t('notes.forbidden')}
          {:else if error instanceof ApiClientError && error.isNotFound}
            {$t('notes.not_found')}
          {:else}
            {$t('notes.failed')}
          {/if}
        </p>
        <div class="mt-4">
          <button
            data-testid="notes-retry"
            type="button"
            class="px-4 py-2 text-sm font-medium rounded-lg bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors btn-press cursor-pointer"
            onclick={() => loadData(page)}
          >
            {$t('notes.retry')}
          </button>
        </div>
      </div>
    {:else if notes.length === 0}
      <div data-testid="notes-empty" class="py-12 text-center text-zinc-500 dark:text-zinc-400">
        <p class="text-base font-medium text-zinc-700 dark:text-zinc-300 mb-1">
          {appliedQ || appliedTag || appliedKind ? $t('notes.empty_filter') : $t('notes.empty')}
        </p>
        {#if appliedQ || appliedTag || appliedKind}
          <button
            type="button"
            class="mt-3 text-xs px-3 py-1.5 rounded-md font-medium text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 bg-zinc-100 dark:bg-zinc-800 transition-colors cursor-pointer"
            onclick={handleFilterReset}
          >
            {$t('notes.filter_reset')}
          </button>
        {/if}
      </div>
    {:else}
      <div data-testid="notes-list" class="space-y-4">
        {#each notes as note (note.id)}
          <div data-testid="note-card-{note.id}" class="card-subtle p-5 rounded-lg border border-zinc-200/60 dark:border-zinc-800/60 flex flex-col gap-3">
            <div class="flex items-center justify-between gap-2 pb-2 border-b border-zinc-200/40 dark:border-zinc-800/40 text-xs">
              <div class="flex items-center gap-2">
                <span class="font-mono font-medium text-zinc-700 dark:text-zinc-300">#{note.id}</span>
                <span class="px-2 py-0.5 rounded font-mono bg-zinc-100 dark:bg-zinc-800 text-zinc-700 dark:text-zinc-300">
                  {note.kind}
                </span>
                {#if note.external_ref}
                  <span class="text-zinc-400 dark:text-zinc-500 text-xs font-mono" title="External Ref">
                    [{note.external_ref}]
                  </span>
                {/if}
              </div>
              <div class="flex items-center gap-3">
                <span class="text-zinc-400 dark:text-zinc-500">
                  {note.created_at ? note.created_at.slice(0, 10) : ''}
                </span>
                <a data-testid="edit-note-{note.id}" href="/decks/{deckId}/notes/{note.id}/edit" class="text-xs underline">{$t('note_edit.action')}</a>
                {#if confirmingDeleteId === note.id}
                  <span class="text-xs">{$t('notes.delete_confirm')}</span>
                  <button data-testid="confirm-delete-note-{note.id}" type="button" disabled={deletingNoteId === note.id} class="text-xs text-rose-700 underline disabled:opacity-50" onclick={() => deleteNote(note)}>{$t(deletingNoteId === note.id ? 'notes.deleting' : 'notes.delete')}</button>
                  <button type="button" class="text-xs underline" onclick={() => confirmingDeleteId = null}>{$t('note_edit.cancel')}</button>
                {:else}
                  <button data-testid="delete-note-{note.id}" type="button" class="text-xs text-rose-700 underline" onclick={() => { confirmingDeleteId = note.id; deleteError = ''; deleteSuccess = false; }}>{$t('notes.delete')}</button>
                {/if}
              </div>
            </div>

            <!-- 内容字段展示（纯文本转义呈现，严禁 HTML 解析） -->
            <div class="space-y-2" data-testid="note-fields-{note.id}">
              {#each Object.entries(note.fields) as [fieldName, fieldValue] (fieldName)}
                <div class="text-xs flex flex-col sm:flex-row sm:items-start gap-1 sm:gap-2">
                  <span class="font-semibold text-zinc-500 dark:text-zinc-400 sm:w-20 shrink-0 capitalize">
                    {fieldName}:
                  </span>
                  <span class="font-mono text-zinc-800 dark:text-zinc-200 whitespace-pre-wrap break-words flex-1 bg-zinc-50 dark:bg-zinc-900/60 px-2 py-1 rounded">
                    {formatFieldValue(fieldValue)}
                  </span>
                </div>
              {/each}
            </div>

            <!-- 标签展示 -->
            {#if note.tags && note.tags.length > 0}
              <div class="flex flex-wrap gap-1.5 pt-2 border-t border-zinc-200/40 dark:border-zinc-800/40" data-testid="note-tags-{note.id}">
                {#each note.tags as tag (tag)}
                  <span class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-zinc-100 dark:bg-zinc-800 text-zinc-600 dark:text-zinc-300">
                    #{tag}
                  </span>
                {/each}
              </div>
            {/if}
          </div>
        {/each}
      </div>

      <!-- 分页控制栏 -->
      {#if totalPages > 1}
        <div data-testid="notes-pagination" class="flex items-center justify-between pt-6 border-t border-zinc-200 dark:border-zinc-800 mt-6">
          <button
            data-testid="notes-prev-page"
            type="button"
            disabled={page <= 1}
            class="px-3 py-1.5 text-xs font-medium rounded-md border border-zinc-300 dark:border-zinc-700 text-zinc-700 dark:text-zinc-300 disabled:opacity-40 disabled:cursor-not-allowed hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors cursor-pointer"
            onclick={handlePrevPage}
          >
            &larr; {$t('notes.prev_page')}
          </button>
          <span class="text-xs text-zinc-500 dark:text-zinc-400" data-testid="notes-page-info">
            {$t('notes.page_info', { page, totalPages })}
          </span>
          <button
            data-testid="notes-next-page"
            type="button"
            disabled={page >= totalPages}
            class="px-3 py-1.5 text-xs font-medium rounded-md border border-zinc-300 dark:border-zinc-700 text-zinc-700 dark:text-zinc-300 disabled:opacity-40 disabled:cursor-not-allowed hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors cursor-pointer"
            onclick={handleNextPage}
          >
            {$t('notes.next_page')} &rarr;
          </button>
        </div>
      {/if}
    {/if}
  </div>
</div>
