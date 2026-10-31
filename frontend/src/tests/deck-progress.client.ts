import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DecksView from '../lib/views/DecksView.svelte';
import DeckDetailView from '../lib/views/DeckDetailView.svelte';
import HomeView from '../lib/views/HomeView.svelte';
import DeckProgressBar from '../lib/components/DeckProgressBar.svelte';
import { apiClient } from '../lib/api';
import type { Deck, DeckQueueCountsResponse, StatsSummary } from '../lib/api';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale } from '../lib/i18n';

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
    window.history.replaceState({}, '', '/decks/d1');
    routeStore.set(matchRoute('/decks/d1'));
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

  function bar(testId: string): HTMLElement | null {
    return target.querySelector(`[data-testid="${testId}"][role="progressbar"]`) as HTMLElement | null;
  }

  it('shows studied and remaining counts per deck as a bar in the deck list', async () => {
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue(counts);
    await mountDecks();
    const d1 = bar('deck-progress-d1');
    expect(d1).not.toBeNull();
    expect(d1!.getAttribute('aria-valuenow')).toBe('120');
    expect(d1!.getAttribute('aria-valuemax')).toBe('500');
    expect(d1!.getAttribute('aria-valuetext')).toBe('120/500');
    expect((d1!.firstElementChild as HTMLElement).style.width).toBe('24%');
    expect(bar('deck-progress-d2')!.getAttribute('aria-valuetext')).toBe('0/0');
  });

  it('puts a review link before the pause button in the deck list', async () => {
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue(counts);
    await mountDecks();
    const review = target.querySelector('[data-testid="deck-review-btn-d1"]') as HTMLAnchorElement;
    expect(review.getAttribute('href')).toBe('/review?deck=d1');
    expect(review.getAttribute('aria-label')).toBe('复习卡组');
    expect(review.nextElementSibling?.getAttribute('data-testid')).toBe('deck-pause-btn-d1');
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
    expect(text('home-deck-new-d1')).toBe('新卡 5');
    expect(text('home-deck-review-d1')).toBe('复习 3');
    const d1 = bar('home-deck-progress-d1');
    expect(d1).not.toBeNull();
    expect(d1!.getAttribute('aria-valuenow')).toBe('120');
    expect(d1!.getAttribute('aria-valuemax')).toBe('500');
    expect(d1!.getAttribute('aria-valuetext')).toBe('120/500');
    expect((d1!.firstElementChild as HTMLElement).style.width).toBe('24%');
    // Phones get no hover, so the numbers are also written next to the bar there.
    expect(text('home-deck-stats-d1')).toContain('120/500');
  });

  it('makes the whole home row the review entry for its deck', async () => {
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue(counts);
    await mountHome();
    const row = target.querySelector('[data-testid="home-deck-row-d1"]') as HTMLAnchorElement;
    expect(row.tagName).toBe('A');
    expect(row.getAttribute('href')).toBe('/review?deck=d1');
    expect(row.querySelector('[data-testid="home-deck-progress-d1"]')).not.toBeNull();
    expect(row.querySelectorAll('a, button')).toHaveLength(0);
  });

  it('greys out zero counts and leaves an empty deck at zero progress on the home deck list', async () => {
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue(counts);
    mountTarget();
    vi.spyOn(apiClient, 'getStatsSummary').mockResolvedValue({} as StatsSummary);
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks: [deck('d1', 'Alpha'), deck('d2', 'Beta')] });
    instance = mount(HomeView, { target });
    await flush();
    const zero = target.querySelector('[data-testid="home-deck-new-d2"]') as HTMLElement;
    const active = target.querySelector('[data-testid="home-deck-new-d1"]') as HTMLElement;
    expect(zero.className).toContain('text-muted-foreground');
    expect(active.className).toContain('text-foreground');
    expect(active.className).not.toContain('text-muted-foreground');
    const fill = bar('home-deck-progress-d2')!.firstElementChild as HTMLElement;
    expect(fill.style.width).toBe('0%');
    expect(fill.className).toContain('bg-foreground');
  });

  it('fills a finished deck in the same colour on the home deck list', async () => {
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue({
      decks: [{ deck_id: 'd1', new_count: 0, review_count: 0, studied_count: 72, remaining_count: 0 }],
    });
    await mountHome();
    const fill = bar('home-deck-progress-d1')!.firstElementChild as HTMLElement;
    expect(fill.style.width).toBe('100%');
    expect(fill.className).toContain('bg-foreground');
    expect(fill.className).not.toContain('bg-success');
  });

  it('omits deck progress in the home deck list when the counts request fails', async () => {
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockRejectedValue(new Error('offline'));
    await mountHome();
    expect(target.querySelector('[data-testid="home-decks-list"]')).not.toBeNull();
    expect(text('home-deck-progress-d1')).toBeNull();
    expect(target.querySelector('[data-testid="home-deck-stats-d1"]')).toBeNull();
  });

  it('shows the exact numbers in a tooltip on hover without adding a tab stop', async () => {
    mountTarget();
    instance = mount(DeckProgressBar, { target, props: { studied: 10, remaining: 90, testId: 'bar' } });
    flushSync();
    const trigger = bar('bar')!.parentElement!;
    expect(trigger.hasAttribute('tabindex')).toBe(false);
    expect(document.body.textContent).not.toContain('10/100');
    trigger.dispatchEvent(new PointerEvent('pointerenter', { bubbles: true, pointerType: 'mouse' }));
    trigger.dispatchEvent(new PointerEvent('pointermove', { bubbles: true, pointerType: 'mouse' }));
    await new Promise((resolve) => setTimeout(resolve, 300));
    flushSync();
    expect(document.body.textContent).toContain('10/100');
  });
});
