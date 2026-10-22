import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard, ExtraNewStatus } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { clearCardTypes, seedCardTypes } from './card-type-fixture';

/**
 * "One more round": when a single-deck review scope's queue is empty but the deck still has
 * new cards beyond today's quota, the completion page offers an extra-round entry; the
 * default count comes from the server (daily cap) and the user can change it; submitting
 * refetches the queue. Multi-deck scopes do not show it.
 */
describe('ReviewView another batch of new cards', () => {
  let instance: ReturnType<typeof mount> | null = null;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    window.history.replaceState({}, '', '/');
    clearCardTypes();
    vi.restoreAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  const status: ExtraNewStatus = {
    deck_id: 'd1',
    new_per_day: 5,
    new_unlimited: false,
    extra_new: 0,
    new_used: 5,
    new_left: 0,
    new_available: 8,
    suggested_count: 5,
    max_count: 1000,
  };

  const card: DueCard = {
    card_id: 'c1',
    note_id: 'n1',
    deck_id: 'd1',
    state: 'new',
    due_at: '',
    retrievability: 0,
    kind: 'basic',
    fields: { front: 'q', back: 'a' },
    tags: [],
    template: 'forward',
    version: 0,
  };

  function fakeClient(extra: ExtraNewStatus) {
    const getDueCards = vi.fn().mockResolvedValueOnce({ cards: [] }).mockResolvedValue({ cards: [card] });
    const getExtraNew = vi.fn().mockResolvedValue(extra);
    const addExtraNew = vi.fn().mockResolvedValue({ ...extra, extra_new: 3, new_left: 3 });
    const client = {
      getDueCards,
      getExtraNew,
      addExtraNew,
      renderReviewCard: vi.fn().mockResolvedValue({ card_id: 'c1', front_html: '<p>q</p>', back_html: '<p>a</p>', blanks: null, edit_href: '' }),
      getCardTypes: vi.fn(),
    } as unknown as ApiClient;
    return { client, getDueCards, getExtraNew, addExtraNew };
  }

  async function setup(search: string, extra: ExtraNewStatus = status) {
    setLocale('zh-CN');
    seedCardTypes();
    window.history.replaceState({}, '', `/review${search}`);
    const fake = fakeClient(extra);
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, { target, props: { client: fake.client } });
    await flush();
    return { target, ...fake };
  }

  function countInput(target: HTMLElement): HTMLInputElement {
    return target.querySelector<HTMLInputElement>('[data-testid="review-extra-count"]')!;
  }

  it('offers another batch with the daily limit as the default count', async () => {
    const { target, getExtraNew } = await setup('?deck=d1&tag=x');
    expect(getExtraNew).toHaveBeenCalledWith('d1', ['x']);
    expect(target.querySelector('[data-testid="review-extra-new"]')).not.toBeNull();
    expect(countInput(target).value).toBe('5');
    // The maximum addable count is the number of new cards left beyond today's quota (8 - 0).
    expect(countInput(target).max).toBe('8');
    expect(target.textContent).toContain('今天已学 5 张新卡');
    expect(target.querySelector('#review-extra-hint')?.textContent).toBe('最多 8 张');
  });

  it('holds the completion page until the offer has loaded', async () => {
    setLocale('zh-CN');
    seedCardTypes();
    window.history.replaceState({}, '', '/review?deck=d1');
    const fake = fakeClient(status);
    let resolveExtra: (value: ExtraNewStatus) => void = () => {};
    fake.getExtraNew.mockReturnValue(new Promise<ExtraNewStatus>((resolve) => (resolveExtra = resolve)));
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, { target, props: { client: fake.client } });
    await flush();
    // The quota has not come back yet: "review complete" must not show first, or the extra form inserting afterwards would make the page jump.
    expect(target.querySelector('[data-testid="review-empty"]')).toBeNull();
    expect(target.querySelector('[data-testid="review-extra-loading"]')).not.toBeNull();
    resolveExtra(status);
    await flush();
    expect(target.querySelector('[data-testid="review-extra-loading"]')).toBeNull();
    expect(target.querySelector('[data-testid="review-empty"]')).not.toBeNull();
    expect(target.querySelector('[data-testid="review-extra-new"]')).not.toBeNull();
  });

  it('submits the typed count and reloads the queue', async () => {
    const { target, addExtraNew, getDueCards } = await setup('?deck=d1');
    const input = countInput(target);
    input.value = '3';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
    (target.querySelector('[data-testid="review-extra-submit"]') as HTMLElement).click();
    await flush();
    expect(addExtraNew).toHaveBeenCalledWith('d1', 3);
    expect(getDueCards).toHaveBeenCalledTimes(2);
    expect(target.querySelector('[data-testid="review-card"]')).not.toBeNull();
    expect(target.querySelector('[data-testid="review-extra-new"]')).toBeNull();
  });

  it.each(['0', '9', '1.5', ''])('rejects the count %j without sending a request', async (value) => {
    const { target, addExtraNew } = await setup('?deck=d1');
    const input = countInput(target);
    input.value = value;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
    (target.querySelector('[data-testid="review-extra-submit"]') as HTMLElement).click();
    await flush();
    expect(addExtraNew).not.toHaveBeenCalled();
    expect(target.querySelector('[data-testid="review-extra-invalid"]')?.textContent).toContain('1 到 8');
  });

  it('hides the offer when no new cards are left beyond today', async () => {
    const { target } = await setup('?deck=d1', { ...status, new_available: 0, suggested_count: 0 });
    expect(target.querySelector('[data-testid="review-empty"]')).not.toBeNull();
    expect(target.querySelector('[data-testid="review-extra-new"]')).toBeNull();
  });

  it('hides the offer for an unlimited deck', async () => {
    const { target } = await setup('?deck=d1', { ...status, new_unlimited: true, suggested_count: 0 });
    expect(target.querySelector('[data-testid="review-extra-new"]')).toBeNull();
  });

  it('does not ask for a multi-deck or all-deck scope', async () => {
    for (const search of ['?deck=d1&deck=d2', '']) {
      const { getExtraNew, target } = await setup(search);
      expect(getExtraNew).not.toHaveBeenCalled();
      expect(target.querySelector('[data-testid="review-extra-new"]')).toBeNull();
      if (instance) unmount(instance);
      instance = null;
      document.body.innerHTML = '';
    }
  });
});
