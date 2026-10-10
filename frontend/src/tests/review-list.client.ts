import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { clearCardTypes, seedCardTypes } from './card-type-fixture';

/**
 * 列表卡的逐项作答：输入框个数来自 render 响应，标签写「第 N 项」；判分反馈带 breakdown 时
 * 逐项标出想起与漏掉的条目，代替整段答案。
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

  it('shows one input per item labelled by item number', async () => {
    const { target } = await setup();
    const inputs = target.querySelectorAll('[data-testid="review-graded-blank"]');
    expect(inputs).toHaveLength(3);
    const labels = target.querySelector('[data-testid="review-graded-blanks"]')?.textContent ?? '';
    expect(labels).toContain('第 3 项');
    expect(labels).not.toContain('空');
  });

  it('submits the items and marks each one right or wrong in the feedback', async () => {
    const { target, submitGradedReview } = await setup();
    const inputs = Array.from(target.querySelectorAll<HTMLInputElement>('[data-testid="review-graded-blank"]'));
    inputs[0]!.value = 'red';
    inputs[0]!.dispatchEvent(new Event('input', { bubbles: true }));
    inputs[2]!.value = 'blue';
    inputs[2]!.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
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
  });
});
