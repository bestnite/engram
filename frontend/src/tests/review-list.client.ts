import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { clearCardTypes, seedCardTypes } from './card-type-fixture';
import { shownHtmlOnSide, postAnswerFromFrame } from './card-frame-fixture';

/**
 * Per-item answering for list cards: the input markup lives in the frame and the frame posts one string per item. The
 * parent submits that array and renders the per-item grading breakdown from the server's feedback.
 */
describe('ReviewView list items', () => {
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

  const card: DueCard = {
    card_id: 'l1',
    note_id: 'n1',
    deck_id: 'd1',
    state: 'new',
    due_at: '',
    retrievability: 0,
    kind: 'list',
    fields: { prompt: '光的三原色是什么？', items: ['red', 'green', 'blue'] },
    tags: [],
    template: 'forward',
    version: 0,
  };

  async function setup() {
    setLocale('zh-CN');
    seedCardTypes();
    const submitGradedReview = vi.fn().mockResolvedValue({
      card_id: 'l1',
      state: 'learning',
      version: 1,
      remaining: 0,
      cards: [],
      feedback: {
        verdict: 'partial',
        score: 2 / 3,
        rating: 2,
        answer_html: '<ul><li>red</li><li>green</li><li>blue</li></ul>',
        given: 'red, blue',
        breakdown: [
          { answer_html: '<p>red</p>', correct: true },
          { answer_html: '<p>green</p>', correct: false },
          { answer_html: '<p>blue</p>', correct: true },
        ],
      },
    });
    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: [card] }),
      renderReviewCard: vi.fn().mockResolvedValue({
        card_id: 'l1',
        front_html: '<p>光的三原色是什么？</p>',
        back_html: '<ul><li>red</li><li>green</li><li>blue</li></ul>',
        blanks: ['', '', ''],
        edit_href: '/decks/d1/notes/n1',
      }),
      getCardTypes: vi.fn(),
      submitGradedReview,
    } as unknown as ApiClient;
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, { target, props: { client } });
    await flush();
    return { target, submitGradedReview };
  }

  it('submits the items and marks each one right or wrong in the feedback', async () => {
    const { target, submitGradedReview } = await setup();
    await postAnswerFromFrame(target, 'question', ['red', '', 'blue']);
    (target.querySelector('[data-testid="review-graded-submit"]') as HTMLElement).click();
    await flush();
    expect(submitGradedReview.mock.calls[0]![0]).toMatchObject({ card_id: 'l1', answer: ['red', '', 'blue'] });
    const rows = Array.from(target.querySelectorAll('[data-testid="review-graded-breakdown-item"]'));
    expect(rows.map((row) => [row.textContent?.trim(), row.getAttribute('data-correct')])).toEqual([
      ['red', 'true'],
      ['green', 'false'],
      ['blue', 'true'],
    ]);
    expect(target.querySelector('[data-testid="review-graded-answer"]')).toBeNull();
    // The stem stays above once the grading result appears; it is rendered by the frame, not by the parent page.
    expect((await shownHtmlOnSide(target, 'question')).join('')).toContain('光的三原色是什么？');
  });
});
