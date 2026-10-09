<script lang="ts">
  import { askConfirm } from '../../components/ui/confirm';
  import Page from '../../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminJobsResponse, AdminJob } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import PageHeader from '../../components/ui/PageHeader.svelte';
  import Pager from '../../components/ui/Pager.svelte';
  import Badge from '../../components/ui/Badge.svelte';
  import { toast } from '../../components/ui/toast';
  import Skeleton from '../../components/ui/Skeleton.svelte';
  import Button from '../../components/ui/Button.svelte';

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
    if (!(await askConfirm({ title: $t('admin.jobs.confirm_cancel'), destructive: true }))) return;
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

  // 操作结果用 toast 报告，不在页面顶部插横幅。
  $effect(() => {
    if (notice) {
      toast.success($t(notice));
      notice = '';
    }
  });
  $effect(() => {
    if (actionError) {
      toast.error($t(actionError));
      actionError = '';
    }
  });
</script>

<Page testId="admin-jobs">
  <AdminNav />
  <PageHeader title={$t('admin.jobs.heading')} testId="admin-jobs-title" />

  {#if loading}
    <Skeleton testId="admin-jobs-loading" label={$t('common.loading')} lines={3} />
  {:else if loadError}
    <div data-testid="admin-jobs-failed" class="py-16 text-center">
      <p role="alert" class="font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="admin-jobs-retry" onclick={() => load(1)} variant="outline" size="lg" class="mt-4">{$t('common.retry')}</Button>
    </div>
  {:else if data}
    {@const view = data}
    {#if view.jobs.length === 0}
      <p data-testid="admin-jobs-empty" class="rounded-lg border border-dashed border-border py-16 text-center text-sm text-muted-foreground">{$t('admin.jobs.empty')}</p>
    {:else}
      <div class="overflow-x-auto rounded-lg border border-border">
        <table class="w-full min-w-[760px] text-left text-sm">
          <thead class="border-b border-border bg-surface text-xs text-muted-foreground">
            <tr>
              <th class="px-4 py-2.5 font-medium">{$t('admin.jobs.col.kind')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.jobs.col.status')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.jobs.col.created')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.jobs.col.log')}</th>
              <th class="w-20 px-4 py-2.5"><span class="sr-only">{$t('admin.jobs.col.actions')}</span></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            {#each view.jobs as job (job.id)}
              <tr data-testid="admin-jobs-row-{job.id}">
                <td class="px-4 py-2.5">
                  <div class="text-foreground">{kindLabel(job.kind)}</div>
                  <div class="font-mono text-xs text-muted-foreground">#{job.id}</div>
                </td>
                <td class="px-4 py-2.5">
                  <Badge variant={job.status === 'failed' ? 'danger' : job.status === 'succeeded' ? 'success' : job.status === 'running' ? 'info' : 'neutral'}>{statusLabel(job.status)}</Badge>
                  {#if job.stage}<div class="mt-1 text-xs text-muted-foreground">{stageLabel(job.stage)}</div>{/if}
                </td>
                <td class="whitespace-nowrap px-4 py-2.5 tabular-nums text-muted-foreground">{job.created_at}</td>
                <td class="max-w-md px-4 py-2.5">
                  <p class="line-clamp-2 break-all font-mono text-xs {job.error ? 'text-destructive-foreground' : 'text-muted-foreground'}" title={job.error || job.log_tail || ''}>{job.error || job.log_tail || $t('admin.jobs.log_empty')}</p>
                </td>
                <td class="px-4 py-2.5 text-right">
                  {#if job.can_cancel}
                    <Button variant="ghost" size="sm" class="hover:bg-destructive-soft hover:text-destructive-foreground" testId="admin-jobs-cancel-{job.id}" onclick={() => cancel(job)}>{$t('admin.jobs.cancel')}</Button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      <Pager page={view.page} pages={view.pages} onPage={(n) => load(n)} testIdPrefix="admin-jobs" />
    {/if}
  {/if}
</Page>
