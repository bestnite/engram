import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { faces } from './card-face-fixture';
import { shownHtmlOnSide, postAnswerFromFrame } from './card-frame-fixture';

/**
 * Per-blank answering for cloze cards: the input markup comes from the server's answer control and lives in the frame,
 * which posts one string per blank in order. The parent submits that array unchanged; without a posted answer the submit
 * is blocked instead of sending an empty array.
 */
describe('ReviewView cloze blanks', () => {
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

  const card: DueCard = {
    card_id: 'c1',
    note_id: 'n1',
    deck_id: 'd1',
    state: 'new',
    due_at: '',
    retrievability: 0,
    kind: 'cloze',
    fields: { text: '{{c1::Paris}} and {{c1::Rome::city}}' },
    tags: [],
    template: 'cloze:1',
    version: 0,
    answer_control: 'cloze_blanks',
    machine_graded: true,
    field_order: ['text'],
  };

  async function setup() {
    setLocale('zh-CN');
    const submitGradedReview = vi.fn().mockResolvedValue({
      card_id: 'c1',
      state: 'learning',
      version: 1,
      remaining: 0,
      cards: [],
      feedback: { verdict: 'partial', score: 0.5, rating: 2, answer_html: '<p>Paris and Rome</p>', given: 'Paris, Milan' },
    });
    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: [card] }),
      renderReviewCard: vi.fn().mockResolvedValue({
        card_id: 'c1',
        ...faces(
          '<p><span class="cloze">[…]</span> and <span class="cloze">[city]</span></p>',
          '<p>Paris and Rome</p>',
        ),
        front_html: '<p><span class="cloze">[…]</span> and <span class="cloze">[city]</span></p>',
        back_html: '<p>Paris and Rome</p>',
        blanks: ['', 'city'],
        edit_href: '/decks/d1/notes/n1',
      }),
      getCardTypes: vi.fn(),
      revealGradedAnswer: vi.fn().mockResolvedValue({ revealed: true, card_id: 'c1', answer_html: '<p>Paris and Rome</p>' }),
      submitGradedReview,
    } as unknown as ApiClient;
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, { target, props: { client } });
    await flush();
    return { target, submitGradedReview };
  }

  it('submits the blanks the frame posted, in order, and shows the verdict', async () => {
    const { target, submitGradedReview } = await setup();
    await postAnswerFromFrame(target, 'question', ['Paris', 'Milan']);
    (target.querySelector('[data-testid="review-graded-submit"]') as HTMLElement).click();
    await flush();
    expect(submitGradedReview).toHaveBeenCalledTimes(1);
    expect(submitGradedReview.mock.calls[0]![0]).toMatchObject({ card_id: 'c1', answer: ['Paris', 'Milan'] });
    expect(target.querySelector('[data-testid="review-graded-verdict"]')).not.toBeNull();
  });

  it('keeps the stem above the revealed answer, both rendered by the frame', async () => {
    const { target } = await setup();
    (target.querySelector('[data-testid="review-graded-reveal"]') as HTMLElement).click();
    await flush();
    expect((await shownHtmlOnSide(target, 'question')).join('')).toContain('city');
    expect((await shownHtmlOnSide(target, 'answer')).join('')).toContain('Paris and Rome');
  });

  it('does not submit before the frame posts an answer', async () => {
    const { target, submitGradedReview } = await setup();
    (target.querySelector('[data-testid="review-graded-submit"]') as HTMLElement).click();
    await flush();
    expect(submitGradedReview).not.toHaveBeenCalled();
    expect(target.querySelector('[data-testid="review-graded-need-answer"]')).not.toBeNull();
  });
});
