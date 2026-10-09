import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render } from 'svelte/server';
import AdminHealthView from '../lib/views/admin/AdminHealthView.svelte';
import AdminAuditView from '../lib/views/admin/AdminAuditView.svelte';
import { ApiClient, ApiClientError } from '../lib/api';
import type { AdminSummary, AdminHealth, AdminAuditResponse } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';
import { matchRoute } from '../lib/router';
import { routes } from '../lib/router/routes';

const summary: AdminSummary = {
  users_total: 5,
  users_active: 4,
  decks: 3,
  notes: 12,
  cards: 20,
  due: 7,
  jobs_running: 1,
  jobs_failed: 2,
};

const health: AdminHealth = {
  database: 'ok',
  schema_version: 42,
  media_bytes: 2048,
  media_truncated: false,
  due: 7,
};

const audit: AdminAuditResponse = {
  rows: [
    { time: '2026-03-10 20:00:00', actor: { user_id: '1', username: 'owner' }, action: 'deck.grant', target: { type: 'deck', id: '7' }, detail: '{"role":"reader"}' },
    { time: '2026-03-10 21:00:00', actor: null, action: 'system.tick', target: null, detail: '' },
  ],
  actions: ['deck.grant', 'system.tick'],
  page: 1,
  pages: 1,
  total: 2,
  notice: '',
};

describe('Admin panel views render server-provided truth', () => {
  beforeEach(() => setLocale('zh-CN'));

  it('renders the admin navigation with every subpage link and no overview entry', () => {
    const { html } = render(AdminHealthView, { props: { initialLoading: false, initialData: health } });
    // 概览页已移除：导航里不再有指向 /admin 本身的链接。
    expect(html).not.toContain('href="/admin"');
    for (const href of ['/admin/users', '/admin/registration', '/admin/oidc', '/admin/smtp', '/admin/settings', '/admin/jobs', '/admin/audit', '/admin/health', '/admin/api-keys', '/admin/i18n']) {
      expect(html).toContain(`href="${href}"`);
    }
    expect(html).toContain('管理面板');
  });

  it('renders the health readouts including a formatted byte count', () => {
    const { html } = render(AdminHealthView, { props: { initialLoading: false, initialData: health } });
    expect(html).toContain('已连通');
    expect(html).toContain('42');
    expect(html).toContain('2.00 KiB');
  });

  it('renders audit rows with system actor and empty detail fallbacks', () => {
    const { html } = render(AdminAuditView, { props: { initialLoading: false, initialData: audit } });
    expect(html).toContain('deck.grant');
    expect(html).toContain('deck#7');
    expect(html).toContain('系统');
    expect(html).toContain('命中 2 条');
  });

  it('renders the forbidden state for a non-admin session', () => {
    const err = new ApiClientError('HTTP 403', { status: 403, code: 'forbidden' });
    const { html } = render(AdminHealthView, { props: { initialLoading: false, initialError: err } });
    expect(html).toContain('data-testid="admin-health-failed"');
    expect(html).toContain('需要管理员权限。');
    expect(html).not.toContain('data-testid="admin-health-rows"');
  });

  it('drops the overview keys from both catalogs', () => {
    for (const key of ['admin.nav.dashboard', 'admin.dashboard.heading', 'admin.dashboard.users_value']) {
      expect(zhCN[key]).toBeUndefined();
      expect(en[key]).toBeUndefined();
    }
  });

  it('covers every admin key used by the views in both locale catalogs', () => {
    const keys = [
      'admin.nav.heading',
      'admin.health.database.ok',
      'admin.health.media.truncated',
      'admin.audit.user_system',
      'admin.audit.detail_empty',
      'admin.audit.total_label',
      'admin.error.forbidden',
    ];
    for (const key of keys) {
      expect(zhCN[key], `zh-CN missing ${key}`).toBeTruthy();
      expect(en[key], `en missing ${key}`).toBeTruthy();
    }
  });
});

describe('Admin API client uses session-only JSON endpoints', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('GETs the summary with same-origin credentials and no CSRF header', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(summary), { status: 200 }));
    await expect(client.getAdminSummary()).resolves.toEqual(summary);
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/admin/summary');
    expect(mockFetch.mock.calls[0]?.[1]).toMatchObject({ credentials: 'same-origin' });
    expect(new Headers(mockFetch.mock.calls[0]?.[1]?.headers).get('X-CSRF-Token')).toBeNull();
    expect(new Headers(mockFetch.mock.calls[0]?.[1]?.headers).get('Authorization')).toBeNull();
  });

  it('builds the audit query string from the filter', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(audit), { status: 200 }));
    await client.getAdminAudit({ user: 'alice', action: 'deck.grant', from: '2026-03-01', page: 2 });
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/admin/audit?user=alice&action=deck.grant&from=2026-03-01&page=2');
  });

  it('omits empty filter fields', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(audit), { status: 200 }));
    await client.getAdminAudit({ user: '', action: '', target_type: 'deck' });
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/admin/audit?target_type=deck');
  });
});

describe('Admin SPA routes own the canonical panel paths', () => {
  it('resolves every admin page route', () => {
    // /admin 只是入口，挂载后换到 /admin/users（AdminIndexView）。
    expect(matchRoute('/admin', routes).route?.name).toBe('admin-index');
    expect(matchRoute('/admin/health', routes).route?.name).toBe('admin-health');
    expect(matchRoute('/admin/audit', routes).route?.name).toBe('admin-audit');
    // /admin 精确匹配，不得吞掉子页。
    expect(matchRoute('/admin/users', routes).route?.name).toBe('admin-users');
  });
});
