import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { get } from 'svelte/store';
import ReviewView from '../lib/views/ReviewView.svelte';
import { routeStore } from '../lib/router';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { clearCardTypes, seedCardTypes } from './card-type-fixture';

/**
 * The edit entry on a review card opens the editor with the review address as its return path, scope
 * included, so leaving the editor lands on the same review session instead of the deck page.
 */
describe('ReviewView edit entry', () => {
  let instance: ReturnType<typeof mount> | null = null;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    clearCardTypes();
    vi.restoreAllMocks();
    window.history.replaceState({}, '', '/');
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  const card: DueCard = {
    card_id: 'c1',
    note_id: 'n1',
    deck_id: 'd1',
    state: 'new',
    due_at: '',
    retrievability: 0,
    kind: 'basic',
    fields: { front: 'Q', back: 'A' },
    tags: [],
    template: 'forward',
    version: 0,
  };

  async function setup(editHref: string): Promise<HTMLElement> {
    setLocale('zh-CN');
    seedCardTypes();
    window.history.replaceState({}, '', '/review?deck=d1&tag=verbs');
    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: [card] }),
      renderReviewCard: vi.fn().mockResolvedValue({
        card_id: 'c1',
        front_html: '<p>Q</p>',
        back_html: '<p>A</p>',
        blanks: null,
        edit_href: editHref,
      }),
      getCardTypes: vi.fn(),
    } as unknown as ApiClient;
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, { target, props: { client } });
    await flush();
    return target;
  }

  it('opens the editor with the review scope as the return path', async () => {
    const target = await setup('/decks/d1/notes/n1');

    (target.querySelector('[data-testid="review-edit"]') as HTMLButtonElement).click();
    await flush();

    const route = get(routeStore);
    expect(route.path).toBe('/decks/d1/notes/n1');
    expect(route.query.return).toBe('/review?deck=d1&tag=verbs');
  });

  it('carries the return path on the fallback link when the server sent no edit_href', async () => {
    const target = await setup('');

    (target.querySelector('[data-testid="review-edit"]') as HTMLButtonElement).click();
    await flush();

    const route = get(routeStore);
    expect(route.path).toBe('/decks/d1/notes/n1');
    expect(route.query.return).toBe('/review?deck=d1&tag=verbs');
  });
});
