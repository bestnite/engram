import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DecksView from '../lib/views/DecksView.svelte';
import HomeView from '../lib/views/HomeView.svelte';
import { apiClient } from '../lib/api';
import type { Deck, StatsSummary } from '../lib/api';
import { setLocale } from '../lib/i18n';

/**
 * Both deck lists print the description as a single line, so they show the server's `description_text`. A description is
 * plain text: markup in it is printed as written, never rendered.
 */

const markupDescription = '<a href="https://example.com"><b>Notes</b></a>';

function baseDeck(overrides: Partial<Deck> = {}): Deck {
  return {
    id: 'd1',
    name: 'Biology',
    description: markupDescription,
    description_text: markupDescription,
    new_per_day: 20,
    reviews_per_day: 200,
    preset_id: 'p1',
    created_at: '2026-01-01T00:00:00Z',
    role: 'owner',
    ...overrides,
  };
}

describe('deck description in the lists', () => {
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

  function mountTarget(): void {
    setLocale('en');
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

  async function mountHome(decks: Deck[]): Promise<void> {
    mountTarget();
    vi.spyOn(apiClient, 'getStatsSummary').mockResolvedValue({} as StatsSummary);
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({ decks });
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue({ decks: [] });
    instance = mount(HomeView, { target });
    await flush();
  }

  it('prints a description as text in the deck list', async () => {
    await mountDecks([baseDeck()]);
    const row = target.querySelector('[data-testid="deck-card-d1"]');
    expect(row).not.toBeNull();
    expect(row!.textContent).toContain(markupDescription);
    expect(row!.querySelector('b, a[href="https://example.com"]')).toBeNull();
  });

  it('prints a description as text in the home deck list', async () => {
    await mountHome([baseDeck()]);
    const row = target.querySelector('[data-testid="home-deck-row-d1"]');
    expect(row).not.toBeNull();
    expect(row!.textContent).toContain(markupDescription);
    expect(row!.querySelector('b, a[href="https://example.com"]')).toBeNull();
  });
});
