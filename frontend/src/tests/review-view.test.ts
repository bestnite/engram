import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { DueCard, GradedFeedback } from '../lib/api';
import { setLocale } from '../lib/i18n';

/** 构造一张作答类到期卡（字段形态与 GET /api/v1/review/due 一致，含答案原文）。 */
function gradedCard(kind: string, fields: Record<string, unknown>): DueCard {
  return {
    card_id: 11,
    note_id: 21,
    deck_id: 5,
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
    expect(singleHtml).toContain('type="radio"');

    const multi = gradedCard('choice_multi', { question: '偶数？', options: ['1', '2', '3', '4'], answers: [1, 3] });
    const multiHtml = render(ReviewView, { props: { initialLoading: false, initialCards: [multi] } }).html;
    expect(multiHtml).toContain('type="checkbox"');
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
    expect(html).toContain('显示答案 · 空格 / 回车');
  });
});
