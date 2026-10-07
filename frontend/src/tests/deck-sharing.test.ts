import { describe, it, expect, vi, beforeEach } from 'vitest';
import { ApiClient, ApiClientError } from '../lib/api';
import { matchRoute } from '../lib/router';
import { routes } from '../lib/router/routes';

/**
 * 卡组共享与克隆入口的 SPA 侧断言。
 *
 * 服务端切流由 internal/web/spa_sharing_cutover_test.go 钉住；这里钉前端：
 * 共享管理路由由客户端接管 /decks/:id/sharing，克隆动作通过类型化客户端打
 * POST /api/v1/decks/:id/clone，并显式声明 Accept: application/json（服务端据此返回
 * JSON 而不是 303 重定向）。
 */

describe('deck sharing SPA route', () => {
  it('resolves /decks/:id/sharing to the sharing view', () => {
    const match = matchRoute('/decks/42/sharing', routes);
    expect(match.route?.name).toBe('deck-sharing');
    expect(match.params).toEqual({ id: '42' });
  });
});

describe('cloneDeck API client', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('POSTs the clone endpoint with session CSRF and Accept: application/json', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'clone-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ id: 42, name: 'Copy' }), { status: 201 }));

    await expect(client.cloneDeck('7')).resolves.toEqual({ id: 42, name: 'Copy' });

    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/session');
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/decks/7/clone');
    expect(init?.method).toBe('POST');
    expect(init?.credentials).toBe('same-origin');
    const headers = new Headers(init?.headers);
    expect(headers.get('Accept')).toBe('application/json');
    expect(headers.get('X-CSRF-Token')).toBe('clone-csrf');
    expect(JSON.parse(String(init?.body))).toEqual({});
  });

  it('surfaces a non-reader 403 as a forbidden ApiClientError', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'clone-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(new Response('', { status: 403 }));

    const err = await client.cloneDeck('7').catch((e) => e);
    expect(err).toBeInstanceOf(ApiClientError);
    expect((err as ApiClientError).isForbidden).toBe(true);
  });
});
