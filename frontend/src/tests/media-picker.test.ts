import { describe, it, expect, vi, beforeEach } from 'vitest';
import { ApiClient, ApiClientError } from '../lib/api';
import { pickerErrorKeyFor } from '../lib/media-upload';
import { checkCatalogParity } from '../lib/i18n/parity';
import { en } from '../lib/i18n/locales/en';
import { zhCN } from '../lib/i18n/locales/zh-CN';

/**
 * The media picker's client side, without mounting: the two client calls it adds, the query string it builds, the shape
 * checks that keep a server response out of a field until it names exactly the chosen bytes, and the localized error
 * codes it maps. The UI behaviour is covered by media-picker.client.ts, which mounts the dialog.
 */

/** The sha of the fixture medium: a valid 64-character lowercase hex digest. */
const SHA = 'a'.repeat(64);

/** One library item in the response shape the server sends. */
function mediaItem(sha = SHA) {
  return { sha256: sha, mime: 'image/png', bytes: 1234, url: `/media/${sha}`, created_at: '2026-11-01T00:00:00Z' };
}

describe('media picker client calls', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  describe('listMediaForPicker', () => {
    it('lists the readable set when no filter is given', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify({ items: [mediaItem()], next_cursor: 'next-token' }), { status: 200 })
      );
      await expect(client.listMediaForPicker()).resolves.toEqual({ items: [mediaItem()], next_cursor: 'next-token' });
      const [url, init] = mockFetch.mock.calls[0]!;
      expect(url).toBe('/api/v1/media');
      expect(init?.credentials).toBe('same-origin');
      expect(new Headers(init?.headers).get('Accept')).toBe('application/json');
      expect(new Headers(init?.headers).has('Authorization')).toBe(false);
    });

    it('sends every filter as its own query parameter, in a stable order', async () => {
      mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ items: [], next_cursor: '' }), { status: 200 }));
      await client.listMediaForPicker('cursor tok/1', {
        deck: 'deck /1',
        class: 'image',
        state: 'referenced',
        q: 'map & plan',
        limit: 12,
      });
      const [url] = mockFetch.mock.calls[0]!;
      expect(url).toBe(
        '/api/v1/media?cursor=cursor+tok%2F1&deck=deck+%2F1&class=image&state=referenced&q=map+%26+plan&limit=12'
      );
    });

    it('omits absent filters instead of sending empty parameters', async () => {
      mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ items: [], next_cursor: '' }), { status: 200 }));
      await client.listMediaForPicker('', { deck: '', q: '' });
      expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/media');
    });

    it('preserves the stable server error code', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify({ error: { code: 'invalid_request', message: 'bad cursor' } }), { status: 400 })
      );
      await expect(client.listMediaForPicker('broken', { class: 'audio' })).rejects.toMatchObject({
        status: 400,
        code: 'invalid_request',
      });
    });
  });

  describe('registerDeckMediaName', () => {
    it('posts the sha to the deck name route with the session CSRF token', async () => {
      client.setCsrfToken('picker-csrf');
      mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ name: `${SHA}.png` }), { status: 200 }));
      await expect(client.registerDeckMediaName('deck /1', SHA)).resolves.toEqual({ name: `${SHA}.png` });
      const [url, init] = mockFetch.mock.calls[0]!;
      expect(url).toBe('/api/v1/decks/deck%20%2F1/media/names');
      expect(init?.method).toBe('POST');
      expect(init?.credentials).toBe('same-origin');
      expect(JSON.parse(String(init?.body))).toEqual({ sha256: SHA });
      expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('picker-csrf');
      expect(new Headers(init?.headers).get('Content-Type')).toBe('application/json');
    });

    it('fetches the session CSRF token first when the client holds none', async () => {
      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify({ authenticated: true, csrf_token: 'fresh-picker-csrf' }), { status: 200 })
      );
      mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ name: `${SHA}.png` }), { status: 200 }));
      await client.registerDeckMediaName('5', SHA);
      expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/session');
      expect(new Headers(mockFetch.mock.calls[1]?.[1]?.headers).get('X-CSRF-Token')).toBe('fresh-picker-csrf');
    });

    it.each([
      ['the canonical name', `${SHA}.png`],
      ['another extension for the same bytes', `${SHA}.mp3`],
    ])('accepts %s', async (_label, name) => {
      client.setCsrfToken('picker-csrf');
      mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ name }), { status: 200 }));
      await expect(client.registerDeckMediaName('5', SHA)).resolves.toEqual({ name });
    });

    it.each([
      ['a missing name', undefined],
      ['the name of other bytes', `${'b'.repeat(64)}.png`],
      ['a path instead of a name', `../${SHA}.png`],
      ['a name without an extension', SHA],
      ['a markup-breaking extension', `${SHA}.png" onerror="x`],
      ['a non-string name', 42],
    ])('rejects a response with %s', async (_label, name) => {
      client.setCsrfToken('picker-csrf');
      mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ name }), { status: 200 }));
      await expect(client.registerDeckMediaName('5', SHA)).rejects.toMatchObject({ code: 'invalid_response' });
    });

    it('preserves the stable server refusal code', async () => {
      client.setCsrfToken('picker-csrf');
      mockFetch.mockResolvedValueOnce(
        new Response(JSON.stringify({ error: { code: 'media_not_readable', message: 'no' } }), { status: 403 })
      );
      await expect(client.registerDeckMediaName('5', SHA)).rejects.toMatchObject({
        status: 403,
        code: 'media_not_readable',
      });
    });
  });

  describe('picker error copy', () => {
    it.each([
      ['media_not_found', 'media.picker.error_not_found'],
      ['media_not_readable', 'media.picker.error_not_readable'],
      ['invalid_response', 'media.picker.error_invalid_response'],
      ['anything_else', 'media.picker.error_failed'],
    ])('maps %s to a catalog key', (code, key) => {
      expect(pickerErrorKeyFor(new ApiClientError('failed', { status: 400, code }))).toBe(key);
    });

    it('maps a non-API error to the generic key', () => {
      expect(pickerErrorKeyFor(new Error('boom'))).toBe('media.picker.error_failed');
    });
  });
});

describe('media picker catalog keys', () => {
  // The keys the picker and the field toolbar read; both catalogs must define every one of them.
  const pickerKeys = [
    'media.picker.search',
    'media.picker.class.label',
    'media.picker.class.all',
    'media.picker.class.image',
    'media.picker.class.audio',
    'media.picker.class.video',
    'media.picker.class.font',
    'media.picker.class.script',
    'media.picker.class.style',
    'media.picker.class.other',
    'media.picker.scope.label',
    'media.picker.scope.all',
    'media.picker.scope.deck',
    'media.picker.no_results',
    'media.picker.more_failed',
    'media.picker.insert',
    'media.picker.close',
    'media.picker.error_failed',
    'media.picker.error_not_found',
    'media.picker.error_not_readable',
    'media.picker.error_invalid_response',
  ];

  // The pre-existing keys the picker reuses instead of duplicating their copy.
  const reusedKeys = ['media.open', 'media.heading', 'media.loading', 'media.failed', 'media.retry', 'media.empty', 'media.next'];

  it('defines every picker key in both catalogs with no key-set drift', () => {
    expect(checkCatalogParity(zhCN, en)).toMatchObject({ identical: true, missingInB: [], extraInB: [] });
    for (const key of [...pickerKeys, ...reusedKeys]) {
      expect(en[key], `en ${key}`).toBeTruthy();
      expect(zhCN[key], `zh-CN ${key}`).toBeTruthy();
    }
  });
});
