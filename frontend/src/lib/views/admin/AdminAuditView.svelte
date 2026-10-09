<script lang="ts">
  import Page from '../../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminAuditResponse, AdminAuditQuery } from '../../api';
  import { routeStore } from '../../router';
  import AdminNav from './AdminNav.svelte';
  import PageHeader from '../../components/ui/PageHeader.svelte';
  import Pager from '../../components/ui/Pager.svelte';
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

<Page testId="admin-audit">
  <AdminNav />
  <PageHeader title={$t('admin.audit.heading')} testId="admin-audit-title" description={$t('admin.audit.intro')} />

  <!-- 筛选条：放在表格上方的一组输入，不再包成卡片。 -->
  <form onsubmit={submit} data-testid="admin-audit-filter" class="mb-5 space-y-3" aria-label={$t('admin.audit.filter.heading')}>
    <div class="grid gap-3 sm:grid-cols-3 lg:grid-cols-6">
      <label class="block text-sm font-medium text-foreground">{$t('admin.audit.filter.user')}
        <input data-testid="admin-audit-filter-user" bind:value={filters.user} class="field-input mt-1.5 w-full text-sm font-normal" />
      </label>
      <div>
        <span class="block text-sm font-medium text-foreground">{$t('admin.audit.filter.action')}</span>
        <Select
          class="mt-1.5"
          bind:value={filters.action}
          testId="admin-audit-filter-action"
          ariaLabel={$t('admin.audit.filter.action')}
          options={[{ value: '', label: $t('admin.audit.filter.any') }, ...(data?.actions ?? []).map((action) => ({ value: action, label: action }))]}
        />
      </div>
      <label class="block text-sm font-medium text-foreground">{$t('admin.audit.filter.target_type')}
        <input data-testid="admin-audit-filter-target-type" bind:value={filters.target_type} class="field-input mt-1.5 w-full text-sm font-normal" />
      </label>
      <label class="block text-sm font-medium text-foreground">{$t('admin.audit.filter.target_id')}
        <input data-testid="admin-audit-filter-target-id" bind:value={filters.target_id} class="field-input mt-1.5 w-full text-sm font-normal" />
      </label>
      <label class="block text-sm font-medium text-foreground">{$t('admin.audit.filter.from')}
        <input data-testid="admin-audit-filter-from" type="date" bind:value={filters.from} class="field-input mt-1.5 w-full text-sm font-normal" />
      </label>
      <label class="block text-sm font-medium text-foreground">{$t('admin.audit.filter.to')}
        <input data-testid="admin-audit-filter-to" type="date" bind:value={filters.to} class="field-input mt-1.5 w-full text-sm font-normal" />
      </label>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <Button type="submit" testId="admin-audit-filter-submit" variant="primary" size="lg">{$t('admin.audit.filter.submit')}</Button>
      <Button type="button" testId="admin-audit-filter-clear" variant="ghost" size="lg" onclick={clearFilters}>{$t('admin.audit.filter.clear')}</Button>
      <p class="text-xs text-muted-foreground">{$t('admin.audit.filter.range_hint')}</p>
    </div>
  </form>

  {#if loading}
    <Skeleton testId="admin-audit-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div data-testid="admin-audit-failed" class="py-16 text-center">
      <p role="alert" class="font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="admin-audit-retry" onclick={() => load(data?.page ?? 1)} variant="outline" size="lg" class="mt-4">{$t('common.retry')}</Button>
    </div>
  {:else if data}
    {@const view = data}
    <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
      <p class="text-[13px] text-muted-foreground" data-testid="admin-audit-total">{$t('admin.audit.total_label', { total: view.total })}</p>
      {#if view.notice && noticeKeys[view.notice]}
        <p data-testid="admin-audit-notice" role="status" class="text-[13px] text-warning">{$t(noticeKeys[view.notice] ?? '')}</p>
      {/if}
    </div>

    {#if view.rows.length === 0}
      <p data-testid="admin-audit-empty" class="rounded-lg border border-dashed border-border py-16 text-center text-sm text-muted-foreground">{$t('admin.audit.empty')}</p>
    {:else}
      <div class="overflow-x-auto rounded-lg border border-border">
        <table class="w-full min-w-[760px] text-left text-sm">
          <thead class="border-b border-border bg-surface text-xs text-muted-foreground">
            <tr>
              <th class="px-4 py-2.5 font-medium">{$t('admin.audit.col.time')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.audit.col.user')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.audit.col.action')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.audit.col.target')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.audit.col.detail')}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            {#each view.rows as row, i (i)}
              <tr data-testid="admin-audit-row-{i}">
                <td class="whitespace-nowrap px-4 py-2.5 tabular-nums text-muted-foreground">{row.time}</td>
                <td class="px-4 py-2.5 text-foreground">{actorLabel(row)}</td>
                <td class="px-4 py-2.5"><code class="rounded bg-muted px-1.5 py-0.5 font-mono text-xs text-foreground">{row.action}</code></td>
                <td class="px-4 py-2.5 font-mono text-xs text-muted-foreground">{targetLabel(row)}</td>
                <td class="max-w-sm px-4 py-2.5"><p class="line-clamp-2 break-all font-mono text-xs text-muted-foreground" title={row.detail}>{row.detail || $t('admin.audit.detail_empty')}</p></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      <Pager page={view.page} pages={view.pages} onPage={(n) => load(n)} testIdPrefix="admin-audit" />
    {/if}
  {/if}
</Page>
