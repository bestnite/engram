import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import DeckSettingsView from '../lib/views/DeckSettingsView.svelte';
import { ApiClient, ApiClientError } from '../lib/api';
import type { DeckSettings } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { matchRoute } from '../lib/router';
import { routes } from '../lib/router/routes';

const settings: DeckSettings = {
  deck_id: '7',
  deck_name: 'Biology',
  preset_id: '3',
  new_per_day: 5,
  reviews_per_day: 10,
  new_used: 2,
  review_used: 3,
  new_left: 3,
  review_left: 7,
  new_unlimited: false,
  review_unlimited: false,
};

describe('DeckSettingsView renders stored caps and today usage truthfully', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders the form, stored caps and today used/remaining', () => {
    const { html } = render(DeckSettingsView, {
      props: { initialLoading: false, initialSettings: settings },
    });
    expect(html).toContain('data-testid="deck-settings-form"');
    expect(html).toContain('data-testid="deck-settings-new-per-day"');
    expect(html).toContain('data-testid="deck-settings-reviews-per-day"');
    expect(html).toContain('data-testid="deck-settings-new-used"');
    expect(html).toContain('data-testid="deck-settings-new-left"');
    expect(html).toContain('data-testid="deck-settings-review-left"');
    // 新卡剩余 3、复习剩余 7（上限减已用），不是上限本身。
    expect(html).toContain('>3</dd>');
    expect(html).toContain('>7</dd>');
  });

  it('shows the unlimited label instead of 0 when a cap is unlimited', () => {
    const unlimited: DeckSettings = {
      ...settings,
      new_per_day: 0,
      reviews_per_day: 0,
      new_unlimited: true,
      review_unlimited: true,
      new_left: 0,
      review_left: 0,
    };
    const { html } = render(DeckSettingsView, {
      props: { initialLoading: false, initialSettings: unlimited },
    });
    expect(html).toContain('不限');
    expect(html).not.toContain('data-testid="deck-settings-failed"');
  });

  it('renders the loading state before any data arrives', () => {
    const { html } = render(DeckSettingsView, { props: { initialLoading: true } });
    expect(html).toContain('data-testid="deck-settings-loading"');
    expect(html).not.toContain('data-testid="deck-settings-form"');
  });

  it('renders a forbidden state for a non-owner', () => {
    const err = new ApiClientError('HTTP 403', { status: 403, code: 'forbidden' });
    const { html } = render(DeckSettingsView, {
      props: { initialLoading: false, initialError: err },
    });
    expect(html).toContain('data-testid="deck-settings-forbidden"');
    expect(html).not.toContain('data-testid="deck-settings-form"');
  });
});

describe('deck settings API client', () => {
  let mockFetch: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    mockFetch = vi.fn();
    client = new ApiClient({ fetch: mockFetch as unknown as typeof fetch });
  });

  it('GETs the session-only settings endpoint', async () => {
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(settings), { status: 200 }));
    await expect(client.getDeckSettings('7')).resolves.toEqual(settings);
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/decks/7/settings');
    expect(mockFetch.mock.calls[0]?.[1]).toMatchObject({ credentials: 'same-origin' });
  });

  it('PATCHes caps with the session CSRF token and keeps 0 verbatim', async () => {
    mockFetch.mockResolvedValueOnce(
      new Response(JSON.stringify({ authenticated: true, csrf_token: 'deck-csrf' }), { status: 200 })
    );
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(settings), { status: 200 }));
    await client.updateDeckSettings('7', { new_per_day: 0, reviews_per_day: 0 });
    expect(mockFetch.mock.calls[0]?.[0]).toBe('/api/v1/auth/session');
    const [url, init] = mockFetch.mock.calls[1]!;
    expect(url).toBe('/api/v1/decks/7/settings');
    expect(init?.method).toBe('PATCH');
    expect(init?.credentials).toBe('same-origin');
    expect(JSON.parse(String(init?.body))).toEqual({ new_per_day: 0, reviews_per_day: 0 });
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('deck-csrf');
  });
});

describe('deck settings SPA route owns the canonical path', () => {
  it('resolves /decks/:id/settings', () => {
    const match = matchRoute('/decks/42/settings', routes);
    expect(match.route?.name).toBe('deck-settings');
    expect(match.params).toEqual({ id: '42' });
    // 卡组详情本身不能被设置页的路径吃掉。
    expect(matchRoute('/decks/42', routes).route?.name).toBe('deck-detail');
  });
});
