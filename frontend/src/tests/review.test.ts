import { describe, it, expect, vi, beforeEach } from 'vitest';
import { ApiClient } from '../lib/api';

describe('review submission API', () => {
  let fetcher: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    fetcher = vi.fn();
    client = new ApiClient({ fetch: fetcher as unknown as typeof fetch });
    client.setCsrfToken('session-token');
  });

  it('sends expected version, deck scope, session CSRF, and no bearer credential', async () => {
    fetcher.mockResolvedValueOnce(new Response(JSON.stringify({ cards: [], remaining: 0 }), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    }));
    await client.submitSelfReview({ card_id: 11, rating: 3, expected_version: 4, deck: [2, 7] });
    const [url, init] = fetcher.mock.calls[0]!;
    expect(url).toBe('/api/v1/review/answer');
    expect(init?.method).toBe('POST');
    expect(init?.credentials).toBe('same-origin');
    const headers = new Headers(init?.headers);
    expect(headers.get('X-CSRF-Token')).toBe('session-token');
    expect(headers.has('Authorization')).toBe(false);
    expect(JSON.parse(init?.body as string)).toEqual({ card_id: 11, rating: 3, expected_version: 4, deck: [2, 7] });
  });
});
