import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { ApiClient, ApiClientError } from '../lib/api';

/**
 * 会话中途失效与陈旧 CSRF 的自愈（见 ApiClient.request 的注释）。
 *
 * 修复前：401 只被映射成一个错误码、交给视图显示一句话；403 csrf_failed 也是。于是会话一旦
 * 失效，界面继续显示「已登录」，每个操作都失败，必须手动刷新才会恢复。
 */

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('session recovery in the API client', () => {
  let originalFetch: typeof fetch;

  beforeEach(() => {
    originalFetch = globalThis.fetch;
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it('notifies the shell once on a 401 so it can clear state and send the user to sign in', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(401, { error: { code: 'unauthorized' } }));
    const client = new ApiClient({ fetch: fetchMock as unknown as typeof fetch });
    const onUnauthorized = vi.fn();
    client.onUnauthorized = onUnauthorized;

    await expect(client.request('/api/v1/decks')).rejects.toBeInstanceOf(ApiClientError);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it('treats csrf_no_session like a lost session rather than a retryable token mismatch', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(403, { error: { code: 'csrf_no_session' } }));
    const client = new ApiClient({ fetch: fetchMock as unknown as typeof fetch });
    const onUnauthorized = vi.fn();
    client.onUnauthorized = onUnauthorized;

    await expect(client.request('/api/v1/profile', { method: 'PATCH', body: '{}' })).rejects.toBeInstanceOf(
      ApiClientError
    );
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    // 会话没了就没有可重试的东西：只应发过一次请求（不是「重取会话 + 重试」）。
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('refreshes the session and retries exactly once when the CSRF token is stale', async () => {
    const fetchMock = vi
      .fn()
      // 第一次：带着陈旧 token 的写请求被拒
      .mockResolvedValueOnce(jsonResponse(403, { error: { code: 'csrf_failed' } }))
      // 第二次：重新取会话，服务端此刻期望的值（这里是匿名时的双提交值）
      .mockResolvedValueOnce(jsonResponse(200, { authenticated: false, csrf_token: 'fresh-token' }))
      // 第三次：用新 token 重试同一个请求，成功
      .mockResolvedValueOnce(jsonResponse(200, { ok: true }));
    const client = new ApiClient({ fetch: fetchMock as unknown as typeof fetch });
    client.setCsrfToken('stale-token');

    const result = await client.request<{ ok: boolean }>('/api/v1/auth/forgot-password', {
      method: 'POST',
      body: '{"email":"a@b.c"}',
    });

    expect(result).toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(String(fetchMock.mock.calls[1]![0])).toContain('/api/v1/auth/session');
    // 重试必须带上新值，而不是继续用陈旧的
    const retryInit = fetchMock.mock.calls[2]![1] as RequestInit;
    expect((retryInit.headers as Headers).get('X-CSRF-Token')).toBe('fresh-token');
  });

  it('does not retry a second time, so a persistent CSRF failure cannot loop', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse(403, { error: { code: 'csrf_failed' } }))
      .mockResolvedValueOnce(jsonResponse(403, { error: { code: 'csrf_failed' } }))
      .mockResolvedValueOnce(jsonResponse(200, { authenticated: false, csrf_token: 'fresh-token' }))
      .mockResolvedValueOnce(jsonResponse(403, { error: { code: 'csrf_failed' } }));
    const client = new ApiClient({ fetch: fetchMock as unknown as typeof fetch });

    await expect(
      client.request('/api/v1/auth/forgot-password', { method: 'POST', body: '{}' })
    ).rejects.toMatchObject({ code: 'csrf_failed' });
    // 原始请求 + 取会话 + 一次重试
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it('leaves successful requests alone', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { ok: true }));
    const client = new ApiClient({ fetch: fetchMock as unknown as typeof fetch });
    const onUnauthorized = vi.fn();
    client.onUnauthorized = onUnauthorized;

    await client.request('/api/v1/decks');
    expect(onUnauthorized).not.toHaveBeenCalled();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
