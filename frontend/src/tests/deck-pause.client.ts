import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DecksView from '../lib/views/DecksView.svelte';
import DeckDetailView from '../lib/views/DeckDetailView.svelte';
import HomeView from '../lib/views/HomeView.svelte';
import { apiClient } from '../lib/api';
import type { Deck, StatsSummary } from '../lib/api';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale } from '../lib/i18n';
import { setCardTypes } from '../lib/card-types';

/**
 * Deck pause: the deck list keeps a paused deck with a badge and a toggle, the deck detail header offers the same toggle
 * with a hint, and the home deck list leaves paused decks out.
 */
describe('deck pause', () => {
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

  function deck(id: string, name: string, paused = false): Deck {
    return {
      id,
      name,
      description: '',
      new_per_day: 20,
      reviews_per_day: 200,
      preset_id: 'p1',
      created_at: '',
      role: 'owner',
      paused,
    };
  }

  function el(testId: string): HTMLElement | null {
    return target.querySelector(`[data-testid="${testId}"]`);
  }

  function mountTarget(): void {
    setLocale('zh-CN');
    target = document.createElement('div');
    document.body.appendChild(target);
  }

  async function mountDecks(decks: Deck[]): Promise<void> {
    mountTarget();
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks });
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'getShareInvites').mockResolvedValue({ invites: [] } as never);
    instance = mount(DecksView, { target });
    await flush();
  }

  async function mountDetail(initial: Deck): Promise<void> {
    mountTarget();
    setCardTypes(null);
    window.history.replaceState({}, '', `/decks/${initial.id}`);
    routeStore.set(matchRoute(`/decks/${initial.id}`));
    vi.spyOn(apiClient, 'getCardTypes').mockResolvedValue({ card_types: [] } as never);
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue({ decks: [] });
    instance = mount(DeckDetailView, {
      target,
      props: { initialDeck: initial, initialNotes: [], initialTotal: 0, initialLoading: false },
    });
    await flush();
  }

  async function mountHome(decks: Deck[]): Promise<void> {
    mountTarget();
    vi.spyOn(apiClient, 'getStatsSummary').mockResolvedValue({} as StatsSummary);
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks });
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue({ decks: [] });
    instance = mount(HomeView, { target });
    await flush();
  }

  it('keeps a paused deck in the deck list with a badge and a resume toggle', async () => {
    await mountDecks([deck('d1', 'Alpha', true), deck('d2', 'Beta')]);
    expect(el('deck-card-d1')).not.toBeNull();
    expect(el('deck-paused-badge-d1')?.textContent?.trim()).toBe('已暂停');
    expect(el('deck-paused-badge-d2')).toBeNull();
    expect(el('deck-pause-btn-d1')?.getAttribute('aria-label')).toBe('恢复卡组');
    expect(el('deck-pause-btn-d2')?.getAttribute('aria-label')).toBe('暂停卡组');
  });

  it('pauses a deck from the deck list', async () => {
    const setPaused = vi.spyOn(apiClient, 'setDeckPaused').mockResolvedValue({ deck_id: 'd2', paused: true });
    await mountDecks([deck('d1', 'Alpha'), deck('d2', 'Beta')]);
    el('deck-pause-btn-d2')!.click();
    await flush();
    expect(setPaused).toHaveBeenCalledWith('d2', true);
    expect(el('deck-paused-badge-d2')).not.toBeNull();
    expect(el('deck-pause-btn-d2')?.getAttribute('aria-pressed')).toBe('true');
  });

  it('leaves the row unchanged when pausing fails', async () => {
    vi.spyOn(apiClient, 'setDeckPaused').mockRejectedValue(new Error('offline'));
    await mountDecks([deck('d1', 'Alpha')]);
    el('deck-pause-btn-d1')!.click();
    await flush();
    expect(el('deck-paused-badge-d1')).toBeNull();
    expect(el('deck-pause-btn-d1')?.hasAttribute('disabled')).toBe(false);
  });

  it('resumes a paused deck from the deck detail header and drops the hint', async () => {
    const setPaused = vi.spyOn(apiClient, 'setDeckPaused').mockResolvedValue({ deck_id: 'd1', paused: false });
    await mountDetail(deck('d1', 'Alpha', true));
    expect(el('deck-paused-hint')).not.toBeNull();
    expect(el('deck-pause-toggle')?.textContent?.trim()).toBe('恢复');
    el('deck-pause-toggle')!.click();
    await flush();
    expect(setPaused).toHaveBeenCalledWith('d1', false);
    expect(el('deck-paused-hint')).toBeNull();
    expect(el('deck-pause-toggle')?.textContent?.trim()).toBe('暂停');
  });

  it('offers the toggle to a reader of a shared deck', async () => {
    await mountDetail({ ...deck('d1', 'Alpha'), role: 'reader' });
    expect(el('deck-pause-toggle')).not.toBeNull();
    expect(el('deck-paused-hint')).toBeNull();
  });

  it('leaves paused decks out of the home deck list', async () => {
    await mountHome([deck('d1', 'Alpha', true), deck('d2', 'Beta')]);
    const list = el('home-decks-list');
    expect(list).not.toBeNull();
    expect(list!.textContent).toContain('Beta');
    expect(list!.textContent).not.toContain('Alpha');
  });

  it('says so on the home page when every deck is paused', async () => {
    await mountHome([deck('d1', 'Alpha', true)]);
    expect(el('home-decks-list')).toBeNull();
    expect(el('home-empty-decks')?.textContent?.trim()).toBe('所有卡组都已暂停。');
  });
});
