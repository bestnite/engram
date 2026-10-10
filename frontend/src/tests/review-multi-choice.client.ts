import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ReviewView from '../lib/views/ReviewView.svelte';
import type { ApiClient, DueCard } from '../lib/api';
import { setLocale } from '../lib/i18n';
import { clearCardTypes, seedCardTypes } from './card-type-fixture';

/**
 * 多选题的选项整行可点：点选项文字切换选中，再点取消；直接点复选框也只切换一次，
 * 不会因为外层 label 转发点击而切换两次。
 */
describe('ReviewView multi choice rows', () => {
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
    card_id: 'm1',
    note_id: 'n1',
    deck_id: 'd1',
    state: 'new',
    due_at: '',
    retrievability: 0,
    kind: 'choice_multi',
    fields: { question: '偶数？', options: ['1', '2', '3', '4'] },
    tags: [],
    template: 'forward',
    version: 0,
  };

  async function setup() {
    setLocale('zh-CN');
    seedCardTypes();
    const submitGradedReview = vi.fn().mockResolvedValue({
      card_id: 'm1',
      state: 'learning',
      version: 1,
      remaining: 0,
      cards: [],
      feedback: { verdict: 'correct', score: 1, rating: 3, answer_html: '<p>2, 4</p>', given: '2, 4' },
    });
    const client = {
      getDueCards: vi.fn().mockResolvedValue({ cards: [card] }),
      renderReviewCard: vi.fn().mockResolvedValue({ card_id: 'm1', front_html: '<p>偶数？</p>', back_html: '<p>2, 4</p>', blanks: null, edit_href: '' }),
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
    return Array.from(target.querySelectorAll<HTMLElement>('[data-testid="review-graded-option-row"]'));
  }

  it('toggles an option when its text is clicked', async () => {
    const { target } = await setup();
    const text = rows(target)[1]!.querySelector('span:last-child') as HTMLElement;
    text.click();
    await flush();
    expect(rows(target)[1]!.getAttribute('data-state')).toBe('checked');
    text.click();
    await flush();
    expect(rows(target)[1]!.getAttribute('data-state')).toBe('unchecked');
  });

  it('toggles once when the checkbox itself is clicked and submits the picked indices', async () => {
    const { target, submitGradedReview } = await setup();
    (rows(target)[1]!.querySelector('[data-testid="review-graded-option"]') as HTMLElement).click();
    (rows(target)[3]!.querySelector('span:last-child') as HTMLElement).click();
    await flush();
    expect(rows(target).map((row) => row.getAttribute('data-state'))).toEqual(['unchecked', 'checked', 'unchecked', 'checked']);
    (target.querySelector('[data-testid="review-graded-submit"]') as HTMLElement).click();
    await flush();
    expect(submitGradedReview.mock.calls[0]![0]).toMatchObject({ answer: [1, 3] });
  });
});
