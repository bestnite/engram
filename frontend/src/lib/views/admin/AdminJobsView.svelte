<script lang="ts">
  import { askConfirm } from '../../components/ui/confirm';
  import Page from '../../components/ui/Page.svelte';
  import { onDestroy, onMount, untrack } from 'svelte';
  import { t, localeStore } from '../../i18n';
  import { apiClient, ApiClientError } from '../../api';
  import type { AdminJobsResponse, AdminJob, AdminTask, AdminTasksResponse } from '../../api';
  import AdminNav from './AdminNav.svelte';
  import PageHeader from '../../components/ui/PageHeader.svelte';
  import Pager from '../../components/ui/Pager.svelte';
  import Badge from '../../components/ui/Badge.svelte';
  import Select from '../../components/ui/Select.svelte';
  import { toast } from '../../components/ui/toast';
  import Skeleton from '../../components/ui/Skeleton.svelte';
  import Button from '../../components/ui/Button.svelte';
  import { CircleStop, ListChecks, Play, RefreshCw, Timer } from '@lucide/svelte';
  import EmptyState from '../../components/ui/EmptyState.svelte';
  import { formatDuration, formatNumber } from '../../format';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialData?: AdminJobsResponse | null;
    initialTasks?: AdminTasksResponse | null;
    initialTasksError?: ApiClientError | Error | null;
  }

  let {
    initialLoading = true,
    initialError = null,
    initialData = null,
    initialTasks = null,
    initialTasksError = null,
  }: Props = $props();

  /** The periodic task kinds the server knows, in its order; the history filter offers them after `optimize`. */
  const taskKinds = ['retention', 'media_gc', 'reminder', 'digest', 'mail_delivery'];
  /** While a task runs or waits to start, the overview is reloaded this often, so its finished round shows up. */
  const pollMs = 3000;

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let data = $state<AdminJobsResponse | null>(initialData);
  // svelte-ignore state_referenced_locally
  let tasks = $state<AdminTask[] | null>(initialTasks?.tasks ?? null);
  // svelte-ignore state_referenced_locally
  let tasksError = $state<ApiClientError | Error | null>(initialTasksError);
  let kindFilter = $state('');
  let notice = $state('');
  let actionError = $state('');
  let starting = $state<string | null>(null);

  const busy = $derived((tasks ?? []).some((task) => task.running || task.pending));

  function statusLabel(status: string): string {
    return $t('admin.jobs.status.' + status);
  }

  function kindLabel(kind: string): string {
    return $t('admin.jobs.kind.' + kind);
  }

  function triggerLabel(trigger: string): string {
    return $t('admin.jobs.trigger.' + trigger);
  }

  function stageLabel(stage: string | null): string {
    return stage ? $t('admin.jobs.stage.' + stage) : $t('admin.jobs.stage.unknown');
  }

  function statusVariant(status: string): 'danger' | 'success' | 'info' | 'neutral' {
    if (status === 'failed') return 'danger';
    if (status === 'succeeded') return 'success';
    if (status === 'running') return 'info';
    return 'neutral';
  }

  function intervalLabel(seconds: number): string {
    return $t('admin.jobs.interval', { duration: formatDuration(seconds * 1000, $localeStore, 'long') });
  }

  /**
   * The action counts of a round as labelled numbers, in the server's key order and without zeros; null when the
   * round did nothing, which the page shows as "nothing to do" rather than as an empty cell.
   */
  function summaryText(summary: Record<string, number> | null): string | null {
    const parts = Object.entries(summary ?? {})
      .filter(([, value]) => value > 0)
      .map(([key, value]) => $t('admin.jobs.summary.item', { label: $t('admin.jobs.summary.' + key), value: formatNumber(value, $localeStore) }));
    return parts.length > 0 ? parts.join(' · ') : null;
  }

  function loadErrorKey(err: ApiClientError | Error | null, fallback: string): string {
    if (err instanceof ApiClientError && (err.isUnauthorized || err.status === 403)) {
      return 'admin.error.forbidden';
    }
    return fallback;
  }

  function cancelErrorKey(err: unknown): string {
    const code = err instanceof ApiClientError ? err.code : '';
    const map: Record<string, string> = {
      not_running: 'admin.jobs.notice.not_running',
      invalid_job: 'admin.jobs.notice.invalid_job',
      unavailable: 'admin.jobs.notice.unavailable',
      failed: 'admin.jobs.notice.failed',
    };
    return map[code] || 'admin.jobs.notice.failed';
  }

  function runErrorKey(err: unknown): string {
    const code = err instanceof ApiClientError ? err.code : '';
    const map: Record<string, string> = {
      already_running: 'admin.jobs.notice.already_running',
      invalid_task: 'admin.jobs.notice.invalid_task',
      unavailable: 'admin.jobs.notice.run_unavailable',
    };
    return map[code] || 'admin.jobs.notice.run_failed';
  }

  async function load(page = 1): Promise<void> {
    loading = true;
    loadError = null;
    try {
      data = await apiClient.getAdminJobs({ page, kind: kindFilter || undefined });
    } catch (err) {
      loadError = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  async function loadTasks(): Promise<void> {
    try {
      tasks = (await apiClient.getAdminTasks()).tasks;
      tasksError = null;
    } catch (err) {
      tasksError = err instanceof Error ? err : new Error(String(err));
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
      actionError = cancelErrorKey(err);
    }
  }

  async function runNow(task: AdminTask): Promise<void> {
    starting = task.kind;
    try {
      const accepted = await apiClient.runAdminTask(task.kind);
      tasks = (tasks ?? []).map((item) => (item.kind === accepted.kind ? accepted : item));
      toast.success($t('admin.jobs.notice.run_started', { task: kindLabel(task.kind) }));
    } catch (err) {
      toast.error($t(runErrorKey(err)));
      await loadTasks();
    } finally {
      starting = null;
    }
  }

  function changeKind(next: string): void {
    kindFilter = next;
    load(1);
  }

  // Polling runs only while some task is busy. When the last busy task finishes, the history is reloaded too,
  // because the finished round may have added a row.
  let pollTimer: ReturnType<typeof setInterval> | null = null;
  let wasBusy = false;
  $effect(() => {
    const active = busy;
    if (active && pollTimer === null) {
      pollTimer = setInterval(() => {
        loadTasks();
      }, pollMs);
    } else if (!active && pollTimer !== null) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
    if (wasBusy && !active) {
      untrack(() => load(data?.page ?? 1));
    }
    wasBusy = active;
  });

  onDestroy(() => {
    if (pollTimer !== null) clearInterval(pollTimer);
  });

  onMount(() => {
    if (initialData === null && initialError === null) {
      load(1);
    }
    if (initialTasks === null && initialTasksError === null) {
      loadTasks();
    }
  });

  // Operation results are reported with a toast, not inserted as a banner at the top of the page.
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

  <section class="mb-10" data-testid="admin-jobs-tasks" aria-labelledby="admin-jobs-tasks-heading">
    <h2 id="admin-jobs-tasks-heading" class="text-base font-semibold text-foreground">{$t('admin.jobs.tasks.heading')}</h2>
    <p class="mt-1 mb-4 text-sm text-muted-foreground">{$t('admin.jobs.tasks.intro')}</p>

    {#if tasksError}
      <div data-testid="admin-jobs-tasks-failed" class="py-10 text-center">
        <p role="alert" class="font-medium text-foreground">{$t(loadErrorKey(tasksError, 'admin.jobs.tasks.load_failed'))}</p>
        <Button type="button" testId="admin-jobs-tasks-retry" onclick={() => loadTasks()} variant="outline" size="lg" class="mt-4"><RefreshCw class="size-4" aria-hidden="true" /><span>{$t('common.retry')}</span></Button>
      </div>
    {:else if tasks === null}
      <Skeleton testId="admin-jobs-tasks-loading" label={$t('common.loading')} lines={3} />
    {:else if tasks.length === 0}
      <EmptyState testId="admin-jobs-tasks-empty" icon={Timer} text={$t('admin.jobs.tasks.empty')} />
    {:else}
      <div class="overflow-x-auto rounded-lg border border-border">
        <table class="w-full min-w-[860px] text-left text-sm">
          <thead class="border-b border-border bg-surface text-xs text-muted-foreground">
            <tr>
              <th class="px-4 py-2.5 font-medium">{$t('admin.jobs.col.task')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.jobs.col.interval')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.jobs.col.last_run')}</th>
              <th class="px-4 py-2.5 font-medium">{$t('admin.jobs.col.next_run')}</th>
              <th class="w-32 px-4 py-2.5"><span class="sr-only">{$t('admin.jobs.col.actions')}</span></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            {#each tasks as task (task.kind)}
              {@const last = task.last}
              {@const lastSummary = last ? summaryText(last.summary) : null}
              <tr data-testid="admin-jobs-task-{task.kind}">
                <td class="max-w-sm px-4 py-2.5 align-top">
                  <div class="font-medium text-foreground">{kindLabel(task.kind)}</div>
                  <div class="mt-0.5 text-xs text-muted-foreground">{$t('admin.jobs.task_desc.' + task.kind)}</div>
                </td>
                <td class="whitespace-nowrap px-4 py-2.5 align-top text-muted-foreground">{intervalLabel(task.interval_seconds)}</td>
                <td class="max-w-xs px-4 py-2.5 align-top" data-testid="admin-jobs-task-last-{task.kind}">
                  {#if last}
                    <div class="flex flex-wrap items-center gap-2">
                      <Badge variant={statusVariant(last.status)}>{statusLabel(last.status)}</Badge>
                      <span class="tabular-nums text-xs text-muted-foreground">{last.finished_at}</span>
                      {#if last.trigger === 'manual'}<span class="text-xs text-muted-foreground">{triggerLabel(last.trigger)}</span>{/if}
                    </div>
                    {#if last.error}
                      <p class="mt-1 line-clamp-2 break-all font-mono text-xs text-destructive-foreground" title={last.error}>{last.error}</p>
                    {:else}
                      <p class="mt-1 text-xs text-muted-foreground">{lastSummary ?? $t('admin.jobs.nothing_to_do')}</p>
                    {/if}
                  {:else}
                    <span class="text-xs text-muted-foreground">{$t('admin.jobs.never_run')}</span>
                  {/if}
                </td>
                <td class="whitespace-nowrap px-4 py-2.5 align-top tabular-nums text-muted-foreground">{task.next_run_at || $t('admin.jobs.not_scheduled')}</td>
                <td class="px-4 py-2.5 text-right align-top">
                  {#if task.running || task.pending}
                    <span class="inline-flex items-center gap-1.5 text-xs text-muted-foreground" data-testid="admin-jobs-task-busy-{task.kind}">
                      <RefreshCw class="size-3.5 animate-spin" aria-hidden="true" />{task.running ? $t('admin.jobs.run_running') : $t('admin.jobs.run_pending')}
                    </span>
                  {:else}
                    <Button variant="outline" size="sm" testId="admin-jobs-run-{task.kind}" loading={starting === task.kind} disabled={starting !== null} onclick={() => runNow(task)}><Play class="size-4" aria-hidden="true" /><span>{$t('admin.jobs.run_now')}</span></Button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>

  <section data-testid="admin-jobs-history" aria-labelledby="admin-jobs-history-heading">
    <div class="mb-4 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h2 id="admin-jobs-history-heading" class="text-base font-semibold text-foreground">{$t('admin.jobs.history.heading')}</h2>
        <p class="mt-1 text-sm text-muted-foreground">{$t('admin.jobs.history.intro')}</p>
      </div>
      <Select
        class="w-56"
        size="sm"
        value={kindFilter}
        allowDeselect={false}
        testId="admin-jobs-filter-kind"
        ariaLabel={$t('admin.jobs.filter.kind')}
        options={[{ value: '', label: $t('admin.jobs.filter.all') }, { value: 'optimize', label: kindLabel('optimize') }, ...taskKinds.map((kind) => ({ value: kind, label: kindLabel(kind) }))]}
        onValueChange={changeKind}
      />
    </div>

    {#if loading}
      <Skeleton testId="admin-jobs-loading" label={$t('common.loading')} lines={3} />
    {:else if loadError}
      <div data-testid="admin-jobs-failed" class="py-16 text-center">
        <p role="alert" class="font-medium text-foreground">{$t(loadErrorKey(loadError, 'admin.jobs.load_failed'))}</p>
        <Button type="button" testId="admin-jobs-retry" onclick={() => load(1)} variant="outline" size="lg" class="mt-4"><RefreshCw class="size-4" aria-hidden="true" /><span>{$t('common.retry')}</span></Button>
      </div>
    {:else if data}
      {@const view = data}
      {#if view.jobs.length === 0}
        <EmptyState testId="admin-jobs-empty" icon={ListChecks} text={$t('admin.jobs.empty')} />
      {:else}
        <div class="overflow-x-auto rounded-lg border border-border">
          <table class="w-full min-w-[900px] text-left text-sm">
            <thead class="border-b border-border bg-surface text-xs text-muted-foreground">
              <tr>
                <th class="px-4 py-2.5 font-medium">{$t('admin.jobs.col.kind')}</th>
                <th class="px-4 py-2.5 font-medium">{$t('admin.jobs.col.trigger')}</th>
                <th class="px-4 py-2.5 font-medium">{$t('admin.jobs.col.status')}</th>
                <th class="px-4 py-2.5 font-medium">{$t('admin.jobs.col.started')}</th>
                <th class="px-4 py-2.5 font-medium">{$t('admin.jobs.col.duration')}</th>
                <th class="px-4 py-2.5 font-medium">{$t('admin.jobs.col.result')}</th>
                <th class="w-20 px-4 py-2.5"><span class="sr-only">{$t('admin.jobs.col.actions')}</span></th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border">
              {#each view.jobs as job (job.id)}
                {@const jobSummary = job.summary ? summaryText(job.summary) : null}
                <tr data-testid="admin-jobs-row-{job.id}">
                  <td class="px-4 py-2.5">
                    <div class="text-foreground">{kindLabel(job.kind)}</div>
                    <div class="font-mono text-xs text-muted-foreground">#{job.id}</div>
                  </td>
                  <td class="whitespace-nowrap px-4 py-2.5 text-muted-foreground">{triggerLabel(job.trigger)}</td>
                  <td class="px-4 py-2.5">
                    <Badge variant={statusVariant(job.status)}>{statusLabel(job.status)}</Badge>
                    {#if job.stage}<div class="mt-1 text-xs text-muted-foreground">{stageLabel(job.stage)}</div>{/if}
                  </td>
                  <td class="whitespace-nowrap px-4 py-2.5 tabular-nums text-muted-foreground">{job.started_at || job.created_at}</td>
                  <td class="whitespace-nowrap px-4 py-2.5 tabular-nums text-muted-foreground">{job.duration_ms === null ? $t('admin.jobs.not_scheduled') : formatDuration(job.duration_ms, $localeStore)}</td>
                  <td class="max-w-md px-4 py-2.5" data-testid="admin-jobs-result-{job.id}">
                    {#if job.error}
                      <p class="line-clamp-2 break-all font-mono text-xs text-destructive-foreground" title={job.error}>{job.error}</p>
                    {:else if job.summary}
                      <p class="text-xs text-muted-foreground">{jobSummary ?? $t('admin.jobs.nothing_to_do')}</p>
                    {:else}
                      <p class="line-clamp-2 break-all font-mono text-xs text-muted-foreground" title={job.log_tail}>{job.log_tail || $t('admin.jobs.log_empty')}</p>
                    {/if}
                  </td>
                  <td class="px-4 py-2.5 text-right">
                    {#if job.can_cancel}
                      <Button variant="ghost" size="sm" class="hover:bg-destructive-soft hover:text-destructive-foreground" testId="admin-jobs-cancel-{job.id}" onclick={() => cancel(job)}><CircleStop class="size-4" aria-hidden="true" /><span>{$t('admin.jobs.cancel')}</span></Button>
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
  </section>
</Page>
