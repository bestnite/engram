import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import { ApiClient } from '../lib/api';
import { matchRoute } from '../lib/router';
import ShareBrowseView from '../lib/views/ShareBrowseView.svelte';
import { setLocale } from '../lib/i18n';
import { en } from '../lib/i18n/locales/en';
import { zhCN } from '../lib/i18n/locales/zh-CN';

// 公开只读分享浏览前端契约：同源 JSON、清洗后 HTML 直接注入、路由可解析、文案来自语言包。
// 服务端可达性、口令门禁与清洗由 internal/web/spa_share_test.go 覆盖。
describe('SPA public share browser client flows', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('reads the share payload without a CSRF token', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ deck_name: 'D', password_required: false, notes: [] }), { status: 200 })
    );

    const res = await client.getShare('tok-123');

    expect(res).toEqual({ deck_name: 'D', password_required: false, notes: [] });
    const [url, init] = mockFetch.mock.calls[0]!;
    expect(url).toBe('/api/v1/share/tok-123');
    expect(init?.credentials).toBe('same-origin');
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBeNull();
  });

  it('posts the password to the unlock endpoint', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(
        JSON.stringify({ deck_name: 'D', password_required: false, notes: [{ front_html: 'F', back_html: 'B' }] }),
        { status: 200 }
      )
    );

    const res = await client.unlockShare('tok-123', 'letmein99');

    expect(res.notes).toHaveLength(1);
    const [url, init] = mockFetch.mock.calls[0]!;
    expect(url).toBe('/api/v1/share/tok-123/unlock');
    expect(init?.method).toBe('POST');
    expect(JSON.parse(String(init?.body))).toEqual({ password: 'letmein99' });
  });

  it('keeps the server stable code for a rejected password', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: { code: 'share_password_invalid', message: 'nope' } }), { status: 401 })
    );
    await expect(client.unlockShare('tok', 'bad')).rejects.toMatchObject({
      status: 401,
      code: 'share_password_invalid',
    });
  });

  it('resolves the share browse route and extracts the token param', () => {
    const match = matchRoute('/s/abc-DEF_123');
    expect(match.route?.name).toBe('share-browse');
    expect(match.params.token).toBe('abc-DEF_123');
  });

  it('renders the localized loading state from the catalog', () => {
    setLocale('en');
    const { html } = render(ShareBrowseView);
    expect(html).toContain(en['share.browse.heading']);
    expect(html).toContain(en['share.browse.loading']);
  });

  it('has both catalogues covering every share.* key used by the view', () => {
    const shareKeys = Object.keys(en).filter((k) => k.startsWith('share.'));
    expect(shareKeys.length).toBeGreaterThan(10);
    for (const key of shareKeys) {
      expect(zhCN[key]).toBeTruthy();
    }
  });
});
