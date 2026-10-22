import { describe, it, expect, vi, beforeEach } from 'vitest';
import { ApiClient, ApiClientError } from '../lib/api';
import { matchRoute } from '../lib/router';
import { routes } from '../lib/router/routes';
import { deckActionKind } from '../lib/labels';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';

/**
 * SPA-side assertions for the deck sharing and cloning entry points.
 *
 * The server's shell rendering and role gating are pinned by
 * internal/web/page_routes_test.go; this file pins the frontend: the sharing management
 * route is taken over by the client at /decks/:id/sharing, and the clone action calls
 * POST /api/v1/decks/:id/clone through the typed client, explicitly declaring
 * Accept: application/json (the server uses that to return JSON instead of a 303
 * redirect).
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

/**
 * Leaving a share: when a sharee clicks the button on the card, it goes through
 * DELETE /api/v1/decks/:id/membership, a separate path from the owner's delete
 * (DELETE /api/v1/decks/:id).
 */
describe('deck action selection by caller role', () => {
  it('maps owner to delete, granted roles to leave, and anything else to nothing', () => {
    expect(deckActionKind('owner')).toBe('delete');
    expect(deckActionKind('editor')).toBe('leave');
    expect(deckActionKind('reader')).toBe('leave');
    // Unknown/missing roles show no action at all: better to drop a button than to render "Leave share" for a deck without an authorized row.
    expect(deckActionKind(undefined)).toBeNull();
    expect(deckActionKind(null)).toBeNull();
    expect(deckActionKind('admin')).toBeNull();
  });
});

describe('leaveDeck API client', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('DELETEs the membership endpoint with the session CSRF token', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'leave-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ left: true }), { status: 200 }));

    await expect(client.leaveDeck('7')).resolves.toEqual({ left: true });

    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/session');
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/decks/7/membership');
    expect(init?.method).toBe('DELETE');
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('leave-csrf');
  });

  it('surfaces a non-member 404 as an ApiClientError', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'leave-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(new Response('', { status: 404 }));

    const err = await client.leaveDeck('7').catch((e) => e);
    expect(err).toBeInstanceOf(ApiClientError);
    expect((err as ApiClientError).status).toBe(404);
  });
});

describe('deck leave catalog keys', () => {
  it('defines every leave/shared key in both locales', () => {
    const keys = [
      'decks.shared_badge',
      'decks.leave.action',
      'decks.leave.confirm_title',
      'decks.leave.confirm_desc',
      'decks.leave.confirm_btn',
      'decks.leave.cancel_btn',
      'decks.leave.failed',
    ];
    for (const key of keys) {
      expect(zhCN[key as keyof typeof zhCN], `zh-CN missing ${key}`).toBeTruthy();
      expect(en[key as keyof typeof en], `en missing ${key}`).toBeTruthy();
    }
  });
});
