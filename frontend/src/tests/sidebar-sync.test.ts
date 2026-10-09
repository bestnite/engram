import { describe, it, expect, vi, beforeEach } from 'vitest';
import { get } from 'svelte/store';
import { ApiClient, ApiClientError } from '../lib/api';
import type { DataChangeKind } from '../lib/api';
import { handleDataChanged, refreshSidebarDecks, resetSidebarDecks, sidebarDecks } from '../lib/components/shell/sidebar';

/**
 * 侧边栏与写操作同步：在页面里删除、新建卡组不会改变路由，侧边栏必须靠接口层的改动通知更新，
 * 不能等用户刷新整个页面。
 */

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

describe('ApiClient change notifications', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;
  let seen: DataChangeKind[];

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
    client.setCsrfToken('csrf');
    seen = [];
    client.onDataChanged = (kind) => seen.push(kind);
  });

  it('notifies "decks" after a deck is deleted or created', async () => {
    mockFetch.mockResolvedValueOnce(json({ deleted: true }));
    await client.deleteDeck('d1');
    mockFetch.mockResolvedValueOnce(json({ id: 'd2', name: 'New' }, 201));
    await client.createDeck({ name: 'New', description: '', preset_id: '' });
    expect(seen).toEqual(['decks', 'decks']);
  });

  it('does not notify when the write fails', async () => {
    mockFetch.mockResolvedValueOnce(json({ error: { code: 'forbidden', message: 'no' } }, 403));
    await expect(client.deleteDeck('d1')).rejects.toBeInstanceOf(ApiClientError);
    expect(seen).toEqual([]);
  });

  it('tells card changes apart from review answers', async () => {
    mockFetch.mockResolvedValueOnce(json({ deleted: true, id: 'n1' }));
    await client.deleteNote('n1');
    mockFetch.mockResolvedValueOnce(json({ card: null }));
    await client.submitSelfReview({ card_id: 'c1', rating: 3, expected_version: 1 } as never);
    expect(seen).toEqual(['cards', 'review']);
  });
});

describe('sidebar sync on data changes', () => {
  function stubClient(names: string[][]) {
    let call = 0;
    return {
      getDecks: vi.fn(async () => ({
        decks: (names[Math.min(call++, names.length - 1)] ?? []).map((name, i) => ({
          id: `d${i}`, name, description: '', new_per_day: 20, reviews_per_day: 200, preset_id: '1', created_at: '', role: 'owner' as const,
        })),
      })),
      getDeckQueueCounts: vi.fn(async () => ({ decks: [] })),
    } as unknown as ApiClient;
  }

  beforeEach(() => resetSidebarDecks());

  it('refetches the deck list right away when decks change', async () => {
    const client = stubClient([['Alpha', 'Beta'], ['Alpha']]);
    await refreshSidebarDecks(client, true);
    expect(get(sidebarDecks).decks.map((d) => d.name)).toEqual(['Alpha', 'Beta']);
    handleDataChanged(client, 'decks');
    await vi.waitFor(() => expect(get(sidebarDecks).decks.map((d) => d.name)).toEqual(['Alpha']));
  });

  it('refetches once more when a forced refresh arrives while one is in flight', async () => {
    const client = stubClient([['Alpha', 'Beta'], ['Alpha']]);
    const first = refreshSidebarDecks(client, true);
    // 删除发生在第一次请求途中：它的结果可能是删除前的，必须再取一次。
    handleDataChanged(client, 'decks');
    await first;
    await vi.waitFor(() => expect(client.getDecks).toHaveBeenCalledTimes(2));
    await vi.waitFor(() => expect(get(sidebarDecks).decks.map((d) => d.name)).toEqual(['Alpha']));
  });

  it('only marks the list stale on review answers, then refetches on the next navigation', async () => {
    const client = stubClient([['Alpha'], ['Alpha']]);
    await refreshSidebarDecks(client, true);
    handleDataChanged(client, 'review');
    expect(client.getDecks).toHaveBeenCalledTimes(1);
    // 换页时的非强制刷新本该被 15 秒节流挡住，过期标记让它直接重取。
    await refreshSidebarDecks(client);
    expect(client.getDecks).toHaveBeenCalledTimes(2);
  });
});
