import { describe, it, expect, vi, beforeEach } from 'vitest';
import { ApiClient } from '../lib/api';

/**
 * The media browser's client method: every filter is an optional query parameter, an absent filter sends nothing, and
 * paging passes the previous response's cursor back verbatim.
 */
describe('media library client method', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  function respond(body: unknown, status = 200): void {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(body), { status }));
  }

  it('sends deck, class, state, q and limit as query parameters', async () => {
    respond({ items: [], next_cursor: '' });
    await client.listMedia({ deck: 'deck /1', class: 'image', state: 'pending', q: 'sun set', limit: 50 });
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/media?deck=deck+%2F1&class=image&state=pending&q=sun+set&limit=50');
  });

  it('sends no filter parameter when the filters are empty and keeps the cursor for the next page', async () => {
    respond({ items: [], next_cursor: '' });
    await client.listMedia({ cursor: 'cursor tok/1' });
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/media?cursor=cursor+tok%2F1');

    respond({ items: [], next_cursor: '' });
    await client.listMedia({ deck: '', class: undefined, state: undefined, q: '', limit: 24 });
    expect(mockFetch.mock.calls[1]?.[0]).toBe('/api/v1/media?limit=24');
  });

  it('keeps the stable server error code of a rejected filter', async () => {
    respond({ error: { code: 'invalid_request', message: 'state is invalid' } }, 400);
    await expect(client.listMedia({ state: 'nope' as never })).rejects.toMatchObject({
      status: 400,
      code: 'invalid_request',
    });
  });
});
