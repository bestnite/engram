import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { ApiClient, ApiClientError } from '../lib/api';

/**
 * Self-healing for mid-session expiry and stale CSRF (see the comment on ApiClient.request).
 *
 * Before the fix: a 401 was mapped to a single error code and handed to the view to display
 * as one sentence; 403 csrf_failed worked the same way. So once the session expired, the UI
 * kept showing "signed in", every action failed, and only a manual refresh recovered it.
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
    // With no session there is nothing to retry: only one request should ever be sent (not "refetch session + retry").
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('refreshes the session and retries exactly once when the CSRF token is stale', async () => {
    const fetchMock = vi
      .fn()
      // First: the write request carrying a stale token is rejected
      .mockResolvedValueOnce(jsonResponse(403, { error: { code: 'csrf_failed' } }))
      // Second: refetch the session — the value the server expects now (here the anonymous double-submit value)
      .mockResolvedValueOnce(jsonResponse(200, { authenticated: false, csrf_token: 'fresh-token' }))
      // Third: retry the same request with the new token, which succeeds
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
    // The retry must carry the new value rather than keep using the stale one
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
    // Original request + session fetch + one retry
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
