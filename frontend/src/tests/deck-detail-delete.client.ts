import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DeckDetailView from '../lib/views/DeckDetailView.svelte';
import { apiClient } from '../lib/api';
import type { Deck } from '../lib/api';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale } from '../lib/i18n';

/**
 * Deck deletion from the deck detail header: the entry point opens a confirmation dialog, the confirmed
 * request deletes the deck and leaves the page, and a failed request keeps the dialog open with an error.
 * These paths need a real mount, because the dialog open state and the history navigation only happen client-side.
 */
describe('DeckDetailView deck deletion', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  const deck: Deck = {
    id: 'd1',
    name: '日语 N2 词汇',
    description: '',
    new_per_day: 20,
    reviews_per_day: 200,
    preset_id: 'p1',
    created_at: '',
    role: 'owner',
  };

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
    vi.clearAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  async function setup(): Promise<void> {
    setLocale('zh-CN');
    window.history.replaceState({}, '', '/decks/d1');
    routeStore.set(matchRoute('/decks/d1'));
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue({ decks: [] });
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(DeckDetailView, {
      target,
      props: { initialDeck: deck, initialNotes: [], initialTotal: 0, initialLoading: false },
    });
    await flush();
  }

  async function openDialog(): Promise<void> {
    const open = target.querySelector('[data-testid="deck-delete-open"]') as HTMLElement | null;
    expect(open).not.toBeNull();
    open!.click();
    await flush();
  }

  async function confirmDelete(): Promise<void> {
    const confirm = document.body.querySelector('[data-testid="deck-delete-confirm"]') as HTMLElement | null;
    expect(confirm).not.toBeNull();
    confirm!.click();
    await flush();
  }

  it('opens the confirmation dialog naming the deck', async () => {
    await setup();
    await openDialog();
    const dialog = document.body.querySelector('[data-testid="deck-detail-delete-dialog"]');
    expect(dialog).not.toBeNull();
    expect(dialog!.textContent).toContain('日语 N2 词汇');
  });

  it('deletes the deck and returns to the deck list', async () => {
    const deleteDeck = vi.spyOn(apiClient, 'deleteDeck').mockResolvedValue({ deleted: true });
    await setup();
    await openDialog();
    await confirmDelete();
    expect(deleteDeck).toHaveBeenCalledWith('d1');
    expect(window.location.pathname).toBe('/decks');
  });

  it('keeps the dialog open with an error when deletion fails', async () => {
    vi.spyOn(apiClient, 'deleteDeck').mockRejectedValue(new Error('network down'));
    await setup();
    await openDialog();
    await confirmDelete();
    expect(document.body.querySelector('[data-testid="deck-delete-error"]')).not.toBeNull();
    expect(document.body.querySelector('[data-testid="deck-detail-delete-dialog"]')).not.toBeNull();
    expect(window.location.pathname).toBe('/decks/d1');
  });
});
