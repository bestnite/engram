import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { faces } from './card-face-fixture';
import { shownFaces, postAnswerFromFrame, frameDocument } from './card-frame-fixture';
import { REVIEW_BACK_CSS, REVIEW_FRONT_CSS } from '../lib/card-frame';

/**
 * The review page's card faces leave the parent DOM: every face — the question, the revealed answer and a graded result's
 * answer — is handed to the sandboxed frame, and the parent markup keeps only the controls and the plain-text fallback.
 * These cases assert the content that reaches the frame, so moving the sink does not weaken "the stored HTML, never the raw
 * field text".
 */
describe('ReviewView card faces in the frame', () => {
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

  async function mountView(card: DueCard, client: Partial<ApiClient>): Promise<HTMLElement> {
    setLocale('zh-CN');
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
      answer_control: null,
      machine_graded: false,
      field_order: ['front', 'back'],
    };
    const renderReviewCard = vi.fn().mockResolvedValue({
      card_id: 'b1',
      ...faces('<p><strong>Stored</strong> front</p>', '<p><em>Stored</em> back</p>', '/fm/token/'),
      front_html: '<p><strong>Stored</strong> front</p>',
      back_html: '<p><em>Stored</em> back</p>',
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
    expect(question[0]).toMatchObject({ html: '<p><strong>Stored</strong> front</p>', side: 'question', mediaBase: '/fm/token/' });
    // The raw field text stays out of the parent page entirely.
    expect(target.textContent).not.toContain('RAW-FRONT-TEXT');

    (target.querySelector('[data-testid="review-reveal"]') as HTMLElement).click();
    await flush();

    const shown = await shownFaces(target);
    expect(shown.map((face) => face.side)).toEqual(['question', 'answer']);
    expect(shown[1]).toMatchObject({ html: '<p><em>Stored</em> back</p>', mediaBase: '/fm/token/' });
    expect(target.textContent).not.toContain('RAW-BACK-TEXT');
  });

  it('hands a graded result answer to the frame and keeps the parent free of the answer HTML', async () => {    const card: DueCard = {
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
      answer_control: 'text',
      machine_graded: true,
      field_order: ['prompt', 'answer'],
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
        ...faces('<p>Capital of France?</p>', '<strong>Paris</strong>', '/fm/token/'),
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

    // The answer control lives in the frame and posts its value; the parent submits what arrived.
    await postAnswerFromFrame(target, 'question', 'Paris');
    (target.querySelector('[data-testid="review-graded-submit"]') as HTMLElement).click();
    await flush();

    expect(submitGradedReview).toHaveBeenCalledTimes(1);
    expect(submitGradedReview.mock.calls[0]![1]).toEqual({ frame: true });
    const shown = await shownFaces(target);
    // The prompt frame is the first; the result panel adds the answer frame with the feedback's own media base.
    const answer = shown.find((face) => face.side === 'answer');
    expect(answer).toMatchObject({ html: '<strong>Paris</strong>', mediaBase: '/fm/graded/' });
    expect(target.querySelector('[data-testid="review-graded-result"]')?.textContent).not.toContain('Paris');
  });

  it('writes the note type CSS and the server body class into the frame document', async () => {
    const card: DueCard = {
      card_id: 'n1',
      note_id: 'n1',
      deck_id: 'd1',
      state: 'new',
      due_at: '',
      retrievability: 0,
      kind: 'basic',
      fields: { front: 'Question', back: 'Answer' },
      tags: [],
      template: 'forward',
      version: 0,
      answer_control: null,
      machine_graded: false,
      field_order: ['front', 'back'],
    };
    // A note type that styles the card body itself: the front and the back carry different rules and class lists, so the
    // case proves each frame takes its own side's object instead of sharing one.
    const frontCSS = '.card { color: rgb(1, 2, 3); }';
    const backCSS = '.card { color: rgb(9, 8, 7); }';
    const target = await mountView(card, {
      renderReviewCard: vi.fn().mockResolvedValue({
        card_id: 'n1',
        front: { html: '<p>Question</p>', css: frontCSS, body_class: 'card type-front', side: 'front', media_base: '/fm/token/' },
        back: { html: '<p>Answer</p>', css: backCSS, body_class: 'card type-back', side: 'back', media_base: '/fm/token/' },
        options_html: [],
        blanks: null,
        edit_href: '',
        media_base: '/fm/token/',
      }),
      getCardTypes: vi.fn(),
    });

    (target.querySelector('[data-testid="review-reveal"]') as HTMLElement).click();
    await flush();

    const shown = await shownFaces(target);
    expect(shown.map((face) => face.side)).toEqual(['question', 'answer']);

    // The question frame: the engram typography first, then the note type rule, so the note type's colour wins.
    const front = await frameDocument(shown[0]!);
    const frontStyle = front.head.querySelector('style[data-card-css]');
    expect(frontStyle?.textContent).toBe(`${REVIEW_FRONT_CSS}\n${frontCSS}`);
    expect(front.body.className).toBe('card type-front card1');

    // The answer frame takes the back object: its own CSS and its own class list.
    const back = await frameDocument(shown[1]!);
    const backStyle = back.head.querySelector('style[data-card-css]');
    expect(backStyle?.textContent).toBe(`${REVIEW_BACK_CSS}\n${backCSS}`);
    expect(back.body.className).toBe('card type-back card1');
  });
});
