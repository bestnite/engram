<script lang="ts">
  import { onMount } from 'svelte';
  import { navigate } from '../router';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { Deck } from '../api';

  // 视图响应式状态定义（Svelte 5 runes）
  let loading = $state(true);
  let error = $state<ApiClientError | Error | null>(null);
  let decks = $state<Deck[]>([]);
  let queueCounts = $state<Record<number, { new_count: number; review_count: number }>>({});

  // 新建卡组弹窗状态
  let showCreateModal = $state(false);
  let name = $state('');
  let description = $state('');
  let creating = $state(false);
  let createError = $state<string | null>(null);

  // 批量选择与导出状态
  let selectedDeckIds = $state<number[]>([]);
  let batchExporting = $state(false);
  let batchExportError = $state<string | null>(null);

  // 删除卡组确认弹窗状态
  let deckToDelete = $state<Deck | null>(null);
  let deleting = $state(false);
  let deleteError = $state<string | null>(null);

  /**
   * 请求后端卡组列表（GET /api/v1/decks）
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

  function openCreateModal(): void {
    name = '';
    description = '';
    createError = null;
    showCreateModal = true;
  }

  function closeCreateModal(): void {
    if (!creating) {
      showCreateModal = false;
    }
  }

  async function createDeck(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    creating = true;
    createError = null;
    try {
      const deck = await apiClient.createDeck({ name, description, visibility: 'private', preset_id: 0 });
      decks = [deck, ...decks];
      queueCounts = { ...queueCounts, [deck.id]: { new_count: 0, review_count: 0 } };
      name = '';
      description = '';
      showCreateModal = false;
    } catch (err) {
      if (err instanceof ApiClientError && err.code === 'deck_name_invalid') {
        createError = 'decks.spa_create.name_invalid';
      } else if (err instanceof ApiClientError && err.code === 'deck_description_invalid') {
        createError = 'decks.spa_create.description_invalid';
      } else if (err instanceof ApiClientError && err.code === 'invalid_request') {
        createError = 'decks.spa_create.invalid_request';
      } else {
        createError = 'decks.spa_create.failed';
      }
    } finally {
      creating = false;
    }
  }

  function toggleSelectDeck(deckId: number, e: Event): void {
    e.stopPropagation();
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

  async function handleBatchExport(): Promise<void> {
    if (selectedDeckIds.length === 0 || batchExporting) return;
    batchExporting = true;
    batchExportError = null;
    try {
      const { blob, filename } = await apiClient.exportDecksZip(selectedDeckIds);
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch {
      batchExportError = 'package.batch_export_failed';
    } finally {
      batchExporting = false;
    }
  }

  function promptDeleteDeck(deck: Deck, e: Event): void {
    e.stopPropagation();
    deckToDelete = deck;
    deleteError = null;
  }

  function closeDeleteModal(): void {
    if (!deleting) {
      deckToDelete = null;
      deleteError = null;
    }
  }

  async function confirmDeleteDeck(): Promise<void> {
    if (!deckToDelete || deleting) return;
    deleting = true;
    deleteError = null;
    try {
      await apiClient.deleteDeck(deckToDelete.id);
      decks = decks.filter((d) => d.id !== deckToDelete!.id);
      selectedDeckIds = selectedDeckIds.filter((id) => id !== deckToDelete!.id);
      deckToDelete = null;
    } catch {
      deleteError = 'decks.delete.failed';
    } finally {
      deleting = false;
    }
  }

  function handleCardClick(e: MouseEvent, deckId: number): void {
    // 忽略复选框、按钮点击触发卡片跳转
    const target = e.target as HTMLElement;
    if (target.closest('input') || target.closest('button')) {
      return;
    }
    navigate(`/decks/${deckId}`);
  }

  function handleKeydown(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      if (showCreateModal) closeCreateModal();
      if (deckToDelete) closeDeleteModal();
    }
  }

  onMount(() => {
    fetchDecks();
    window.addEventListener('keydown', handleKeydown);
    return () => window.removeEventListener('keydown', handleKeydown);
  });
</script>

<div class="py-10 max-w-5xl mx-auto px-4">
  <div class="card-elevated p-6 sm:p-8 rounded-2xl">
    <!-- 顶栏标题与操作 -->
    <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-6 pb-5 border-b border-zinc-100 dark:border-zinc-800">
      <div>
        <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
          {$t('decks.title')}
        </h1>
        <p class="text-xs text-zinc-500 dark:text-zinc-400 mt-1">
          {decks.length} 个卡组
        </p>
      </div>

      <div class="flex items-center gap-2">
        {#if !loading && !error && decks.length > 0}
          <button
            type="button"
            title={$t('decks.retry')}
            aria-label={$t('decks.retry')}
            class="p-2 rounded-xl text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors btn-press cursor-pointer border border-zinc-200 dark:border-zinc-700/80"
            onclick={fetchDecks}
          >
            <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M3 12a9 9 0 0 1 15-6.7L21 8" />
              <path d="M21 3v5h-5" />
              <path d="M21 12a9 9 0 0 1-15 6.7L3 16" />
              <path d="M3 21v-5h5" />
            </svg>
          </button>
        {/if}

        <button
          type="button"
          data-testid="deck-create-open"
          onclick={openCreateModal}
          class="inline-flex items-center gap-2 px-4 py-2 text-sm font-semibold rounded-xl bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors btn-press cursor-pointer shadow-xs"
        >
          <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
            <line x1="12" y1="5" x2="12" y2="19" />
            <line x1="5" y1="12" x2="19" y2="12" />
          </svg>
          <span>{$t('decks.spa_create.heading')}</span>
        </button>
      </div>
    </div>

    <!-- 批量工具栏 -->
    {#if !loading && !error && decks.length > 0}
      <div class="mb-4 flex flex-wrap items-center justify-between gap-3 px-3 py-2 rounded-xl bg-zinc-50 dark:bg-zinc-900/60 border border-zinc-200/60 dark:border-zinc-800/60 text-xs">
        <div class="flex items-center gap-3">
          <label class="flex items-center gap-1.5 cursor-pointer text-zinc-600 dark:text-zinc-400 font-medium select-none">
            <input
              type="checkbox"
              checked={selectedDeckIds.length > 0 && selectedDeckIds.length === decks.length}
              onchange={toggleSelectAll}
              class="rounded text-blue-600 focus:ring-blue-500 cursor-pointer"
            />
            <span>{selectedDeckIds.length === decks.length ? $t('decks.deselect_all') : $t('decks.select_all')}</span>
          </label>
          {#if selectedDeckIds.length > 0}
            <span class="text-zinc-400 dark:text-zinc-500">已选 {selectedDeckIds.length} 个</span>
          {/if}
        </div>

        {#if selectedDeckIds.length > 0}
          <div class="flex items-center gap-2">
            <button
              type="button"
              data-testid="decks-batch-export"
              disabled={batchExporting}
              onclick={handleBatchExport}
              class="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-blue-600 text-white hover:bg-blue-700 disabled:opacity-50 transition-colors font-medium btn-press cursor-pointer"
            >
              <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                <polyline points="7 10 12 15 17 10" />
                <line x1="12" y1="15" x2="12" y2="3" />
              </svg>
              <span>{batchExporting ? '导出中...' : $t('package.batch_export', { count: selectedDeckIds.length })}</span>
            </button>
          </div>
        {/if}
      </div>
      {#if batchExportError}
        <p role="alert" class="mb-4 text-xs text-rose-600 dark:text-rose-400">{$t(batchExportError)}</p>
      {/if}
    {/if}

    <!-- 列表内容区 -->
    {#if loading}
      <!-- 骨架屏 -->
      <div data-testid="decks-loading" class="grid grid-cols-1 sm:grid-cols-2 gap-4">
        {#each [1, 2, 3, 4] as item (item)}
          <div class="p-5 rounded-xl border border-zinc-200/60 dark:border-zinc-800/60 space-y-3 skeleton-block">
            <div class="h-5 w-1/3 bg-zinc-200 dark:bg-zinc-700/60 rounded"></div>
            <div class="h-4 w-3/4 bg-zinc-200 dark:bg-zinc-700/60 rounded"></div>
            <div class="h-4 w-1/2 bg-zinc-200 dark:bg-zinc-700/60 rounded pt-3"></div>
          </div>
        {/each}
      </div>
    {:else if error}
      <div
        data-testid={error instanceof ApiClientError && error.isUnauthorized ? 'decks-unauthorized' : 'decks-failed'}
        class="py-12 text-center space-y-4"
      >
        <div class="inline-flex items-center justify-center w-12 h-12 rounded-full bg-rose-100 dark:bg-rose-950/50 text-rose-600 dark:text-rose-400 mb-1">
          <svg class="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
          </svg>
        </div>
        <p class="text-base font-medium text-zinc-900 dark:text-zinc-100">
          {#if error instanceof ApiClientError && error.isUnauthorized}
            {$t('decks.unauthorized')}
          {:else}
            {$t('decks.failed')}
          {/if}
        </p>
        <button
          data-testid="decks-retry"
          type="button"
          class="inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-xl bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors btn-press cursor-pointer"
          onclick={fetchDecks}
        >
          <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M3 12a9 9 0 0 1 15-6.7L21 8" /><path d="M21 3v5h-5" /><path d="M21 12a9 9 0 0 1-15 6.7L3 16" /><path d="M3 21v-5h5" />
          </svg>
          <span>{$t('decks.retry')}</span>
        </button>
      </div>
    {:else if decks.length === 0}
      <div data-testid="decks-empty" class="py-16 text-center text-zinc-500 dark:text-zinc-400 space-y-3">
        <p class="text-base font-medium text-zinc-700 dark:text-zinc-300">
          {$t('decks.empty')}
        </p>
        <button
          type="button"
          onclick={openCreateModal}
          class="text-xs px-3 py-1.5 rounded-lg bg-zinc-100 dark:bg-zinc-800 text-zinc-700 dark:text-zinc-200 hover:bg-zinc-200 dark:hover:bg-zinc-700 transition-colors cursor-pointer"
        >
          + {$t('decks.spa_create.heading')}
        </button>
      </div>
    {:else}
      <!-- 卡组卡片列表：整体可点击，去除了下划线与多余查看按钮 -->
      <div data-testid="decks-list" class="grid grid-cols-1 sm:grid-cols-2 gap-4">
        {#each decks as deck (deck.id)}
          <div
            role="button"
            tabindex="0"
            data-testid={`deck-card-${deck.id}`}
            class="card-subtle p-5 rounded-xl border border-zinc-200/70 dark:border-zinc-800/70 hover:border-blue-500/50 dark:hover:border-blue-400/50 hover:shadow-sm transition-all duration-150 flex flex-col justify-between cursor-pointer group text-left select-none relative"
            onclick={(e) => handleCardClick(e, deck.id)}
            onkeydown={(e) => { if (e.key === 'Enter') navigate(`/decks/${deck.id}`); }}
          >
            <div>
              <div class="flex items-start justify-between gap-2 mb-2">
                <div class="flex items-center gap-2 flex-1 min-w-0">
                  <input
                    type="checkbox"
                    checked={selectedDeckIds.includes(deck.id)}
                    onclick={(e) => e.stopPropagation()}
                    onchange={(e) => toggleSelectDeck(deck.id, e)}
                    class="rounded text-blue-600 focus:ring-blue-500 cursor-pointer shrink-0 mt-0.5"
                    aria-label={`选择卡组 ${deck.name}`}
                  />
                  <h2 class="text-base font-semibold text-zinc-900 dark:text-zinc-100 group-hover:text-blue-600 dark:group-hover:text-blue-400 transition-colors truncate">
                    {deck.name}
                  </h2>
                </div>

                <div class="flex items-center gap-1.5 shrink-0">
                  {#if deck.visibility}
                    <span class="text-xs px-2 py-0.5 rounded bg-zinc-100 dark:bg-zinc-800 text-zinc-500 dark:text-zinc-400 font-mono">
                      {deck.visibility}
                    </span>
                  {/if}
                  <button
                    type="button"
                    data-testid={`deck-delete-btn-${deck.id}`}
                    title={$t('decks.delete.action')}
                    aria-label={$t('decks.delete.action')}
                    class="p-1 rounded-md text-zinc-400 hover:text-rose-600 dark:hover:text-rose-400 hover:bg-rose-50 dark:hover:bg-rose-950/40 transition-colors cursor-pointer"
                    onclick={(e) => promptDeleteDeck(deck, e)}
                  >
                    <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                      <polyline points="3 6 5 6 21 6" />
                      <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                    </svg>
                  </button>
                </div>
              </div>

              {#if deck.description}
                <p class="text-xs text-zinc-600 dark:text-zinc-400 line-clamp-2 mb-4 leading-relaxed pl-6">
                  {deck.description}
                </p>
              {/if}
            </div>

            <div class="pt-3 border-t border-zinc-200/50 dark:border-zinc-800/50 text-xs text-zinc-400 dark:text-zinc-500 flex items-center justify-between">
              <span class="font-mono">{deck.new_per_day} / {deck.reviews_per_day}</span>
              <span data-testid={`deck-queue-count-${deck.id}`} class="font-medium text-zinc-700 dark:text-zinc-300">
                {$t('decks.queue_counts', { new: queueCounts[deck.id]?.new_count ?? 0, review: queueCounts[deck.id]?.review_count ?? 0 })}
              </span>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>

<!-- 新建卡组对话框（Modal） -->
{#if showCreateModal}
  <div class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50 backdrop-blur-xs animate-in fade-in duration-150" role="dialog" aria-modal="true">
    <div
      role="document"
      class="card-elevated w-full max-w-lg p-6 rounded-2xl shadow-xl space-y-5 animate-in zoom-in-95 duration-150"
    >
      <div class="flex items-center justify-between pb-3 border-b border-zinc-100 dark:border-zinc-800">
        <h2 class="text-lg font-bold text-zinc-900 dark:text-zinc-100">{$t('decks.spa_create.heading')}</h2>
        <button
          type="button"
          class="p-1 rounded-lg text-zinc-400 hover:text-zinc-700 dark:hover:text-zinc-200 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors cursor-pointer"
          onclick={closeCreateModal}
          aria-label="关闭"
        >
          <svg class="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
        </button>
      </div>

      <form onsubmit={createDeck} class="space-y-4">
        <div>
          <label for="deck-name-input" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1">
            {$t('decks.spa_create.name')}
          </label>
          <input
            id="deck-name-input"
            data-testid="deck-create-name"
            bind:value={name}
            required
            maxlength="200"
            placeholder="例如：高级英汉词汇"
            class="block w-full rounded-xl border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-3.5 py-2 text-sm text-zinc-900 dark:text-zinc-100"
          />
        </div>

        <div>
          <label for="deck-desc-input" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1">
            {$t('decks.spa_create.description')}
          </label>
          <textarea
            id="deck-desc-input"
            data-testid="deck-create-description"
            bind:value={description}
            maxlength="2000"
            rows="3"
            placeholder="可选填写卡组简介"
            class="block w-full rounded-xl border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-3.5 py-2 text-sm text-zinc-900 dark:text-zinc-100"
          ></textarea>
        </div>

        {#if createError}
          <p role="alert" class="text-xs text-rose-600 dark:text-rose-400">{$t(createError)}</p>
        {/if}

        <div class="pt-2 flex items-center justify-end gap-3">
          <button
            type="button"
            onclick={closeCreateModal}
            class="px-4 py-2 text-sm font-medium rounded-xl border border-zinc-200 dark:border-zinc-700 hover:bg-zinc-50 dark:hover:bg-zinc-800 text-zinc-700 dark:text-zinc-300 transition-colors cursor-pointer"
          >
            取消
          </button>
          <button
            data-testid="deck-create-submit"
            type="submit"
            disabled={creating}
            class="px-4 py-2 text-sm font-semibold rounded-xl bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 hover:bg-zinc-800 dark:hover:bg-zinc-200 disabled:opacity-50 transition-colors btn-press cursor-pointer"
          >
            {creating ? $t('decks.spa_create.submitting') : $t('decks.spa_create.submit')}
          </button>
        </div>
      </form>
    </div>
  </div>
{/if}

<!-- 删除卡组二次确认对话框（Modal） -->
{#if deckToDelete}
  <div class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50 backdrop-blur-xs animate-in fade-in duration-150" role="dialog" aria-modal="true">
    <div
      role="alertdialog"
      class="card-elevated w-full max-w-md p-6 rounded-2xl shadow-xl space-y-4 animate-in zoom-in-95 duration-150"
    >
      <div class="flex items-center gap-3">
        <div class="w-10 h-10 rounded-full bg-rose-100 dark:bg-rose-950/60 text-rose-600 dark:text-rose-400 flex items-center justify-center shrink-0">
          <svg class="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
        </div>
        <div>
          <h2 class="text-base font-bold text-zinc-900 dark:text-zinc-100">{$t('decks.delete.confirm_title')}</h2>
          <p class="text-xs text-zinc-500 dark:text-zinc-400 mt-0.5">
            {$t('decks.delete.confirm_desc', { name: deckToDelete.name })}
          </p>
        </div>
      </div>

      {#if deleteError}
        <p role="alert" class="text-xs text-rose-600 dark:text-rose-400">{$t(deleteError)}</p>
      {/if}

      <div class="pt-2 flex items-center justify-end gap-3">
        <button
          type="button"
          disabled={deleting}
          onclick={closeDeleteModal}
          class="px-4 py-2 text-sm font-medium rounded-xl border border-zinc-200 dark:border-zinc-700 hover:bg-zinc-50 dark:hover:bg-zinc-800 text-zinc-700 dark:text-zinc-300 transition-colors cursor-pointer"
        >
          {$t('decks.delete.cancel_btn')}
        </button>
        <button
          type="button"
          disabled={deleting}
          onclick={confirmDeleteDeck}
          class="px-4 py-2 text-sm font-semibold rounded-xl bg-rose-600 text-white hover:bg-rose-700 disabled:opacity-50 transition-colors btn-press cursor-pointer"
        >
          {deleting ? '正在删除...' : $t('decks.delete.confirm_btn')}
        </button>
      </div>
    </div>
  </div>
{/if}
