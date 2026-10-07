import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render } from 'svelte/server';
import AdminUsersView from '../lib/views/admin/AdminUsersView.svelte';
import AdminRegistrationView from '../lib/views/admin/AdminRegistrationView.svelte';
import AdminAPIKeysView from '../lib/views/admin/AdminAPIKeysView.svelte';
import { ApiClient } from '../lib/api';
import type { AdminUsersResponse, AdminRegistrationResponse, AdminAPIKeysResponse } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';
import { matchRoute } from '../lib/router';
import { routes } from '../lib/router/routes';

const users: AdminUsersResponse = {
  users: [
    { id: 1, username: 'owner', email: 'owner@example.com', display_name: 'Owner', role: 'admin', status: 'active', decks: 2, cards: 5, reviews: 9, is_self: true },
    { id: 2, username: 'alice', email: 'alice@example.com', display_name: 'Alice', role: 'user', status: 'disabled', decks: 1, cards: 1, reviews: 0, is_self: false },
  ],
  page: 1,
  pages: 2,
  total: 7,
  query: '',
};

const registration: AdminRegistrationResponse = {
  policy: 'invite',
  email_domains: 'example.com',
  invites: [
    { id: 3, token: 'tok-abc', link: '/register?invite=tok-abc', email: 'invitee@example.com', role: 'user', status: 'active', created_at: '2026-03-10 20:00', expires_at: '2026-03-17 20:00', used_at: '', used_by: '' },
  ],
};

const apiKeys: AdminAPIKeysResponse = {
  keys: [
    { id: 9, user_id: 1, owner: 'owner', name: 'ci', prefix: 'fcard_ab12cd34', scopes: ['read', 'write'], last_used_at: null, expires_at: null, state: 'active' },
  ],
  page: 1,
  pages: 1,
  total: 1,
};

describe('Admin management views render server-provided truth', () => {
  beforeEach(() => setLocale('zh-CN'));

  it('renders the user list, roles, statuses and usage counts', () => {
    const { html } = render(AdminUsersView, { props: { initialLoading: false, initialData: users } });
    expect(html).toContain('data-testid="admin-users-row-1"');
    expect(html).toContain('alice@example.com');
    expect(html).toContain('管理员');
    expect(html).toContain('已禁用');
    expect(html).toContain('data-testid="admin-users-create"');
  });

  it('renders the registration policy, domains and invite table', () => {
    const { html } = render(AdminRegistrationView, { props: { initialLoading: false, initialData: registration } });
    expect(html).toContain('data-testid="admin-registration-policy-invite"');
    expect(html).toContain('invitee@example.com');
    expect(html).toContain('可用');
    expect(html).toContain('data-testid="admin-registration-invite-create"');
  });

  it('renders the API key metadata without any plaintext', () => {
    const { html } = render(AdminAPIKeysView, { props: { initialLoading: false, initialData: apiKeys } });
    expect(html).toContain('fcard_ab12cd34');
    expect(html).toContain('read, write');
    expect(html).toContain('从未');
    expect(html).toContain('data-testid="admin-api-keys-revoke-9"');
  });

  it('covers every admin management key used by the views in both catalogs', () => {
    const keys = [
      'admin.users.heading',
      'admin.users.col.reviews',
      'admin.users.notice.last_admin',
      'admin.registration.policy.invite',
      'admin.registration.status.used',
      'admin.registration.notice.mail_unconfigured',
      'admin.keys.state.revoked',
      'admin.keys.last_used_never',
      'admin.common.copy',
    ];
    for (const key of keys) {
      expect(zhCN[key], `zh-CN missing ${key}`).toBeTruthy();
      expect(en[key], `en missing ${key}`).toBeTruthy();
    }
  });
});

describe('Admin management API client', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('GETs users with the search query', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(users), { status: 200 }));
    await client.getAdminUsers({ q: 'ali', page: 2 });
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/admin/users?q=ali&page=2');
  });

  it('POSTs a create request with the session CSRF token', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ authenticated: true, csrf_token: 'mgmt-csrf' }), { status: 200 }));
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(users.users[0]), { status: 201 }));
    await client.createAdminUser({ username: 'x', email: 'x@example.com', display_name: '', password: 'Sup3rSecret!', role: 'user' });
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/admin/users');
    expect(init?.method).toBe('POST');
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('mgmt-csrf');
  });

  it('sends confirm=true for dangerous user actions', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ authenticated: true, csrf_token: 'c' }), { status: 200 }));
    mockFetch.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await client.deleteAdminUser(5);
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/admin/users/5/delete');
    expect(JSON.parse(String(init?.body))).toEqual({ confirm: true });
  });

  it('GETs the registration payload and revokes an invite', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(registration), { status: 200 }));
    await expect(client.getAdminRegistration()).resolves.toEqual(registration);
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/admin/registration');

    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ authenticated: true, csrf_token: 'c' }), { status: 200 }));
    mockFetch.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await client.revokeAdminInvite(3);
    expect(mockFetch.mock.calls[2]?.[0]).toBe('/api/v1/admin/invites/3/revoke');
  });
});

describe('Admin management SPA routes', () => {
  it('resolves the management page routes', () => {
    expect(matchRoute('/admin/users', routes).route?.name).toBe('admin-users');
    expect(matchRoute('/admin/registration', routes).route?.name).toBe('admin-registration');
    expect(matchRoute('/admin/api-keys', routes).route?.name).toBe('admin-api-keys');
  });
});
