import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { DueCard, GradedFeedback } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { face } from './card-face-fixture';

/** How the server answers one note type shape: the template's answer control, whether the submit path grades it, and the field order. */
const flows: Record<string, Pick<DueCard, 'answer_control' | 'machine_graded' | 'field_order'>> = {
  basic: { answer_control: null, machine_graded: false, field_order: ['front', 'back'] },
  typed: { answer_control: 'text', machine_graded: true, field_order: ['prompt', 'answer', 'accept'] },
  choice_single: { answer_control: 'single', machine_graded: true, field_order: ['question', 'options', 'answer'] },
  choice_multi: { answer_control: 'multi', machine_graded: true, field_order: ['question', 'options', 'answers'] },
  true_false: { answer_control: 'bool', machine_graded: true, field_order: ['statement', 'answer'] },
  short_answer: { answer_control: 'short_answer', machine_graded: false, field_order: ['prompt', 'reference'] },
  numeric: { answer_control: 'number', machine_graded: true, field_order: ['prompt', 'value', 'unit'] },
};

/** Build a due answering-type card (field shape matching GET /api/v1/review/due, including the raw answer text). */
function gradedCard(kind: string, fields: Record<string, unknown>): DueCard {
  return {
    card_id: '11',
    note_id: '21',
    deck_id: '5',
    state: 'new',
    due_at: '2026-10-06T00:00:00Z',
    retrievability: 0,
    fields,
    tags: [],
    template: 'forward',
    version: 0,
    ...flows[kind] ?? { answer_control: null, machine_graded: false, field_order: [] },
  };
}

describe('ReviewView graded answering', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders typed input controls instead of the four self-ratings and never leaks the answer', () => {
    const card = gradedCard('typed', { prompt: '法国首都？', answer: 'Paris', accept: ['巴黎'] });
    const { html } = render(ReviewView, {
      props: { initialLoading: false, initialCards: [card] },
    });

    expect(html).toContain('data-testid="review-graded-prompt"');
    // The answer control is part of the frame's front face now; the parent keeps only the submit/reveal buttons.
    expect(html).not.toContain('data-testid="review-graded-input"');
    expect(html).toContain('data-testid="review-graded-submit"');
    expect(html).toContain('data-testid="review-graded-reveal"');
    // The question must not leak the correct answer, and the four self-rating buttons must not appear.
    expect(html).not.toContain('Paris');
    expect(html).not.toContain('data-rating=');
    // The key hints must change with it too: advertise Enter to submit, no longer 1–4 to rate.
    expect(html).toContain('回车：提交答案');
    expect(html).not.toContain('重来');
  });

  it('keeps choice options and true/false radios out of the parent DOM', () => {
    const single = gradedCard('choice_single', { question: '2+2=?', options: ['3', '4', '5'], answer: 1 });
    const singleHtml = render(ReviewView, { props: { initialLoading: false, initialCards: [single] } }).html;
    expect(singleHtml).toContain('data-testid="review-graded-prompt"');
    expect(singleHtml).not.toContain('role="radio"');
    expect(singleHtml).not.toContain('data-testid="review-graded-option"');
    expect(singleHtml).not.toContain('data-testid="review-graded-options"');

    const multi = gradedCard('choice_multi', { question: '偶数？', options: ['1', '2', '3', '4'], answers: [1, 3] });
    const multiHtml = render(ReviewView, { props: { initialLoading: false, initialCards: [multi] } }).html;
    expect(multiHtml).not.toContain('role="checkbox"');
    expect(multiHtml).not.toContain('data-testid="review-graded-option-row"');

    const bool = gradedCard('true_false', { statement: '地球是圆的', answer: true });
    const boolHtml = render(ReviewView, { props: { initialLoading: false, initialCards: [bool] } }).html;
    expect(boolHtml).not.toContain('role="radio"');
    expect(boolHtml).toContain('data-testid="review-graded-submit"');
  });

  it('renders the result panel from the server feedback, including answer HTML and rating mapping', () => {
    const feedback: GradedFeedback = {
      verdict: 'correct',
      score: 1,
      rating: 3,
      answer_html: '<strong>Paris</strong>',
      given: 'paris',
    };
    const { html } = render(ReviewView, {
      props: {
        initialLoading: false,
        initialCards: [gradedCard('typed', { prompt: '法国首都？', answer: 'Paris' })],
        initialFeedback: feedback,
      },
    });

    expect(html).toContain('data-testid="review-graded-result"');
    expect(html).toContain('回答正确');
    expect(html).toContain('data-testid="review-graded-score"');
    expect(html).toContain('100%');
    // The correct answer is a card face: it is handed to the sandboxed frame, so it is not part of the parent's markup.
    expect(html).toContain('data-testid="review-graded-answer"');
    expect(html).toContain('data-card-frame');
    expect(html).not.toContain('<strong>Paris</strong>');
    // The answer text renders as plain text and does not pass through the HTML sink.
    expect(html).toContain('paris');
    // The rating mapping is visible.
    expect(html).toContain('评分 · ');
    expect(html).toContain('良好');
    expect(html).toContain('data-testid="review-graded-continue"');
    // In the result state 1–4 no longer advertise rating.
    expect(html).toContain('回车 / 空格：继续');
  });

  it('renders the revealed answer with only the give-up action', () => {
    const { html } = render(ReviewView, {
      props: {
        initialLoading: false,
        initialCards: [gradedCard('true_false', { statement: '地球是圆的', answer: true })],
        initialRevealedAnswerHTML: '<em>对</em>',
      },
    });

    expect(html).toContain('data-testid="review-graded-revealed"');
    // The revealed answer is a card face and lives in the frame, not in the parent's markup.
    expect(html).toContain('data-testid="review-graded-answer"');
    expect(html).toContain('data-card-frame');
    expect(html).not.toContain('<em>对</em>');
    expect(html).toContain('data-testid="review-graded-give-up"');
    expect(html).toContain('记为重来并继续');
    expect(html).not.toContain('data-testid="review-graded-submit"');
  });

  it('keeps the four self-ratings for self-assessable cards', () => {
    const basic = gradedCard('basic', { front: 'Question', back: 'Answer' });
    const { html } = render(ReviewView, {
      props: { initialLoading: false, initialCards: [basic] },
    });
    expect(html).toContain('data-testid="review-front"');
    expect(html).not.toContain('data-testid="review-graded-prompt"');
    expect(html).toContain('显示答案');
    expect(html).toContain('空格 / 回车：显示答案');
  });
});

describe('ReviewView face HTML in the frame, edit, and bury parity', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders the render-endpoint front/back HTML instead of the raw field text', () => {
    const basic = gradedCard('basic', { front: 'RAW-FRONT-TEXT', back: 'RAW-BACK-TEXT' });
    const { html } = render(ReviewView, {
      props: {
        initialLoading: false,
        initialCards: [basic],
        initialRevealed: true,
        initialFrontFace: face('<p><strong>Stored</strong> front</p>'),
        initialBackFace: face('<p><em>Stored</em> back</p>', { side: 'back' }),
        initialEditHref: '/decks/5/notes/21',
      },
    });
    // Both faces are rendered by the frame, so the parent markup carries one frame per face and no face HTML of its own.
    expect((html.match(/data-card-frame/g) ?? [])).toHaveLength(2);
    expect(html).not.toContain('<strong>Stored</strong> front');
    expect(html).not.toContain('<em>Stored</em> back');
    expect(html).not.toContain('RAW-FRONT-TEXT');
    expect(html).not.toContain('RAW-BACK-TEXT');
    // After flipping, the four self-ratings are still there.
    expect(html).toContain('data-rating="1"');
    expect(html).toContain('data-rating="4"');
  });

  it('falls back to plain-text fields when the render HTML is empty', () => {
    const basic = gradedCard('basic', { front: 'Plain front', back: 'Plain back' });
    const { html } = render(ReviewView, {
      props: { initialLoading: false, initialCards: [basic], initialRevealed: true },
    });
    expect(html).toContain('Plain front');
    expect(html).toContain('Plain back');
  });

  it('renders the front face HTML for a graded prompt', () => {
    const typed = gradedCard('typed', { prompt: 'RAW-PROMPT', answer: 'Paris' });
    const { html } = render(ReviewView, {
      props: {
        initialLoading: false,
        initialCards: [typed],
        initialFrontFace: face('<p>Stored <code>prompt</code></p>'),
      },
    });
    expect(html).toContain('data-testid="review-graded-prompt"');
    // The prompt is a card face and is rendered by the frame; the raw prompt text never reaches the parent.
    expect(html).toContain('data-card-frame');
    expect(html).not.toContain('<code>prompt</code>');
    expect(html).not.toContain('RAW-PROMPT');
    // The front face, including its input control, is rendered by the frame; the submit button stays here.
    expect(html).toContain('data-testid="review-graded-submit"');
  });

  it('shows the edit and bury controls with catalog labels for an active card', () => {
    const basic = gradedCard('basic', { front: 'Q', back: 'A' });
    const { html } = render(ReviewView, {
      props: { initialLoading: false, initialCards: [basic] },
    });
    expect(html).toContain('data-testid="review-edit"');
    expect(html).toContain('data-testid="review-bury"');
    expect(html).toContain('data-testid="review-suspend"');
    expect(html).toContain('data-testid="review-bury-hint"');
    // The difference between bury and pause moved into the question-mark popover rather than a full sentence permanently at the bottom of every card.
    expect(html).toContain('aria-label="埋藏与暂停是什么意思？"');
    expect(html).not.toContain('埋藏：今天跳过');
    // The string comes from the language pack, not a bare key.
    expect(html).toContain('编辑');
    expect(html).toContain('埋藏');
    expect(html).toContain('暂停');
    expect(html).not.toContain('review.suspend');
    expect(html).not.toContain('review.edit');
    expect(html).not.toContain('review.bury');
  });

  it('drops the bury control on the graded result panel but keeps edit', () => {
    const feedback: GradedFeedback = {
      verdict: 'correct',
      score: 1,
      rating: 3,
      answer_html: '<strong>Paris</strong>',
      given: 'paris',
    };
    const { html } = render(ReviewView, {
      props: {
        initialLoading: false,
        initialCards: [gradedCard('typed', { prompt: 'Q', answer: 'Paris' })],
        initialFeedback: feedback,
      },
    });
    expect(html).toContain('data-testid="review-edit"');
    expect(html).not.toContain('data-testid="review-bury"');
  });

  it('offers undo on the graded result panel, and from the header for any last submission', () => {
    const feedback: GradedFeedback = {
      verdict: 'correct',
      score: 1,
      rating: 3,
      answer_html: '<strong>Paris</strong>',
      given: 'paris',
    };
    const card = gradedCard('typed', { prompt: 'Q', answer: 'Paris' });
    const result = render(ReviewView, {
      props: {
        initialLoading: false,
        initialCards: [card],
        initialFeedback: feedback,
        initialLastUndo: { cardId: '11', version: 5, rating: 3 },
      },
    }).html;
    // The result panel shows an undo button, its string from the language pack (not a bare key).
    expect(result).toContain('data-testid="review-undo"');
    expect(result).toContain('撤销');
    expect(result).not.toContain('review.undo');
    // While the panel is open no second (header) entry appears.
    expect(result).not.toContain('data-testid="review-undo-last"');

    // Graded card awaiting an answer with no submit record: neither entry is present.
    const answering = render(ReviewView, {
      props: { initialLoading: false, initialCards: [card] },
    }).html;
    expect(answering).not.toContain('data-testid="review-undo"');
    expect(answering).not.toContain('data-testid="review-undo-last"');
  });

  it('offers the header undo entry for a self-rated card, which has no result panel', () => {
    const { html } = render(ReviewView, {
      props: {
        initialLoading: false,
        initialCards: [gradedCard('basic', { front: 'Q', back: 'A' })],
        initialRevealed: true,
        initialLastUndo: { cardId: '11', version: 5, rating: 3 },
      },
    });
    expect(html).toContain('data-testid="review-undo-last"');
    // The string names the rating, so it cannot be mistaken for undoing the card currently shown.
    expect(html).toContain('撤销上一次');
    expect(html).toContain('良好');
    // The result-panel button is absent (self-rated questions have no panel).
    expect(html).not.toContain('data-testid="review-undo"');
    // The key hints mention u as well.
    expect(html).toContain('u：撤销');
  });

  it('keeps the header undo entry reachable when the queue has emptied', () => {
    // After undoing the last card current is null: the entry lives in the page header, so it stays reachable.
    const { html } = render(ReviewView, {
      props: {
        initialLoading: false,
        initialCards: [],
        initialLastUndo: { cardId: '11', version: 5, rating: 1 },
      },
    });
    expect(html).toContain('data-testid="review-empty"');
    expect(html).toContain('data-testid="review-undo-last"');
  });
});

describe('ReviewView card sides and controls come from the due-card payload', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders the short_answer front from prompt and the back from reference', () => {
    // The short answer note type's fields are prompt/reference. The plain-text fallback shows the first field as the
    // question and the rest as the answer, so the reference stays hidden until the reveal.
    const card = gradedCard('short_answer', {
      prompt: '解释一下 CAP 定理',
      reference: '一致性、可用性、分区容忍',
    });

    const front = render(ReviewView, {
      props: { initialLoading: false, initialCards: [card] },
    }).html;
    expect(front).toContain('data-testid="review-front"');
    expect(front).toContain('解释一下 CAP 定理');
    expect(front).not.toContain('一致性、可用性、分区容忍');
    // Self-rated type: takes the four self-ratings instead of an answering control.
    expect(front).toContain('显示答案');
    expect(front).not.toContain('data-testid="review-graded-input"');

    const revealed = render(ReviewView, {
      props: { initialLoading: false, initialCards: [card], initialRevealed: true },
    }).html;
    expect(revealed).toContain('data-testid="review-answer"');
    expect(revealed).toContain('一致性、可用性、分区容忍');
    expect(revealed).toContain('data-rating="1"');
  });

  it('renders the numeric answer control in the frame, not in the parent', () => {
    const card = gradedCard('numeric', { prompt: '光速（m/s）？', value: 299792458, unit: 'm/s' });
    const { html } = render(ReviewView, {
      props: { initialLoading: false, initialCards: [card] },
    });
    expect(html).toContain('data-testid="review-graded-prompt"');
    expect(html).not.toContain('data-testid="review-graded-input"');
    expect(html).not.toContain('inputmode="decimal"');
    // The question must not leak the numeric answer.
    expect(html).not.toContain('299792458');
  });

  it('renders a note type that matches no stock kind without loading any catalog', () => {
    // A note type created through the API has opaque field ids and a kind the catalog never described. The due-card
    // payload carries the answer flow and the field order, so the page renders it exactly like a stock type.
    const card: DueCard = {
      card_id: '31',
      note_id: '41',
      deck_id: '5',
      state: 'new',
      due_at: '2026-10-06T00:00:00Z',
      retrievability: 0,
      fields: { 'f-front': '自定义问题', 'f-back': '自定义答案' },
      tags: [],
      template: 'card-1',
      version: 0,
      answer_control: null,
      machine_graded: false,
      field_order: ['f-front', 'f-back'],
    };
    const { html } = render(ReviewView, {
      props: { initialLoading: false, initialCards: [card] },
    });
    expect(html).toContain('data-testid="review-front"');
    expect(html).toContain('自定义问题');
    // The answer stays hidden until the reveal.
    expect(html).not.toContain('自定义答案');
    expect(html).toContain('显示答案');
    // Never print a bare language-pack key or machine vocabulary as user-facing copy.
    expect(html).not.toContain('notes.kind.');
  });
});
