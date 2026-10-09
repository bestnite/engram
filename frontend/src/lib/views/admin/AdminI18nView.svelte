<script lang="ts">
  import Page from '../../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminI18nResponse } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import PageHeader from '../../components/ui/PageHeader.svelte';
  import Badge from '../../components/ui/Badge.svelte';
  import Skeleton from '../../components/ui/Skeleton.svelte';
  import Button from '../../components/ui/Button.svelte';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialData?: AdminI18nResponse | null;
  }

  let { initialLoading = true, initialError = null, initialData = null }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let data = $state<AdminI18nResponse | null>(initialData);

  function loadErrorKey(): string {
    if (loadError instanceof ApiClientError && (loadError.isUnauthorized || loadError.status === 403)) {
      return 'admin.error.forbidden';
    }
    return 'admin.i18n.load_failed';
  }

  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      data = await apiClient.getAdminI18n();
    } catch (err) {
      loadError = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    if (initialData === null && initialError === null) {
      load();
    }
  });
</script>

<Page testId="admin-i18n">
  <AdminNav />
  <PageHeader title={$t('admin.i18n.heading')} testId="admin-i18n-title">
    {#snippet meta()}
      {#if data?.all_complete}
        <span data-testid="admin-i18n-all-complete" role="status"><Badge variant="success">{$t('admin.i18n.all_complete')}</Badge></span>
      {/if}
    {/snippet}
  </PageHeader>

  {#if loading}
    <Skeleton testId="admin-i18n-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div data-testid="admin-i18n-failed" class="py-16 text-center">
      <p role="alert" class="font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="admin-i18n-retry" onclick={() => load()} variant="outline" size="lg" class="mt-4">{$t('common.retry')}</Button>
    </div>
  {:else if data}
    {@const view = data}
    {#if view.locales.length === 0}
      <p data-testid="admin-i18n-empty" class="rounded-lg border border-dashed border-border py-16 text-center text-sm text-muted-foreground">{$t('admin.i18n.empty')}</p>
    {:else}
      <div class="overflow-x-auto rounded-lg border border-border">
        <table class="w-full min-w-[560px] text-left text-sm">
          <thead class="border-b border-border bg-surface text-xs text-muted-foreground">
            <tr>
              <th class="px-4 py-2.5 font-medium">{$t('admin.i18n.col.locale')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.i18n.col.coverage')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.i18n.col.status')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.i18n.col.missing')}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            {#each view.locales as cov (cov.code)}
              <tr data-testid="admin-i18n-row-{cov.code}">
                <td class="px-4 py-2.5 font-medium text-foreground">{cov.code}</td>
                <td class="px-4 py-2.5">
                  <div class="flex items-center gap-3">
                    <div class="h-1.5 w-24 overflow-hidden rounded-full bg-muted" aria-hidden="true">
                      <div class="h-full rounded-full {cov.complete ? 'bg-success' : 'bg-brand'}" style="width: {cov.percent}%"></div>
                    </div>
                    <span class="tabular-nums text-muted-foreground">{$t('admin.i18n.percent', { percent: cov.percent })} ({$t('admin.i18n.counts', { present: cov.present, total: cov.total })})</span>
                  </div>
                </td>
                <td class="px-4 py-2.5">
                  <Badge variant={cov.complete ? 'success' : 'warning'}>{cov.complete ? $t('admin.i18n.status.complete') : $t('admin.i18n.status.incomplete', { count: cov.missing.length })}</Badge>
                </td>
                <td class="max-w-80 px-4 py-2.5 font-mono text-xs break-words text-muted-foreground">{cov.missing.join(', ') || '—'}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</Page>
