<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminJobsResponse, AdminJob } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import Skeleton from '../../components/ui/Skeleton.svelte';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialData?: AdminJobsResponse | null;
  }

  let { initialLoading = true, initialError = null, initialData = null }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let data = $state<AdminJobsResponse | null>(initialData);
  let notice = $state('');
  let actionError = $state('');

  function statusLabel(status: string): string {
    return $t('admin.jobs.status.' + status);
  }

  function kindLabel(kind: string): string {
    return $t('admin.jobs.kind.' + kind);
  }

  function stageLabel(stage: string | null): string {
    return stage ? $t('admin.jobs.stage.' + stage) : $t('admin.jobs.stage.unknown');
  }

  function loadErrorKey(): string {
    if (loadError instanceof ApiClientError && (loadError.isUnauthorized || loadError.status === 403)) {
      return 'admin.error.forbidden';
    }
    return 'admin.jobs.load_failed';
  }

  function actionErrorKey(err: unknown): string {
    const code = err instanceof ApiClientError ? err.code : '';
    const map: Record<string, string> = {
      not_running: 'admin.jobs.notice.not_running',
      invalid_job: 'admin.jobs.notice.invalid_job',
      unavailable: 'admin.jobs.notice.unavailable',
      failed: 'admin.jobs.notice.failed',
    };
    return map[code] || 'admin.jobs.notice.failed';
  }

  async function load(page = 1): Promise<void> {
    loading = true;
    loadError = null;
    try {
      data = await apiClient.getAdminJobs({ page });
    } catch (err) {
      loadError = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  async function cancel(job: AdminJob): Promise<void> {
    notice = '';
    actionError = '';
    if (typeof window !== 'undefined' && !window.confirm($t('admin.jobs.confirm_cancel'))) return;
    try {
      await apiClient.cancelAdminJob(job.id);
      notice = 'admin.jobs.notice.cancelled';
      await load(data?.page ?? 1);
    } catch (err) {
      actionError = actionErrorKey(err);
    }
  }

  onMount(() => {
    if (initialData === null && initialError === null) {
      load(1);
    }
  });
</script>

<div class="mx-auto max-w-5xl space-y-6 px-4 py-10" data-testid="admin-jobs">
  <AdminNav />

  <header class="space-y-1">
    <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100" data-testid="admin-jobs-title">{$t('admin.jobs.heading')}</h1>
  </header>

  {#if notice}<div data-testid="admin-jobs-notice" role="status" class="rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">{$t(notice)}</div>{/if}
  {#if actionError}<div data-testid="admin-jobs-error" role="alert" class="rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700 dark:border-rose-900/60 dark:bg-rose-950/40 dark:text-rose-300">{$t(actionError)}</div>{/if}

  {#if loading}
    <Skeleton testId="admin-jobs-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div data-testid="admin-jobs-failed" class="card-elevated rounded-xl p-8 text-center">
      <p role="alert" class="font-medium text-zinc-900 dark:text-zinc-100">{$t(loadErrorKey())}</p>
      <button type="button" data-testid="admin-jobs-retry" onclick={() => load(1)} class="btn-press mt-4 cursor-pointer rounded-lg bg-zinc-900 px-4 py-2 text-sm font-medium text-white dark:bg-zinc-100 dark:text-zinc-900">{$t('common.retry')}</button>
    </div>
  {:else if data}
    {@const view = data}
    {#if view.jobs.length === 0}
      <p data-testid="admin-jobs-empty" class="py-8 text-center text-sm text-zinc-500 dark:text-zinc-400">{$t('admin.jobs.empty')}</p>
    {:else}
      <div class="card-elevated overflow-x-auto rounded-xl">
        <table class="w-full text-left text-sm">
          <thead class="border-b border-zinc-200 text-xs uppercase tracking-wider text-zinc-500 dark:border-zinc-800 dark:text-zinc-400">
            <tr>
              <th class="px-4 py-3 font-semibold">{$t('admin.jobs.col.id')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.jobs.col.kind')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.jobs.col.status')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.jobs.col.stage')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.jobs.col.created')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.jobs.col.log')}</th>
              <th class="px-4 py-3 font-semibold">{$t('admin.jobs.col.actions')}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-zinc-100 dark:divide-zinc-800">
            {#each view.jobs as job (job.id)}
              <tr data-testid="admin-jobs-row-{job.id}">
                <td class="px-4 py-2.5 text-zinc-700 dark:text-zinc-300">#{job.id}</td>
                <td class="px-4 py-2.5 text-zinc-600 dark:text-zinc-400">{kindLabel(job.kind)}</td>
                <td class="px-4 py-2.5 text-zinc-600 dark:text-zinc-400">{statusLabel(job.status)}</td>
                <td class="px-4 py-2.5 text-zinc-600 dark:text-zinc-400">{stageLabel(job.stage)}</td>
                <td class="px-4 py-2.5 text-zinc-600 dark:text-zinc-400">{job.created_at}</td>
                <td class="px-4 py-2.5 font-mono text-xs text-zinc-500 dark:text-zinc-500">{job.error || job.log_tail || $t('admin.jobs.log_empty')}</td>
                <td class="px-4 py-2.5">
                  {#if job.can_cancel}
                    <button type="button" data-testid="admin-jobs-cancel-{job.id}" onclick={() => cancel(job)} class="cursor-pointer rounded border border-rose-200 px-2 py-1 text-xs text-rose-600 dark:border-rose-900">{$t('admin.jobs.cancel')}</button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>

      <div class="flex items-center justify-between" data-testid="admin-jobs-pager">
        <button type="button" data-testid="admin-jobs-prev" disabled={view.page <= 1} onclick={() => load(view.page - 1)} class="btn-press cursor-pointer rounded-lg border border-zinc-200 px-4 py-2 text-sm disabled:opacity-40 dark:border-zinc-700">{$t('admin.common.prev')}</button>
        <span class="text-sm text-zinc-500 dark:text-zinc-400">{view.page} / {view.pages}</span>
        <button type="button" data-testid="admin-jobs-next" disabled={view.page >= view.pages} onclick={() => load(view.page + 1)} class="btn-press cursor-pointer rounded-lg border border-zinc-200 px-4 py-2 text-sm disabled:opacity-40 dark:border-zinc-700">{$t('admin.common.next')}</button>
      </div>
    {/if}
  {/if}
</div>
