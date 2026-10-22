import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import { ApiClient } from '../lib/api';
import { matchRoute } from '../lib/router';
import UnsubscribeView from '../lib/views/UnsubscribeView.svelte';
import { setLocale } from '../lib/i18n';
import { en } from '../lib/i18n/locales/en';
import { zhCN } from '../lib/i18n/locales/zh-CN';

// Frontend contract for one-click unsubscribe: read/confirm use same-origin JSON, confirm carries the in-memory CSRF token automatically, stable codes map to localized text,
// canonical routes resolve, and view copy comes from the language pack. Server behaviour is covered by internal/web/unsubscribe_test.go.
describe('SPA one-click unsubscribe client flows', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('reads the named type without a CSRF token or bearer header', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ type: 'review_reminder' }), { status: 200 }));
    await expect(client.readUnsubscribe('tok /1')).resolves.toEqual({ type: 'review_reminder' });
    const [url, init] = mockFetch.mock.calls[0]!;
    expect(url).toBe('/api/v1/unsubscribe?token=tok%20%2F1');
    expect(init?.credentials).toBe('same-origin');
    expect(new Headers(init?.headers).get('Accept')).toBe('application/json');
    expect(new Headers(init?.headers).has('Authorization')).toBe(false);
    // The read endpoint is not a write request: it should not fetch a session CSRF first.
    expect(mockFetch).toHaveBeenCalledTimes(1);
  });

  it('loads session CSRF then confirms the token over JSON', async () => {
    mockFetch
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ authenticated: false, user: null, csrf_token: 'unsub-csrf' }), { status: 200 })
      )
      .mockResolvedValueOnce(new Response(JSON.stringify({ type: 'invite' }), { status: 200 }));

    await expect(client.confirmUnsubscribe('tok-1')).resolves.toEqual({ type: 'invite' });
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/session');
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/unsubscribe');
    expect(init?.method).toBe('POST');
    expect(init?.credentials).toBe('same-origin');
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('unsub-csrf');
    expect(JSON.parse(String(init?.body))).toEqual({ token: 'tok-1' });
  });

  it('keeps the stable server error code when the link is dead', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: { code: 'token_used', message: 'no' } }), { status: 400 })
    );
    await expect(client.readUnsubscribe('tok')).rejects.toMatchObject({ status: 400, code: 'token_used' });
  });
});

describe('SPA unsubscribe route and view', () => {
  it('serves /unsubscribe canonically', async () => {
    const { routes } = await import('../lib/router/routes');
    expect(matchRoute('/unsubscribe', routes).route?.name).toBe('unsubscribe');
    expect(matchRoute('/unsubscribe?token=abc', routes).query).toEqual({ token: 'abc' });
  });

  it('renders the confirm copy from the catalog', () => {
    setLocale('en');
    const { html } = render(UnsubscribeView, { props: {} });
    // onMount only runs in the browser: server-side rendering stops at the loading state, and every string comes from the language pack.
    expect(html).toContain('data-testid="unsubscribe-loading"');
    expect(html).toContain('Checking your unsubscribe link');
    expect(html).toContain('Manage email preferences');
  });

  it('pins the complete set of unsubscribe keys', () => {
    const keys = Object.keys(en).filter((key) => key.startsWith('unsubscribe.')).sort();
    expect(keys).toEqual([
      'unsubscribe.back',
      'unsubscribe.confirming',
      'unsubscribe.done',
      'unsubscribe.error.expired',
      'unsubscribe.error.invalid',
      'unsubscribe.error.used',
      'unsubscribe.heading',
      'unsubscribe.intro',
      'unsubscribe.loading',
      'unsubscribe.submit',
    ]);
  });

  it('reuses the shared notification type labels for the named type', () => {
    // The view renders the type name via settings.notifications.type.<type>, avoiding a second copy of the labels.
    expect(en['settings.notifications.type.review_reminder']).toBeTruthy();
    expect(zhCN['settings.notifications.type.review_reminder']).toBeTruthy();
  });
});
