import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import { ApiClient } from '../lib/api';
import { matchRoute } from '../lib/router';
import UnsubscribeView from '../lib/views/UnsubscribeView.svelte';
import { setLocale } from '../lib/i18n';
import { en } from '../lib/i18n/locales/en';
import { zhCN } from '../lib/i18n/locales/zh-CN';

// 一键退订前端契约：读/确认走同源 JSON、确认自动带内存 CSRF、稳定 code 映射本地化、
// 规范路由可解析、视图文案来自语言包。服务端行为由 internal/web/unsubscribe_test.go 覆盖。
describe('SPA one-click unsubscribe client flows', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('reads the named type without a CSRF token or bearer header', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ type: 'review_reminder' }), { status: 200 }));
    await expect(client.readUnsubscribe('tok /1')).resolves.toEqual({ type: 'review_reminder' });
    const [url, init] = mockFetch.mock.calls[0]!;
    expect(url).toBe('/api/v1/unsubscribe?token=tok%20%2F1');
    expect(init?.credentials).toBe('same-origin');
    expect(new Headers(init?.headers).get('Accept')).toBe('application/json');
    expect(new Headers(init?.headers).has('Authorization')).toBe(false);
    // 读端点不是写请求：不应先取会话 CSRF。
    expect(mockFetch).toHaveBeenCalledTimes(1);
  });

  it('loads session CSRF then confirms the token over JSON', async () => {
    mockFetch
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ authenticated: false, user: null, csrf_token: 'unsub-csrf' }), { status: 200 })
      )
      .mockResolvedValueOnce(new Response(JSON.stringify({ type: 'invite' }), { status: 200 }));

    await expect(client.confirmUnsubscribe('tok-1')).resolves.toEqual({ type: 'invite' });
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/session');
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/unsubscribe');
    expect(init?.method).toBe('POST');
    expect(init?.credentials).toBe('same-origin');
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('unsub-csrf');
    expect(JSON.parse(String(init?.body))).toEqual({ token: 'tok-1' });
  });

  it('keeps the stable server error code when the link is dead', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: { code: 'token_used', message: 'no' } }), { status: 400 })
    );
    await expect(client.readUnsubscribe('tok')).rejects.toMatchObject({ status: 400, code: 'token_used' });
  });
});

describe('SPA unsubscribe route and view', () => {
  it('serves /unsubscribe canonically and keeps the /spa alias', async () => {
    const { routes } = await import('../lib/router/routes');
    expect(matchRoute('/unsubscribe', routes).route?.name).toBe('unsubscribe');
    expect(matchRoute('/unsubscribe?token=abc', routes).query).toEqual({ token: 'abc' });
    expect(matchRoute('/spa/unsubscribe', routes).route?.name).toBe('unsubscribe-spa');
  });

  it('renders the confirm copy from the catalog', () => {
    setLocale('en');
    const { html } = render(UnsubscribeView, { props: {} });
    // onMount 只在浏览器运行：服务端渲染停在 loading 状态，且文案全部来自语言包。
    expect(html).toContain('data-testid="unsubscribe-loading"');
    expect(html).toContain('Checking your unsubscribe link');
    expect(html).toContain('Manage email preferences');
  });

  it('provides identical unsubscribe key sets in both catalogs', () => {
    const keys = Object.keys(en).filter((key) => key.startsWith('unsubscribe.')).sort();
    expect(keys).toEqual([
      'unsubscribe.back',
      'unsubscribe.confirming',
      'unsubscribe.done',
      'unsubscribe.error.expired',
      'unsubscribe.error.invalid',
      'unsubscribe.error.used',
      'unsubscribe.heading',
      'unsubscribe.intro',
      'unsubscribe.loading',
      'unsubscribe.submit',
    ]);
    expect(Object.keys(zhCN).filter((key) => key.startsWith('unsubscribe.')).sort()).toEqual(keys);
  });

  it('reuses the shared notification type labels for the named type', () => {
    // 视图用 settings.notifications.type.<type> 渲染类型名，避免重复定义一份标签。
    expect(en['settings.notifications.type.review_reminder']).toBeTruthy();
    expect(zhCN['settings.notifications.type.review_reminder']).toBeTruthy();
  });
});
