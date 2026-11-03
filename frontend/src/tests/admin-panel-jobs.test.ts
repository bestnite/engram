import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render } from 'svelte/server';
import AdminJobsView from '../lib/views/admin/AdminJobsView.svelte';
import AdminI18nView from '../lib/views/admin/AdminI18nView.svelte';
import { ApiClient } from '../lib/api';
import type { AdminJobsResponse, AdminI18nResponse, AdminTasksResponse } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';
import { matchRoute } from '../lib/router';
import { routes } from '../lib/router/routes';

const jobs: AdminJobsResponse = {
  jobs: [
    { id: '12', kind: 'optimize', trigger: 'user', status: 'running', stage: 'training', created_at: '2026-03-10 20:00:00', started_at: '2026-03-10 20:00:01', finished_at: '', duration_ms: null, summary: null, log_tail: 'epoch 3', error: '', can_cancel: true },
    { id: '11', kind: 'optimize', trigger: 'user', status: 'failed', stage: null, created_at: '2026-03-09 20:00:00', started_at: '2026-03-09 20:00:01', finished_at: '2026-03-09 20:00:05', duration_ms: 4000, summary: null, log_tail: '', error: 'killed', can_cancel: false },
    { id: '10', kind: 'retention', trigger: 'auto', status: 'succeeded', stage: null, created_at: '2026-03-09 19:00:00', started_at: '2026-03-09 19:00:00', finished_at: '2026-03-09 19:00:00', duration_ms: 35, summary: { sessions: 3, action_tokens: 0, job_runs: 2 }, log_tail: '', error: '', can_cancel: false },
    { id: '9', kind: 'media_gc', trigger: 'manual', status: 'succeeded', stage: null, created_at: '2026-03-09 18:00:00', started_at: '2026-03-09 18:00:00', finished_at: '2026-03-09 18:00:01', duration_ms: 1200, summary: { removed: 0 }, log_tail: '', error: '', can_cancel: false },
  ],
  page: 1,
  pages: 1,
  total: 4,
};

const round = (summary: Record<string, number>, status = 'succeeded', error = '') => ({
  trigger: 'auto', started_at: '2026-03-10 20:00:00', finished_at: '2026-03-10 20:00:01', status, error, summary,
});

const tasks: AdminTasksResponse = {
  tasks: [
    { kind: 'retention', interval_seconds: 600, running: false, pending: false, next_run_at: '2026-03-10 20:10:00', last: round({ sessions: 0, invites: 0 }) },
    { kind: 'media_gc', interval_seconds: 600, running: false, pending: false, next_run_at: '2026-03-10 20:10:00', last: round({ removed: 4 }) },
    { kind: 'reminder', interval_seconds: 600, running: true, pending: false, next_run_at: '2026-03-10 20:10:00', last: null },
    { kind: 'digest', interval_seconds: 600, running: false, pending: false, next_run_at: '2026-03-10 20:10:00', last: round({}, 'failed', 'load digest candidates: boom') },
    { kind: 'mail_delivery', interval_seconds: 5, running: false, pending: true, next_run_at: '2026-03-10 20:00:05', last: round({ sent: 2, failed: 0, retried: 1 }) },
  ],
};

const i18n: AdminI18nResponse = {
  locales: [
    { code: 'en', percent: 100, present: 10, total: 10, complete: true, missing: [] },
    { code: 'zh-CN', percent: 90, present: 9, total: 10, complete: false, missing: ['a.b'] },
  ],
  all_complete: false,
};

describe('Admin jobs and i18n views render server-provided truth', () => {
  beforeEach(() => setLocale('zh-CN'));

  it('renders job rows with mapped status, kind and stage', () => {
    const { html } = render(AdminJobsView, { props: { initialLoading: false, initialData: jobs } });
    expect(html).toContain('data-testid="admin-jobs-row-12"');
    expect(html).toContain('参数优化');
    expect(html).toContain('执行中');
    expect(html).toContain('训练中');
    expect(html).toContain('data-testid="admin-jobs-cancel-12"');
    expect(html).not.toContain('data-testid="admin-jobs-cancel-11"');
  });

  it('renders the trigger, duration and labelled summary counts in the history', () => {
    const { html } = render(AdminJobsView, { props: { initialLoading: false, initialData: jobs } });
    const row = (id: string) => html.slice(html.indexOf(`data-testid="admin-jobs-row-${id}"`), html.indexOf('</tr>', html.indexOf(`data-testid="admin-jobs-row-${id}"`)));
    expect(row('10')).toContain('过期记录清理');
    expect(row('10')).toContain('自动');
    expect(row('10')).toContain('过期会话 3 · 任务记录 2');
    expect(row('10')).not.toContain('令牌');
    expect(row('9')).toContain('手动');
    expect(row('9')).toContain('无事可做');
    expect(row('11')).toContain('用户请求');
    expect(row('11')).toContain('killed');
    expect(row('11')).toContain('4秒');
    expect(row('12')).toContain('epoch 3');
  });

  it('renders every task kind in the overview with its latest round', () => {
    const { html } = render(AdminJobsView, { props: { initialLoading: false, initialData: jobs, initialTasks: tasks } });
    for (const task of tasks.tasks) {
      expect(html).toContain(`data-testid="admin-jobs-task-${task.kind}"`);
    }
    const last = (kind: string) => html.slice(html.indexOf(`data-testid="admin-jobs-task-last-${kind}"`), html.indexOf('</td>', html.indexOf(`data-testid="admin-jobs-task-last-${kind}"`)));
    expect(last('retention')).toContain('无事可做');
    expect(last('media_gc')).toContain('媒体文件 4');
    expect(last('reminder')).toContain('尚未运行');
    expect(last('digest')).toContain('失败');
    expect(last('digest')).toContain('load digest candidates: boom');
    expect(last('mail_delivery')).toContain('已发送 2 · 待重试 1');
    expect(html).toContain('每10分钟');
    expect(html).toContain('每5秒');
    expect(html).toContain('data-testid="admin-jobs-run-retention"');
    expect(html).not.toContain('data-testid="admin-jobs-run-reminder"');
    expect(html).toContain('data-testid="admin-jobs-task-busy-reminder"');
    expect(html).toContain('data-testid="admin-jobs-task-busy-mail_delivery"');
  });

  it('renders the overview in English', () => {
    setLocale('en');
    const { html } = render(AdminJobsView, { props: { initialLoading: false, initialData: jobs, initialTasks: tasks } });
    expect(html).toContain('Every 10 minutes');
    expect(html).toContain('Unreferenced media cleanup');
    expect(html).toContain('Media files 4');
    expect(html).toContain('Nothing to do');
  });

  it('renders the coverage report with the incomplete row', () => {
    const { html } = render(AdminI18nView, { props: { initialLoading: false, initialData: i18n } });
    expect(html).toContain('data-testid="admin-i18n-row-zh-CN"');
    expect(html).toContain('90%');
    expect(html).toContain('缺失 1 条');
    expect(html).toContain('a.b');
    expect(html).not.toContain('data-testid="admin-i18n-all-complete"');
  });

  it('shows the all-complete banner when every pack is complete', () => {
    const complete: AdminI18nResponse = { locales: [i18n.locales[0]!], all_complete: true };
    const { html } = render(AdminI18nView, { props: { initialLoading: false, initialData: complete } });
    expect(html).toContain('data-testid="admin-i18n-all-complete"');
  });

  it('covers every jobs/i18n key used by the views in both catalogs', () => {
    const keys = [
      'admin.jobs.heading',
      'admin.jobs.status.running',
      'admin.jobs.stage.training',
      'admin.jobs.notice.not_running',
      'admin.jobs.tasks.heading',
      'admin.jobs.history.heading',
      'admin.jobs.nothing_to_do',
      'admin.jobs.run_now',
      'admin.jobs.notice.already_running',
      ...['retention', 'media_gc', 'reminder', 'digest', 'mail_delivery'].flatMap((kind) => [`admin.jobs.kind.${kind}`, `admin.jobs.task_desc.${kind}`]),
      ...['auto', 'manual', 'user'].map((trigger) => `admin.jobs.trigger.${trigger}`),
      ...['sessions', 'action_tokens', 'invites', 'deck_share_invites', 'login_fingerprints', 'job_runs', 'removed', 'queued', 'sent', 'failed', 'retried'].map((key) => `admin.jobs.summary.${key}`),
      'admin.i18n.status.incomplete',
      'admin.i18n.percent',
      'admin.i18n.all_complete',
    ];
    for (const key of keys) {
      expect(zhCN[key], `zh-CN missing ${key}`).toBeTruthy();
      expect(en[key], `en missing ${key}`).toBeTruthy();
    }
  });
});

describe('Admin jobs/i18n API client', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('GETs jobs with the page query', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(jobs), { status: 200 }));
    await client.getAdminJobs({ page: 2 });
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/admin/jobs?page=2');
  });

  it('GETs jobs with the kind filter', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(jobs), { status: 200 }));
    await client.getAdminJobs({ page: 1, kind: 'media_gc' });
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/admin/jobs?page=1&kind=media_gc');
  });

  it('GETs the task overview', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(tasks), { status: 200 }));
    await expect(client.getAdminTasks()).resolves.toEqual(tasks);
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/admin/tasks');
  });

  it('starts a task with a CSRF-protected POST and returns the accepted task', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ authenticated: true, csrf_token: 'k' }), { status: 200 }));
    const accepted = { ...tasks.tasks[0]!, pending: true };
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(accepted), { status: 202 }));
    await expect(client.runAdminTask('retention')).resolves.toEqual(accepted);
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/admin/tasks/retention/run');
    expect(init?.method).toBe('POST');
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('k');
  });

  it('cancels a job with a CSRF-protected POST', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ authenticated: true, csrf_token: 'j' }), { status: 200 }));
    mockFetch.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await client.cancelAdminJob('12');
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/admin/jobs/12/cancel');
    expect(init?.method).toBe('POST');
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('j');
  });

  it('GETs the i18n report', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(i18n), { status: 200 }));
    await expect(client.getAdminI18n()).resolves.toEqual(i18n);
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/admin/i18n');
  });
});

describe('Admin jobs/i18n SPA routes', () => {
  it('resolves the job and i18n page routes', () => {
    expect(matchRoute('/admin/jobs', routes).route?.name).toBe('admin-jobs');
    expect(matchRoute('/admin/i18n', routes).route?.name).toBe('admin-i18n');
  });
});
