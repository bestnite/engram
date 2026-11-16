import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { faces } from './card-face-fixture';
import { postAnswerFromFrame } from './card-frame-fixture';

describe('study session counts', () => {
  let instance: ReturnType<typeof mount> | undefined;
  afterEach(() => {
    if (instance) unmount(instance);
    instance = undefined;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  async function flush() {
    for (let i = 0; i < 30; i++) await Promise.resolve();
    flushSync();
  }

  function card(state: DueCard['state'], graded = false): DueCard {
    return {
      card_id: 'c1', note_id: 'n1', deck_id: 'd1', state, due_at: '', retrievability: 0,
      fields: { front: 'Question', back: 'Answer' }, tags: [], template: 'forward', version: 0,
      answer_control: graded ? 'number' : null, machine_graded: graded, field_order: ['front', 'back'],
    };
  }

  it('counts a repeated learning step as review and ignores failed submissions', async () => {
    setLocale('en');
    const fresh = card('new');
    const learning = { ...card('learning'), version: 1 };
    const submitSelfReview = vi.fn()
      .mockRejectedValueOnce(new Error('Rating refused'))
      .mockResolvedValueOnce({ card_id: 'c1', state: 'learning', version: 1, remaining: 1, cards: [learning] })
      .mockResolvedValueOnce({ card_id: 'c1', state: 'review', version: 2, remaining: 0, cards: [] });
    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: [fresh] }),
      renderReviewCard: vi.fn().mockResolvedValue({ card_id: 'c1', ...faces('<p>Question</p>', '<p>Answer</p>'), edit_href: '' }),
      submitSelfReview,
    } as unknown as ApiClient;
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, { target, props: { client } });
    await flush();
    const counts = () => target.querySelector('[data-testid="review-session-done"]')?.textContent?.replace(/\s+/g, '');
    (target.querySelector('[data-testid="review-reveal"]') as HTMLElement).click();
    await flush();
    (target.querySelector('[data-rating="3"]') as HTMLElement).click();
    await flush();
    expect(counts()).toBe('Thissession:0new·0reviews');
    // Retrying the failed queue load restores the same New card without counting it.
    (target.querySelector('[data-testid="review-retry"]') as HTMLElement).click();
    await flush();
    for (let attempt = 0; attempt < 2; attempt++) {
      (target.querySelector('[data-testid="review-reveal"]') as HTMLElement).click();
      await flush();
      (target.querySelector('[data-rating="3"]') as HTMLElement).click();
      await flush();
    }
    expect(counts()).toBe('Thissession:1new·1reviews');
    expect(target.querySelector('[data-testid="review-empty"]')).not.toBeNull();
  });

  const cases = ['new', 'learning', 'review', 'relearning'] as const;
  for (const locale of ['zh-CN', 'en'] as const) {
    for (const flow of ['self', 'graded', 'give_up'] as const) {
      it.each(cases)(`${locale} ${flow}: classifies %s before submission and rolls it back on undo`, async (state) => {
        setLocale(locale);
        const original = card(state, flow !== 'self');
        const response = {
          card_id: 'c1', state: 'learning', version: 1, remaining: 0, cards: [],
          feedback: { verdict: 'correct', score: 1, rating: 3, answer_html: '<p>Answer</p>' },
        };
        const client = {
          getDueCards: vi.fn().mockResolvedValue({ cards: [original] }),
          renderReviewCard: vi.fn().mockResolvedValue({ card_id: 'c1', ...faces('<p>Question</p>', '<p>Answer</p>'), edit_href: '' }),
          submitSelfReview: vi.fn().mockResolvedValue(response),
          submitGradedReview: vi.fn().mockResolvedValue(response),
          revealGradedAnswer: vi.fn().mockResolvedValue({ card_id: 'c1', revealed: true, answer_html: '<p>Answer</p>' }),
          undoReview: vi.fn().mockResolvedValue({ undone_card_id: 'c1', remaining: 1, cards: [original] }),
        } as unknown as ApiClient;
        const target = document.createElement('div');
        document.body.appendChild(target);
        instance = mount(ReviewView, { target, props: { client } });
        await flush();
        const click = async (testId: string) => {
          const button = target.querySelector(`[data-testid="${testId}"]`) as HTMLElement;
          expect(button, testId).not.toBeNull();
          button.click();
          await flush();
        };
        if (flow === 'self') {
          await click('review-reveal');
          (target.querySelector('[data-rating="3"]') as HTMLElement).click();
          await flush();
        } else if (flow === 'graded') {
          await postAnswerFromFrame(target, 'question', 42);
          await click('review-graded-submit');
        } else {
          await click('review-graded-reveal');
          await click('review-graded-give-up');
        }
        const counts = () => target.querySelector('[data-testid="review-session-done"]')?.textContent?.replace(/\s+/g, '');
        const n = state === 'new' ? 1 : 0;
        const r = state === 'new' ? 0 : 1;
        expect(counts()).toBe(locale === 'en' ? `Thissession:${n}new·${r}reviews` : `本次新学：${n}·复习：${r}`);
        // A rejected undo must not change either count.
        if (flow !== 'graded') {
          vi.mocked(client.undoReview).mockRejectedValueOnce(new Error('Undo refused'));
          await click('review-undo-last');
          expect(counts()).toBe(locale === 'en' ? `Thissession:${n}new·${r}reviews` : `本次新学：${n}·复习：${r}`);
        }
        await click(flow === 'graded' ? 'review-undo' : 'review-undo-last');
        expect(counts()).toBe(locale === 'en' ? 'Thissession:0new·0reviews' : '本次新学：0·复习：0');
        expect(client.undoReview).toHaveBeenLastCalledWith(expect.objectContaining({ card_id: 'c1', expected_version: 1 }));
      });
    }
  }
});
