import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render } from 'svelte/server';
import AdminJobsView from '../lib/views/admin/AdminJobsView.svelte';
import AdminI18nView from '../lib/views/admin/AdminI18nView.svelte';
import { ApiClient } from '../lib/api';
import type { AdminJobsResponse, AdminI18nResponse } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';
import { matchRoute } from '../lib/router';
import { routes } from '../lib/router/routes';

const jobs: AdminJobsResponse = {
  jobs: [
    { id: 12, kind: 'optimize', status: 'running', stage: 'training', created_at: '2026-03-10 20:00:00', started_at: '2026-03-10 20:00:01', finished_at: '', log_tail: 'epoch 3', error: '', can_cancel: true },
    { id: 11, kind: 'optimize', status: 'failed', stage: null, created_at: '2026-03-09 20:00:00', started_at: '2026-03-09 20:00:01', finished_at: '2026-03-09 20:00:05', log_tail: '', error: 'killed', can_cancel: false },
  ],
  page: 1,
  pages: 1,
  total: 2,
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

  it('cancels a job with a CSRF-protected POST', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ authenticated: true, csrf_token: 'j' }), { status: 200 }));
    mockFetch.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await client.cancelAdminJob(12);
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
