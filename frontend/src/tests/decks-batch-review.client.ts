import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DecksView from '../lib/views/DecksView.svelte';
import { apiClient } from '../lib/api';
import type { Deck } from '../lib/api';
import { setLocale } from '../lib/i18n';

/**
 * 卡组列表的批量复习：勾选若干卡组后，操作条给出一个指向复习页的链接，
 * 地址上为每个选中的卡组带一个 deck 参数——复习页正是按这组参数取多卡组队列。
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
    // 按列表顺序而不是勾选顺序：同一组卡组只对应一个地址。
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
