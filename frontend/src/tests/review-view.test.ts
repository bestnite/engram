import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { DueCard, GradedFeedback } from '../lib/api';
import { setLocale } from '../lib/i18n';

/** 构造一张作答类到期卡（字段形态与 GET /api/v1/review/due 一致，含答案原文）。 */
function gradedCard(kind: string, fields: Record<string, unknown>): DueCard {
  return {
    card_id: '11',
    note_id: '21',
    deck_id: '5',
    state: 'new',
    due_at: '2026-10-06T00:00:00Z',
    retrievability: 0,
    kind,
    fields,
    tags: [],
    template: 'forward',
    version: 0,
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
    expect(html).toContain('data-testid="review-graded-input"');
    expect(html).toContain('data-testid="review-graded-submit"');
    expect(html).toContain('data-testid="review-graded-reveal"');
    // 题面不得泄露正确答案，四档自评按钮不得出现。
    expect(html).not.toContain('Paris');
    expect(html).not.toContain('data-rating=');
    // 键位提示也必须跟着变，不再宣传 1–4 评分。
    expect(html).toContain('空格：显示答案');
    expect(html).not.toContain('重来');
  });

  it('renders selectable options for choice_single and choice_multi', () => {
    const single = gradedCard('choice_single', { question: '2+2=?', options: ['3', '4', '5'], answer: 1 });
    const singleHtml = render(ReviewView, { props: { initialLoading: false, initialCards: [single] } }).html;
    expect(singleHtml).toContain('data-testid="review-graded-options"');
    expect(singleHtml).toContain('2+2=?');
    expect(singleHtml).toContain('4');
    // 单选组由组件库提供：渲染成 button + role="radio"（不再是原生 input[type=radio]）。
    expect(singleHtml).toContain('role="radio"');

    const multi = gradedCard('choice_multi', { question: '偶数？', options: ['1', '2', '3', '4'], answers: [1, 3] });
    const multiHtml = render(ReviewView, { props: { initialLoading: false, initialCards: [multi] } }).html;
    expect(multiHtml).toContain('role="checkbox"');
    expect(multiHtml).toContain('偶数？');
  });

  it('renders the result panel from the server feedback, including sanitized answer HTML and rating mapping', () => {
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
    // 正确答案是服务端清洗后的 HTML，原样渲染（唯一 HTML 汇）。
    expect(html).toContain('<strong>Paris</strong>');
    // 作答文本按纯文本渲染，不经过 HTML 汇。
    expect(html).toContain('paris');
    // 档位映射可见。
    expect(html).toContain('记入评分');
    expect(html).toContain('良好');
    expect(html).toContain('data-testid="review-graded-continue"');
    // 结果态下 1–4 不再宣传评分。
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
    expect(html).toContain('<em>对</em>');
    expect(html).toContain('data-testid="review-graded-give-up"');
    expect(html).toContain('已揭示答案，记 0 分并继续');
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

describe('ReviewView server-sanitized HTML, edit, and bury parity', () => {
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
        initialFrontHTML: '<p><strong>Sanitized</strong> front</p>',
        initialBackHTML: '<p><em>Sanitized</em> back</p>',
        initialEditHref: '/decks/5/notes/21',
      },
    });
    // 卡面是 render 端点清洗后的 HTML，字段原文不再出现。
    expect(html).toContain('<strong>Sanitized</strong> front');
    expect(html).toContain('<em>Sanitized</em> back');
    expect(html).not.toContain('RAW-FRONT-TEXT');
    expect(html).not.toContain('RAW-BACK-TEXT');
    // 翻面后四档自评仍在。
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

  it('renders the sanitized front HTML for a graded prompt', () => {
    const typed = gradedCard('typed', { prompt: 'RAW-PROMPT', answer: 'Paris' });
    const { html } = render(ReviewView, {
      props: {
        initialLoading: false,
        initialCards: [typed],
        initialFrontHTML: '<p>Sanitized <code>prompt</code></p>',
      },
    });
    expect(html).toContain('data-testid="review-graded-prompt"');
    expect(html).toContain('<code>prompt</code>');
    expect(html).not.toContain('RAW-PROMPT');
    // 输入控件与提交按钮仍在。
    expect(html).toContain('data-testid="review-graded-input"');
    expect(html).toContain('data-testid="review-graded-submit"');
  });

  it('shows the edit and bury controls with catalog labels for an active card', () => {
    const basic = gradedCard('basic', { front: 'Q', back: 'A' });
    const { html } = render(ReviewView, {
      props: { initialLoading: false, initialCards: [basic] },
    });
    expect(html).toContain('data-testid="review-edit"');
    expect(html).toContain('data-testid="review-bury"');
    expect(html).toContain('data-testid="review-bury-hint"');
    // 文案来自语言包，不是裸 key。
    expect(html).toContain('编辑');
    expect(html).toContain('埋藏');
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
});
