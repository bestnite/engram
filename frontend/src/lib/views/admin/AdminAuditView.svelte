<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminAuditResponse, AdminAuditQuery } from '../../api';
  import { routeStore } from '../../router';
  import AdminNav from './AdminNav.svelte';
  import Select from '../../components/ui/Select.svelte';
  import Skeleton from '../../components/ui/Skeleton.svelte';
  import Button from '../../components/ui/Button.svelte';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialData?: AdminAuditResponse | null;
  }

  let { initialLoading = true, initialError = null, initialData = null }: Props = $props();

  /** 从当前 URL 的查询串初始化过滤条件（服务端 SSR 页也接受同样的参数）。 */
  function initialQuery(): Required<Pick<AdminAuditQuery, 'user' | 'action' | 'target_type' | 'target_id' | 'from' | 'to'>> {
    const q = $routeStore.query ?? {};
    return {
      user: q.user ?? '',
      action: q.action ?? '',
      target_type: q.target_type ?? '',
      target_id: q.target_id ?? '',
      from: q.from ?? '',
      to: q.to ?? '',
    };
  }

  // svelte-ignore state_referenced_locally
  let filters = $state(initialQuery());
  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let data = $state<AdminAuditResponse | null>(initialData);

  const noticeKeys: Record<string, string> = {
    user_not_found: 'admin.audit.notice.user_not_found',
    invalid_target: 'admin.audit.notice.invalid_target',
    invalid_date: 'admin.audit.notice.invalid_date',
  };

  async function load(page = 1): Promise<void> {
    loading = true;
    loadError = null;
    try {
      data = await apiClient.getAdminAudit({ ...filters, page });
    } catch (err) {
      loadError = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  function submit(event: SubmitEvent): void {
    event.preventDefault();
    load(1);
  }

  function clearFilters(): void {
    filters = { user: '', action: '', target_type: '', target_id: '', from: '', to: '' };
    load(1);
  }

  function loadErrorKey(): string {
    if (loadError instanceof ApiClientError && (loadError.isUnauthorized || loadError.status === 403)) {
      return 'admin.error.forbidden';
    }
    return 'admin.audit.load_failed';
  }

  function actorLabel(row: AdminAuditResponse['rows'][number]): string {
    if (row.actor === null) return $t('admin.audit.user_system');
    return row.actor.username || `#${row.actor.user_id}`;
  }

  function targetLabel(row: AdminAuditResponse['rows'][number]): string {
    if (row.target === null) return $t('admin.audit.target_empty');
    return row.target.id === null ? row.target.type : `${row.target.type}#${row.target.id}`;
  }

  onMount(() => {
    if (initialData === null && initialError === null) {
      load(1);
    }
  });
</script>

<div class="mx-auto max-w-5xl space-y-6 px-4 py-10" data-testid="admin-audit">
  <AdminNav />

  <header class="space-y-1">
    <h1 class="text-2xl font-bold tracking-tight text-foreground" data-testid="admin-audit-title">
      {$t('admin.audit.heading')}
    </h1>
    <p class="text-sm leading-relaxed text-muted-foreground">{$t('admin.audit.intro')}</p>
  </header>

  <form onsubmit={submit} data-testid="admin-audit-filter" class="card-elevated grid gap-3 rounded-xl p-5 sm:grid-cols-3">
    <h2 class="sm:col-span-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
      {$t('admin.audit.filter.heading')}
    </h2>
    <label class="block">
      <span class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.audit.filter.user')}</span>
      <input data-testid="admin-audit-filter-user" bind:value={filters.user} class="field-input text-sm mt-1.5 w-full" />
    </label>
    <label class="block">
      <span class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.audit.filter.action')}</span>
      <Select
        class="mt-1.5"
        bind:value={filters.action}
        testId="admin-audit-filter-action"
        options={[{ value: '', label: $t('admin.audit.filter.any') }, ...(data?.actions ?? []).map((action) => ({ value: action, label: action }))]}
      />
    </label>
    <label class="block">
      <span class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.audit.filter.target_type')}</span>
      <input data-testid="admin-audit-filter-target-type" bind:value={filters.target_type} class="field-input text-sm mt-1.5 w-full" />
    </label>
    <label class="block">
      <span class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.audit.filter.target_id')}</span>
      <input data-testid="admin-audit-filter-target-id" bind:value={filters.target_id} class="field-input text-sm mt-1.5 w-full" />
    </label>
    <label class="block">
      <span class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.audit.filter.from')}</span>
      <input data-testid="admin-audit-filter-from" type="date" bind:value={filters.from} class="field-input text-sm mt-1.5 w-full" />
    </label>
    <label class="block">
      <span class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('admin.audit.filter.to')}</span>
      <input data-testid="admin-audit-filter-to" type="date" bind:value={filters.to} class="field-input text-sm mt-1.5 w-full" />
    </label>
    <p class="sm:col-span-3 text-xs text-muted-foreground/70">{$t('admin.audit.filter.range_hint')}</p>
    <div class="flex items-center gap-3 sm:col-span-3">
      <Button type="submit" testId="admin-audit-filter-submit" variant="primary" size="lg">
        {$t('admin.audit.filter.submit')}
      </Button>
      <button type="button" data-testid="admin-audit-filter-clear" onclick={clearFilters} class="btn-press cursor-pointer rounded-lg border border-zinc-200 px-4 py-2 text-sm font-medium text-zinc-600 dark:border-zinc-700 dark:text-zinc-300">
        {$t('admin.audit.filter.clear')}
      </button>
    </div>
  </form>

  {#if loading}
    <Skeleton testId="admin-audit-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div data-testid="admin-audit-failed" class="card-elevated rounded-xl p-8 text-center">
      <p role="alert" class="text-base font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="admin-audit-retry" onclick={() => load(data?.page ?? 1)} variant="primary" size="lg" class="mt-4">
        {$t('common.retry')}
      </Button>
    </div>
  {:else if data}
    {@const view = data}
    {#if view.notice && noticeKeys[view.notice]}
      <div data-testid="admin-audit-notice" role="status" class="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/40 dark:text-amber-300">
        {$t(noticeKeys[view.notice] ?? '')}
      </div>
    {/if}

    <p class="text-sm text-muted-foreground" data-testid="admin-audit-total">
      {$t('admin.audit.total_label', { total: view.total })}
    </p>

    {#if view.rows.length === 0}
      <p data-testid="admin-audit-empty" class="py-8 text-center text-sm text-muted-foreground">{$t('admin.audit.empty')}</p>
    {:else}
      <div class="overflow-x-auto card-elevated rounded-xl">
        <table class="w-full text-left text-sm">
          <thead class="border-b border-zinc-200 text-xs uppercase tracking-wider text-zinc-500 dark:border-zinc-800 dark:text-zinc-400">
            <tr>
              <th class="px-4 py-3 font-semibold">{$t('admin.audit.col.time')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.audit.col.user')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.audit.col.action')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.audit.col.target')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.audit.col.detail')}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-zinc-100 dark:divide-zinc-800">
            {#each view.rows as row, i (i)}
              <tr data-testid="admin-audit-row-{i}">
                <td class="whitespace-nowrap px-4 py-2.5 text-muted-foreground">{row.time}</td>
                <td class="px-4 py-2.5 text-foreground">{actorLabel(row)}</td>
                <td class="px-4 py-2.5 text-muted-foreground">{row.action}</td>
                <td class="px-4 py-2.5 text-muted-foreground">{targetLabel(row)}</td>
                <td class="px-4 py-2.5 font-mono text-xs text-muted-foreground">{row.detail || $t('admin.audit.detail_empty')}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>

      <div class="flex items-center justify-between" data-testid="admin-audit-pager">
        <button
          type="button"
          data-testid="admin-audit-prev"
          disabled={view.page <= 1}
          onclick={() => load(view.page - 1)}
          class="btn-press cursor-pointer rounded-lg border border-zinc-200 px-4 py-2 text-sm font-medium text-zinc-600 disabled:cursor-not-allowed disabled:opacity-40 dark:border-zinc-700 dark:text-zinc-300"
        >
          {$t('admin.common.prev')}
        </button>
        <span class="text-sm text-muted-foreground">{view.page} / {view.pages}</span>
        <button
          type="button"
          data-testid="admin-audit-next"
          disabled={view.page >= view.pages}
          onclick={() => load(view.page + 1)}
          class="btn-press cursor-pointer rounded-lg border border-zinc-200 px-4 py-2 text-sm font-medium text-zinc-600 disabled:cursor-not-allowed disabled:opacity-40 dark:border-zinc-700 dark:text-zinc-300"
        >
          {$t('admin.common.next')}
        </button>
      </div>
    {/if}
  {/if}
</div>
