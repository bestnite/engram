<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminI18nResponse } from '../../api';
  import AdminNav from './AdminNav.svelte';
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

<div class="mx-auto max-w-5xl space-y-6 px-4 py-10" data-testid="admin-i18n">
  <AdminNav />

  <header class="space-y-1">
    <h1 class="text-2xl font-bold tracking-tight text-foreground" data-testid="admin-i18n-title">{$t('admin.i18n.heading')}</h1>
  </header>

  {#if loading}
    <Skeleton testId="admin-i18n-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div data-testid="admin-i18n-failed" class="card-elevated rounded-xl p-8 text-center">
      <p role="alert" class="font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="admin-i18n-retry" onclick={() => load()} variant="primary" size="lg" class="mt-4">{$t('common.retry')}</Button>
    </div>
  {:else if data}
    {@const view = data}
    {#if view.all_complete}
      <div data-testid="admin-i18n-all-complete" role="status" class="rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">
        {$t('admin.i18n.all_complete')}
      </div>
    {/if}

    {#if view.locales.length === 0}
      <p data-testid="admin-i18n-empty" class="py-8 text-center text-sm text-muted-foreground">{$t('admin.i18n.empty')}</p>
    {:else}
      <div class="card-elevated overflow-x-auto rounded-xl">
        <table class="w-full text-left text-sm">
          <thead class="border-b border-zinc-200 text-xs uppercase tracking-wider text-zinc-500 dark:border-zinc-800 dark:text-zinc-400">
            <tr>
              <th class="px-4 py-3 font-semibold">{$t('admin.i18n.col.locale')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.i18n.col.coverage')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.i18n.col.status')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.i18n.col.missing')}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-zinc-100 dark:divide-zinc-800">
            {#each view.locales as cov (cov.code)}
              <tr data-testid="admin-i18n-row-{cov.code}">
                <td class="px-4 py-2.5 font-medium text-foreground">{cov.code}</td>
                <td class="px-4 py-2.5 text-muted-foreground">{$t('admin.i18n.percent', { percent: cov.percent })} ({$t('admin.i18n.counts', { present: cov.present, total: cov.total })})</td>
                <td class="px-4 py-2.5 text-muted-foreground">{cov.complete ? $t('admin.i18n.status.complete') : $t('admin.i18n.status.incomplete', { count: cov.missing.length })}</td>
                <td class="px-4 py-2.5 font-mono text-xs text-muted-foreground">{cov.missing.join(', ')}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</div>
