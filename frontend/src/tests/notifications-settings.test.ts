import { describe, it, expect, vi, beforeEach } from 'vitest';
import { readFileSync } from 'node:fs';
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
    expect(html).toContain('学习与运营');
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
    // 小时选项现在是组件库下拉的 options 数据：弹层只在展开时渲染（bits-ui Portal），
    // 不在首屏 HTML 里。因此这条断言改到源码上——它保证选项仍由 hours 逐项生成。
    const src = readFileSync(
      new URL('../lib/views/NotificationPrefsView.svelte', import.meta.url),
      'utf8'
    );
    expect(src).toContain('...hours.map((hour) => ({ value: String(hour), label: `${hour}:00` }))');
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
  it('resolves /settings/notifications and keeps the /spa alias', () => {
    expect(matchRoute('/settings/notifications', routes).route?.name).toBe('notification-settings');
    expect(matchRoute('/spa/settings/notifications', routes).route?.name).toBe('notification-settings-spa');
    expect(matchRoute('/settings', routes).route?.name).toBe('settings');
  });
});
