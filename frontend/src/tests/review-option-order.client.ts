import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { clearCardTypes, seedCardTypes } from './card-type-fixture';

// The server shuffles choice options for display: option_order maps display slot -> stored option index. The view must render
// the rows in that order while every submitted answer stays a stored index.
describe('ReviewView choice option display order', () => {
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

  function card(kind: 'choice_single' | 'choice_multi'): DueCard {
    return {
      card_id: 'c1',
      note_id: 'n1',
      deck_id: 'd1',
      state: 'new',
      due_at: '',
      retrievability: 0,
      kind,
      fields: { question: '问？', options: ['ALPHA', 'BETA', 'GAMMA'] },
      tags: [],
      template: 'forward',
      version: 0,
    };
  }

  async function setup(
    kind: 'choice_single' | 'choice_multi',
    optionOrder: number[] | null,
    optionsHTML: string[] | null
  ) {
    setLocale('zh-CN');
    seedCardTypes();
    const submitGradedReview = vi.fn().mockResolvedValue({
      card_id: 'c1',
      state: 'learning',
      version: 1,
      remaining: 0,
      cards: [],
      feedback: { verdict: 'correct', score: 1, rating: 3, answer_html: '', given: '' },
    });
    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: [card(kind)] }),
      renderReviewCard: vi.fn().mockResolvedValue({
        card_id: 'c1',
        front_html: '<p>问？</p>',
        back_html: '',
        options_html: optionsHTML,
        option_order: optionOrder,
        blanks: null,
        edit_href: '',
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

  function rows(target: HTMLElement): HTMLElement[] {
    return Array.from(target.querySelectorAll<HTMLElement>('[data-testid="review-graded-option"]'));
  }

  function rowTexts(target: HTMLElement): string[] {
    return rows(target).map((row) => row.textContent?.trim() ?? '');
  }

  /** Multi-choice rows are labels: the text sits in the span next to the checkbox, not inside the checkbox button. */
  function multiRowTexts(target: HTMLElement): string[] {
    return Array.from(target.querySelectorAll<HTMLElement>('[data-testid="review-graded-option-row"]')).map(
      (row) => row.querySelector('span:last-child')?.textContent?.trim() ?? ''
    );
  }

  it('renders single-choice rows in display order and submits the stored index of the clicked slot', async () => {
    const { target, submitGradedReview } = await setup('choice_single', [2, 0, 1], [
      '<p>ALPHA</p>',
      '<p>BETA</p>',
      '<p>GAMMA</p>',
    ]);
    expect(rowTexts(target)).toEqual(['GAMMA', 'ALPHA', 'BETA']);

    rows(target)[0]!.click();
    await flush();
    (target.querySelector('[data-testid="review-graded-submit"]') as HTMLElement).click();
    await flush();
    expect(submitGradedReview.mock.calls[0]![0]).toMatchObject({ answer: 2 });
  });

  it('submits the stored index of a middle display slot, not the slot itself', async () => {
    const { target, submitGradedReview } = await setup('choice_single', [2, 0, 1], null);
    expect(rowTexts(target)).toEqual(['GAMMA', 'ALPHA', 'BETA']);

    rows(target)[1]!.click();
    await flush();
    (target.querySelector('[data-testid="review-graded-submit"]') as HTMLElement).click();
    await flush();
    expect(submitGradedReview.mock.calls[0]![0]).toMatchObject({ answer: 0 });
  });

  it('toggles the stored index of the display slot for a multi-choice card', async () => {
    const { target, submitGradedReview } = await setup('choice_multi', [2, 0, 1], [
      '<p>ALPHA</p>',
      '<p>BETA</p>',
      '<p>GAMMA</p>',
    ]);
    expect(multiRowTexts(target)).toEqual(['GAMMA', 'ALPHA', 'BETA']);

    rows(target)[0]!.click();
    rows(target)[2]!.click();
    await flush();
    expect(rows(target).map((row) => row.getAttribute('data-state'))).toEqual(['checked', 'unchecked', 'checked']);
    (target.querySelector('[data-testid="review-graded-submit"]') as HTMLElement).click();
    await flush();
    const answer = submitGradedReview.mock.calls[0]![0].answer as number[];
    expect([...answer].sort()).toEqual([1, 2]);
  });

  it('renders the stored order when option_order is null', async () => {
    const { target, submitGradedReview } = await setup('choice_single', null, null);
    expect(rowTexts(target)).toEqual(['ALPHA', 'BETA', 'GAMMA']);

    rows(target)[1]!.click();
    await flush();
    (target.querySelector('[data-testid="review-graded-submit"]') as HTMLElement).click();
    await flush();
    expect(submitGradedReview.mock.calls[0]![0]).toMatchObject({ answer: 1 });
  });

  it('falls back to the stored order when option_order is not a permutation, losing no option', async () => {
    const { target, submitGradedReview } = await setup('choice_single', [0, 0, 1], null);
    // A duplicate-heavy order is not a permutation of 0..2, so every option is still shown once in stored order.
    expect(rowTexts(target)).toEqual(['ALPHA', 'BETA', 'GAMMA']);

    rows(target)[2]!.click();
    await flush();
    (target.querySelector('[data-testid="review-graded-submit"]') as HTMLElement).click();
    await flush();
    expect(submitGradedReview.mock.calls[0]![0]).toMatchObject({ answer: 2 });
  });
});
