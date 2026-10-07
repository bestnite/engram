import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render } from 'svelte/server';
import AdminSettingsView from '../lib/views/admin/AdminSettingsView.svelte';
import AdminSMTPView from '../lib/views/admin/AdminSMTPView.svelte';
import AdminOIDCView from '../lib/views/admin/AdminOIDCView.svelte';
import { ApiClient } from '../lib/api';
import type { AdminSettingsResponse, AdminSMTPResponse, AdminOIDCResponse } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';
import { matchRoute } from '../lib/router';
import { routes } from '../lib/router/routes';

const settings: AdminSettingsResponse = {
  sections: [
    { name: 'general', rows: [
      { key: 'site.name', value: 'Engram', source: 'default', editable: true, sensitive: false, configured: false },
      { key: 'site.default_locale', value: 'zh-CN', source: 'db', editable: true, sensitive: false, configured: false },
    ] },
    { name: 'media', rows: [
      { key: 'media_max_bytes', value: '1048576', source: 'default', editable: true, sensitive: false, configured: false },
      { key: 'media_dir', value: '/data/media', source: 'env', editable: false, sensitive: false, configured: false },
      { key: 'media_usage', value: '2048', source: 'computed', editable: false, sensitive: false, configured: false, unit: 'bytes' },
    ] },
    { name: 'sensitive', rows: [
      { key: 'oidc_client_secret', value: '', source: 'db', editable: true, sensitive: true, configured: true },
    ] },
  ],
};

const smtp: AdminSMTPResponse = {
  host: 'smtp.example.com', host_source: 'db',
  port: '587', port_source: 'default',
  username: '', username_source: 'default',
  from: 'no-reply@example.com', from_source: 'db',
  tls_mode: 'starttls', password_configured: true, password_source: 'db',
  configured: true,
  outbox: { pending: 2, failed: 1, last_error: 'dial tcp: refused', last_attempts: 3 },
  admin_notify_ready: true,
};

const oidc: AdminOIDCResponse = {
  enabled: true, issuer: 'https://idp.example.com', client_id: 'engram', secret_configured: true,
  redirect_uri: 'https://app.example.com/auth/oidc/callback', scopes: 'openid email',
  claim_subject: 'sub', claim_email: 'email', claim_name: 'name', claim_email_verified: 'email_verified',
  identities: [
    { id: 4, provider: 'https://idp.example.com', subject: 'abc', email: 'u@example.com', username: 'alice', linked_at: '2026-03-10T12:00:00Z' },
  ],
};

describe('Admin config views render server-provided truth', () => {
  beforeEach(() => setLocale('zh-CN'));

  it('renders settings sections, sources and read-only readouts', () => {
    const { html } = render(AdminSettingsView, { props: { initialLoading: false, initialData: settings } });
    expect(html).toContain('data-testid="admin-settings-section-general"');
    expect(html).toContain('data-testid="admin-settings-value-site.name"');
    expect(html).toContain('data-testid="admin-settings-secret-oidc_client_secret"');
    expect(html).toContain('data-testid="admin-settings-row-media_usage"');
    expect(html).toContain('2.00 KiB');
    expect(html).toContain('已配置');
  });

  it('renders the SMTP form with source labels and outbox readouts', () => {
    const { html } = render(AdminSMTPView, { props: { initialLoading: false, initialData: smtp } });
    expect(html).toContain('data-testid="admin-smtp-host"');
    expect(html).toContain('data-testid="admin-smtp-tls"');
    expect(html).toContain('smtp.example.com');
    expect(html).toContain('dial tcp: refused');
    expect(html).toContain('STARTTLS');
  });

  it('renders the OIDC form, redirect URI and identity list', () => {
    const { html } = render(AdminOIDCView, { props: { initialLoading: false, initialData: oidc } });
    expect(html).toContain('data-testid="admin-oidc-redirect-uri"');
    expect(html).toContain('https://app.example.com/auth/oidc/callback');
    expect(html).toContain('data-testid="admin-oidc-identity-4"');
    expect(html).toContain('alice');
    expect(html).toContain('data-testid="admin-oidc-unlink-4"');
  });
});

describe('Admin config API client', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('GETs settings with same-origin credentials', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(settings), { status: 200 }));
    await expect(client.getAdminSettings()).resolves.toEqual(settings);
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/admin/settings');
    expect(new Headers(mockFetch.mock.calls[0]?.[1]?.headers).get('Authorization')).toBeNull();
  });

  it('POSTs settings values wrapped in a values map with CSRF', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ authenticated: true, csrf_token: 'cfg' }), { status: 200 }));
    mockFetch.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await client.saveAdminSettings({ 'site.name': 'X' });
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/admin/settings');
    expect(JSON.parse(String(init?.body))).toEqual({ values: { 'site.name': 'X' } });
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('cfg');
  });

  it('tests the SMTP connection', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ authenticated: true, csrf_token: 'c' }), { status: 200 }));
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ ok: false, code: 'failed', message: 'boom' }), { status: 200 }));
    const res = await client.testAdminSMTP({ host: 'h' });
    expect(res).toEqual({ ok: false, code: 'failed', message: 'boom' });
    expect(mockFetch.mock.calls[1]?.[0]).toBe('/api/v1/admin/smtp/test');
  });

  it('unlinks an OIDC identity', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ authenticated: true, csrf_token: 'c' }), { status: 200 }));
    mockFetch.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await client.unlinkAdminOIDCIdentity(4);
    expect(mockFetch.mock.calls[1]?.[0]).toBe('/api/v1/admin/oidc/identities/4/unlink');
  });
});

describe('Admin config SPA routes and catalog coverage', () => {
  it('resolves the config page routes', () => {
    expect(matchRoute('/admin/settings', routes).route?.name).toBe('admin-settings');
    expect(matchRoute('/admin/smtp', routes).route?.name).toBe('admin-smtp');
    expect(matchRoute('/admin/oidc', routes).route?.name).toBe('admin-oidc');
  });

  it('covers every config key used by the views in both catalogs', () => {
    const keys = [
      'admin.settings.heading',
      'admin.settings.source.computed',
      'admin.settings.setting.media_user_quota_bytes',
      'admin.smtp.tls.starttls',
      'admin.smtp.outbox.last_error',
      'admin.oidc.redirect_uri.hint',
      'admin.oidc.notice.invalid_issuer',
    ];
    for (const key of keys) {
      expect(zhCN[key], `zh-CN missing ${key}`).toBeTruthy();
      expect(en[key], `en missing ${key}`).toBeTruthy();
    }
  });
});
