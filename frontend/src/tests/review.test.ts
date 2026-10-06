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

  it('submits a graded answer to the CSRF-protected grade endpoint without sending a rating', async () => {
    fetcher.mockResolvedValueOnce(new Response(JSON.stringify({ cards: [], remaining: 0 }), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    }));
    await client.submitGradedReview({ card_id: 11, expected_version: 4, deck: [2, 7], answer: 'Paris' });
    const [url, init] = fetcher.mock.calls[0]!;
    expect(url).toBe('/api/v1/review/grade');
    expect(init?.method).toBe('POST');
    expect(init?.credentials).toBe('same-origin');
    const headers = new Headers(init?.headers);
    expect(headers.get('X-CSRF-Token')).toBe('session-token');
    expect(headers.has('Authorization')).toBe(false);
    const body = JSON.parse(init?.body as string);
    expect(body).toEqual({ card_id: 11, expected_version: 4, deck: [2, 7], answer: 'Paris' });
    // 客户端绝不提交档位：rating 由服务端判分产生。
    expect(body).not.toHaveProperty('rating');
  });

  it('requests answer reveal through the same grade endpoint with the reveal action', async () => {
    fetcher.mockResolvedValueOnce(new Response(JSON.stringify({ revealed: true, card_id: 11, answer_html: '<b>x</b>' }), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    }));
    const result = await client.revealGradedAnswer({ card_id: 11, deck: [2, 7] });
    const [url, init] = fetcher.mock.calls[0]!;
    expect(url).toBe('/api/v1/review/grade');
    expect(JSON.parse(init?.body as string)).toEqual({ card_id: 11, deck: [2, 7], action: 'reveal' });
    expect(result.answer_html).toBe('<b>x</b>');
  });

  it('renders one card through the sanitized render endpoint with session CSRF', async () => {
    fetcher.mockResolvedValueOnce(new Response(JSON.stringify({
      card_id: 11, front_html: '<p>F</p>', back_html: '<p>B</p>', edit_href: '/decks/2/notes/7',
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }));

    const result = await client.renderReviewCard({ card_id: 11, deck: [2, 7] });
    const [url, init] = fetcher.mock.calls[0]!;
    expect(url).toBe('/api/v1/review/render');
    expect(init?.method).toBe('POST');
    expect(init?.credentials).toBe('same-origin');
    const headers = new Headers(init?.headers);
    expect(headers.get('X-CSRF-Token')).toBe('session-token');
    expect(headers.has('Authorization')).toBe(false);
    expect(JSON.parse(init?.body as string)).toEqual({ card_id: 11, deck: [2, 7] });
    expect(result.front_html).toBe('<p>F</p>');
    expect(result.edit_href).toBe('/decks/2/notes/7');
  });

  it('buries the current card through the session-CSRF bury endpoint', async () => {
    fetcher.mockResolvedValueOnce(new Response(JSON.stringify({ cards: [], remaining: 0 }), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    }));

    const result = await client.buryReview({ card_id: 11, deck: [2, 7] });
    const [url, init] = fetcher.mock.calls[0]!;
    expect(url).toBe('/api/v1/review/bury');
    expect(init?.method).toBe('POST');
    expect(init?.credentials).toBe('same-origin');
    const headers = new Headers(init?.headers);
    expect(headers.get('X-CSRF-Token')).toBe('session-token');
    expect(headers.has('Authorization')).toBe(false);
    expect(JSON.parse(init?.body as string)).toEqual({ card_id: 11, deck: [2, 7] });
    expect(result.remaining).toBe(0);
  });
});
