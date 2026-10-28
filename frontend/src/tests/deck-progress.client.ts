import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DecksView from '../lib/views/DecksView.svelte';
import DeckDetailView from '../lib/views/DeckDetailView.svelte';
import HomeView from '../lib/views/HomeView.svelte';
import { apiClient } from '../lib/api';
import type { Deck, DeckQueueCountsResponse, StatsSummary } from '../lib/api';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale } from '../lib/i18n';
import { setCardTypes } from '../lib/card-types';

/**
 * Deck progress: the deck list, the home deck list and the deck detail header show how many cards of the deck
 * the user has studied and how many are left, taken from GET /api/v1/decks/queue-counts.
 * The numbers are supplementary, so a failed counts request hides them instead of failing the page.
 */
describe('deck progress display', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  function deck(id: string, name: string): Deck {
    return {
      id,
      name,
      description: '',
      new_per_day: 20,
      reviews_per_day: 200,
      preset_id: 'p1',
      created_at: '',
      role: 'owner',
    };
  }

  const counts: DeckQueueCountsResponse = {
    decks: [
      { deck_id: 'd1', new_count: 5, review_count: 3, studied_count: 120, remaining_count: 380 },
      { deck_id: 'd2', new_count: 0, review_count: 0, studied_count: 0, remaining_count: 0 },
    ],
  };

  function text(testId: string): string | null {
    const el = target.querySelector(`[data-testid="${testId}"]`);
    return el ? (el.textContent ?? '').trim() : null;
  }

  function mountTarget(): void {
    setLocale('zh-CN');
    target = document.createElement('div');
    document.body.appendChild(target);
  }

  async function mountDecks(): Promise<void> {
    mountTarget();
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [deck('d1', 'Alpha'), deck('d2', 'Beta')] });
    vi.spyOn(apiClient, 'getShareInvites').mockResolvedValue({ invites: [] } as never);
    instance = mount(DecksView, { target });
    await flush();
  }

  async function mountDetail(): Promise<void> {
    mountTarget();
    setCardTypes(null);
    window.history.replaceState({}, '', '/decks/d1');
    routeStore.set(matchRoute('/decks/d1'));
    vi.spyOn(apiClient, 'getCardTypes').mockResolvedValue({ card_types: [] } as never);
    instance = mount(DeckDetailView, {
      target,
      props: { initialDeck: deck('d1', 'Alpha'), initialNotes: [], initialTotal: 0, initialLoading: false },
    });
    await flush();
  }

  async function mountHome(): Promise<void> {
    mountTarget();
    vi.spyOn(apiClient, 'getStatsSummary').mockResolvedValue({} as StatsSummary);
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [deck('d1', 'Alpha')] });
    instance = mount(HomeView, { target });
    await flush();
  }

  it('shows studied and remaining counts per deck in the deck list', async () => {
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue(counts);
    await mountDecks();
    expect(text('deck-progress-d1')).toBe('已学 120 · 剩余 380');
    expect(text('deck-progress-d2')).toBe('已学 0 · 剩余 0');
  });

  it('shows the current deck progress in the deck detail header', async () => {
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue(counts);
    await mountDetail();
    expect(text('deck-progress')).toBe('已学 120 · 剩余 380');
  });

  it('omits the deck detail progress when the counts request fails', async () => {
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockRejectedValue(new Error('offline'));
    await mountDetail();
    expect(text('deck-progress')).toBeNull();
  });

  it('omits the deck detail progress when the deck is missing from the counts', async () => {
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue({ decks: [] });
    await mountDetail();
    expect(text('deck-progress')).toBeNull();
  });

  it('shows deck progress in the home deck list', async () => {
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue(counts);
    await mountHome();
    expect(text('home-deck-progress-d1')).toBe('已学 120 · 剩余 380');
    expect(text('home-deck-new-d1')).toBe('新 5');
    expect(text('home-deck-review-d1')).toBe('复习 3');
    const bar = target.querySelector('[data-testid="home-deck-stats-d1"] [role="progressbar"]') as HTMLElement | null;
    expect(bar).not.toBeNull();
    expect(bar!.getAttribute('aria-valuenow')).toBe('120');
    expect(bar!.getAttribute('aria-valuemax')).toBe('500');
    expect((bar!.firstElementChild as HTMLElement).style.width).toBe('24%');
  });

  it('greys out zero badges and leaves an empty deck at zero progress on the home deck list', async () => {
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue(counts);
    mountTarget();
    vi.spyOn(apiClient, 'getStatsSummary').mockResolvedValue({} as StatsSummary);
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [deck('d1', 'Alpha'), deck('d2', 'Beta')] });
    instance = mount(HomeView, { target });
    await flush();
    const newBadge = target.querySelector('[data-testid="home-deck-new-d2"]') as HTMLElement;
    const activeBadge = target.querySelector('[data-testid="home-deck-new-d1"]') as HTMLElement;
    expect(newBadge.className).toContain('bg-muted');
    expect(activeBadge.className).toContain('bg-brand-soft');
    const bar = target.querySelector('[data-testid="home-deck-stats-d2"] [role="progressbar"]') as HTMLElement;
    expect((bar.firstElementChild as HTMLElement).style.width).toBe('0%');
    expect((bar.firstElementChild as HTMLElement).className).toContain('bg-brand');
  });

  it('marks a finished deck with the success colour on the home deck list', async () => {
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue({
      decks: [{ deck_id: 'd1', new_count: 0, review_count: 0, studied_count: 72, remaining_count: 0 }],
    });
    await mountHome();
    const fill = target.querySelector('[data-testid="home-deck-stats-d1"] [role="progressbar"] > div') as HTMLElement;
    expect(fill.style.width).toBe('100%');
    expect(fill.className).toContain('bg-success');
  });

  it('omits deck progress in the home deck list when the counts request fails', async () => {
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockRejectedValue(new Error('offline'));
    await mountHome();
    expect(target.querySelector('[data-testid="home-decks-list"]')).not.toBeNull();
    expect(text('home-deck-progress-d1')).toBeNull();
    expect(target.querySelector('[data-testid="home-deck-stats-d1"]')).toBeNull();
  });
});
