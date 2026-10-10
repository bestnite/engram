<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { Deck, DeckShareInvite } from '../api';
  import Dialog from '../components/ui/Dialog.svelte';
  import Button from '../components/ui/Button.svelte';
  import Badge from '../components/ui/Badge.svelte';
  import Select from '../components/ui/Select.svelte';
  import Checkbox from '../components/ui/Checkbox.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import SelectionBar from '../components/ui/SelectionBar.svelte';
  import { listClasses, selectionBarButton } from '../components/ui/variants';
  import { CirclePlay, Download, LogOut, Plus, RefreshCw, Trash2, Upload } from '@lucide/svelte';
  import { deckActionKind, presetSelectOptions } from '../labels';

  // 视图响应式状态定义（Svelte 5 runes）
  let loading = $state(true);
  let error = $state<ApiClientError | Error | null>(null);
  let decks = $state<Deck[]>([]);
  let queueCounts = $state<Record<string, { new_count: number; review_count: number }>>({});

  // 新建卡组弹窗状态
  let showCreateModal = $state(false);
  let name = $state('');
  let description = $state('');
  // 新建卡组时可选调度预设。下拉只列真实预设（不含「默认预设」哨兵项），
  // 加载完成后预选默认预设；拉取失败时 createPresetId 留空，按服务端默认预设提交
  // （请求体 preset_id 为空串）。
  let createPresets = $state<Array<{ value: string; label: string }>>([]);
  let createPresetDefault = $state('');
  let createPresetId = $state('');
  let creating = $state(false);
  let createError = $state<string | null>(null);

  // 批量选择与导出状态
  let selectedDeckIds = $state<string[]>([]);
  let batchExporting = $state(false);
  let batchExportError = $state<string | null>(null);

  // 待接受的共享邀请（同意制）：分享先产生邀请，接受那一步才写授权。
  // 拉取失败不设 error——邀请拉不到不该让整页变成错误页，它只是这一块不显示。
  let invites = $state<DeckShareInvite[]>([]);
  let inviteBusy = $state<string | null>(null);
  let inviteError = $state<string | null>(null);

  // 卡组危险操作的确认弹窗状态：删除（自有卡组）与退出共享（被共享卡组）共用一套弹窗，
  // 只有文案与调用的接口不同，避免两套重复的确认 UI。
  let deckAction = $state<{ deck: Deck; kind: 'delete' | 'leave' } | null>(null);
  let acting = $state(false);
  let actionError = $state<string | null>(null);

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
  async function respondToInvite(deckId: string, accept: boolean): Promise<void> {
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
    createPresetId = createPresetDefault;
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
          const { options, selected } = presetSelectOptions(response.presets, $t);
          createPresets = options;
          createPresetDefault = selected;
          if (!createPresetId) {
            createPresetId = selected;
          }
        })
        .catch(() => {
          // 预设列表拿不到不影响建卡组：留空即走服务端默认预设。
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
      queueCounts = { ...queueCounts, [deck.id]: { new_count: 0, review_count: 0 } };
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

  // 批量复习直接跳到复习页：复习页本就按地址上可重复的 deck 参数取多卡组队列，这里只负责拼地址。
  // 按列表顺序而非勾选顺序拼接，同一组卡组无论怎么勾选都得到同一个地址。
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

  /** 打开确认弹窗。kind 决定这是「删除自有卡组」还是「退出共享卡组」。 */
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
   * 执行确认的危险操作：删除走 deleteDeck，退出共享走 leaveDeck。
   * 两者成功后都把卡组从本地列表移除——退出共享后它不再对你可见，效果与删除相同。
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
      <!-- 卡组包导入入口；侧边栏的「导入」是同一个页面，这里是就近入口。 -->
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

  <!-- 待接受的共享邀请：同意制的入口。没有邀请时整块不渲染。 -->
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
                disabled={inviteBusy === invite.deck_id}
                onclick={() => respondToInvite(invite.deck_id, false)}
              >
                {$t('decks.invites.reject')}
              </Button>
              <Button
                testId="deck-invite-accept-{invite.deck_id}"
                variant="primary"
                size="sm"
                disabled={inviteBusy === invite.deck_id}
                onclick={() => respondToInvite(invite.deck_id, true)}
              >
                {$t('decks.invites.accept')}
              </Button>
            </div>
          </li>
        {/each}
      </ul>
      {#if inviteError}
        <p role="alert" class="mt-2 text-xs text-destructive-foreground">{$t(inviteError)}</p>
      {/if}
    </section>
  {/if}

  <!-- 列表内容区 -->
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
      <!-- 表头兼作全选：选择状态变化只改这一行的勾选框，不插入任何会撑高页面的工具栏。 -->
      <div class={listClasses.header}>
        <Checkbox
          checked={selectedDeckIds.length > 0 && selectedDeckIds.length === decks.length}
          indeterminate={selectedDeckIds.length > 0 && selectedDeckIds.length < decks.length}
          onCheckedChange={toggleSelectAll}
          label={selectedDeckIds.length === decks.length ? $t('decks.deselect_all') : $t('decks.select_all')}
        />
        <span class="flex-1">{$t('list.col.name')}</span>
        <span class="hidden w-36 sm:block">{$t('list.col.today')}</span>
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
    error={batchExportError ? $t(batchExportError) : undefined}
  >
    <a data-testid="decks-batch-review" class={selectionBarButton} href={batchReviewHref}>
      <CirclePlay aria-hidden="true" />
      {$t('decks.batch_review')}
    </a>
    <button
      type="button"
      data-testid="decks-batch-export"
      class={selectionBarButton}
      disabled={batchExporting}
      onclick={handleBatchExport}
    >
      <Download aria-hidden="true" />
      {batchExporting ? $t('package.batch_export_progress') : $t('package.export.short')}
    </button>
  </SelectionBar>
</Page>

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

<!-- 卡组危险操作二次确认对话框（Modal）：删除自有卡组 / 退出共享卡组共用一套 -->
{#if deckAction}
  <Dialog
    open={true}
    onOpenChange={(open) => { if (!open) closeDeckAction(); }}
    title={$t(deckAction.kind === 'delete' ? 'decks.delete.confirm_title' : 'decks.leave.confirm_title')}
    description={$t(deckAction.kind === 'delete' ? 'decks.delete.confirm_desc' : 'decks.leave.confirm_desc', { name: deckAction.deck.name })}
    testId={deckAction.kind === 'delete' ? 'deck-delete-dialog' : 'deck-leave-dialog'}
  >
      {#if actionError}
        <p role="alert" class="text-xs text-rose-600 dark:text-rose-400">{$t(actionError)}</p>
      {/if}

      <div class="pt-2 flex items-center justify-end gap-3">
        <Button variant="outline" size="lg" disabled={acting} onclick={closeDeckAction}>
          {$t(deckAction.kind === 'delete' ? 'decks.delete.cancel_btn' : 'decks.leave.cancel_btn')}
        </Button>
        <Button
          variant={deckAction.kind === 'delete' ? 'danger' : 'danger-outline'}
          size="lg"
          disabled={acting}
          onclick={confirmDeckAction}
          testId={deckAction.kind === 'delete' ? 'deck-delete-confirm' : 'deck-leave-confirm'}
        >
          {#if acting}
            {$t(deckAction.kind === 'delete' ? 'common.deleting' : 'decks.leave.submitting')}
          {:else}
            {$t(deckAction.kind === 'delete' ? 'decks.delete.confirm_btn' : 'decks.leave.confirm_btn')}
          {/if}
        </Button>
      </div>
  </Dialog>
{/if}
