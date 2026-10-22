import { describe, it, expect, vi, beforeEach } from 'vitest';
import { ApiClient } from '../lib/api';

/**
 * Frontend contract for share consent: which endpoints the client calls and the CSRF
 * behaviour.
 *
 * The server semantics are pinned by internal/web/share_invite_test.go (still unauthorized
 * after sharing, takes effect only on acceptance, rejection leaves no trace, and the policy
 * blocks before the invite is sent).
 */

describe('share invite API client', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('GETs /api/v1/sharing/invites and returns pending invites with the policy', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          invites: [
            {
              deck_id: '7',
              deck_name: 'Biology',
              user_id: '2',
              username: 'me',
              role: 'reader',
              invited_by: '1',
              inviter_name: 'Alice',
              created_at: '2026-10-07T00:00:00Z',
              expires_at: '2026-11-06T00:00:00Z',
            },
          ],
          policy: 'whitelist',
          allow_list: [{ user_id: '1', username: 'Alice' }],
        }),
        { status: 200 }
      )
    );

    const res = await client.getShareInvites();

    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/sharing/invites');
    expect(res.invites[0]?.deck_name).toBe('Biology');
    expect(res.policy).toBe('whitelist');
    expect(res.allow_list[0]?.username).toBe('Alice');
  });

  it('accepts an invite with a fetched session CSRF token', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'invite-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ accepted: true }), { status: 200 }));

    await client.acceptShareInvite('7');

    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/session');
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/sharing/invites/7/accept');
    expect(init?.method).toBe('POST');
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('invite-csrf');
  });

  it('rejects an invite through its own endpoint', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'invite-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ rejected: true }), { status: 200 }));

    await client.rejectShareInvite('7');

    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/sharing/invites/7/reject');
    expect(init?.method).toBe('POST');
  });

  it('PUTs the share policy and surfaces user_not_found as a stable code', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'policy-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ policy: 'whitelist', allow_list: [] }), { status: 200 })
    );

    const res = await client.saveSharePolicy({ allow_usernames: ['Alice'] });

    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/settings/share-policy');
    expect(init?.method).toBe('PUT');
    expect(JSON.parse(String(init?.body))).toEqual({ allow_usernames: ['Alice'] });
    expect(res.policy).toBe('whitelist');

    // A second client: the previous one already holds a CSRF token (so it no longer hits the session endpoint), and what is needed here is exactly the "fetch session, then submit" path.
    const fresh = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
    mockFetch.mockReset();
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'policy-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: { code: 'user_not_found', message: 'No such user.' } }), { status: 400 })
    );
    await expect(fresh.saveSharePolicy({ allow_usernames: ['nobody'] })).rejects.toMatchObject({
      code: 'user_not_found',
    });
  });
});
