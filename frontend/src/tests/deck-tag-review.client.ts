import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DeckDetailView from '../lib/views/DeckDetailView.svelte';
import { apiClient } from '../lib/api';
import type { Deck } from '../lib/api';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale } from '../lib/i18n';

/**
 * 卡组详情页的「按标签学习」：打开对话框时取本卡组的标签，勾选后「开始复习」链接到
 * `/review?deck=<本卡组>&tag=...`——复习页按这组参数只取带所选标签的卡。
 */
describe('DeckDetailView tag review', () => {
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

  const deck: Deck = {
    id: 'd1',
    name: '成语解释',
    description: '',
    new_per_day: 20,
    reviews_per_day: 200,
    preset_id: 'p1',
    created_at: '',
    role: 'owner',
  };

  async function setup(tags: { tag: string; notes: number }[]): Promise<ReturnType<typeof vi.fn>> {
    setLocale('zh-CN');
    routeStore.set(matchRoute('/decks/d1'));
    vi.spyOn(apiClient, 'getCardTypes').mockResolvedValue({ card_types: [] } as never);
    const getDeckTags = vi.spyOn(apiClient, 'getDeckTags').mockResolvedValue({ tags });
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(DeckDetailView, {
      target,
      props: { initialDeck: deck, initialNotes: [], initialTotal: 0, initialLoading: false },
    });
    await flush();
    (target.querySelector('[data-testid="deck-tag-review-open"]') as HTMLElement).click();
    await flush();
    return getDeckTags as unknown as ReturnType<typeof vi.fn>;
  }

  function startControl(): HTMLElement | null {
    return document.body.querySelector('[data-testid="deck-tag-review-start"]');
  }

  async function check(tag: string): Promise<void> {
    const box = document.body.querySelector(`[aria-label="${tag}"]`) as HTMLElement | null;
    expect(box).not.toBeNull();
    box!.click();
    await flush();
  }

  it('loads this deck\'s tags and disables start until a tag is picked', async () => {
    const getDeckTags = await setup([
      { tag: '创新类成语', notes: 3 },
      { tag: '文化传承类成语', notes: 5 },
    ]);
    expect(getDeckTags).toHaveBeenCalledWith('d1');
    const list = document.body.querySelector('[data-testid="deck-tag-review-list"]');
    expect(list?.textContent).toContain('文化传承类成语');
    expect(list?.textContent).toContain('5 条笔记');
    const start = startControl();
    expect(start?.tagName).toBe('BUTTON');
    expect(start?.hasAttribute('disabled')).toBe(true);
  });

  it('links to the review page with this deck and one tag parameter per picked tag', async () => {
    await setup([
      { tag: '创新类成语', notes: 3 },
      { tag: '文化传承类成语', notes: 5 },
      { tag: '高频', notes: 1 },
    ]);
    await check('高频');
    await check('创新类成语');
    const start = startControl();
    expect(start?.tagName).toBe('A');
    // 按标签列表顺序而不是勾选顺序：同一组勾选只对应一个地址。
    const params = new URL(start!.getAttribute('href')!, 'http://localhost').searchParams;
    expect(params.getAll('deck')).toEqual(['d1']);
    expect(params.getAll('tag')).toEqual(['创新类成语', '高频']);
  });

  it('says so when the deck has no tags', async () => {
    await setup([]);
    expect(document.body.querySelector('[data-testid="deck-tag-review-empty"]')).not.toBeNull();
  });
});
