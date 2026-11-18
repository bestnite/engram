import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { faces } from './card-face-fixture';

/**
 * The header's due counter is split into new cards and review cards. The first load receives the whole queue, so its new
 * count is counted from that queue; an action response carries only one card, so its totals come from the server's
 * `queue_size` and `new_remaining` and the review count is the rest of the due total. A new count of zero is hidden
 * rather than rendered as "new 0".
 */
describe('ReviewView due counter split', () => {
  let instance: ReturnType<typeof mount> | null = null;

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

  function dueCard(id: string, state: DueCard['state']): DueCard {
    return {
      card_id: id,
      note_id: 'n-' + id,
      deck_id: 'd1',
      state,
      due_at: '',
      retrievability: 0,
      fields: { front: 'front ' + id, back: 'back ' + id },
      tags: [],
      template: 'forward',
      version: 0,
      answer_control: null,
      machine_graded: false,
      field_order: ['front', 'back'],
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
    } as unknown as ApiClient;
    const target = mountReview({ client, initialCards, initialLoading: true });
    await flush();
    expect(counterText(target, 'review-due-new')).toBe('新学：2');
    expect(counterText(target, 'review-due-review')).toBe('复习：3');
  });

  it('hides the new counter when the queue holds no new cards', async () => {
    setLocale('zh-CN');
    const initialCards = [dueCard('c1', 'learning'), dueCard('c2', 'review'), dueCard('c3', 'relearning')];
    const client = {
      getDueCards: vi.fn().mockReturnValue(new Promise(() => {})),
    } as unknown as ApiClient;
    const target = mountReview({ client, initialCards, initialLoading: true });
    await flush();
    expect(target.querySelector('[data-testid="review-due-new"]')).toBeNull();
    expect(counterText(target, 'review-due-review')).toBe('复习：3');
  });

  it('refreshes both counters from the totals a rating returns', async () => {
    setLocale('zh-CN');
    const queue = [
      dueCard('c1', 'new'),
      dueCard('c2', 'new'),
      dueCard('c3', 'review'),
      dueCard('c4', 'learning'),
    ];
    // The action response carries one card only, so the new count cannot come from it: the visible card is a review
    // card while new_remaining says one new card is still queued.
    const afterRating = [dueCard('c2', 'review')];
    const submitSelfReview = vi
      .fn()
      .mockResolvedValue({
        card_id: 'c1',
        state: 'learning',
        version: 1,
        remaining: 3,
        queue_size: 3,
        new_remaining: 1,
        cards: afterRating,
      });
    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: queue }),
      renderReviewCard: vi.fn().mockResolvedValue({
        card_id: 'c1',
        ...faces('<p>front</p>', '<p>back</p>'),
        edit_href: '',
      }),
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
