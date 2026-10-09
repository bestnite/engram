import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import NotificationPrefsView from '../lib/views/NotificationPrefsView.svelte';
import { ApiClient } from '../lib/api';
import type { NotificationPrefsResponse } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';
import { matchRoute } from '../lib/router';
import { routes } from '../lib/router/routes';

// 目录镜像（与 internal/mail/catalog.go 一致）：测试用它构造服务端响应，并断言两套语言包都覆盖这些标识。
const catalog = {
  security: ['password_reset', 'email_verification', 'new_device_login', 'credential_changed', 'account_status'],
  collab: ['deck_shared', 'deck_permission_changed', 'invite'],
  study: ['review_reminder', 'study_digest', 'optimize_done'],
  admin: ['job_failed', 'media_disk_alert'],
} as const;

const fixture: NotificationPrefsResponse = {
  groups: [
    { class: 'security', types: catalog.security.map((t) => ({ type: t, enabled: true, locked: true })) },
    { class: 'collab', types: catalog.collab.map((t) => ({ type: t, enabled: true, locked: false })) },
    { class: 'study', types: catalog.study.map((t) => ({ type: t, enabled: false, locked: false })) },
    { class: 'admin', types: catalog.admin.map((t) => ({ type: t, enabled: true, locked: false })) },
  ],
  reminder_hour: null,
  default_reminder_hour: 19,
  timezone: 'Asia/Shanghai',
};

/**
 * 取包含 testid 的那个标签本身（不再假定它是 <input>：复选框已换成组件库的 button 语义，
 * 状态用 data-state/aria-checked 表达而不是 checked 属性）。
 */
function controlTag(html: string, testid: string): string {
  const i = html.indexOf(`data-testid="${testid}"`);
  if (i < 0) return '';
  const start = html.lastIndexOf('<', i);
  const end = html.indexOf('>', i);
  return html.slice(start, end + 1);
}

describe('NotificationPrefsView renders the server-derived catalog truthfully', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders every class group from the response', () => {
    const { html } = render(NotificationPrefsView, { props: { initialLoading: false, initialData: fixture } });
    for (const cls of Object.keys(catalog)) {
      expect(html).toContain(`data-testid="notifications-group-${cls}"`);
    }
    expect(html).toContain('安全与事务');
    expect(html).toContain('协作与授权');
    expect(html).toContain('学习');
    expect(html).toContain('管理员通知');
  });

  it('locks class A as a disabled checkbox with no submittable input', () => {
    const { html } = render(NotificationPrefsView, { props: { initialLoading: false, initialData: fixture } });
    const locked = controlTag(html, 'notifications-locked-password_reset');
    expect(locked).toContain('disabled');
    expect(locked).toContain('data-state="checked"');
    expect(html).not.toContain('data-testid="notifications-type-password_reset"');
    expect(html).toContain('始终开启');
  });

  it('reflects the directory defaults: B on, C off', () => {
    const { html } = render(NotificationPrefsView, { props: { initialLoading: false, initialData: fixture } });
    expect(controlTag(html, 'notifications-type-deck_shared')).toContain('data-state="checked"');
    expect(controlTag(html, 'notifications-type-review_reminder')).toContain('data-state="unchecked"');
  });

  it('renders the localized reminder select with the site default and every hour', () => {
    const { html } = render(NotificationPrefsView, { props: { initialLoading: false, initialData: fixture } });
    expect(html).toContain('data-testid="notifications-reminder"');
    expect(html).toContain('站点默认（19:00）');
    expect(html).toContain('Asia/Shanghai');
  });

  it('renders the loading state before any data arrives', () => {
    const { html } = render(NotificationPrefsView, { props: { initialLoading: true } });
    expect(html).toContain('data-testid="notifications-loading"');
    expect(html).not.toContain('data-testid="notifications-submit"');
  });

  it('renders an unauthorized state for an anonymous session', () => {
    const err = Object.assign(new Error('HTTP 401'), { code: 'unauthorized', status: 401 });
    const { html } = render(NotificationPrefsView, { props: { initialLoading: false, initialError: err } });
    expect(html).toContain('data-testid="notifications-failed"');
    expect(html).toContain('data-testid="notifications-retry"');
    expect(html).not.toContain('data-testid="notifications-submit"');
  });

  it('renders only the groups the server sent: a non-admin response has no administrator section', () => {
    const nonAdmin: NotificationPrefsResponse = {
      ...fixture,
      groups: fixture.groups.filter((group) => group.class !== 'admin'),
    };
    const { html } = render(NotificationPrefsView, { props: { initialLoading: false, initialData: nonAdmin } });
    expect(html).toContain('data-testid="notifications-group-security"');
    expect(html).not.toContain('data-testid="notifications-group-admin"');
    expect(html).not.toContain('管理员通知');
  });

  it('covers every catalog identifier in both locale catalogs', () => {
    for (const [cls, types] of Object.entries(catalog)) {
      expect(zhCN[`settings.notifications.class.${cls}.heading`]).toBeTruthy();
      expect(en[`settings.notifications.class.${cls}.heading`]).toBeTruthy();
      for (const type of types) {
        expect(zhCN[`settings.notifications.type.${type}`]).toBeTruthy();
        expect(en[`settings.notifications.type.${type}`]).toBeTruthy();
      }
    }
  });

  // 视图把服务端的稳定错误 code 映射成本地化 key（actionErrorKey）；这里守住那组 key 存在，
  // 否则一次 403（forbidden_type）会退化成通用失败提示。
  it('localizes every rejection code the view maps, including the admin-only one', () => {
    const codes = ['unknown_type', 'class_locked', 'reminder_hour_invalid', 'invalid_request', 'forbidden_type'];
    for (const code of codes) {
      expect(zhCN[`settings.notifications.error.${code}`]).toBeTruthy();
      expect(en[`settings.notifications.error.${code}`]).toBeTruthy();
    }
  });
});

describe('Notification preferences API client uses session-only, CSRF-protected endpoints', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('GETs the notification preferences without a CSRF header', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(fixture), { status: 200 }));
    await expect(client.getNotificationPrefs()).resolves.toEqual(fixture);
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/settings/notifications');
    expect(mockFetch.mock.calls[0]?.[1]).toMatchObject({ credentials: 'same-origin' });
    expect(new Headers(mockFetch.mock.calls[0]?.[1]?.headers).get('X-CSRF-Token')).toBeNull();
  });

  it('PATCHes the preferences with the session CSRF token', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'prefs-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(fixture), { status: 200 }));
    const res = await client.updateNotificationPrefs({ choices: { review_reminder: true }, reminder_hour: 0 });
    expect(res).toEqual(fixture);
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/session');
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/settings/notifications');
    expect(init?.method).toBe('PATCH');
    expect(init?.credentials).toBe('same-origin');
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('prefs-csrf');
    expect(JSON.parse(String(init?.body))).toEqual({ choices: { review_reminder: true }, reminder_hour: 0 });
  });
});

describe('Notification SPA route owns the canonical path', () => {
  it('resolves /settings/notifications', () => {
    expect(matchRoute('/settings/notifications', routes).route?.name).toBe('notification-settings');
    expect(matchRoute('/settings', routes).route?.name).toBe('settings');
  });
});
