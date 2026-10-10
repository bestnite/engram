<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { routeStore } from '../router';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { Deck, DeckTag, Note, BulkNotesResponse } from '../api';
  import DeckSharingView from './DeckSharingView.svelte';
  import DeckSettingsView from './DeckSettingsView.svelte';
  import Select from '../components/ui/Select.svelte';
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
  import PackageExportOptions from '../components/PackageExportOptions.svelte';
  import { DropdownMenu } from 'bits-ui';
  import { Copy, Download, Link2, MoreHorizontal, Pause, Pencil, Play, Plus, Search, Tags, Trash2 } from '@lucide/svelte';
  import { noteKindLabel as kindLabel } from '../labels';
  import { backField, cardTypes, frontField, kindOrder, loadCardTypes } from '../card-types';

  interface Props {
    /** 测试注入：给定后挂载时不再取数（与 HomeView 等视图同一约定）。 */
    initialDeck?: Deck | null;
    initialNotes?: Note[];
    initialTotal?: number;
    initialLoading?: boolean;
  }

  let { initialDeck = null, initialNotes = [], initialTotal = 0, initialLoading = true }: Props = $props();

  // 状态变量（Svelte 5 runes）
  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  let error = $state<ApiClientError | Error | null>(null);
  // svelte-ignore state_referenced_locally
  let deck = $state<Deck | null>(initialDeck);
  // svelte-ignore state_referenced_locally
  let notes = $state<Note[]>(initialNotes);
  // svelte-ignore state_referenced_locally
  let total = $state(initialTotal);
  let page = $state(1);
  let activeTab = $state<'cards' | 'sharing' | 'settings'>('cards');
  // 打开过的标签页保持挂载、只切换显隐：来回切换时不重新取数，也就不会每次都闪一下加载占位。
  let visitedTabs = $state<Record<string, boolean>>({ cards: true });

  // 逐条删除状态
  // 待确认删除的笔记：确认放在对话框里，不在行内插入文字（插入会把行撑高、文字被挤得换行）。
  let noteToDelete = $state<Note | null>(null);
  let deletingNoteId = $state<string | null>(null);
  let deleteError = $state('');

  // 导出对话框状态
  let showExportModal = $state(false);
  let exporting = $state(false);
  let exportError = $state(false);
  let includeMedia = $state(true);
  let includeProgress = $state(false);
  let includeReviews = $state(false);
  let includeWeights = $state(false);

  const perPage = 50;

  // 筛选条件：输入即生效（没有「应用」按钮），文本输入 300ms 防抖。
  // 已删除的卡片不再提供筛选入口（列表只呈现未删除内容）。
  let queryInput = $state('');
  let tagInput = $state('');
  let kindSelect = $state('');
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;

  // 按标签学习：标签只在本卡组内选（别的卡组里的同名标签不是同一个分类），多选取并集。
  let tagReviewOpen = $state(false);
  let tagReviewLoading = $state(false);
  let tagReviewError = $state(false);
  let deckTags = $state<DeckTag[]>([]);
  let tagReviewChecked = $state<string[]>([]);

  // 复习页按地址上的 deck / tag 参数取队列；按标签列表顺序拼接，同一组勾选总得到同一个地址。
  const tagReviewHref = $derived.by(() => {
    const params = new URLSearchParams();
    params.append('deck', deckId);
    for (const item of deckTags) {
      if (tagReviewChecked.includes(item.tag)) params.append('tag', item.tag);
    }
    return params.has('tag') ? `/review?${params.toString()}` : '';
  });

  // 每次打开都重新取：笔记的标签可能刚在本页被批量改过。
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

  // 批量动作
  let selectedIds = $state<string[]>([]);
  let bulkTagInput = $state('');
  let bulkBusy = $state(false);
  let bulkError = $state('');
  let confirmingBulkDelete = $state(false);
  let bulkTagsOpen = $state(false);
  let bulkResult = $state<{ affected: number; notFound: number; insufficientRole: number } | null>(null);

  // 题型清单的后端标识与展示名分开：标识符进查询串，展示名一律走语言包
  // （此前下拉里直接渲染 kind 字面量，中文界面下会露出 basic/cloze 这类内部标识）。
  // 清单本身取自服务端的题型自描述——同一份题型清单在别处再写一遍，迟早会漂移。
  const cardKinds = $derived(kindOrder($cardTypes));

  const deckId = $derived($routeStore.params.id || '');
  const totalPages = $derived(Math.max(1, Math.ceil(total / perPage)));
  const hasFilter = $derived(Boolean(queryInput.trim() || tagInput.trim() || kindSelect));
  const allSelected = $derived(notes.length > 0 && notes.every((n) => selectedIds.includes(n.id)));
  // 内容修改入口按服务端返回的角色显示：reader 能看、能复习、能暂停与配置学习设置，
  // 但不能新建/编辑/删除卡片或执行批量标签动作（服务端本就会拒绝这些写入）。
  // deck 尚未加载时（role 未知）先按可编辑渲染，避免页面标题区出现无谓的空档。
  const canEditContent = $derived(deck?.role !== 'reader');
  // 名称与描述只有属主能改（服务端同一判据）；其他成员只看到文字。
  const isOwner = $derived(deck?.role === 'owner');

  /**
   * 页头原地编辑的保存：名称与描述走同一个接口，没改的那一项按当前值一并提交。
   * 空名称前端先拦下；400 为名称/描述不合法，403/404 为无权修改。侧边栏里的卡组名由接口层的改动通知同步（AppShell）。
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
      deck = { ...deck, name: updated.name, description: updated.description };
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
        q: queryInput.trim() || undefined,
        tag: tagInput.trim() || undefined,
        kind: kindSelect || undefined,
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

  /** 文本输入：防抖后重新取数，避免每敲一个字都打一次接口。 */
  function scheduleFilterReload(): void {
    if (debounceTimer) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => loadData(1), 300);
  }

  /** 下拉选择：立即生效（选择本身就是一次明确的决定，不需要防抖）。 */
  function applyFilterNow(): void {
    if (debounceTimer) clearTimeout(debounceTimer);
    loadData(1);
  }

  function handleFilterReset(): void {
    if (debounceTimer) clearTimeout(debounceTimer);
    queryInput = '';
    tagInput = '';
    kindSelect = '';
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
    bulkBusy = true;
    bulkError = '';
    bulkResult = null;
    confirmingBulkDelete = false;
    try {
      const res = await apiClient.bulkNotes({ action, note_ids: selectedIds, tags, dry_run: false });
      bulkResult = summarizeBulk(res);
      announceBulkResult(bulkResult);
      bulkTagInput = '';
      bulkTagsOpen = false;
      if (action === 'delete') selectedIds = [];
      await loadData(1);
    } catch (err) {
      bulkError = err instanceof ApiClientError && err.isForbidden ? 'error.forbidden' : 'notes.bulk_failed';
    } finally {
      bulkBusy = false;
    }
  }

  // 批量结果用 toast 报告：处理条数一行，跳过的原因各一行。
  function announceBulkResult(result: { affected: number; notFound: number; insufficientRole: number }): void {
    const details = [
      result.notFound > 0 ? $t('notes.bulk_skipped_not_found', { count: result.notFound }) : '',
      result.insufficientRole > 0 ? $t('notes.bulk_skipped_forbidden', { count: result.insufficientRole }) : '',
    ].filter(Boolean);
    toast.success($t('notes.bulk_result', { affected: result.affected, skipped: result.notFound + result.insufficientRole }), {
      description: details.join('；') || undefined,
    });
  }

  // 暂停只对本人生效：列表上的切换改的是调用者自己在这条 note 下全部卡的暂停状态。
  let suspendingNoteId = $state<string | null>(null);

  async function toggleSuspended(note: Note): Promise<void> {
    suspendingNoteId = note.id;
    try {
      const res = await apiClient.setNoteSuspended(note.id, !note.suspended);
      notes = notes.map((item) => (item.id === note.id ? { ...item, suspended: res.suspended } : item));
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
   * 列表行的两行摘要：第一行取题型声明的正面字段，第二行取背面字段；
   * 题型元数据还没加载、或题型没有声明时，退回到第一、二个非空字段。
   * 换行与连续空白压成一个空格——行内只显示一行，完整内容在编辑页。
   */
  function noteSummary(note: Note): { primary: string; secondary: string } {
    const flat = (value: unknown) => formatFieldValue(value).replace(/\s+/g, ' ').trim();
    const front = frontField($cardTypes, note.kind);
    const back = backField($cardTypes, note.kind);
    const filled = Object.entries(note.fields)
      .map(([key, value]) => [key, flat(value)] as const)
      .filter(([, value]) => value !== '');
    const pick = (key: string) => (key ? flat(note.fields[key]) : '');
    const primary = pick(front) || filled[0]?.[1] || '';
    const secondary = pick(back) || filled.find(([, value]) => value !== primary)?.[1] || '';
    return { primary, secondary };
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

  onMount(() => {
    if (initialLoading) loadData(1);
    // 元数据只加载一次（模块内幂等）；失败时筛选下拉退化为只剩「全部题型」，不打断页面。
    void loadCardTypes(apiClient).catch(() => {});
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
            value={deck.description}
            editable={isOwner}
            multiline
            placeholder={$t('deck.add_description')}
            label={$t('deck.settings.description')}
            maxlength={2000}
            testId="deck-description"
            onSave={(description) => saveDeckInfo({ description })}
          />
        </div>
      {/if}
    {/snippet}
    {#snippet actions()}
      <Button variant="outline" size="lg" onclick={() => (showExportModal = true)} testId="deck-export-open">
        <Download class="size-4" aria-hidden="true" />
        <span>{$t('package.export.short')}</span>
      </Button>
      {#if canEditContent}
        <Button variant="outline" size="lg" href="/decks/{encodeURIComponent(deckId)}/notes/new" testId="create-note-link">
          <Plus class="size-4" aria-hidden="true" />
          <span>{$t('notes.create')}</span>
        </Button>
      {/if}
      <Button variant="outline" size="lg" onclick={() => void openTagReview()} testId="deck-tag-review-open">
        <Tags class="size-4" aria-hidden="true" />
        <span>{$t('deck.tag_review.open')}</span>
      </Button>
      <Button variant="primary" size="lg" href="/review?deck={encodeURIComponent(deckId)}" testId="deck-start-review">
        <Play class="size-4" aria-hidden="true" />
        <span>{$t('home.start_review')}</span>
      </Button>
    {/snippet}
  </PageHeader>

  <!-- 标签页：下划线样式，切换只换下方内容，页头不动。 -->
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
    <!-- 学习设置是每个成员自己的，所有成员都能打开。 -->
    <div hidden={activeTab !== 'settings'}><DeckSettingsView embedded /></div>
  {/if}
  <div hidden={activeTab !== 'cards'}>
    <!-- 搜索与筛选 -->
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
      <Select
        class="w-full sm:w-40"
        value={kindSelect}
        onValueChange={(value) => { kindSelect = value; applyFilterNow(); }}
        testId="filter-kind-select"
        ariaLabel={$t('notes.filter_kind')}
        options={[
          { value: '', label: $t('notes.all_kinds') },
          ...cardKinds.map((kind) => ({ value: kind, label: kindLabel(kind, $cardTypes, $t) })),
        ]}
      />
      {#if hasFilter}
        <Button variant="ghost" size="lg" testId="filter-reset-btn" onclick={handleFilterReset}>
          {$t('notes.filter_reset')}
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
          {$t('notes.retry')}
        </Button>
      </div>
    {:else if notes.length === 0}
      <div data-testid="notes-empty" class="rounded-lg border border-dashed border-border py-16 text-center text-sm text-muted-foreground">
        {hasFilter ? $t('notes.empty_filter') : $t('notes.empty')}
      </div>
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
          <span class="hidden md:block">{$t('notes.col_kind')}</span>
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
                    href="/decks/{encodeURIComponent(deckId)}/notes/{encodeURIComponent(note.id)}/edit"
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
            <span class="hidden truncate text-[13px] text-muted-foreground md:block">{kindLabel(note.kind, $cardTypes, $t)}</span>
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
                  href="/decks/{encodeURIComponent(deckId)}/notes/{encodeURIComponent(note.id)}/edit"
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
              {$t('notes.prev_page')}
            </Button>
            <Button testId="notes-next-page" type="button" disabled={page >= totalPages} onclick={handleNextPage} variant="outline" size="sm">
              {$t('notes.next_page')}
            </Button>
          </div>
        {/if}
      </div>
    {/if}

    <!-- 批量操作：只有能改内容的角色才会有选中项（reader 没有勾选框）。 -->
    <SelectionBar
      count={selectedIds.length}
      onClear={() => (selectedIds = [])}
      testId="notes-bulk-toolbar"
      error={bulkError && !bulkTagsOpen && !confirmingBulkDelete ? $t(bulkError) : undefined}
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

<!-- 按标签学习 -->
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

<!-- 批量编辑标签 -->
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
    <Button type="button" testId="bulk-remove-tags" disabled={bulkBusy} onclick={() => runBulk('remove_tags')} variant="outline" size="lg">{$t('notes.bulk_remove_tags')}</Button>
    <Button type="button" testId="bulk-set-tags" disabled={bulkBusy} onclick={() => runBulk('set_tags')} variant="outline" size="lg">{$t('notes.bulk_set_tags')}</Button>
    <Button type="button" testId="bulk-add-tags" disabled={bulkBusy} onclick={() => runBulk('add_tags')} variant="primary" size="lg">
      {bulkBusy ? $t('notes.bulk_applying') : $t('notes.bulk_add_tags')}
    </Button>
  </div>
</Dialog>

<!-- 批量删除确认 -->
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
    <Button type="button" testId="bulk-confirm-delete" variant="danger" size="lg" disabled={bulkBusy} onclick={() => runBulk('delete')}>
      {bulkBusy ? $t('notes.bulk_applying') : $t('notes.bulk_delete')}
    </Button>
  </div>
</Dialog>

<!-- 单条删除确认 -->
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
      disabled={deletingNoteId !== null}
      onclick={() => noteToDelete && deleteNote(noteToDelete)}
    >
      {deletingNoteId ? $t('notes.deleting') : $t('notes.delete')}
    </Button>
  </div>
</Dialog>

<!-- 导出包设置 -->
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
    <Button type="button" testId="deck-package-export" disabled={exporting} onclick={exportPackage} variant="primary" size="lg">
      {exporting ? $t('package.export.exporting') : $t('package.export.action')}
    </Button>
  </div>
</Dialog>
