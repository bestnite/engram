import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import AdminJobsView from '../lib/views/admin/AdminJobsView.svelte';
import { apiClient, ApiClientError, type AdminJobsResponse, type AdminTask, type AdminTasksResponse } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { toast } from '../lib/components/ui/toast';

vi.mock('../lib/components/ui/toast', () => ({ toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));

const history: AdminJobsResponse = {
  jobs: [
    {
      id: 'j1', kind: 'retention', trigger: 'auto', status: 'succeeded', stage: null, created_at: '2026-03-10 19:00:00',
      started_at: '2026-03-10 19:00:00', finished_at: '2026-03-10 19:00:00', duration_ms: 20, summary: { sessions: 1 },
      log_tail: '', error: '', can_cancel: false,
    },
  ],
  page: 1,
  pages: 1,
  total: 1,
};

function task(kind: string, state: Partial<AdminTask> = {}): AdminTask {
  return {
    kind,
    interval_seconds: 600,
    running: false,
    pending: false,
    next_run_at: '2026-03-10 20:10:00',
    last: { trigger: 'auto', started_at: '2026-03-10 20:00:00', finished_at: '2026-03-10 20:00:00', status: 'succeeded', error: '', summary: {} },
    ...state,
  };
}

function overview(...tasks: AdminTask[]): AdminTasksResponse {
  return { tasks };
}

let instance: ReturnType<typeof mount> | null = null;

afterEach(() => {
  if (instance) unmount(instance);
  instance = null;
  document.body.innerHTML = '';
  vi.restoreAllMocks();
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
  vi.useRealTimers();
});

async function flush(): Promise<void> {
  for (let i = 0; i < 20; i++) await Promise.resolve();
  flushSync();
}

async function mountView(tasks: AdminTasksResponse): Promise<void> {
  setLocale('zh-CN');
  const target = document.createElement('div');
  document.body.appendChild(target);
  instance = mount(AdminJobsView, {
    target,
    props: { initialLoading: false, initialData: structuredClone(history), initialTasks: structuredClone(tasks) },
  });
  await flush();
}

function el(testId: string): HTMLElement | null {
  return document.body.querySelector(`[data-testid="${testId}"]`);
}

describe('AdminJobsView task overview', () => {
  it('starts a task with the run button and shows it as waiting', async () => {
    const run = vi.spyOn(apiClient, 'runAdminTask').mockResolvedValue(task('media_gc', { pending: true }));
    vi.spyOn(apiClient, 'getAdminTasks').mockResolvedValue(overview(task('retention'), task('media_gc', { pending: true })));
    await mountView(overview(task('retention'), task('media_gc')));

    el('admin-jobs-run-media_gc')!.click();
    await flush();

    expect(run).toHaveBeenCalledWith('media_gc');
    expect(toast.success).toHaveBeenCalledWith('已开始运行：未引用媒体清理');
    expect(el('admin-jobs-run-media_gc')).toBeNull();
    expect(el('admin-jobs-task-busy-media_gc')?.textContent).toContain('等待开始');
    expect(el('admin-jobs-run-retention')).not.toBeNull();
  });

  it('reports a refused run with its localised reason and reloads the overview', async () => {
    vi.spyOn(apiClient, 'runAdminTask').mockRejectedValue(new ApiClientError('HTTP 409', { status: 409, code: 'already_running' }));
    const reload = vi.spyOn(apiClient, 'getAdminTasks').mockResolvedValue(overview(task('digest', { running: true })));
    await mountView(overview(task('digest')));

    el('admin-jobs-run-digest')!.click();
    await flush();

    expect(toast.error).toHaveBeenCalledWith('该任务正在运行或已在等待。');
    expect(toast.success).not.toHaveBeenCalled();
    expect(reload).toHaveBeenCalled();
    expect(el('admin-jobs-task-busy-digest')?.textContent).toContain('运行中');
  });

  it('maps an unknown task and an unavailable registry to their own messages', async () => {
    const run = vi.spyOn(apiClient, 'runAdminTask');
    vi.spyOn(apiClient, 'getAdminTasks').mockResolvedValue(overview(task('reminder')));
    await mountView(overview(task('reminder')));

    run.mockRejectedValueOnce(new ApiClientError('HTTP 404', { status: 404, code: 'invalid_task' }));
    el('admin-jobs-run-reminder')!.click();
    await flush();
    expect(toast.error).toHaveBeenLastCalledWith('该任务不存在或未启动。');

    run.mockRejectedValueOnce(new ApiClientError('HTTP 503', { status: 503, code: 'unavailable' }));
    el('admin-jobs-run-reminder')!.click();
    await flush();
    expect(toast.error).toHaveBeenLastCalledWith('定时任务当前不可用。');
  });

  it('polls while a task runs, stops after it finishes, and reloads the history then', async () => {
    vi.useFakeTimers();
    const finished = task('retention', {
      last: { trigger: 'manual', started_at: '2026-03-10 20:05:00', finished_at: '2026-03-10 20:05:01', status: 'succeeded', error: '', summary: { sessions: 2 } },
    });
    const poll = vi
      .spyOn(apiClient, 'getAdminTasks')
      .mockResolvedValueOnce(overview(task('retention', { running: true })))
      .mockResolvedValue(overview(finished));
    const jobs = vi.spyOn(apiClient, 'getAdminJobs').mockResolvedValue(structuredClone(history));
    await mountView(overview(task('retention', { running: true })));
    expect(poll).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(3000);
    await flush();
    expect(poll).toHaveBeenCalledTimes(1);
    expect(jobs).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(3000);
    await flush();
    expect(poll).toHaveBeenCalledTimes(2);
    expect(el('admin-jobs-task-last-retention')?.textContent).toContain('过期会话 2');
    expect(jobs).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(9000);
    await flush();
    expect(poll).toHaveBeenCalledTimes(2);
  });

  it('does not poll when every task is idle', async () => {
    vi.useFakeTimers();
    const poll = vi.spyOn(apiClient, 'getAdminTasks').mockResolvedValue(overview(task('retention')));
    await mountView(overview(task('retention')));
    await vi.advanceTimersByTimeAsync(10000);
    await flush();
    expect(poll).not.toHaveBeenCalled();
  });
});

describe('AdminJobsView history filter', () => {
  it('reloads the first page of the chosen kind', async () => {
    const jobs = vi.spyOn(apiClient, 'getAdminJobs').mockResolvedValue(structuredClone(history));
    await mountView(overview(task('retention')));

    el('admin-jobs-filter-kind')!.click();
    await flush();
    const option = Array.from(document.body.querySelectorAll('[role="option"]')).find((entry) => entry.textContent?.trim() === '未引用媒体清理');
    expect(option, 'media_gc option').toBeTruthy();
    // The select commits on pointerup; a plain click is ignored by the component.
    option!.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, pointerType: 'mouse' }));
    await flush();

    expect(jobs).toHaveBeenLastCalledWith({ page: 1, kind: 'media_gc' });
  });
});

describe('AdminJobsView optimizer cancellation', () => {
  it('still cancels a running optimizer job from the history', async () => {
    const running: AdminJobsResponse = {
      ...history,
      jobs: [{ ...history.jobs[0]!, id: 'o1', kind: 'optimize', trigger: 'user', status: 'running', summary: null, duration_ms: null, can_cancel: true }],
    };
    const cancel = vi.spyOn(apiClient, 'cancelAdminJob').mockResolvedValue();
    vi.spyOn(apiClient, 'getAdminJobs').mockResolvedValue(structuredClone(history));
    const confirm = await import('../lib/components/ui/confirm');
    vi.spyOn(confirm, 'askConfirm').mockResolvedValue(true);
    setLocale('zh-CN');
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(AdminJobsView, { target, props: { initialLoading: false, initialData: running, initialTasks: overview() } });
    await flush();

    el('admin-jobs-cancel-o1')!.click();
    await flush();
    expect(cancel).toHaveBeenCalledWith('o1');
    expect(toast.success).toHaveBeenCalledWith('作业已取消。');
  });
});
