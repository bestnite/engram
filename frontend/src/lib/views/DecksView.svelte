<script lang="ts">
  import { onMount } from 'svelte';
  import { navigate } from '../router';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { Deck, DeckShareInvite } from '../api';
  import Dialog from '../components/ui/Dialog.svelte';
  import Button from '../components/ui/Button.svelte';
  import Badge from '../components/ui/Badge.svelte';
  import Select from '../components/ui/Select.svelte';
  import Checkbox from '../components/ui/Checkbox.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import { deckVisibilityLabel as visibilityLabel, presetDisplayName } from '../labels';

  // 视图响应式状态定义（Svelte 5 runes）
  let loading = $state(true);
  let error = $state<ApiClientError | Error | null>(null);
  let decks = $state<Deck[]>([]);
  let queueCounts = $state<Record<number, { new_count: number; review_count: number }>>({});

  // 新建卡组弹窗状态
  let showCreateModal = $state(false);
  let name = $state('');
  let description = $state('');
  // 新建卡组时可选调度预设；空值表示交给服务端的默认预设（请求体 preset_id: 0）。
  let createPresets = $state<Array<{ value: string; label: string }>>([]);
  let createPresetId = $state('');
  let creating = $state(false);
  let createError = $state<string | null>(null);

  // 批量选择与导出状态
  let selectedDeckIds = $state<number[]>([]);
  let batchExporting = $state(false);
  let batchExportError = $state<string | null>(null);

  // 待接受的共享邀请（同意制）：分享先产生邀请，接受那一步才写授权。
  // 拉取失败不设 error——邀请拉不到不该让整页变成错误页，它只是这一块不显示。
  let invites = $state<DeckShareInvite[]>([]);
  let inviteBusy = $state<number | null>(null);
  let inviteError = $state<string | null>(null);

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

  /**
   * 读取待接受的共享邀请（GET /api/v1/sharing/invites）。
   * 未登录或接口失败都只是"没有邀请可显示"，不升级成页面级错误。
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
   * 接受或拒绝一条邀请。接受成功后重新拉卡组列表——卡组正是那一步才出现在这里。
   * 失败时只在这一块提示，不动整页状态。
   */
  async function respondToInvite(deckId: number, accept: boolean): Promise<void> {
    inviteBusy = deckId;
    inviteError = null;
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
      inviteError = 'decks.invites.failed';
    } finally {
      inviteBusy = null;
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

  /** 打开弹窗时才拉预设列表：多数用户不开这个弹窗，没必要每次都取。 */
  $effect(() => {
    if (showCreateModal && createPresets.length === 0) {
      apiClient
        .listPresets()
        .then((response) => {
          createPresets = response.presets.map((item) => ({
            value: String(item.id),
            // 默认预设的库内名是机器标识，显示名走语言包（labels.presetDisplayName）。
            label: presetDisplayName(item.name, item.is_default, $t),
          }));
        })
        .catch(() => {
          // 预设列表拿不到不影响建卡组：仍走服务端默认预设。
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
        visibility: 'private',
        preset_id: createPresetId ? Number(createPresetId) : 0,
      });
      decks = [deck, ...decks];
      queueCounts = { ...queueCounts, [deck.id]: { new_count: 0, review_count: 0 } };
      name = '';
      description = '';
      createPresetId = '';
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

  function toggleSelectDeck(deckId: number): void {
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
    fetchInvites();
    window.addEventListener('keydown', handleKeydown);
    return () => window.removeEventListener('keydown', handleKeydown);
  });
</script>

<div class="py-10 max-w-4xl mx-auto px-4">
  <div class="card-elevated p-6 sm:p-8 rounded-2xl">
    <!-- 顶栏标题与操作 -->
    <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-6 pb-5 border-b border-zinc-100 dark:border-zinc-800">
      <div>
        <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
          {$t('decks.title')}
        </h1>
        <p class="text-xs text-zinc-500 dark:text-zinc-400 mt-1">
          {$t('decks.count', { count: decks.length })}
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

        <Button type="button" testId="deck-create-open" onclick={openCreateModal} variant="primary" size="lg">
          <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
            <line x1="12" y1="5" x2="12" y2="19" />
            <line x1="5" y1="12" x2="19" y2="12" />
          </svg>
          <span>{$t('decks.create.heading')}</span>
        </Button>
      </div>
    </div>

    <!-- 待接受的共享邀请：同意制的入口。没有邀请时整块不渲染。 -->
    {#if invites.length > 0}
      <div
        data-testid="deck-invites"
        class="mb-4 rounded-xl border border-indigo-200/70 dark:border-indigo-900/60 bg-indigo-50/50 dark:bg-indigo-950/25 p-4"
      >
        <div class="flex items-center gap-2 mb-1">
          <svg class="w-4 h-4 text-indigo-600 dark:text-indigo-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M4 4h16v16H4z" />
            <path d="m4 6 8 6 8-6" />
          </svg>
          <h2 class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">{$t('decks.invites.title')}</h2>
        </div>
        <p class="text-xs text-zinc-500 dark:text-zinc-400 mb-3">{$t('decks.invites.hint')}</p>

        <ul class="space-y-2">
          {#each invites as invite (invite.deck_id)}
            <li class="flex flex-wrap items-center justify-between gap-3 rounded-lg bg-white/80 dark:bg-zinc-900/60 px-3 py-2">
              <div class="min-w-0">
                <p class="text-sm font-medium text-zinc-900 dark:text-zinc-100 truncate">{invite.deck_name}</p>
                <p class="text-xs text-zinc-500 dark:text-zinc-400">
                  {$t('decks.invites.from', { name: invite.inviter_name || invite.username || '—' })}
                  ·
                  {$t('decks.invites.role', { role: $t(`deck.sharing.role.${invite.role}`) })}
                </p>
              </div>
              <div class="flex items-center gap-2">
                <Button
                  testId="deck-invite-accept-{invite.deck_id}"
                  variant="primary"
                  size="sm"
                  disabled={inviteBusy === invite.deck_id}
                  onclick={() => respondToInvite(invite.deck_id, true)}
                >
                  {$t('decks.invites.accept')}
                </Button>
                <Button
                  testId="deck-invite-reject-{invite.deck_id}"
                  variant="outline"
                  size="sm"
                  disabled={inviteBusy === invite.deck_id}
                  onclick={() => respondToInvite(invite.deck_id, false)}
                >
                  {$t('decks.invites.reject')}
                </Button>
              </div>
            </li>
          {/each}
        </ul>

        {#if inviteError}
          <p role="alert" class="mt-2 text-xs text-rose-600 dark:text-rose-400">{$t(inviteError)}</p>
        {/if}
      </div>
    {/if}

    <!-- 批量工具栏 -->
    {#if !loading && !error && decks.length > 0}
      <div class="mb-4 flex flex-wrap items-center justify-between gap-3 px-3 py-2 rounded-xl bg-zinc-50 dark:bg-zinc-900/60 border border-zinc-200/60 dark:border-zinc-800/60 text-xs">
        <div class="flex items-center gap-3">
          <label class="flex items-center gap-1.5 cursor-pointer text-zinc-600 dark:text-zinc-400 font-medium select-none">
            <Checkbox
              checked={selectedDeckIds.length > 0 && selectedDeckIds.length === decks.length}
              onCheckedChange={toggleSelectAll}
              label={selectedDeckIds.length === decks.length ? $t('decks.deselect_all') : $t('decks.select_all')}
            />
            <span>{selectedDeckIds.length === decks.length ? $t('decks.deselect_all') : $t('decks.select_all')}</span>
          </label>
          {#if selectedDeckIds.length > 0}
            <span class="text-zinc-400 dark:text-zinc-500">{$t('decks.selected_count', { count: selectedDeckIds.length })}</span>
          {/if}
        </div>

        {#if selectedDeckIds.length > 0}
          <div class="flex items-center gap-2">
            <Button
              variant="primary"
              testId="decks-batch-export"
              disabled={batchExporting}
              onclick={handleBatchExport}
            >
              <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                <polyline points="7 10 12 15 17 10" />
                <line x1="12" y1="15" x2="12" y2="3" />
              </svg>
              <span>{batchExporting ? $t('package.batch_export_progress') : $t('package.batch_export', { count: selectedDeckIds.length })}</span>
            </Button>
          </div>
        {/if}
      </div>
      {#if batchExportError}
        <p role="alert" class="mb-4 text-xs text-rose-600 dark:text-rose-400">{$t(batchExportError)}</p>
      {/if}
    {/if}

    <!-- 列表内容区 -->
    {#if loading}
      <Skeleton testId="decks-loading" label={$t('common.loading')} variant="cards" count={4} columns={2} />
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
        <Button testId="decks-retry" type="button" onclick={fetchDecks} variant="primary" size="lg">
          <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M3 12a9 9 0 0 1 15-6.7L21 8" /><path d="M21 3v5h-5" /><path d="M21 12a9 9 0 0 1-15 6.7L3 16" /><path d="M3 21v-5h5" />
          </svg>
          <span>{$t('decks.retry')}</span>
        </Button>
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
          + {$t('decks.create.heading')}
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
            class="card-elevated p-5 rounded-xl border border-zinc-200/70 dark:border-zinc-800/70 hover:border-blue-500/50 dark:hover:border-blue-400/50 hover:shadow-sm transition-all duration-150 flex flex-col justify-between cursor-pointer group text-left select-none relative"
            onclick={(e) => handleCardClick(e, deck.id)}
            onkeydown={(e) => { if (e.key === 'Enter') navigate(`/decks/${deck.id}`); }}
          >
            <div>
              <div class="flex items-start justify-between gap-2 mb-2">
                <div class="flex items-center gap-2 flex-1 min-w-0">
                  <Checkbox
                    class="mt-0.5"
                    checked={selectedDeckIds.includes(deck.id)}
                    onclick={(event) => event.stopPropagation()}
                    onCheckedChange={() => toggleSelectDeck(deck.id)}
                    label={$t('decks.select_deck', { name: deck.name })}
                  />
                  <h2 class="text-base font-semibold text-zinc-900 dark:text-zinc-100 group-hover:text-blue-600 dark:group-hover:text-blue-400 transition-colors truncate">
                    {deck.name}
                  </h2>
                </div>

                <div class="flex items-center gap-1.5 shrink-0">
                  {#if visibilityLabel(deck.visibility, $t)}
                    <Badge>{visibilityLabel(deck.visibility, $t)}</Badge>
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
  <Dialog
    open={true}
    onOpenChange={(open) => { if (!open) closeCreateModal(); }}
    title={$t('decks.create.heading')}
    size="lg"
    testId="deck-create-dialog"
  >
      <form onsubmit={createDeck} class="space-y-4">
        <div>
          <label for="deck-name-input" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1">
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
          <label for="deck-desc-input" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1">
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
            <label class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1" for="deck-create-preset">
              {$t('decks.create.preset')}
            </label>
            <Select
              class="w-full"
              testId="deck-create-preset"
              value={createPresetId}
              onValueChange={(value: string) => (createPresetId = value)}
              options={[{ value: '', label: $t('decks.preset_default') }, ...createPresets]}
            />
          </div>
        </div>

        {#if createError}
          <p role="alert" class="text-xs text-rose-600 dark:text-rose-400">{$t(createError)}</p>
        {/if}

        <div class="pt-2 flex items-center justify-end gap-3">
          <Button variant="outline" size="lg" onclick={closeCreateModal}>{$t('note_edit.cancel')}</Button>
          <Button type="submit" size="lg" testId="deck-create-submit" disabled={creating}>
            {creating ? $t('decks.create.submitting') : $t('decks.create.submit')}
          </Button>
        </div>
      </form>
  </Dialog>
{/if}

<!-- 删除卡组二次确认对话框（Modal） -->
{#if deckToDelete}
  <Dialog
    open={true}
    onOpenChange={(open) => { if (!open) closeDeleteModal(); }}
    title={$t('decks.delete.confirm_title')}
    description={$t('decks.delete.confirm_desc', { name: deckToDelete.name })}
    testId="deck-delete-dialog"
  >
      {#if deleteError}
        <p role="alert" class="text-xs text-rose-600 dark:text-rose-400">{$t(deleteError)}</p>
      {/if}

      <div class="pt-2 flex items-center justify-end gap-3">
        <Button variant="outline" size="lg" disabled={deleting} onclick={closeDeleteModal}>
          {$t('decks.delete.cancel_btn')}
        </Button>
        <Button variant="danger" size="lg" disabled={deleting} onclick={confirmDeleteDeck} testId="deck-delete-confirm">
          {deleting ? $t('common.deleting') : $t('decks.delete.confirm_btn')}
        </Button>
      </div>
  </Dialog>
{/if}
