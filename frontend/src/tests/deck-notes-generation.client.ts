import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DeckDetailView from '../lib/views/DeckDetailView.svelte';
import { apiClient } from '../lib/api';
import type { Deck, Note } from '../lib/api';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale } from '../lib/i18n';

/**
 * Two filter requests can overlap when the debounce fires again before the previous answer lands. The
 * newest request must own the list and the address, whatever order the responses arrive in.
 */
describe('DeckDetailView list request generation', () => {
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

  function note(id: string, front: string): Note {
    return {
      id,
      deck_id: 'd1',
      fields: { front },
      tags: [],
      created_at: '2026-10-01T00:00:00Z',
      updated_at: '2026-10-01T00:00:00Z',
      external_ref: '',
      suspended: false,
    };
  }

  function defer<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((done) => {
      resolve = done;
    });
    return { promise, resolve };
  }

  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.useRealTimers();
    vi.restoreAllMocks();
    window.history.replaceState({}, '', '/');
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  async function type(value: string): Promise<void> {
    const input = target.querySelector('[data-testid="filter-query-input"]') as HTMLInputElement;
    input.value = value;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    await vi.advanceTimersByTimeAsync(300);
  }

  it('drops an older response that lands after the newer filter', async () => {
    setLocale('en');
    window.history.replaceState({}, '', '/decks/d1');
    routeStore.set(matchRoute('/decks/d1'));

    const older = defer<unknown>();
    const newer = defer<unknown>();
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue({ decks: [] });
    const getDeckNotes = vi
      .spyOn(apiClient, 'getDeckNotes')
      .mockResolvedValueOnce({ notes: [note('base', 'BASE')], total: 1, page: 1, per_page: 50 } as never)
      .mockImplementationOnce(() => older.promise as never)
      .mockImplementationOnce(() => newer.promise as never);

    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(DeckDetailView, { target, props: { initialDeck: deck } });
    await flush();

    await type('a');
    await type('ab');
    expect(getDeckNotes).toHaveBeenCalledTimes(3);

    newer.resolve({ notes: [note('new', 'NEW')], total: 1, page: 1, per_page: 50 });
    await flush();
    expect(target.textContent).toContain('NEW');
    expect(window.location.pathname + window.location.search).toBe('/decks/d1?q=ab');

    older.resolve({ notes: [note('old', 'OLD-A')], total: 1, page: 1, per_page: 50 });
    await flush();

    expect(target.textContent).toContain('NEW');
    expect(target.textContent).not.toContain('OLD-A');
    expect((target.querySelector('[data-testid="filter-query-input"]') as HTMLInputElement).value).toBe('ab');
    expect(window.location.pathname + window.location.search).toBe('/decks/d1?q=ab');
  });
});
