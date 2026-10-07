import { describe, it, expect, vi, beforeEach } from 'vitest';
import { ApiClient, ApiClientError } from '../lib/api';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

/**
 * 分享同意制的前端断言（DESIGN.md §5）。
 *
 * 服务端语义由 internal/web/share_invite_test.go 钉住（分享后仍无授权、接受才生效、
 * 拒绝不留痕、策略在邀请发出前拦住）；这里钉三件事：
 *   1. 客户端打的端点与 CSRF 行为正确；
 *   2. /decks 的邀请卡片把「接受/拒绝」接到这两个端点上；
 *   3. 设置页的接收策略三档齐全，且保存后以服务端回读为准。
 */

const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8');

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
              deck_id: 7,
              deck_name: 'Biology',
              user_id: 2,
              username: 'me',
              role: 'reader',
              invited_by: 1,
              inviter_name: 'Alice',
              created_at: '2026-10-07T00:00:00Z',
              expires_at: '2026-11-06T00:00:00Z',
            },
          ],
          policy: 'whitelist',
          allow_list: [{ user_id: 1, username: 'Alice' }],
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

    await client.acceptShareInvite(7);

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

    await client.rejectShareInvite(7);

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

    // 第二个客户端：上一个已经拿到 CSRF（不再去打会话端点），这里要的正是"先取会话再提交"的路径。
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

describe('the decks page offers accept and decline', () => {
  const source = read('../lib/views/DecksView.svelte');

  it('loads pending invites and renders one accept/reject pair per invite', () => {
    expect(source).toContain('getShareInvites()');
    expect(source).toContain('acceptShareInvite(deckId)');
    expect(source).toContain('rejectShareInvite(deckId)');
    expect(source).toContain('data-testid="deck-invites"');
    expect(source).toContain('testId="deck-invite-accept-{invite.deck_id}"');
    expect(source).toContain('testId="deck-invite-reject-{invite.deck_id}"');
  });

  it('says that nothing changes until the recipient accepts', () => {
    expect(source).toContain("$t('decks.invites.hint')");
    expect(source).toContain("$t('decks.invites.accept')");
    expect(source).toContain("$t('decks.invites.reject')");
  });
});

describe('the settings page owns the receive policy', () => {
  const settings = read('../lib/views/SettingsView.svelte');
  const sharing = read('../lib/views/DeckSharingView.svelte');

  it('offers all three policies and saves on change', () => {
    expect(settings).toContain('data-testid="settings-share-policy"');
    for (const value of ["'anyone'", "'whitelist'", "'nobody'"]) {
      expect(settings).toContain(`value: ${value}`);
    }
    expect(settings).toContain('saveSharePolicy({ policy: next');
    expect(settings).toContain('data-testid="share-policy-allow-input"');
  });

  it('explains that the policy is checked before the invitation is sent', () => {
    expect(settings).toContain("$t('settings.share_policy.intro')");
  });

  it('the owner sees pending invitations separately from actual access', () => {
    expect(sharing).toContain('data-testid="sharing-pending-invites"');
    expect(sharing).toContain('pending_invites');
    expect(sharing).toContain("$t('deck.sharing.pending_hint')");
    // 对方拒收是可解释的结果，要单独告诉属主，而不是笼统的「操作失败」。
    expect(sharing).toContain("'recipient_refuses_shares'");
    expect(sharing).toContain("'deck.sharing.refused'");
  });
});
