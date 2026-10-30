import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { clearCardTypes, seedCardTypes } from './card-type-fixture';
import { faces } from './card-face-fixture';

/**
 * The header's due counter is split into new cards and review cards. Both numbers come from the
 * queue already carried by the response: `cards` is sliced down to the card on screen, so the new
 * count is read from the response's full queue and the review count is the rest of `remaining`.
 * A new count of zero is hidden rather than rendered as "new 0".
 */
describe('ReviewView due counter split', () => {
  let instance: ReturnType<typeof mount> | null = null;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    clearCardTypes();
    vi.restoreAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  function dueCard(id: string, state: DueCard['state']): DueCard {
    return {
      card_id: id,
      note_id: 'n-' + id,
      deck_id: 'd1',
      state,
      due_at: '',
      retrievability: 0,
      kind: 'basic',
      fields: { front: 'front ' + id, back: 'back ' + id },
      tags: [],
      template: 'forward',
      version: 0,
    };
  }

  /** The visible text of one counter, with the label and the number collapsed into one run of characters. */
  function counterText(target: HTMLElement, testId: string): string {
    const element = target.querySelector(`[data-testid="${testId}"]`);
    expect(element).not.toBeNull();
    return (element?.textContent ?? '').replace(/\s+/g, '');
  }

  function mountReview(props: { client: ApiClient; initialCards?: DueCard[]; initialLoading?: boolean }) {
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, { target, props });
    return target;
  }

  it('splits five queued cards into two new and three review at the first render', async () => {
    setLocale('zh-CN');
    seedCardTypes();
    const initialCards = [
      dueCard('c1', 'new'),
      dueCard('c2', 'new'),
      dueCard('c3', 'learning'),
      dueCard('c4', 'review'),
      dueCard('c5', 'relearning'),
    ];
    // The queue request never settles: the numbers proven here can only come from the mount props.
    const client = {
      getDueCards: vi.fn().mockReturnValue(new Promise(() => {})),
      getCardTypes: vi.fn(),
    } as unknown as ApiClient;
    const target = mountReview({ client, initialCards, initialLoading: true });
    await flush();
    expect(counterText(target, 'review-due-new')).toBe('新学：2');
    expect(counterText(target, 'review-due-review')).toBe('复习：3');
  });

  it('hides the new counter when the queue holds no new cards', async () => {
    setLocale('zh-CN');
    seedCardTypes();
    const initialCards = [dueCard('c1', 'learning'), dueCard('c2', 'review'), dueCard('c3', 'relearning')];
    const client = {
      getDueCards: vi.fn().mockReturnValue(new Promise(() => {})),
      getCardTypes: vi.fn(),
    } as unknown as ApiClient;
    const target = mountReview({ client, initialCards, initialLoading: true });
    await flush();
    expect(target.querySelector('[data-testid="review-due-new"]')).toBeNull();
    expect(counterText(target, 'review-due-review')).toBe('复习：3');
  });

  it('refreshes both counters from the queue returned by a rating', async () => {
    setLocale('zh-CN');
    seedCardTypes();
    const queue = [
      dueCard('c1', 'new'),
      dueCard('c2', 'new'),
      dueCard('c3', 'review'),
      dueCard('c4', 'learning'),
    ];
    const afterRating = [dueCard('c2', 'new'), dueCard('c3', 'review'), dueCard('c4', 'learning')];
    const submitSelfReview = vi
      .fn()
      .mockResolvedValue({ card_id: 'c1', state: 'learning', version: 1, remaining: 3, cards: afterRating });
    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: queue }),
      renderReviewCard: vi.fn().mockResolvedValue({
        card_id: 'c1',
        ...faces('<p>front</p>', '<p>back</p>'),
        front_html: '<p>front</p>',
        back_html: '<p>back</p>',
        blanks: null,
        edit_href: '',
      }),
      getCardTypes: vi.fn(),
      submitSelfReview,
    } as unknown as ApiClient;
    const target = mountReview({ client });
    await flush();
    expect(counterText(target, 'review-due-new')).toBe('新学：2');
    expect(counterText(target, 'review-due-review')).toBe('复习：2');
    (target.querySelector('[data-testid="review-reveal"]') as HTMLElement).click();
    await flush();
    (target.querySelector('[data-rating="3"]') as HTMLElement).click();
    await flush();
    expect(submitSelfReview).toHaveBeenCalledTimes(1);
    expect(counterText(target, 'review-due-new')).toBe('新学：1');
    expect(counterText(target, 'review-due-review')).toBe('复习：2');
  });
});
