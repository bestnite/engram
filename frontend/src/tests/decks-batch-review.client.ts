import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DecksView from '../lib/views/DecksView.svelte';
import { apiClient } from '../lib/api';
import type { Deck } from '../lib/api';
import { setLocale } from '../lib/i18n';

/**
 * Batch review from the deck list: after ticking several decks, the action bar offers a
 * link to the review page carrying one deck param per selected deck — the review page
 * reads exactly those params to build a multi-deck queue.
 */
describe('DecksView batch review', () => {
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

  async function setup(): Promise<void> {
    setLocale('zh-CN');
    vi.spyOn(apiClient, 'getDecks').mockResolvedValue({
      decks: [deck('d1', 'Alpha'), deck('d2', 'Beta'), deck('d3', 'Gamma')],
    });
    vi.spyOn(apiClient, 'getDeckQueueCounts').mockResolvedValue({ decks: [] } as never);
    vi.spyOn(apiClient, 'getShareInvites').mockResolvedValue({ invites: [] } as never);
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(DecksView, { target });
    await flush();
  }

  async function toggle(name: string): Promise<void> {
    const box = target.querySelector(`[aria-label="选择卡组 ${name}"]`) as HTMLElement | null;
    expect(box).not.toBeNull();
    box!.click();
    await flush();
  }

  function reviewLink(): HTMLAnchorElement | null {
    return target.querySelector('[data-testid="decks-batch-review"]');
  }

  it('shows no review action until a deck is selected', async () => {
    await setup();
    expect(reviewLink()).toBeNull();
  });

  it('links to the review page with one deck parameter per selected deck', async () => {
    await setup();
    await toggle('Gamma');
    await toggle('Alpha');
    const link = reviewLink();
    expect(link).not.toBeNull();
    // Ordered by the list, not by tick order: the same set of decks maps to a single URL.
    expect(link!.getAttribute('href')).toBe('/review?deck=d1&deck=d3');
    expect(link!.textContent?.trim()).toBe('复习');
  });

  it('drops a deck from the link when it is deselected', async () => {
    await setup();
    await toggle('Alpha');
    await toggle('Beta');
    await toggle('Alpha');
    expect(reviewLink()!.getAttribute('href')).toBe('/review?deck=d2');
  });
});
