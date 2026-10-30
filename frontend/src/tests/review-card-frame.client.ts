import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { clearCardTypes, seedCardTypes } from './card-type-fixture';
import { shownFaces } from './card-frame-fixture';

/**
 * The review page's card faces leave the parent DOM: every face — the question, the revealed answer and a graded result's
 * answer — is handed to the sandboxed frame, and the parent markup keeps only the controls and the plain-text fallback.
 * These cases assert the content that reaches the frame, so moving the sink does not weaken "sanitized HTML, never the raw
 * field text".
 */
describe('ReviewView card faces in the frame', () => {
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

  async function mountView(card: DueCard, client: Partial<ApiClient>): Promise<HTMLElement> {
    setLocale('zh-CN');
    seedCardTypes();
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ReviewView, {
      target,
      props: { client: { getDueCards: vi.fn().mockResolvedValue({ cards: [card] }), ...client } as unknown as ApiClient },
    });
    await flush();
    return target;
  }

  it('hands the front and the revealed back to one frame each, with the response media base', async () => {
    const card: DueCard = {
      card_id: 'b1',
      note_id: 'n1',
      deck_id: 'd1',
      state: 'new',
      due_at: '',
      retrievability: 0,
      kind: 'basic',
      fields: { front: 'RAW-FRONT-TEXT', back: 'RAW-BACK-TEXT' },
      tags: [],
      template: 'forward',
      version: 0,
    };
    const renderReviewCard = vi.fn().mockResolvedValue({
      card_id: 'b1',
      front_html: '<p><strong>Sanitized</strong> front</p>',
      back_html: '<p><em>Sanitized</em> back</p>',
      options_html: [],
      blanks: null,
      edit_href: '/decks/d1/notes/n1',
      media_base: '/fm/token/',
    });
    const target = await mountView(card, { renderReviewCard, getCardTypes: vi.fn() });

    // The request asks for frame-ready HTML: relative media names plus the base they resolve against.
    expect(renderReviewCard).toHaveBeenCalledWith({ card_id: 'b1', deck: undefined, tags: undefined }, { frame: true });
    const question = await shownFaces(target);
    expect(question).toHaveLength(1);
    expect(question[0]).toMatchObject({ html: '<p><strong>Sanitized</strong> front</p>', side: 'question', mediaBase: '/fm/token/' });
    // The raw field text stays out of the parent page entirely.
    expect(target.textContent).not.toContain('RAW-FRONT-TEXT');

    (target.querySelector('[data-testid="review-reveal"]') as HTMLElement).click();
    await flush();

    const faces = await shownFaces(target);
    expect(faces.map((face) => face.side)).toEqual(['question', 'answer']);
    expect(faces[1]).toMatchObject({ html: '<p><em>Sanitized</em> back</p>', mediaBase: '/fm/token/' });
    expect(target.textContent).not.toContain('RAW-BACK-TEXT');
  });

  it('hands a graded result answer to the frame and keeps the parent free of the answer HTML', async () => {
    const card: DueCard = {
      card_id: 'g1',
      note_id: 'n2',
      deck_id: 'd1',
      state: 'new',
      due_at: '',
      retrievability: 0,
      kind: 'typed',
      fields: { prompt: 'Capital of France?', answer: 'Paris' },
      tags: [],
      template: 'forward',
      version: 0,
    };
    const submitGradedReview = vi.fn().mockResolvedValue({
      card_id: 'g1',
      state: 'learning',
      version: 1,
      remaining: 0,
      cards: [],
      feedback: {
        verdict: 'correct',
        score: 1,
        rating: 3,
        answer_html: '<strong>Paris</strong>',
        media_base: '/fm/graded/',
        given: 'paris',
      },
    });
    const target = await mountView(card, {
      renderReviewCard: vi.fn().mockResolvedValue({
        card_id: 'g1',
        front_html: '<p>Capital of France?</p>',
        back_html: '<strong>Paris</strong>',
        options_html: [],
        blanks: null,
        edit_href: '/decks/d1/notes/n2',
        media_base: '/fm/token/',
      }),
      getCardTypes: vi.fn(),
      submitGradedReview,
    });

    const input = target.querySelector('[data-testid="review-graded-input"]') as HTMLInputElement;
    input.value = 'Paris';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
    (target.querySelector('[data-testid="review-graded-submit"]') as HTMLElement).click();
    await flush();

    expect(submitGradedReview).toHaveBeenCalledTimes(1);
    expect(submitGradedReview.mock.calls[0]![1]).toEqual({ frame: true });
    const faces = await shownFaces(target);
    // The prompt frame is the first; the result panel adds the answer frame with the feedback's own media base.
    const answer = faces.find((face) => face.side === 'answer');
    expect(answer).toMatchObject({ html: '<strong>Paris</strong>', mediaBase: '/fm/graded/' });
    expect(target.querySelector('[data-testid="review-graded-result"]')?.textContent).not.toContain('Paris');
  });
});
