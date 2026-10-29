import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { get } from 'svelte/store';
import DeckDetailView from '../lib/views/DeckDetailView.svelte';
import { apiClient } from '../lib/api';
import type { Deck, Note } from '../lib/api';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale } from '../lib/i18n';
import { setCardTypes } from '../lib/card-types';
import { withReturn } from '../lib/return-to';

/**
 * The card list keeps its filters and page number in the address, and every way into the card editor
 * carries that address as the return path, so leaving the editor lands on the same filtered page. The
 * address is rewritten in place: the route store, and with it the view, stays as it was.
 */
describe('DeckDetailView list address', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  const deck: Deck = {
    id: 'd1',
    name: 'Vocabulary',
    description: '',
    new_per_day: 20,
    reviews_per_day: 200,
    preset_id: 'p1',
    created_at: '',
    role: 'owner',
  };

  const note: Note = {
    id: 'n1',
    deck_id: 'd1',
    kind: 'basic',
    fields: { front: 'Q', back: 'A' },
    tags: [],
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
    external_ref: '',
    suspended: false,
  };

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
    window.history.replaceState({}, '', '/');
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  async function setup(address: string) {
    setLocale('zh-CN');
    setCardTypes(null);
    window.history.replaceState({}, '', address);
    routeStore.set(matchRoute(address));
    vi.spyOn(apiClient, 'getCardTypes').mockResolvedValue({ card_types: [] } as never);
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue({ decks: [] });
    vi.spyOn(apiClient, 'getDeck').mockResolvedValue(deck);
    const getDeckNotes = vi
      .spyOn(apiClient, 'getDeckNotes')
      .mockImplementation(async (_deck, params) => ({ notes: [note], total: 120, page: params?.page ?? 1, per_page: 50 }) as never);
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(DeckDetailView, { target, props: { initialDeck: deck } });
    await flush();
    return { getDeckNotes };
  }

  function href(testId: string): string | null {
    return target.querySelector(`[data-testid="${testId}"]`)?.getAttribute('href') ?? null;
  }

  it('restores the filters and the page from the address', async () => {
    const { getDeckNotes } = await setup('/decks/d1?q=verb&tag=n2&kind=cloze&page=2');

    expect(getDeckNotes).toHaveBeenCalledWith('d1', { page: 2, per_page: 50, q: 'verb', tag: 'n2', kind: 'cloze' });
    expect((target.querySelector('[data-testid="filter-query-input"]') as HTMLInputElement).value).toBe('verb');
  });

  it('hands the filtered address to the editor and the create page as the return path', async () => {
    await setup('/decks/d1?q=verb&page=2');

    const list = '/decks/d1?q=verb&page=2';
    expect(href('edit-note-n1')).toBe(withReturn('/decks/d1/notes/n1/edit', list));
    expect(href('create-note-link')).toBe(withReturn('/decks/d1/notes/new', list));
  });

  it('writes a page change into the address without rebuilding the view', async () => {
    await setup('/decks/d1?q=verb');
    const routeBefore = get(routeStore);

    (target.querySelector('[data-testid="notes-next-page"]') as HTMLButtonElement).click();
    await flush();

    expect(window.location.pathname + window.location.search).toBe('/decks/d1?q=verb&page=2');
    expect(href('edit-note-n1')).toBe(withReturn('/decks/d1/notes/n1/edit', '/decks/d1?q=verb&page=2'));
    expect(get(routeStore)).toBe(routeBefore);
  });

  it('drops the filters from the address when they are reset', async () => {
    await setup('/decks/d1?q=verb&page=2');

    (target.querySelector('[data-testid="filter-reset-btn"]') as HTMLButtonElement).click();
    await flush();

    expect(window.location.pathname + window.location.search).toBe('/decks/d1');
    expect(href('edit-note-n1')).toBe(withReturn('/decks/d1/notes/n1/edit', '/decks/d1'));
  });

  it('ignores a page number that is not a positive integer', async () => {
    const { getDeckNotes } = await setup('/decks/d1?page=-3');

    expect(getDeckNotes).toHaveBeenCalledWith('d1', expect.objectContaining({ page: 1 }));
  });
});
