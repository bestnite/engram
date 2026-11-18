import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard, ReviewRenderResponse } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { faces } from './card-face-fixture';
import { shownFaces, postAnswerFromFrame } from './card-frame-fixture';

/**
 * The review action responses (answer, grade, undo, suspend, bury) carry the next card's faces inline. The page caches
 * that render and applies it when the card becomes current, so the common path — the next card straight after a grade —
 * costs no render request; a response without an inline render falls back to the render endpoint, and the type-in reveal
 * still asks for the answer face with the typed value.
 */
describe('ReviewView inline card render', () => {
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

  function card(id: string, overrides: Partial<DueCard> = {}): DueCard {
    return {
      card_id: id,
      note_id: 'n-' + id,
      deck_id: 'd1',
      state: 'new',
      due_at: '',
      retrievability: 0,
      fields: { front: 'front ' + id, back: 'back ' + id },
      tags: [],
      template: 'forward',
      version: 0,
      answer_control: null,
      machine_graded: false,
      field_order: ['front', 'back'],
      ...overrides,
    };
  }

  /** The inline render of one card, in the shape POST /review/render returns. */
  function rendered(target: DueCard, frontHTML: string, backHTML: string): ReviewRenderResponse {
    return {
      card_id: target.card_id,
      ordinal: 0,
      ...faces(frontHTML, backHTML, '/fm/inline/'),
      edit_href: `/decks/${target.deck_id}/notes/${target.note_id}`,
    };
  }

  async function mountView(first: DueCard, client: Partial<ApiClient>): Promise<HTMLElement> {
    setLocale('zh-CN');
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, {
      target,
      props: {
        client: {
          getDueCards: vi.fn().mockResolvedValue({ cards: [first] }),
          ...client,
        } as unknown as ApiClient,
      },
    });
    await flush();
    return target;
  }

  async function rate(target: HTMLElement): Promise<void> {
    (target.querySelector('[data-testid="review-reveal"]') as HTMLElement).click();
    await flush();
    (target.querySelector('[data-rating="3"]') as HTMLElement).click();
    await flush();
  }

  it('applies the inline render of the next card without requesting a render', async () => {
    const first = card('c1');
    const next = card('c2');
    const renderReviewCard = vi.fn().mockResolvedValue({
      card_id: 'c1',
      ...faces('<p>first front</p>', '<p>first back</p>'),
      edit_href: '/decks/d1/notes/n-c1',
    });
    const submitSelfReview = vi.fn().mockResolvedValue({
      card_id: 'c1',
      state: 'learning',
      version: 1,
      remaining: 1,
      queue_size: 1,
      new_remaining: 1,
      cards: [next],
      rendered_card: rendered(next, '<p>NEXT-FRONT</p>', '<p>NEXT-BACK</p>'),
    });
    const target = await mountView(first, { renderReviewCard, submitSelfReview });

    await rate(target);

    // Only the first card asked the render endpoint; the next card came from the action response.
    expect(renderReviewCard).toHaveBeenCalledTimes(1);
    expect(renderReviewCard.mock.calls[0]![0]).toMatchObject({ card_id: 'c1' });
    const shown = await shownFaces(target);
    expect(shown[0]).toMatchObject({ html: '<p>NEXT-FRONT</p>', side: 'question', mediaBase: '/fm/inline/' });
    expect(target.textContent).not.toContain('front c2');

    // The prefetched answer face is applied as well, so revealing this card asks for nothing either.
    (target.querySelector('[data-testid="review-reveal"]') as HTMLElement).click();
    await flush();
    expect(renderReviewCard).toHaveBeenCalledTimes(1);
    const revealed = await shownFaces(target);
    expect(revealed[1]).toMatchObject({ html: '<p>NEXT-BACK</p>', side: 'answer' });
  });

  it('falls back to the render endpoint when the response carries no inline render', async () => {
    const first = card('c1');
    const next = card('c2');
    const renderReviewCard = vi
      .fn()
      .mockResolvedValueOnce({
        card_id: 'c1',
        ...faces('<p>first front</p>', '<p>first back</p>'),
        edit_href: '/decks/d1/notes/n-c1',
      })
      .mockResolvedValueOnce({
        card_id: 'c2',
        ...faces('<p>FALLBACK-FRONT</p>', '<p>FALLBACK-BACK</p>'),
        edit_href: '/decks/d1/notes/n-c2',
      });
    const submitSelfReview = vi.fn().mockResolvedValue({
      card_id: 'c1',
      state: 'learning',
      version: 1,
      remaining: 1,
      queue_size: 1,
      new_remaining: 1,
      cards: [next],
    });
    const target = await mountView(first, { renderReviewCard, submitSelfReview });

    await rate(target);

    expect(renderReviewCard).toHaveBeenCalledTimes(2);
    expect(renderReviewCard.mock.calls[1]![0]).toMatchObject({ card_id: 'c2' });
    const shown = await shownFaces(target);
    expect(shown[0]).toMatchObject({ html: '<p>FALLBACK-FRONT</p>', side: 'question' });
  });

  it('keeps the graded flow next render until continue is pressed', async () => {
    const first = card('g1', { answer_control: 'text', machine_graded: true, field_order: ['prompt', 'answer'] });
    const next = card('g2');
    const renderReviewCard = vi.fn().mockResolvedValue({
      card_id: 'g1',
      ...faces('<p>question g1</p>', '<p>answer g1</p>'),
      edit_href: '/decks/d1/notes/n-g1',
    });
    const submitGradedReview = vi.fn().mockResolvedValue({
      card_id: 'g1',
      state: 'learning',
      version: 1,
      remaining: 1,
      queue_size: 1,
      new_remaining: 1,
      cards: [next],
      rendered_card: rendered(next, '<p>GRADED-NEXT-FRONT</p>', '<p>GRADED-NEXT-BACK</p>'),
      feedback: { verdict: 'correct', score: 1, rating: 3, answer_html: '<p>answer g1</p>', given: 'x' },
    });
    const target = await mountView(first, { renderReviewCard, submitGradedReview });

    await postAnswerFromFrame(target, 'question', 'x');
    (target.querySelector('[data-testid="review-graded-submit"]') as HTMLElement).click();
    await flush();

    // The result panel keeps the answered card, so the inline render is parked rather than dropped.
    expect(renderReviewCard).toHaveBeenCalledTimes(1);
    expect(target.querySelector('[data-testid="review-graded-result"]')).not.toBeNull();

    (target.querySelector('[data-testid="review-graded-continue"]') as HTMLElement).click();
    await flush();

    expect(renderReviewCard).toHaveBeenCalledTimes(1);
    const shown = await shownFaces(target);
    expect(shown[0]).toMatchObject({ html: '<p>GRADED-NEXT-FRONT</p>', side: 'question' });
  });

  it('renders the undone card inline and shows it instead of the queue head', async () => {
    const first = card('u1');
    const next = card('u2');
    const renderReviewCard = vi.fn().mockResolvedValue({
      card_id: 'u1',
      ...faces('<p>u1 front</p>', '<p>u1 back</p>'),
      edit_href: '/decks/d1/notes/n-u1',
    });
    const submitSelfReview = vi.fn().mockResolvedValue({
      card_id: 'u1',
      state: 'learning',
      version: 1,
      remaining: 1,
      queue_size: 1,
      new_remaining: 1,
      cards: [next],
      rendered_card: rendered(next, '<p>u2 front</p>', '<p>u2 back</p>'),
    });
    const undoReview = vi.fn().mockResolvedValue({
      undone_card_id: 'u1',
      remaining: 2,
      queue_size: 2,
      new_remaining: 2,
      // cards holds the undone card itself, not the queue head: the queue is ordered by due_at.
      cards: [first],
      rendered_card: rendered(first, '<p>RESTORED-FRONT</p>', '<p>RESTORED-BACK</p>'),
    });
    const target = await mountView(first, { renderReviewCard, submitSelfReview, undoReview });

    await rate(target);
    expect(renderReviewCard).toHaveBeenCalledTimes(1);

    (target.querySelector('[data-testid="review-undo-last"]') as HTMLElement).click();
    await flush();

    expect(undoReview).toHaveBeenCalledTimes(1);
    // The restored card is applied from the undo response's inline render, so no render request is made for it.
    expect(renderReviewCard).toHaveBeenCalledTimes(1);
    const shown = await shownFaces(target);
    expect(shown[0]).toMatchObject({ html: '<p>RESTORED-FRONT</p>', side: 'question' });
    expect(target.textContent).not.toContain('front u1');
    expect(target.textContent).not.toContain('front u2');
  });

  it('shows the undone card when the rebuilt queue does not contain it', async () => {
    const first = card('x1');
    const renderReviewCard = vi.fn().mockResolvedValue({
      card_id: 'x1',
      ...faces('<p>x1 front</p>', '<p>x1 back</p>'),
      edit_href: '/decks/d1/notes/n-x1',
    });
    // Rating the last queued card leaves no next card, and undo restores a card that is outside today's queue, so the
    // response's queue counts are zero while cards still holds the undone card.
    const submitSelfReview = vi.fn().mockResolvedValue({
      card_id: 'x1',
      state: 'learning',
      version: 1,
      remaining: 0,
      queue_size: 0,
      new_remaining: 0,
      cards: [],
    });
    const undoReview = vi.fn().mockResolvedValue({
      undone_card_id: 'x1',
      remaining: 0,
      queue_size: 0,
      new_remaining: 0,
      cards: [first],
      rendered_card: rendered(first, '<p>RESTORED-OUTSIDE-FRONT</p>', '<p>RESTORED-OUTSIDE-BACK</p>'),
    });
    const target = await mountView(first, { renderReviewCard, submitSelfReview, undoReview });

    await rate(target);
    (target.querySelector('[data-testid="review-undo-last"]') as HTMLElement).click();
    await flush();

    expect(undoReview).toHaveBeenCalledTimes(1);
    expect(renderReviewCard).toHaveBeenCalledTimes(1);
    const shown = await shownFaces(target);
    expect(shown[0]).toMatchObject({ html: '<p>RESTORED-OUTSIDE-FRONT</p>', side: 'question' });
  });

  it('applies the inline render of the next card after a bury', async () => {
    const first = card('b1');
    const next = card('b2');
    const renderReviewCard = vi.fn().mockResolvedValue({
      card_id: 'b1',
      ...faces('<p>b1 front</p>', '<p>b1 back</p>'),
      edit_href: '/decks/d1/notes/n-b1',
    });
    const buryReview = vi.fn().mockResolvedValue({
      remaining: 1,
      queue_size: 1,
      new_remaining: 1,
      cards: [next],
      rendered_card: rendered(next, '<p>BURIED-NEXT-FRONT</p>', '<p>BURIED-NEXT-BACK</p>'),
    });
    const target = await mountView(first, { renderReviewCard, buryReview });

    (target.querySelector('[data-testid="review-bury"]') as HTMLElement).click();
    await flush();

    expect(buryReview).toHaveBeenCalledTimes(1);
    expect(renderReviewCard).toHaveBeenCalledTimes(1);
    const shown = await shownFaces(target);
    expect(shown[0]).toMatchObject({ html: '<p>BURIED-NEXT-FRONT</p>', side: 'question' });
  });

  it('discards a stale render that resolves after the cached next card is applied', async () => {
    const first = card('s1');
    const next = card('s2');
    // The first card's render stays in flight until after the rating, so only the token check can keep it out.
    let resolveFirst: (value: ReviewRenderResponse) => void = () => {};
    const renderReviewCard = vi.fn().mockReturnValue(new Promise<ReviewRenderResponse>((resolve) => {
      resolveFirst = resolve;
    }));
    const submitSelfReview = vi.fn().mockResolvedValue({
      card_id: 's1',
      state: 'learning',
      version: 1,
      remaining: 1,
      queue_size: 1,
      new_remaining: 1,
      cards: [next],
      rendered_card: rendered(next, '<p>FRESH-NEXT-FRONT</p>', '<p>FRESH-NEXT-BACK</p>'),
    });
    const target = await mountView(first, { renderReviewCard, submitSelfReview });

    await rate(target);
    resolveFirst({
      card_id: 's1',
      ...faces('<p>STALE-FIRST-FRONT</p>', '<p>STALE-FIRST-BACK</p>'),
      edit_href: '/decks/d1/notes/n-s1',
    });
    await flush();

    const shown = await shownFaces(target);
    expect(shown[0]).toMatchObject({ html: '<p>FRESH-NEXT-FRONT</p>', side: 'question' });
    expect(shown[0]?.html).not.toContain('STALE-FIRST');
  });

  it('still requests the answer face with the typed value on a type-in reveal', async () => {
    const typeIn = card('t1');
    const renderReviewCard = vi
      .fn()
      .mockResolvedValueOnce({
        card_id: 't1',
        ...faces('<p>type in front</p>', '<p>expected</p>'),
        type_in: true,
        edit_href: '/decks/d1/notes/n-t1',
      })
      .mockResolvedValueOnce({
        card_id: 't1',
        ...faces('<p>type in front</p>', '<p>COMPARED WITH Paris</p>'),
        type_in: true,
        edit_href: '/decks/d1/notes/n-t1',
      });
    const target = await mountView(typeIn, { renderReviewCard });

    await postAnswerFromFrame(target, 'question', 'Paris');
    (target.querySelector('[data-testid="review-reveal"]') as HTMLElement).click();
    await flush();

    // The type-in comparison is the server's, so the reveal asks for the answer face with the typed value.
    expect(renderReviewCard).toHaveBeenCalledTimes(2);
    expect(renderReviewCard.mock.calls[1]![0]).toMatchObject({ card_id: 't1', answer: 'Paris' });
    const shown = await shownFaces(target);
    expect(shown[1]).toMatchObject({ html: '<p>COMPARED WITH Paris</p>', side: 'answer' });
  });
});
